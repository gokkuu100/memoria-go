-- name: CreateExportJob :one
INSERT INTO export_jobs (user_id, kind, status)
VALUES ($1, $2, 'pending')
RETURNING *;

-- name: GetExportJob :one
SELECT * FROM export_jobs WHERE id = $1 AND user_id = $2;

-- name: GetExportJobByID :one
SELECT * FROM export_jobs WHERE id = $1;

-- name: ListPendingExportJobs :many
SELECT * FROM export_jobs
WHERE status = 'pending'
ORDER BY created_at
LIMIT $1;

-- name: MarkExportJobProcessing :execrows
UPDATE export_jobs SET status = 'processing' WHERE id = $1 AND status = 'pending';

-- name: MarkExportJobReady :exec
UPDATE export_jobs
SET status = 'ready', result_key = $2, completed_at = now()
WHERE id = $1;

-- name: MarkExportJobFailed :exec
UPDATE export_jobs
SET status = 'failed', error = $2, completed_at = now()
WHERE id = $1;

-- name: ListExportableMemories :many
SELECT m.id,
       m.container_type,
       m.container_id,
       m.caption,
       m.created_at,
       med.bucket_key AS media_key,
       med.kind AS media_kind,
       med.content_type,
       vm.bucket_key AS voice_key
FROM memories m
JOIN media med ON med.id = m.media_id
LEFT JOIN media vm ON vm.id = m.voice_media_id
WHERE m.deleted_at IS NULL
  AND (
    (m.container_type = 'album' AND EXISTS (
        SELECT 1 FROM album_members am
        JOIN albums a ON a.id = am.album_id
        WHERE am.album_id = m.container_id
          AND am.user_id = $1
          AND am.invite_status = 'accepted'
          AND am.left_at IS NULL
          AND a.state IN ('active', 'archived')
    ))
    OR (m.container_type = 'capsule' AND EXISTS (
        SELECT 1 FROM capsule_members cm
        JOIN capsules c ON c.id = cm.capsule_id
        WHERE cm.capsule_id = m.container_id
          AND cm.user_id = $1
          AND cm.invite_status = 'accepted'
          AND cm.view_blocked = false
          AND c.state = 'unlocked'
          AND (c.viewable_until IS NULL OR c.viewable_until > now())
    ))
  )
ORDER BY m.created_at;
