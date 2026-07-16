package analytics

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Metric is one of the 15 product metrics.
type Metric struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Value       *float64 `json:"value"` // nil when undefined (no denominator)
	Unit        string   `json:"unit"`  // ratio | hours | count | seconds
	Numerator   float64  `json:"numerator"`
	Denominator float64  `json:"denominator"`
	Note        string   `json:"note"`
}

// Summary is the founder dashboard payload.
type Summary struct {
	GeneratedAt time.Time `json:"generated_at"`
	LookbackDays int      `json:"lookback_days"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	Metrics     []Metric  `json:"metrics"`
}

func ratio(num, den float64) *float64 {
	if den <= 0 {
		return nil
	}
	v := num / den
	return &v
}

func median(vals []float64) *float64 {
	if len(vals) == 0 {
		return nil
	}
	// copy + sort via insertion for small n (fine for summary windows)
	cp := append([]float64(nil), vals...)
	for i := 1; i < len(cp); i++ {
		j := i
		for j > 0 && cp[j-1] > cp[j] {
			cp[j-1], cp[j] = cp[j], cp[j-1]
			j--
		}
	}
	mid := len(cp) / 2
	var v float64
	if len(cp)%2 == 0 {
		v = (cp[mid-1] + cp[mid]) / 2
	} else {
		v = cp[mid]
	}
	return &v
}

// ComputeSummary calculates the 15 Memoria metrics over the lookback window.
func ComputeSummary(ctx context.Context, pool *pgxpool.Pool, days int) (*Summary, error) {
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -days)
	s := &Summary{
		GeneratedAt:  end,
		LookbackDays: days,
		WindowStart:  start,
		WindowEnd:    end,
		Metrics:      make([]Metric, 0, 15),
	}

	m1, err := metricSignupToFirstCapsule(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m2, err := metricInviteConversion(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m3, err := metricActivatedCapsule(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m4, err := metricDisintegration(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m5, err := metricTimeToFirstMemory(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m6, err := metricPerspectiveDensity(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m7, err := metricMemoriesAtUnlock(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m8, err := metricPreRevealReturn(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m9, err := metricFreezeRate(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m10, err := metricRevealReturn(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m11, err := metricTimeToReveal(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m12, err := metricDiscoveryIndex(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m13, err := metricRevealCompletion(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m14, err := metricRevealDayEngagement(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}
	m15, err := metricRepeatCapsule(ctx, pool, start, end)
	if err != nil {
		return nil, err
	}

	s.Metrics = []Metric{m1, m2, m3, m4, m5, m6, m7, m8, m9, m10, m11, m12, m13, m14, m15}
	return s, nil
}

func metricSignupToFirstCapsule(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
WITH cohort AS (
  SELECT id, created_at FROM users
  WHERE deleted_at IS NULL AND created_at >= $1 AND created_at < $2
),
first_capsule AS (
  SELECT cm.user_id, MIN(COALESCE(cm.accepted_at, c.created_at)) AS first_at
  FROM capsule_members cm
  JOIN capsules c ON c.id = cm.capsule_id
  WHERE cm.invite_status = 'accepted'
  GROUP BY cm.user_id
)
SELECT
  COUNT(*)::float8 AS den,
  COUNT(*) FILTER (
    WHERE fc.first_at IS NOT NULL AND fc.first_at <= cohort.created_at + interval '7 days'
  )::float8 AS num
FROM cohort
LEFT JOIN first_capsule fc ON fc.user_id = cohort.id`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "signup_to_first_capsule_7d", Name: "Signup → First Capsule (7d)",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of new users who create or join a capsule within 7 days of signup",
	}, nil
}

func metricInviteConversion(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT
  COUNT(*) FILTER (WHERE invite_status IN ('pending','accepted','declined'))::float8 AS den,
  COUNT(*) FILTER (WHERE invite_status = 'accepted' AND role = 'member')::float8 AS num
FROM capsule_members cm
JOIN capsules c ON c.id = cm.capsule_id
WHERE c.created_at >= $1 AND c.created_at < $2
  AND cm.role = 'member'`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "invite_conversion", Name: "Invite Conversion Rate",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "accepted member invites / member invites on capsules created in window",
	}, nil
}

func metricActivatedCapsule(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
WITH caps AS (
  SELECT c.id, c.type, c.state,
    (SELECT COUNT(*) FROM memories m
      WHERE m.container_type = 'capsule' AND m.container_id = c.id AND m.deleted_at IS NULL) AS mem_count,
    (SELECT COUNT(*) FROM capsule_members cm
      WHERE cm.capsule_id = c.id AND cm.invite_status = 'accepted') AS member_count,
    (SELECT COUNT(DISTINCT m.author_id) FROM memories m
      WHERE m.container_type = 'capsule' AND m.container_id = c.id AND m.deleted_at IS NULL) AS contributors
  FROM capsules c
  WHERE c.created_at >= $1 AND c.created_at < $2
    AND c.state NOT IN ('disintegrated')
)
SELECT
  COUNT(*)::float8 AS den,
  COUNT(*) FILTER (
    WHERE state IN ('unlocked','archived')
      AND (
        (type = 'solo' AND mem_count >= 10)
        OR (type = 'group' AND member_count >= 2 AND contributors >= 2 AND mem_count >= 5)
      )
  )::float8 AS num
FROM caps`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "activated_capsule_rate", Name: "Activated Capsule Rate",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "solo: unlocked + ≥10 memories; group: unlocked + ≥2 members/contributors + ≥5 memories",
	}, nil
}

