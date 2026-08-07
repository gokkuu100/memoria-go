# Memoria API — Security Review (B11)

> IDOR sweep checklist for every `{id}` / `{userId}` route. Re-run before launch
> and after any new endpoint lands.

## Invariants (non-negotiable)

| Control | Where | Status |
|---|---|---|
| JWT alg pinned HS256 | `internal/auth/token.go` `ParseToken` | ✅ |
| RevenueCat webhook `Authorization` verify | `internal/billing/webhook.go` | ✅ |
| Signed media URL TTL 10 min | `internal/media/store.go` `SignedURLTTL` | ✅ |
| Capsule sealing (no media URLs before unlock) | `internal/capsules/guard.go` + handlers | ✅ |
| Plan limits single source | `internal/billing/plans.go` | ✅ |
| Global rate limit 600/min IP, 900/min user (media streams excluded) | `internal/server/ratelimit.go` (production only); IP from `X-Forwarded-For` | ✅ |
| Auth OTP brute-force limits | `internal/auth/limits.go` + handler | ✅ |

## IDOR checklist — `{id}` routes

| Route | Resource | Membership / ownership check | Notes |
|---|---|---|---|
| `GET /v1/albums/{id}` | album | `loadAlbumMember` | ✅ |
| `POST /v1/albums/{id}/accept` | album invite | `loadAlbumMember` (pending) | ✅ |
| `POST /v1/albums/{id}/decline` | album invite | `loadAlbumMember` | ✅ |
| `POST /v1/albums/{id}/memories` | album | `loadActiveMember` | ✅ |
| `GET /v1/albums/{id}/memories` | album | `loadActiveMember` + sealing N/A | ✅ |
| `POST /v1/albums/{id}/leave` | album | `loadAlbumMember` | ✅ |
| `GET /v1/capsules/{id}` | capsule | `loadCapsuleMember` + sealing guards | ✅ |
| `POST /v1/capsules/{id}/accept` | capsule | `loadCapsuleMember` | ✅ |
| `POST /v1/capsules/{id}/decline` | capsule | `loadCapsuleMember` | ✅ |
| `POST /v1/capsules/{id}/members` | capsule | admin + `loadCapsuleMember` | ✅ |
| `DELETE /v1/capsules/{id}/members/{userId}` | member | admin role | ✅ |
| `POST /v1/capsules/{id}/memories` | capsule | member + sealing write rules | ✅ |
| `GET /v1/capsules/{id}/memories` | capsule | member + unlock/viewability | ✅ |
| `POST /v1/capsules/{id}/unfreeze-vote` | capsule | member | ✅ |
| `POST /v1/capsules/{id}/unblock/{userId}` | capsule | admin | ✅ |
| `GET /v1/capsules/{id}/stats` | capsule | member + unlocked | ✅ |
| `POST /v1/capsules/{id}/leave` | capsule | member | ✅ |
| `DELETE /v1/capsules/{id}` | capsule | creator/admin | ✅ |
| `GET /v1/media/{id}` | media | owner-only (`ownedMedia`) | ✅ 404 for non-owner |
| `POST /v1/media/{id}/confirm` | media | owner-only | ✅ |
| `GET /v1/exports/{id}` | export job | `GetExportJob` scoped by `user_id` | ✅ |
| `POST /v1/notifications/{id}/read` | notification | `MarkNotificationRead` + `user_id` | ✅ |
| `PUT/DELETE /v1/memories/{id}/reactions` | memory | `loadAccessibleMemory` (album/capsule member) | ✅ |
| `GET/POST /v1/memories/{id}/comments` | memory | `loadAccessibleMemory` | ✅ |
| `DELETE /v1/memories/{id}/comments/{commentId}` | comment | memory access + `memory_id` on delete (**B11 fix**) | ✅ fixed |

## IDOR checklist — `{userId}` routes

| Route | Check | Notes |
|---|---|---|
| `POST /v1/friends/requests/{userId}/accept` | incoming request row | ✅ |
| `POST /v1/friends/requests/{userId}/decline` | incoming request row | ✅ |
| `DELETE /v1/friends/requests/{userId}` | outgoing request row | ✅ |
| `DELETE /v1/friends/{userId}` | friendship membership | ✅ |
| `POST/DELETE /v1/blocks/{userId}` | self only (cannot block self) | ✅ |
| `DELETE /v1/capsules/{id}/members/{userId}` | capsule admin | ✅ |
| `POST /v1/capsules/{id}/unblock/{userId}` | capsule admin | ✅ |

## Non-ID routes reviewed

| Route | Risk | Mitigation |
|---|---|---|
| `GET /v1/users/{username}` | profile enumeration | block → 404; no email exposed |
| `GET /v1/users/by-invite/{token}` | token guessing | opaque token; invalid → 404 |
| `POST /v1/webhooks/revenuecat` | forged billing | shared secret header verify |
| `GET /v1/me/*` | cross-user | always `httpx.UserID` from JWT |

## B11 fixes applied

1. **Comment delete scoping** — `DeleteComment` now requires `memory_id` matching the
   path parameter, preventing deletion of the author's comment on another memory
   via a mismatched URL (`internal/social/handler.go`, `db/queries/memories.sql`).

## Production reminders (ops credentials)

- Terminate TLS at reverse proxy; configure trusted `X-Forwarded-For` / `X-Real-IP`
  for accurate IP rate limits (`internal/server/ratelimit.go` helper documented).
- Set strong `JWT_SECRET`, `REVENUECAT_WEBHOOK_SECRET`, R2 keys via env — never in repo.
- Enable `SENTRY_DSN` for error reporting.
- Nightly `pg_dump` → private R2 bucket (`scripts/backup.sh`, `docs/RUNBOOK.md`).
