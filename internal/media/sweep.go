package media

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/pg"
)

// OrphanTTL is how long a pending upload may sit unconfirmed before the sweep
// deletes it (and any bytes the client managed to PUT).
const OrphanTTL = 24 * time.Hour

// SweepOrphans deletes media rows stuck in pending longer than OrphanTTL,
// removing the S3 object first so a failure never strands unreferenced bytes.
func SweepOrphans(ctx context.Context, q *dbgen.Queries, store *Store) error {
	cutoff := time.Now().Add(-OrphanTTL)
	rows, err := q.ListExpiredPendingMedia(ctx, pg.Time(cutoff))
	if err != nil {
		return fmt.Errorf("listing expired pending media: %w", err)
	}
	for _, row := range rows {
		if err := store.Delete(ctx, row.BucketKey); err != nil {
			slog.Error("orphan sweep: deleting object", "key", row.BucketKey, "error", err)
			continue // keep the row so a later sweep retries the object
		}
		if err := q.DeleteMedia(ctx, row.ID); err != nil {
			slog.Error("orphan sweep: deleting row", "id", pg.UUIDValue(row.ID), "error", err)
		}
	}
	if len(rows) > 0 {
		slog.Info("orphan sweep: cleaned pending media", "count", len(rows))
	}
	return nil
}