func metricDisintegration(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT
  COUNT(*)::float8 AS den,
  COUNT(*) FILTER (WHERE state = 'disintegrated')::float8 AS num
FROM capsules
WHERE created_at >= $1 AND created_at < $2`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "disintegration_rate", Name: "Disintegration Rate",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of created capsules that died before activation (invite expiry etc.)",
	}, nil
}

func metricTimeToFirstMemory(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT EXTRACT(EPOCH FROM (first_mem - accepted_at)) / 3600.0 AS hours
FROM (
  SELECT cm.accepted_at,
    (SELECT MIN(COALESCE(m.captured_at, m.created_at))
     FROM memories m
     WHERE m.container_type = 'capsule' AND m.container_id = cm.capsule_id
       AND m.author_id = cm.user_id AND m.deleted_at IS NULL) AS first_mem
  FROM capsule_members cm
  JOIN capsules c ON c.id = cm.capsule_id
  WHERE cm.invite_status = 'accepted'
    AND cm.accepted_at IS NOT NULL
    AND cm.accepted_at >= $1 AND cm.accepted_at < $2
) t
WHERE first_mem IS NOT NULL AND first_mem >= accepted_at`
	rows, err := pool.Query(ctx, q, start, end)
	if err != nil {
		return Metric{}, err
	}
	defer rows.Close()
	var vals []float64
	for rows.Next() {
		var h float64
		if err := rows.Scan(&h); err != nil {
			return Metric{}, err
		}
		if !math.IsNaN(h) && !math.IsInf(h, 0) && h >= 0 {
			vals = append(vals, h)
		}
	}
	if err := rows.Err(); err != nil {
		return Metric{}, err
	}
	med := median(vals)
	num := float64(len(vals))
	return Metric{
		ID: "time_to_first_memory_hours", Name: "Time to First Memory",
		Value: med, Unit: "hours", Numerator: num, Denominator: num,
		Note: "median hours from invite accept → first capture by that member",
	}, nil
}

func metricPerspectiveDensity(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT AVG(contributors)::float8, COUNT(*)::float8
FROM (
  SELECT COUNT(DISTINCT m.author_id)::float8 AS contributors
  FROM capsules c
  JOIN memories m ON m.container_type = 'capsule' AND m.container_id = c.id AND m.deleted_at IS NULL
  WHERE c.state IN ('unlocked','archived')
    AND c.unlocked_at IS NOT NULL
    AND c.unlocked_at >= $1 AND c.unlocked_at < $2
  GROUP BY c.id
) t`
	var avg *float64
	var den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&avg, &den); err != nil {
		return Metric{}, err
	}
	num := 0.0
	if avg != nil {
		num = *avg * den
	}
	return Metric{
		ID: "perspective_density", Name: "Perspective Density",
		Value: avg, Unit: "count", Numerator: num, Denominator: den,
		Note: "average distinct contributors per unlocked capsule",
	}, nil
}

func metricMemoriesAtUnlock(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT AVG(cnt)::float8, COUNT(*)::float8
FROM (
  SELECT COUNT(*)::float8 AS cnt
  FROM capsules c
  JOIN memories m ON m.container_type = 'capsule' AND m.container_id = c.id AND m.deleted_at IS NULL
  WHERE c.state IN ('unlocked','archived')
    AND c.unlocked_at IS NOT NULL
    AND c.unlocked_at >= $1 AND c.unlocked_at < $2
  GROUP BY c.id
) t`
	var avg *float64
	var den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&avg, &den); err != nil {
		return Metric{}, err
	}
	num := 0.0
	if avg != nil {
		num = *avg * den
	}
	return Metric{
		ID: "memories_per_capsule_at_unlock", Name: "Memories per Capsule at Unlock",
		Value: avg, Unit: "count", Numerator: num, Denominator: den,
		Note: "average memory count on capsules unlocked in window",
	}, nil
}

