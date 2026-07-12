-- name: HeatmapAlbumContributions :many
SELECT (m.created_at AT TIME ZONE @tz::text)::date AS day,
       COUNT(*)::bigint AS count
FROM memories m
WHERE m.author_id = $1
  AND m.container_type = 'album'
  AND m.deleted_at IS NULL
  AND m.created_at >= sqlc.arg('from_ts')::timestamptz
  AND m.created_at < sqlc.arg('to_ts')::timestamptz
GROUP BY day
ORDER BY day;

-- name: HeatmapCapsuleContributions :many
SELECT sd.day::date AS day,
       COUNT(*)::bigint AS count
FROM streak_days sd
JOIN capsule_members cm ON cm.capsule_id = sd.capsule_id
JOIN memories m ON m.container_type = 'capsule'
  AND m.container_id = sd.capsule_id
  AND m.author_id = $1
  AND m.deleted_at IS NULL
  AND (m.captured_at AT TIME ZONE @tz::text)::date = sd.day
WHERE cm.user_id = $1
  AND cm.invite_status = 'accepted'
  AND sd.day >= sqlc.arg('from_day')::date
  AND sd.day <= sqlc.arg('to_day')::date
GROUP BY sd.day
ORDER BY sd.day;

-- name: CountCapsulesJoined :one
SELECT COUNT(*)::bigint AS count
FROM capsule_members
WHERE user_id = $1 AND invite_status = 'accepted';

-- name: CountAlbumsJoined :one
SELECT COUNT(*)::bigint AS count
FROM album_members
WHERE user_id = $1 AND invite_status = 'accepted';

-- name: CountUserMemoriesByKind :one
SELECT COUNT(*)::bigint AS total,
       COUNT(*) FILTER (WHERE med.kind = 'photo')::bigint AS photos,
       COUNT(*) FILTER (WHERE med.kind = 'video')::bigint AS videos,
       COUNT(*) FILTER (WHERE m.voice_media_id IS NOT NULL)::bigint AS voice_notes
FROM memories m
JOIN media med ON med.id = m.media_id
WHERE m.author_id = $1 AND m.deleted_at IS NULL;

-- name: MostActiveMonth :one
SELECT to_char(date_trunc('month', m.created_at AT TIME ZONE @tz::text), 'YYYY-MM') AS month,
       COUNT(*)::bigint AS count
FROM memories m
WHERE m.author_id = $1 AND m.deleted_at IS NULL
GROUP BY month
ORDER BY count DESC, month DESC
LIMIT 1;
