package exports_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"memoria-backend/db"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/exports"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/server"
)

type env struct {
	ts     *httptest.Server
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	store  *media.Store
	worker *exports.Worker
	mailer *capturingMailer
}

var (
	setupOnce sync.Once
	testEnv   *env
	setupErr  error
)

type capturingMailer struct {
	mu   sync.Mutex
	last string
}

func (m *capturingMailer) Send(_ context.Context, _, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last = body
	return nil
}

var codeRe = regexp.MustCompile(`\d{6}`)

func (m *capturingMailer) lastCode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return codeRe.FindString(m.last)
}

func getEnv(t *testing.T) *env {
	t.Helper()
	setupOnce.Do(func() {
		adminURL := os.Getenv("TEST_ADMIN_DATABASE_URL")
		if adminURL == "" {
			adminURL = "postgres://memoria:memoria@localhost:5432/memoria?sslmode=disable"
		}
		admin, err := sql.Open("pgx", adminURL)
		if err != nil {
			setupErr = err
			return
		}
		defer func() { _ = admin.Close() }()
		if err := admin.Ping(); err != nil {
			setupErr = fmt.Errorf("postgres unreachable (run `make up`): %w", err)
			return
		}
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_exports_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_exports_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_exports_test?sslmode=disable"
		mig, err := sql.Open("pgx", testURL)
		if err != nil {
			setupErr = err
			return
		}
		defer func() { _ = mig.Close() }()
		goose.SetBaseFS(db.Migrations)
		goose.SetLogger(goose.NopLogger())
		if err := goose.SetDialect("postgres"); err != nil {
			setupErr = err
			return
		}
		if err := goose.Up(mig, "migrations"); err != nil {
			setupErr = err
			return
		}

		pool, err := pgxpool.New(context.Background(), testURL)
		if err != nil {
			setupErr = err
			return
		}

		cfg := &config.Config{
			Env: config.EnvDevelopment, JWTSecret: "test-secret",
			S3Endpoint: "http://localhost:9000", S3PublicEndpoint: "http://localhost:9000",
			S3AccessKey: "memoria", S3SecretKey: "memoria-secret",
			S3Bucket: "memoria-exports-test", S3UsePathStyle: true,
		}
		store, err := media.NewStore(context.Background(), cfg)
		if err != nil {
			setupErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.EnsureBucket(ctx); err != nil {
			setupErr = fmt.Errorf("minio unreachable (run `make up`): %w", err)
			return
		}

		q := dbgen.New(pool)
		worker := &exports.Worker{Pool: pool, Q: q, Store: store}
		m := &capturingMailer{}
		testEnv = &env{
			ts: httptest.NewServer(server.New(pool, cfg, m, store, &server.Deps{
				Notify:       notifications.LogSender{},
				ExportWorker: worker,
			}).Router()),
			pool:   pool,
			q:      q,
			store:  store,
			worker: worker,
			mailer: m,
		}
	})
	if setupErr != nil {
		t.Skipf("integration environment unavailable: %v", setupErr)
	}
	return testEnv
}

func doJSON(t *testing.T, method, url, bearer string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
	}
	return resp.StatusCode
}

func signUp(t *testing.T, e *env, email, username string) (token, userID string) {
	t.Helper()
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/request", "",
		map[string]string{"email": email, "purpose": "signup"}, nil); status != http.StatusOK {
		t.Fatalf("otp request: status %d", status)
	}
	var verify struct {
		Ticket string `json:"ticket"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/verify", "",
		map[string]string{"email": email, "purpose": "signup", "code": e.mailer.lastCode()}, &verify); status != http.StatusOK {
		t.Fatalf("otp verify: status %d", status)
	}
	var resp struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/signup", "", map[string]string{
		"ticket": verify.Ticket, "password": "supersecret1", "username": username, "display_name": "Export User",
	}, &resp); status != http.StatusCreated {
		t.Fatalf("signup: status %d", status)
	}
	return resp.Tokens.AccessToken, resp.User.ID
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, rand.Int63n(1_000_000_000)) //nolint:gosec
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadPhoto(t *testing.T, e *env, token string) string {
	t.Helper()
	img := tinyPNG(t)
	var pre struct {
		MediaID   string            `json:"media_id"`
		UploadURL string            `json:"upload_url"`
		Headers   map[string]string `json:"headers"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/media/presign", token, map[string]any{
		"kind": "photo", "content_type": "image/png", "byte_size": len(img),
	}, &pre); status != http.StatusOK {
		t.Fatalf("presign: status %d", status)
	}
	put, err := http.NewRequest(http.MethodPut, pre.UploadURL, bytes.NewReader(img))
	if err != nil {
		t.Fatal(err)
	}
	put.Header.Set("Content-Type", "image/png")
	putResp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	_ = putResp.Body.Close()
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("put object: status %d", putResp.StatusCode)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", token, nil, nil); status != http.StatusOK {
		t.Fatalf("confirm: status %d", status)
	}
	return pre.MediaID
}

func TestZIPExportDownloadable(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("expa")+"@example.com", uniq("expa_"))
	tokenB, idB := signUp(t, e, uniq("expb")+"@example.com", uniq("expb_"))

	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", tokenB,
		map[string]string{"user_id": idA}, nil); status != http.StatusCreated {
		t.Fatalf("friend request: status %d", status)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests/"+idB+"/accept", tokenA, nil, nil); status != http.StatusOK {
		t.Fatalf("accept friend: status %d", status)
	}

	var album struct {
		ID string `json:"id"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums", tokenA, map[string]any{
		"name": "Export Album", "cover_style": "espresso", "invited_user_id": idB,
	}, &album); status != http.StatusCreated {
		t.Fatalf("create album: status %d", status)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+album.ID+"/accept", tokenB, nil, nil); status != http.StatusOK {
		t.Fatalf("accept album: status %d", status)
	}

	mediaID := uploadPhoto(t, e, tokenA)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+album.ID+"/memories", tokenA,
		map[string]string{"media_id": mediaID}, nil); status != http.StatusCreated {
		t.Fatalf("add memory: status %d", status)
	}

	var created map[string]any
	if status := doJSON(t, "POST", e.ts.URL+"/v1/exports", tokenA,
		map[string]string{"kind": "zip"}, &created); status != http.StatusAccepted {
		t.Fatalf("create export: status %d", status)
	}
	jobID, _ := created["id"].(string)

	deadline := time.Now().Add(30 * time.Second)
	var downloadURL string
	for time.Now().Before(deadline) {
		var job map[string]any
		status := doJSON(t, "GET", e.ts.URL+"/v1/exports/"+jobID, tokenA, nil, &job)
		if status != http.StatusOK {
			t.Fatalf("get export: status %d", status)
		}
		if job["status"] == "ready" {
			downloadURL, _ = job["download_url"].(string)
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if downloadURL == "" {
		t.Fatal("export did not become ready")
	}

	resp, err := http.Get(downloadURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 22 || body[0] != 'P' || body[1] != 'K' {
		t.Fatalf("expected ZIP bytes, got len=%d", len(body))
	}
}

func TestPDFExportRequiresPro(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("pdf")+"@example.com", uniq("pdf_"))
	if status := doJSON(t, "POST", e.ts.URL+"/v1/exports", token,
		map[string]string{"kind": "pdf"}, nil); status != http.StatusForbidden {
		t.Fatalf("pdf on spark: status %d, want 403", status)
	}
}