func metricPreRevealReturn(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
WITH unlocked AS (
  SELECT c.id, c.unlocked_at
  FROM capsules c
  WHERE c.unlocked_at IS NOT NULL
    AND c.unlocked_at >= $1 AND c.unlocked_at < $2
),
members AS (
  SELECT cm.user_id, u.id AS capsule_id, u.unlocked_at
  FROM unlocked u
  JOIN capsule_members cm ON cm.capsule_id = u.id AND cm.invite_status = 'accepted'
)
SELECT
  COUNT(*)::float8 AS den,
  COUNT(*) FILTER (
    WHERE EXISTS (
      SELECT 1 FROM analytics_events ae
      WHERE ae.event_name = 'capsule_opened'
        AND ae.user_id = members.user_id
        AND ae.properties->>'capsule_id' = members.capsule_id::text
        AND ae.occurred_at < members.unlocked_at
    )
  )::float8 AS num
FROM members`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "pre_reveal_return_rate", Name: "Pre-Reveal Return Rate",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of members who opened the capsule at least once before unlock",
	}, nil
}

func metricFreezeRate(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT
  COUNT(*) FILTER (WHERE state IN ('active','frozen','unlocked','archived') OR frozen_at IS NOT NULL)::float8 AS den,
  COUNT(*) FILTER (WHERE frozen_at IS NOT NULL)::float8 AS num
FROM capsules
WHERE created_at >= $1 AND created_at < $2
  AND state NOT IN ('pending','disintegrated')`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "freeze_rate", Name: "Freeze Rate",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of capsules that reached active (or beyond) and froze before unlock",
	}, nil
}

func metricRevealReturn(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
WITH unlocked AS (
  SELECT c.id, c.unlocked_at
  FROM capsules c
  WHERE c.unlocked_at IS NOT NULL
    AND c.unlocked_at >= $1 AND c.unlocked_at < $2
),
members AS (
  SELECT cm.user_id, u.id AS capsule_id, u.unlocked_at, cm.unlock_reveal_seen_at
  FROM unlocked u
  JOIN capsule_members cm ON cm.capsule_id = u.id AND cm.invite_status = 'accepted'
)
SELECT
  COUNT(*)::float8 AS den,
  COUNT(*) FILTER (
    WHERE (unlock_reveal_seen_at IS NOT NULL AND unlock_reveal_seen_at <= unlocked_at + interval '24 hours')
       OR EXISTS (
         SELECT 1 FROM analytics_events ae
         WHERE ae.event_name IN ('capsule_opened','unlock_story_completed','unlock_story_slide')
           AND ae.user_id = members.user_id
           AND ae.properties->>'capsule_id' = members.capsule_id::text
           AND ae.occurred_at >= members.unlocked_at
           AND ae.occurred_at <= members.unlocked_at + interval '24 hours'
       )
  )::float8 AS num
FROM members`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "reveal_return_rate_24h", Name: "Reveal Return Rate (24h)",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of members who open/reveal within 24h of unlock",
	}, nil
}

func metricTimeToReveal(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
WITH unlocked AS (
  SELECT c.id, c.unlocked_at
  FROM capsules c
  WHERE c.unlocked_at IS NOT NULL
    AND c.unlocked_at >= $1 AND c.unlocked_at < $2
),
members AS (
  SELECT cm.user_id, u.id AS capsule_id, u.unlocked_at, cm.unlock_reveal_seen_at
  FROM unlocked u
  JOIN capsule_members cm ON cm.capsule_id = u.id AND cm.invite_status = 'accepted'
),
first_open AS (
  SELECT m.user_id, m.capsule_id, m.unlocked_at,
    LEAST(
      m.unlock_reveal_seen_at,
      (SELECT MIN(ae.occurred_at) FROM analytics_events ae
       WHERE ae.user_id = m.user_id
         AND ae.event_name IN ('capsule_opened','unlock_story_slide','unlock_story_completed')
         AND ae.properties->>'capsule_id' = m.capsule_id::text
         AND ae.occurred_at >= m.unlocked_at)
    ) AS first_at
  FROM members m
)
SELECT EXTRACT(EPOCH FROM (first_at - unlocked_at)) AS secs
FROM first_open
WHERE first_at IS NOT NULL AND first_at >= unlocked_at`
	rows, err := pool.Query(ctx, q, start, end)
	if err != nil {
		return Metric{}, err
	}
	defer rows.Close()
	var vals []float64
	for rows.Next() {
		var s float64
		if err := rows.Scan(&s); err != nil {
			return Metric{}, err
		}
		if s >= 0 {
			vals = append(vals, s)
		}
	}
	if err := rows.Err(); err != nil {
		return Metric{}, err
	}
	med := median(vals)
	n := float64(len(vals))
	return Metric{
		ID: "time_to_reveal_seconds", Name: "Time-to-Reveal",
		Value: med, Unit: "seconds", Numerator: n, Denominator: n,
		Note: "median seconds from unlock → first open / reveal",
	}, nil
}

