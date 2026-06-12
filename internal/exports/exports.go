// Package exports handles user data export jobs (ZIP now, PDF stub).
package exports

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/billing"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/httpx"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
)

const (
	kindZIP       = "zip"
	kindPDF       = "pdf"
	downloadTTL   = 24 * time.Hour
	exportKeyPref = "exports"
)

type Notifier interface {
	ExportComplete(ctx context.Context, userID uuid.UUID, jobID uuid.UUID, kind string)
}

type Handler struct {
	Pool     *pgxpool.Pool
	Q        *dbgen.Queries
	Store    *media.Store
	Notifier Notifier
	Process  func(ctx context.Context, jobID uuid.UUID)
}

func (h *Handler) Mount(r chi.Router) {
	r.Post("/exports", h.create)
	r.Get("/exports/{id}", h.get)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	var req struct {
		Kind string `json:"kind"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, err.Error())
		return
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind != kindZIP && kind != kindPDF {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "kind must be zip or pdf")
		return
	}

	user, err := h.Q.GetUserByID(r.Context(), pg.UUID(userID))
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "account no longer exists")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	if kind == kindPDF && billing.ForUser(user.Plan).Name != billing.PlanPro {
		httpx.Error(w, http.StatusForbidden, "plan_required", "Pro plan required for PDF export")
		return
	}

	job, err := h.Q.CreateExportJob(r.Context(), dbgen.CreateExportJobParams{
		UserID: pg.UUID(userID),
		Kind:   kind,
	})
	if err != nil {
		httpx.InternalError(w, err)
		return
	}
	jobID := pg.UUIDValue(job.ID)
	if h.Process != nil {
		h.Process(r.Context(), jobID)
	}

	httpx.JSON(w, http.StatusAccepted, jobDTO(job, nil))
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	userID, _ := httpx.UserID(r.Context())
	jobID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid export id")
		return
	}

	job, err := h.Q.GetExportJob(r.Context(), dbgen.GetExportJobParams{
		ID: pg.UUID(jobID), UserID: pg.UUID(userID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, httpx.CodeNotFound, "export not found")
		return
	}
	if err != nil {
		httpx.InternalError(w, err)
		return
	}

	var downloadURL *string
	if job.Status == "ready" && job.ResultKey != nil && *job.ResultKey != "" && h.Store != nil {
		url, err := h.Store.SignedURL(r.Context(), *job.ResultKey, downloadTTL)
		if err != nil {
			httpx.InternalError(w, err)
			return
		}
		downloadURL = &url
	}
	httpx.JSON(w, http.StatusOK, jobDTO(job, downloadURL))
}

func jobDTO(job dbgen.ExportJob, downloadURL *string) map[string]any {
	resp := map[string]any{
		"id":         pg.UUIDValue(job.ID).String(),
		"kind":       job.Kind,
		"status":     job.Status,
		"created_at": pg.TimeValue(job.CreatedAt).UTC().Format(time.RFC3339),
	}
	if job.ResultKey != nil {
		resp["result_key"] = *job.ResultKey
	}
	if job.Error != nil {
		resp["error"] = *job.Error
	}
	if job.CompletedAt.Valid {
		resp["completed_at"] = pg.TimeValue(job.CompletedAt).UTC().Format(time.RFC3339)
	}
	if downloadURL != nil {
		resp["download_url"] = *downloadURL
	}
	return resp
}

// Worker processes pending export jobs.
type Worker struct {
	Pool     *pgxpool.Pool
	Q        *dbgen.Queries
	Store    *media.Store
	Notifier Notifier
}

func (w *Worker) RunPending(ctx context.Context) error {
	jobs, err := w.Q.ListPendingExportJobs(ctx, 10)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := w.processOne(ctx, job); err != nil {
			slog.Error("exports: job failed", "job", pg.UUIDValue(job.ID), "error", err)
		}
	}
	return nil
}

func (w *Worker) processOne(ctx context.Context, job dbgen.ExportJob) error {
	n, err := w.Q.MarkExportJobProcessing(ctx, job.ID)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}

	jobID := pg.UUIDValue(job.ID)
	userID := pg.UUIDValue(job.UserID)

	if job.Kind == kindPDF {
		errMsg := "not_implemented"
		if err := w.Q.MarkExportJobFailed(ctx, dbgen.MarkExportJobFailedParams{
			ID: job.ID, Error: &errMsg,
		}); err != nil {
			return err
		}
		return nil
	}

	if err := w.buildZIP(ctx, job); err != nil {
		msg := err.Error()
		_ = w.Q.MarkExportJobFailed(ctx, dbgen.MarkExportJobFailedParams{ID: job.ID, Error: &msg})
		return err
	}

	if w.Notifier != nil {
		w.Notifier.ExportComplete(ctx, userID, jobID, job.Kind)
	}
	return nil
}

func (w *Worker) buildZIP(ctx context.Context, job dbgen.ExportJob) error {
	userID := pg.UUIDValue(job.UserID)
	jobID := pg.UUIDValue(job.ID)

	memories, err := w.Q.ListExportableMemories(ctx, job.UserID)
	if err != nil {
		return err
	}

	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for i, mem := range memories {
		entryName := fmt.Sprintf("%04d_%s%s", i+1, pg.UUIDValue(mem.ID).String(), extForContent(mem.ContentType))
		if err := w.addZipEntry(ctx, zw, entryName, mem.MediaKey); err != nil {
			_ = zw.Close()
			return err
		}
		if mem.VoiceKey != nil && *mem.VoiceKey != "" {
			voiceName := fmt.Sprintf("%04d_%s_voice.m4a", i+1, pg.UUIDValue(mem.ID).String())
			if err := w.addZipEntry(ctx, zw, voiceName, *mem.VoiceKey); err != nil {
				_ = zw.Close()
				return err
			}
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}

	key := path.Join(exportKeyPref, userID.String(), jobID.String()+".zip")
	if err := w.Store.Put(ctx, key, "application/zip", bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		return err
	}
	return w.Q.MarkExportJobReady(ctx, dbgen.MarkExportJobReadyParams{
		ID: job.ID, ResultKey: &key,
	})
}

func (w *Worker) addZipEntry(ctx context.Context, zw *zip.Writer, name, objectKey string) error {
	rc, err := w.Store.Open(ctx, objectKey)
	if err != nil {
		return fmt.Errorf("opening %s: %w", objectKey, err)
	}
	defer func() { _ = rc.Close() }()

	fw, err := zw.Create(name)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, rc); err != nil {
		return err
	}
	return nil
}

func extForContent(ct string) string {
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "video"):
		return ".mp4"
	case strings.Contains(ct, "audio"):
		return ".m4a"
	default:
		return ".bin"
	}
}

// OutboxNotifier sends export completion via system_alerts.
type OutboxNotifier struct {
	Svc *notifications.OutboxService
}

func (n *OutboxNotifier) ExportComplete(ctx context.Context, userID, jobID uuid.UUID, kind string) {
	title := "Export ready"
	body := "Your data export is ready to download."
	data := map[string]any{"export_id": jobID.String(), "kind": kind}
	if err := n.Svc.Enqueue(ctx, userID, notifications.CategorySystemAlerts, title, body, data, nil); err != nil {
		slog.Error("exports: enqueue completion notification", "user", userID, "error", err)
	}
}

// ProcessJobAsync runs a single export job in the background.
func (w *Worker) ProcessJobAsync(jobID uuid.UUID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		job, err := w.Q.GetExportJobByID(ctx, pg.UUID(jobID))
		if err != nil {
			slog.Error("exports: loading job", "job", jobID, "error", err)
			return
		}
		if job.Status != "pending" {
			return
		}
		if err := w.processOne(ctx, job); err != nil {
			slog.Error("exports: async job failed", "job", jobID, "error", err)
		}
	}()
}
