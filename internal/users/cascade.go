package users

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/media"
	"memoria-backend/internal/pg"
)

// HardDeleteCascade removes a soft-deleted user's media, memories, memberships,
// and social graph. Called asynchronously after DELETE /v1/me succeeds.
func HardDeleteCascade(ctx context.Context, pool *pgxpool.Pool, q *dbgen.Queries, store *media.Store, userID uuid.UUID) error {
	memberships, err := q.ListActiveAlbumMembershipsForUser(ctx, pg.UUID(userID))
	if err != nil {
		return err
	}
	for _, m := range memberships {
		if err := exitAlbumOnDelete(ctx, q, store, pg.UUIDValue(m.AlbumID), userID); err != nil {
			return err
		}
	}

	rows, err := q.ListAuthoredMemoriesWithMedia(ctx, pg.UUID(userID))
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := deleteMemoryMedia(ctx, q, store, pg.UUIDValue(row.ID), row.MediaKey, row.VoiceKey); err != nil {
			return err
		}
	}

	owned, err := q.ListOwnedMediaKeys(ctx, pg.UUID(userID))
	if err != nil {
		return err
	}
	for _, row := range owned {
		if err := store.Delete(ctx, row.BucketKey); err != nil {
			slog.Error("users: deleting owned media object", "media", pg.UUIDValue(row.ID), "error", err)
		}
		if err := q.DeleteMedia(ctx, row.ID); err != nil {
			return err
		}
	}

	if err := q.DeleteReactionsForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	if err := q.DeleteCommentsForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	if err := q.DeleteFriendshipsForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	if err := q.DeleteBlocksForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	if err := q.DeleteInviteLinkForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	if err := q.DeleteCapsuleMembershipsForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	if err := q.DeleteNotificationPrefsForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	if err := q.DeleteNotificationOutboxForUser(ctx, pg.UUID(userID)); err != nil {
		return err
	}
	return nil
}

func deleteMemoryMedia(ctx context.Context, q *dbgen.Queries, store *media.Store, memoryID uuid.UUID, mediaKey string, voiceKey *string) error {
	if err := store.Delete(ctx, mediaKey); err != nil {
		slog.Error("users: deleting memory media object", "memory", memoryID, "error", err)
	}
	if voiceKey != nil && *voiceKey != "" {
		if err := store.Delete(ctx, *voiceKey); err != nil {
			slog.Error("users: deleting voice media object", "memory", memoryID, "error", err)
		}
	}
	return q.HardDeleteMemory(ctx, pg.UUID(memoryID))
}

func exitAlbumOnDelete(ctx context.Context, q *dbgen.Queries, store *media.Store, albumID, userID uuid.UUID) error {
	rows, err := q.ListMemoriesByAuthorInAlbum(ctx, dbgen.ListMemoriesByAuthorInAlbumParams{
		ContainerID: pg.UUID(albumID),
		AuthorID:    pg.UUID(userID),
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		var voiceKey *string
		if row.VoiceKey != nil {
			voiceKey = row.VoiceKey
		}
		if err := deleteMemoryMedia(ctx, q, store, pg.UUIDValue(row.ID), row.MediaKey, voiceKey); err != nil {
			return err
		}
	}
	if _, err := q.MarkMemberLeft(ctx, dbgen.MarkMemberLeftParams{
		AlbumID: pg.UUID(albumID), UserID: pg.UUID(userID),
	}); err != nil {
		return err
	}
	remaining, err := q.CountActiveMembers(ctx, pg.UUID(albumID))
	if err != nil {
		return err
	}
	if remaining == 0 {
		allRows, err := q.ListAlbumMemoryMediaKeys(ctx, pg.UUID(albumID))
		if err != nil {
			return err
		}
		for _, row := range allRows {
			var voiceKey *string
			if row.VoiceKey != nil {
				voiceKey = row.VoiceKey
			}
			if err := deleteMemoryMedia(ctx, q, store, pg.UUIDValue(row.ID), row.MediaKey, voiceKey); err != nil {
				return err
			}
		}
		if _, err := q.MarkAlbumDeleted(ctx, pg.UUID(albumID)); err != nil {
			return err
		}
	}
	return nil
}

// ScheduleHardDelete runs HardDeleteCascade in the background.
func ScheduleHardDelete(pool *pgxpool.Pool, q *dbgen.Queries, store *media.Store, userID uuid.UUID) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := HardDeleteCascade(ctx, pool, q, store, userID); err != nil {
			slog.Error("users: hard delete cascade failed", "user", userID, "error", err)
		}
	}()
}
