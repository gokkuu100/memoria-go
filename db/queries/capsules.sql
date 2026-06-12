-- name: CreateCapsule :one
INSERT INTO capsules (
    creator_id, name, description, type, state, unlock_at, invite_expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetCapsuleByID :one
SELECT * FROM capsules WHERE id = $1;

-- name: AddCapsuleMember :exec
INSERT INTO capsule_members (capsule_id, user_id, role, invite_status, accepted_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetCapsuleMember :one
SELECT * FROM capsule_members WHERE capsule_id = $1 AND user_id = $2;

-- name: AcceptCapsuleInvite :one
UPDATE capsule_members
SET invite_status = 'accepted', accepted_at = now()
WHERE capsule_id = $1 AND user_id = $2 AND invite_status = 'pending'
RETURNING *;

-- name: DeclineCapsuleInvite :one
UPDATE capsule_members
SET invite_status = 'declined'
WHERE capsule_id = $1 AND user_id = $2 AND invite_status = 'pending'
RETURNING *;

-- name: RemoveCapsuleMember :exec
DELETE FROM capsule_members WHERE capsule_id = $1 AND user_id = $2;

-- name: ActivateCapsule :one
UPDATE capsules
SET state = 'active'
WHERE id = $1 AND state = 'pending'
RETURNING *;

-- name: MarkCapsuleDisintegrated :one
UPDATE capsules SET state = 'disintegrated' WHERE id = $1 RETURNING *;

-- name: CountActiveCapsulesForUser :one
SELECT COUNT(*)::bigint AS count
FROM capsule_members cm
JOIN capsules c ON c.id = cm.capsule_id
WHERE cm.user_id = $1
  AND cm.invite_status = 'accepted'
  AND c.state IN ('pending', 'active', 'frozen');

-- name: CountAcceptedCapsuleMembers :one
SELECT COUNT(*)::bigint AS count
FROM capsule_members
WHERE capsule_id = $1 AND invite_status = 'accepted';

-- name: CountPendingCapsuleInvites :one
SELECT COUNT(*)::bigint AS count
FROM capsule_members
WHERE capsule_id = $1 AND invite_status = 'pending';

-- name: ListCapsuleMembers :many
SELECT cm.*, u.username, u.display_name, u.avatar_media_id
FROM capsule_members cm
JOIN users u ON u.id = cm.user_id
WHERE cm.capsule_id = $1 AND u.deleted_at IS NULL
ORDER BY cm.accepted_at NULLS LAST, cm.user_id;

-- name: ListCapsulesForUser :many
SELECT c.*
FROM capsules c
JOIN capsule_members cm ON cm.capsule_id = c.id
WHERE cm.user_id = $1
  AND cm.invite_status = 'accepted'
  AND (
    (sqlc.arg('filter') = 'ongoing' AND c.state IN ('pending', 'active'))
    OR (sqlc.arg('filter') = 'frozen' AND c.state = 'frozen')
    OR (sqlc.arg('filter') = 'completed' AND c.state = 'unlocked'
        AND (c.viewable_until IS NULL OR c.viewable_until > now()))
  )
ORDER BY c.unlock_at ASC;

-- name: ListPendingExpiredCapsules :many
SELECT * FROM capsules
WHERE state = 'pending' AND invite_expires_at IS NOT NULL AND invite_expires_at <= now()
ORDER BY invite_expires_at
LIMIT 100;

-- name: UpdateCapsuleLastContribution :exec
UPDATE capsules
SET last_contribution_at = $2, streak_current = $3
WHERE id = $1;

-- name: UpsertStreakDay :exec
INSERT INTO streak_days (capsule_id, day, contributor_count)
VALUES ($1, $2, 1)
ON CONFLICT (capsule_id, day) DO UPDATE
SET contributor_count = streak_days.contributor_count + 1;

-- name: GetStreakDay :one
SELECT * FROM streak_days WHERE capsule_id = $1 AND day = $2;

-- name: HasStreakDay :one
SELECT EXISTS(
    SELECT 1 FROM streak_days WHERE capsule_id = $1 AND day = $2
)::boolean AS exists;

-- name: SetCapsuleStreakPerfect :exec
UPDATE capsules SET streak_perfect = $2 WHERE id = $1;

-- name: ListCapsulesNeedingFreezeWarning :many
SELECT c.* FROM capsules c
WHERE c.state = 'active'
  AND c.last_contribution_at IS NOT NULL
  AND c.last_contribution_at <= now() - interval '5 days'
  AND c.last_contribution_at > now() - interval '7 days'
  AND c.freeze_warning_sent = false
ORDER BY c.last_contribution_at
LIMIT 100;

-- name: MarkFreezeWarningSent :exec
UPDATE capsules SET freeze_warning_sent = true WHERE id = $1;

-- name: ListCapsulesToFreeze :many
SELECT * FROM capsules
WHERE state = 'active'
  AND (
    (last_contribution_at IS NOT NULL AND last_contribution_at <= now() - interval '7 days')
    OR (last_contribution_at IS NULL AND created_at <= now() - interval '7 days')
  )
ORDER BY COALESCE(last_contribution_at, created_at)
LIMIT 100;

-- name: FreezeCapsule :one
UPDATE capsules
SET state = 'frozen', frozen_at = now(), streak_perfect = false
WHERE id = $1 AND state = 'active'
RETURNING *;

-- name: AddUnfreezeVote :exec
INSERT INTO capsule_unfreeze_votes (capsule_id, user_id)
VALUES ($1, $2)
ON CONFLICT (capsule_id, user_id) DO NOTHING;

-- name: CountUnfreezeVotes :one
SELECT COUNT(*)::bigint AS count
FROM capsule_unfreeze_votes WHERE capsule_id = $1;

-- name: ClearUnfreezeVotes :exec
DELETE FROM capsule_unfreeze_votes WHERE capsule_id = $1;

-- name: UnfreezeCapsule :one
UPDATE capsules
SET state = 'active', frozen_at = NULL, streak_current = 0, freeze_warning_sent = false
WHERE id = $1 AND state = 'frozen'
RETURNING *;

-- name: ListCapsulesDueUnlock :many
SELECT c.*, u.plan AS creator_plan
FROM capsules c
JOIN users u ON u.id = c.creator_id
WHERE c.state IN ('active', 'frozen')
  AND c.unlock_at <= now()
ORDER BY c.unlock_at
LIMIT 100;

-- name: UnlockCapsule :one
UPDATE capsules
SET state = 'unlocked', unlocked_at = now(), viewable_until = $2
WHERE id = $1 AND state IN ('active', 'frozen')
RETURNING *;

-- name: SetCapsuleMemberViewBlocked :exec
UPDATE capsule_members
SET view_blocked = $3
WHERE capsule_id = $1 AND user_id = $2;

-- name: SetAllNonAdminViewBlocked :exec
UPDATE capsule_members
SET view_blocked = true
WHERE capsule_id = $1
  AND role <> 'admin'
  AND invite_status = 'accepted';

-- name: UnblockCapsuleMember :one
UPDATE capsule_members
SET view_blocked = false
WHERE capsule_id = $1 AND user_id = $2 AND view_blocked = true
RETURNING *;

-- name: TransferCapsuleAdmin :exec
UPDATE capsule_members SET role = 'admin'
WHERE capsule_id = $1 AND user_id = $2;

-- name: DemoteCapsuleAdmin :exec
UPDATE capsule_members SET role = 'member'
WHERE capsule_id = $1 AND user_id = $2;

-- name: UpdateCapsuleCreator :exec
UPDATE capsules SET creator_id = $2 WHERE id = $1;

-- name: ListArchivedCapsulesPastViewable :many
SELECT * FROM capsules
WHERE state = 'unlocked'
  AND viewable_until IS NOT NULL
  AND viewable_until <= now()
ORDER BY viewable_until
LIMIT 100;

-- name: ArchiveCapsule :one
UPDATE capsules SET state = 'archived' WHERE id = $1 AND state = 'unlocked' RETURNING *;

-- name: RestampCapsuleViewableUntil :exec
UPDATE capsules
SET viewable_until = $2
WHERE id = $1 AND state = 'unlocked'
  AND (viewable_until IS NULL OR viewable_until > now());

-- name: RestampCreatorCapsulesViewable :exec
UPDATE capsules c
SET viewable_until = CASE
    WHEN $2::int = -1 THEN NULL
    ELSE COALESCE(c.unlocked_at, now()) + ($2 || ' days')::interval
END
FROM users u
WHERE c.creator_id = u.id
  AND u.id = $1
  AND c.state = 'unlocked'
  AND (c.viewable_until IS NULL OR c.viewable_until > now());

-- name: GetLastCapsuleContributor :one
SELECT cm.user_id, u.username, u.display_name, c.last_contribution_at
FROM capsules c
JOIN capsule_members cm ON cm.capsule_id = c.id AND cm.invite_status = 'accepted'
JOIN users u ON u.id = cm.user_id
WHERE c.id = $1
  AND c.last_contribution_at IS NOT NULL
ORDER BY c.last_contribution_at DESC
LIMIT 1;

-- name: ListCapsuleMemberUserIDs :many
SELECT user_id FROM capsule_members
WHERE capsule_id = $1 AND invite_status = 'accepted';

-- name: ListCapsulesOwnedByUser :many
SELECT * FROM capsules WHERE creator_id = $1 AND state NOT IN ('disintegrated', 'archived');

-- name: GetNextCapsuleAdminCandidate :one
SELECT cm.*
FROM capsule_members cm
WHERE cm.capsule_id = $1
  AND cm.user_id <> $2
  AND cm.invite_status = 'accepted'
ORDER BY cm.accepted_at NULLS LAST, cm.user_id
LIMIT 1;

-- name: GetMemberLastCapsuleContribution :one
SELECT created_at FROM memories
WHERE container_type = 'capsule'
  AND container_id = $1
  AND author_id = $2
  AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: CountMemoriesByAuthorInCapsule :one
SELECT COUNT(*)::bigint AS count
FROM memories
WHERE container_type = 'capsule'
  AND container_id = $1
  AND author_id = $2
  AND deleted_at IS NULL;

-- name: CountCapsuleMemories :one
SELECT COUNT(*)::bigint AS count
FROM memories
WHERE container_type = 'capsule'
  AND container_id = $1
  AND deleted_at IS NULL;

-- name: ListCapsuleMemories :many
SELECT m.*,
       (SELECT COUNT(*)::bigint FROM reactions r WHERE r.memory_id = m.id) AS reaction_count,
       (SELECT COUNT(*)::bigint FROM comments c WHERE c.memory_id = m.id) AS comment_count
FROM memories m
WHERE m.container_type = 'capsule'
  AND m.container_id = $1
  AND m.deleted_at IS NULL
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (m.created_at, m.id) > (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY m.created_at ASC, m.id ASC
LIMIT sqlc.arg('page_limit');

-- name: GetCapsuleUnlockStats :one
SELECT
    COUNT(*)::bigint AS total_memories,
    COUNT(*) FILTER (WHERE med.kind = 'photo')::bigint AS photo_count,
    COUNT(*) FILTER (WHERE med.kind = 'video')::bigint AS video_count,
    COUNT(*) FILTER (WHERE m.voice_media_id IS NOT NULL)::bigint AS voice_count
FROM memories m
JOIN media med ON med.id = m.media_id
WHERE m.container_type = 'capsule'
  AND m.container_id = $1
  AND m.deleted_at IS NULL;

-- name: GetTopCapsuleContributor :one
SELECT m.author_id, u.username, u.display_name, COUNT(*)::bigint AS memory_count
FROM memories m
JOIN users u ON u.id = m.author_id
WHERE m.container_type = 'capsule'
  AND m.container_id = $1
  AND m.deleted_at IS NULL
GROUP BY m.author_id, u.username, u.display_name
ORDER BY memory_count DESC
LIMIT 1;

-- name: GetMostReactedCapsuleMemory :one
SELECT m.id, COUNT(r.*)::bigint AS reaction_count
FROM memories m
LEFT JOIN reactions r ON r.memory_id = m.id
WHERE m.container_type = 'capsule'
  AND m.container_id = $1
  AND m.deleted_at IS NULL
GROUP BY m.id
ORDER BY reaction_count DESC
LIMIT 1;

-- name: ListTimelineCapsuleSealedMarkers :many
SELECT c.id AS capsule_id, c.name AS capsule_name, sd.day AS contribution_day
FROM streak_days sd
JOIN capsules c ON c.id = sd.capsule_id
JOIN capsule_members cm ON cm.capsule_id = c.id AND cm.user_id = $1
WHERE cm.invite_status = 'accepted'
  AND c.state IN ('active', 'frozen', 'pending')
  AND sd.day >= sqlc.arg('start_day')::date
  AND sd.day < sqlc.arg('end_day')::date
ORDER BY sd.day;

-- name: ListTimelineCapsuleCountdowns :many
SELECT c.id AS capsule_id, c.name AS capsule_name, c.unlock_at
FROM capsules c
JOIN capsule_members cm ON cm.capsule_id = c.id AND cm.user_id = $1
WHERE cm.invite_status = 'accepted'
  AND c.state IN ('active', 'frozen', 'pending')
  AND c.unlock_at >= sqlc.arg('range_start')
  AND c.unlock_at < sqlc.arg('range_end')
ORDER BY c.unlock_at;

-- name: ListTimelineUnlockedCapsuleMemories :many
SELECT m.id, m.container_id AS capsule_id, m.created_at,
       med.bucket_key AS media_key, c.name AS capsule_name
FROM memories m
JOIN media med ON med.id = m.media_id
JOIN capsules c ON c.id = m.container_id
JOIN capsule_members cm ON cm.capsule_id = c.id AND cm.user_id = $1
WHERE m.container_type = 'capsule'
  AND m.deleted_at IS NULL
  AND cm.invite_status = 'accepted'
  AND cm.view_blocked = false
  AND c.state = 'unlocked'
  AND (c.viewable_until IS NULL OR c.viewable_until > now())
  AND m.created_at >= $2
  AND m.created_at < $3
ORDER BY m.created_at;
