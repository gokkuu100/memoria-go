package albums

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"memoria-backend/internal/billing"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
)

// ExpireInvites disintegrates pending albums past invite_expires_at.
func ExpireInvites(ctx context.Context, q *dbgen.Queries, notify notifications.Sender) error {
	rows, err := q.ListPendingExpiredAlbums(ctx)
	if err != nil {
		return err
	}
	for _, album := range rows {
		if _, err := q.MarkAlbumDeleted(ctx, album.ID); err != nil {
			slog.Error("albums job: marking expired invite deleted", "album", pg.UUIDValue(album.ID), "error", err)
			continue
		}
		if notify != nil {
			notify.AlbumInviteExpired(ctx, pg.UUIDValue(album.CreatorID), pg.UUIDValue(album.ID), album.Name)
		}
	}
	return nil
}

// RunLifespan archives active albums past the creator's plan lifespan.
func RunLifespan(ctx context.Context, q *dbgen.Queries) error {
	rows, err := q.ListActiveAlbumsWithCreatorPlan(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, row := range rows {
		plan := billing.ForUser(row.CreatorPlan)
		if billing.IsUnlimited(plan.AlbumLifespanDays) {
			continue
		}
		if !row.ActivatedAt.Valid {
			continue
		}
		end := pg.TimeValue(row.ActivatedAt).Add(time.Duration(plan.AlbumLifespanDays) * 24 * time.Hour)
		if now.Before(end) {
			continue
		}
		var archiveUntil pgtype.Timestamptz
		if !billing.IsUnlimited(plan.AlbumArchiveDays) {
			until := now.Add(time.Duration(plan.AlbumArchiveDays) * 24 * time.Hour)
			archiveUntil = pg.Time(until)
		}
		if _, err := q.ArchiveAlbum(ctx, dbgen.ArchiveAlbumParams{
			ID:           row.ID,
			ArchiveUntil: archiveUntil,
		}); err != nil {
			slog.Error("albums job: archiving album", "album", pg.UUIDValue(row.ID), "error", err)
		}
	}
	return nil
}

// ArchiveSweep hard-deletes archived albums past retention and their media.
func ArchiveSweep(ctx context.Context, q *dbgen.Queries, store *media.Store) error {
	rows, err := q.ListArchivedAlbumsPastRetention(ctx)
	if err != nil {
		return err
	}
	for _, album := range rows {
		if err := purgeAlbumMemories(ctx, q, store, pg.UUIDValue(album.ID)); err != nil {
			slog.Error("albums job: purging archived album memories", "album", pg.UUIDValue(album.ID), "error", err)
			continue
		}
		if err := q.HardDeleteAlbum(ctx, album.ID); err != nil {
			slog.Error("albums job: hard deleting archived album", "album", pg.UUIDValue(album.ID), "error", err)
		}
	}
	return nil
}
