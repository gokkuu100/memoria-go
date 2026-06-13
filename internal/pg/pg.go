// Package pg converts between pgtype values (used by sqlc-generated code) and
// plain Go types used at API boundaries.
package pg

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func UUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func UUIDValue(p pgtype.UUID) uuid.UUID {
	return uuid.UUID(p.Bytes)
}

// UUIDPtr returns nil for NULL uuids, otherwise the string form.
func UUIDPtr(p pgtype.UUID) *string {
	if !p.Valid {
		return nil
	}
	s := uuid.UUID(p.Bytes).String()
	return &s
}

func Time(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func TimeValue(t pgtype.Timestamptz) time.Time {
	return t.Time
}

func Date(t time.Time) pgtype.Date {
	y, m, d := t.Date()
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

func DateValue(d pgtype.Date) time.Time {
	return d.Time
}

// StringSliceFromPG parses PostgreSQL text[] values scanned as interface{}.
func StringSliceFromPG(v any) []string {
	if v == nil {
		return nil
	}
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
