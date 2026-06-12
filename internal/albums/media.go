package albums

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/media"
	"memoria-backend/internal/pg"
)

// deleteMemoryObjects removes S3 objects for a memory row and hard-deletes the row.
func deleteMemoryObjects(ctx context.Context, q *dbgen.Queries, store *media.Store, memoryID uuid.UUID, mediaKey string, voiceKey *string) error {
	if err := store.Delete(ctx, mediaKey); err != nil {
		slog.Error("albums: deleting memory media object", "memory", memoryID, "error", err)
	}
	if voiceKey != nil && *voiceKey != "" {
		if err := store.Delete(ctx, *voiceKey); err != nil {
			slog.Error("albums: deleting voice media object", "memory", memoryID, "error", err)
		}
	}
	return q.HardDeleteMemory(ctx, pg.UUID(memoryID))
}

// deleteAuthorMemoriesInAlbum removes all memories (rows + S3) authored by user in album.
func deleteAuthorMemoriesInAlbum(ctx context.Context, q *dbgen.Queries, store *media.Store, albumID, authorID uuid.UUID) error {
	rows, err := q.ListMemoriesByAuthorInAlbum(ctx, dbgen.ListMemoriesByAuthorInAlbumParams{
		ContainerID: pg.UUID(albumID),
		AuthorID:    pg.UUID(authorID),
	})
	if err != nil {
		return err
	}
	for _, row := range rows {
		var voiceKey *string
		if row.VoiceKey != nil {
			voiceKey = row.VoiceKey
		}
		if err := deleteMemoryObjects(ctx, q, store, pg.UUIDValue(row.ID), row.MediaKey, voiceKey); err != nil {
			return err
		}
	}
	return nil
}

// purgeAlbumMemories hard-deletes every memory in an album and its S3 objects.
func purgeAlbumMemories(ctx context.Context, q *dbgen.Queries, store *media.Store, albumID uuid.UUID) error {
	rows, err := q.ListAlbumMemoryMediaKeys(ctx, pg.UUID(albumID))
	if err != nil {
		return err
	}
	for _, row := range rows {
		var voiceKey *string
		if row.VoiceKey != nil {
			voiceKey = row.VoiceKey
		}
		if err := deleteMemoryObjects(ctx, q, store, pg.UUIDValue(row.ID), row.MediaKey, voiceKey); err != nil {
			return err
		}
	}
	return nil
}