func metricDiscoveryIndex(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT
  COUNT(*)::float8 AS den,
  COUNT(*) FILTER (
    WHERE COALESCE((properties->>'is_own')::boolean, false) = false
  )::float8 AS num
FROM analytics_events
WHERE event_name = 'memory_viewed'
  AND occurred_at >= $1 AND occurred_at < $2`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "discovery_index", Name: "Discovery Index",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of viewed memories that were not uploaded by the viewer",
	}, nil
}

func metricRevealCompletion(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
SELECT
  COUNT(*)::float8 AS den,
  COUNT(*) FILTER (WHERE cm.unlock_reveal_seen_at IS NOT NULL)::float8 AS num
FROM capsules c
JOIN capsule_members cm ON cm.capsule_id = c.id AND cm.invite_status = 'accepted'
WHERE c.unlocked_at IS NOT NULL
  AND c.unlocked_at >= $1 AND c.unlocked_at < $2
  AND COALESCE(cm.view_blocked, false) = false`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "reveal_completion_rate", Name: "Reveal Completion Rate",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of eligible members who finished the UnlockStory flow",
	}, nil
}

func metricRevealDayEngagement(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	const q = `
WITH unlocked AS (
  SELECT c.id, c.unlocked_at
  FROM capsules c
  WHERE c.unlocked_at IS NOT NULL
    AND c.unlocked_at >= $1 AND c.unlocked_at < $2
),
members AS (
  SELECT cm.user_id, u.id AS capsule_id, u.unlocked_at
  FROM unlocked u
  JOIN capsule_members cm ON cm.capsule_id = u.id AND cm.invite_status = 'accepted'
),
eng AS (
  SELECT m.user_id, m.capsule_id,
    (
      SELECT COUNT(*) FROM reactions r
      JOIN memories mem ON mem.id = r.memory_id
      WHERE mem.container_type = 'capsule' AND mem.container_id = m.capsule_id
        AND r.user_id = m.user_id
        AND r.created_at >= m.unlocked_at
        AND r.created_at < m.unlocked_at + interval '1 day'
    ) + (
      SELECT COUNT(*) FROM comments cmt
      JOIN memories mem ON mem.id = cmt.memory_id
      WHERE mem.container_type = 'capsule' AND mem.container_id = m.capsule_id
        AND cmt.author_id = m.user_id
        AND cmt.created_at >= m.unlocked_at
        AND cmt.created_at < m.unlocked_at + interval '1 day'
    ) AS actions
  FROM members m
)
SELECT AVG(actions)::float8, COUNT(*)::float8 FROM eng`
	var avg *float64
	var den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&avg, &den); err != nil {
		return Metric{}, err
	}
	num := 0.0
	if avg != nil {
		num = *avg * den
	}
	return Metric{
		ID: "reveal_day_engagement", Name: "Reveal Day Engagement",
		Value: avg, Unit: "count", Numerator: num, Denominator: den,
		Note: "avg reactions+comments per member on unlock day",
	}, nil
}

func metricRepeatCapsule(ctx context.Context, pool *pgxpool.Pool, start, end time.Time) (Metric, error) {
	// Users whose first accepted membership in window is followed by a 2nd within 90 days.
	const q = `
WITH firsts AS (
  SELECT cm.user_id, MIN(COALESCE(cm.accepted_at, c.created_at)) AS first_at
  FROM capsule_members cm
  JOIN capsules c ON c.id = cm.capsule_id
  WHERE cm.invite_status = 'accepted'
  GROUP BY cm.user_id
  HAVING MIN(COALESCE(cm.accepted_at, c.created_at)) >= $1
     AND MIN(COALESCE(cm.accepted_at, c.created_at)) < $2
),
seconds AS (
  SELECT f.user_id
  FROM firsts f
  WHERE EXISTS (
    SELECT 1 FROM capsule_members cm2
    JOIN capsules c2 ON c2.id = cm2.capsule_id
    WHERE cm2.user_id = f.user_id AND cm2.invite_status = 'accepted'
      AND COALESCE(cm2.accepted_at, c2.created_at) > f.first_at
      AND COALESCE(cm2.accepted_at, c2.created_at) <= f.first_at + interval '90 days'
  )
)
SELECT
  (SELECT COUNT(*)::float8 FROM firsts) AS den,
  (SELECT COUNT(*)::float8 FROM seconds) AS num`
	var num, den float64
	if err := pool.QueryRow(ctx, q, start, end).Scan(&den, &num); err != nil {
		return Metric{}, err
	}
	return Metric{
		ID: "repeat_capsule_rate_90d", Name: "Repeat Capsule Rate (90d)",
		Value: ratio(num, den), Unit: "ratio", Numerator: num, Denominator: den,
		Note: "% of users who join/create a 2nd capsule within 90 days of their first",
	}, nil
}
