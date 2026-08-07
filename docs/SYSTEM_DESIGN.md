# Memoria — System Design

> Source of truth for architecture decisions. Read `project.md` for the full product spec.
> Update this file whenever an architectural decision changes.

## 1. Overview

Memoria is a private photo/video sharing app built on two experiences: **Capsules**
(time-locked shared containers) and **Albums** (live two-person shared spaces).
Media is captured in-app only, delivered through a CDN, and capsule contents are
**server-enforced invisible** until the unlock date.

```
┌─────────────────────────────────────────────────────────┐
│  Expo App (iOS + Android)  +  Widget extensions         │
│  expo-router · React Query · expo-camera · Reanimated   │
└──────┬───────────────┬────────────────────┬─────────────┘
       │ REST/JSON     │ direct media       │ push (silent + alert)
       │ (JWT)         │ upload/download    │
       ▼               ▼ (presigned URLs)   ▼
┌───────────────┐  ┌──────────────┐  ┌────────────────────┐
│ Go API server │  │ Cloudflare   │  │ Expo Push Service  │
│ (one binary)  │  │ R2 + CDN     │  │  → APNs / FCM      │
│ - REST API    │  │ private      │  └────────────────────┘
│ - cron jobs   │  │ buckets      │  ┌────────────────────┐
│ - batch queue │  └──────────────┘  │ RevenueCat webhook │
└──────┬────────┘                    └────────────────────┘
       ▼
┌───────────────┐   ┌──────────────┐
│ PostgreSQL    │   │ Resend (OTP  │
│               │   │ emails)      │
└───────────────┘   └──────────────┘
```

## 2. Repositories / folders

| Path | What | Stack |
|---|---|---|
| `memoria/` | Mobile app | Expo SDK 54, expo-router, TypeScript |
| `memoria-gobackend/` | API server | Go, Postgres, Docker, Makefile |
| `docs/` | Plans + this doc | Markdown |
| `project.md` | Product spec (source of truth for features) | — |

## 3. Backend architecture (Go)

### Layout

```
memoria-gobackend/
├── cmd/api/main.go            # entrypoint: config, DI, server, cron
├── internal/
│   ├── config/                # env-based config
│   ├── server/                # router, middleware (auth, ratelimit, logging, recovery)
│   ├── auth/                  # OTP, passwords (argon2id), JWT access+refresh
│   ├── users/                 # profiles, usernames, retired usernames
│   ├── friends/               # requests, invite links/QR, block
│   ├── media/                 # presign upload/download, validation, R2 client
│   ├── capsules/              # state machine, members, contributions, streaks, votes
│   ├── albums/                # lifecycle, members, contributions
│   ├── social/                # reactions, comments (shared by capsules+albums)
│   ├── notifications/         # prefs, batching, Expo push sender
│   ├── billing/               # RevenueCat webhook, plans, limit checks
│   ├── stats/                 # heatmap, lifetime stats, unlock stats
│   ├── exports/               # ZIP (and later PDF) export jobs
│   └── jobs/                  # cron scheduler + job implementations
├── db/
│   ├── migrations/            # goose migrations (sequential SQL)
│   └── queries/               # sqlc query files
├── Dockerfile                 # multi-stage, distroless final image
├── docker-compose.yml         # api + postgres + minio (local R2)
├── Makefile
└── sqlc.yaml
```

### Key choices

- **HTTP**: `chi` router, standard `net/http`. JSON REST, versioned under `/v1`.
- **DB access**: `sqlc` (type-safe generated Go from raw SQL) over `pgx`.
- **Migrations**: `goose`, run via Makefile and on container start.
- **Auth**: argon2id password hashes; JWT access tokens (15 min) + rotating
  refresh tokens (stored hashed in `sessions` table, per device).
- **Scheduled jobs**: in-process cron (`robfig/cron`) — see §7. The job runner
  (`internal/jobs`) wraps every job in `pg_try_advisory_lock`, so jobs are
  already safe to run from multiple API instances; held locks skip the run.
- **Local dev**: docker-compose runs Postgres + MinIO (S3-compatible, emulates R2).
- **Email**: Gmail SMTP (app password, STARTTLS :587) for OTP codes; dev mode
  without SMTP config logs the email instead. Swap to a transactional provider
  later only if Gmail's sending limits become a problem.
- **Push**: Expo Push Service (one API for APNs+FCM). Silent pushes for widgets.
- **Payments**: RevenueCat; server only consumes its webhook + verifies entitlements.

## 4. Data model (core tables)

```
users(id, email, username, display_name, avatar_media_id, password_hash,
      timezone, plan, plan_expires_at, created_at, deleted_at)
retired_usernames(username, retired_at)              -- permanent, checked on signup
sessions(id, user_id, refresh_token_hash, device_name, expires_at)
otp_codes(id, email, code_hash, purpose, expires_at, attempts)
push_tokens(user_id, expo_token, platform, updated_at)

friendships(user_a, user_b, status: pending|accepted, requested_by, created_at)
blocks(blocker_id, blocked_id, created_at)
invite_links(token, user_id, created_at)             -- QR / profile share links

media(id, owner_id, kind: photo|video|voice, bucket_key, content_type,
      duration_ms, width, height, byte_size, status: pending|ready, created_at)

capsules(id, creator_id, name, description, type: solo|group,
         state: pending|active|frozen|unlocked|archived|disintegrated,
         unlock_at, invite_expires_at, frozen_at, unlocked_at,
         viewable_until,            -- stamped at unlock from creator plan; NULL = forever
         streak_current, streak_perfect, last_contribution_at, created_at)
capsule_members(capsule_id, user_id, role: admin|member,
                invite_status: pending|accepted|declined, accepted_at,
                view_blocked bool,  -- frozen-at-unlock blocking
                unlock_reveal_seen_at)  -- first unlock-story view per member
capsule_unfreeze_votes(capsule_id, user_id, voted_at)
streak_days(capsule_id, day, contributor_count)      -- for stats + heatmap

albums(id, creator_id, name, cover_style, state: pending|active|archived|deleted,
       activated_at, archive_until, invite_expires_at, created_at)
album_members(album_id, user_id, invite_status, accepted_at, left_at)

memories(id, container_type: capsule|album, container_id, author_id,
         media_id, voice_media_id, caption, created_at, captured_at,
         deleted_at)

memory_idempotency(user_id, idempotency_key, capsule_id, memory_id, created_at)
-- offline capsule sync: client queue UUID → one memory row (safe retries)

reactions(memory_id, user_id, emoji, created_at)
comments(id, memory_id, author_id, body, created_at)

notification_prefs(user_id, category, enabled)
notification_outbox(id, user_id, category, title, body, data, batch_key,
                    deliver_after, sent_at, read_at, created_at)  -- outbox + in-app feed
subscription_events(id, user_id, revenuecat_event, plan, raw, created_at)
export_jobs(id, user_id, kind: zip|pdf, status, result_key, error, created_at, completed_at)
```

### Capsule state machine

```
            all invites accepted              7 days no posts
 pending ──────────────────────► active ◄──────────────────► frozen
    │                              │        all members vote      │
    │ 48h invite window expires    │ unlock_at reached            │ unlock_at reached
    ▼                              ▼                              ▼ (creator-only view,
 disintegrated                 unlocked ──────────────────► archived   view_blocked=true
                                          viewable_until            for others)
```

Solo capsules skip `pending` and go straight to `active`.

## 5. Media pipeline & the sealing guarantee

**This is the most important security property in the system.**

1. **Upload** (implemented in B2 as a container-independent core): client calls
   `POST /v1/media/presign` with kind (photo|video|voice), content type, byte
   size, and duration for video/voice → server validates a per-kind content-type
   allowlist and the caller's plan caps (byte size, duration) from
   `billing/plans.go` → returns a presigned PUT URL (10 min, Content-Type
   signed) + a `media` row in `pending` status (key `media/{userID}/{uuid}`) →
   client uploads bytes directly → `POST /v1/media/{id}/confirm` HEADs the
   object, verifies real size against the declaration and plan caps (violation
   deletes object + row), decodes photo dimensions server-side, and marks
   `ready`. Memory creation (B5/B6) attaches ready media to a container after
   validating membership, container state, and count caps. Duration is
   client-declared but plan-capped (no server-side probing without ffmpeg).
   Local MinIO note: presigned URLs are signed against `S3_PUBLIC_ENDPOINT`
   (the host clients reach) while server-side S3 calls use `S3_ENDPOINT`.
2. **Download**: media bucket is **private**. The only way to view a memory is
   `GET /v1/.../memories` which returns short-lived (10 min) signed GET URLs —
   and the handler refuses to return URLs for capsule memories unless
   `capsule.state = unlocked`, the requester is a member, `view_blocked = false`,
   and `now() < viewable_until` (or the requester's access rule for frozen-unlock applies).
3. Before unlock, members may read **counts and contributor metadata only**
   (spec: "Sarah added a memory 2 hours ago") — never media rows or keys.
4. Plan limits on capture (3s boomerang / 20s / 30s video, voice note lengths) are validated
   server-side at presign time and re-checked at confirm time using actual metadata.

## 6. Plans & enforcement

All limits live in one Go table-driven config (`billing/plans.go`):

| Limit | Spark (free) | Plus | Pro |
|---|---|---|---|
| Active capsules | 2 | 5 | ∞ |
| People per capsule | 5 | 10 | 20 |
| Max unlock distance | 30d | 60d | 90d |
| Memories per capsule | 50 | 100 | 200 |
| Post-unlock viewability | 30d | 90d | forever |
| Active albums | 1 | 3 | ∞ |
| People per album | 2 | 3 | 5 |
| Photos per album | 30 | 75 | 150 |
| Album lifespan / archive | 1mo / 90d | 3mo / 1yr | forever |
| Video / voice length | 3s boomerang+sound / 10s | 20s+sound / 20s | 30s+sound / 30s |

Rules:
- Enforced at **creation/contribution time** in the API (never client-only).
- `viewable_until` is stamped at unlock from the **creator's plan**; if the
  creator upgrades, a plan-change hook re-stamps forward (never retroactively
  for already-expired capsules).
- RevenueCat webhook is the only writer of `users.plan`.

## 7. Scheduled jobs (cron, all idempotent)

| Job | Schedule | Action |
|---|---|---|
| `expire_invites` | every 15 min | pending capsules/albums past `invite_expires_at` → disintegrate, notify creator |
| `freeze_warning` | hourly | active capsules with 5 days since `last_contribution_at` → one-time warning push |
| `freeze_capsules` | hourly | 7 days without contribution → `frozen`, notify all members |
| `unlock_capsules` | every 5 min | `unlock_at <= now()` → `unlocked`, stamp `viewable_until`, set `view_blocked` for non-admins if frozen, queue unlock pushes (delivered at 9am user-local or on app open) |
| `archive_sweep` | daily | unlocked capsules past `viewable_until` → archived; albums past lifespan → archived; archives past retention → delete media |
| `album_lifespan` | hourly | active albums past 1 month (per plan) → archived (view-only) |
| `flush_notification_batches` | every 5 min | send due batched notifications (4-hour capsule window) |
| `streak_rollover` | daily per-timezone | close out streak days, update `streak_current` / `streak_perfect` |

## 8. Notifications

- Categories exactly as the spec matrix (instant vs batched). Outbox pattern:
  every event writes to `notification_outbox`; instant categories flush
  immediately, batched ones get a `batch_key` (e.g. `capsule:{id}`) and
  `deliver_after` (max one per 4h window per capsule).
- Per-category user preferences checked at flush time.
- Silent (content-available) pushes for widget refresh ride the same sender.

## 9. Widgets

- **iOS**: `expo-widgets` (official, stable since SDK 56 — requires SDK upgrade
  later) or `@bacons/apple-targets`; **Android**: `react-native-android-widget`.
- Flow: server event (album photo added / capsule unlocked) → silent push →
  app wakes in background → fetches signed URL → writes image + metadata to the
  App Group shared container → reloads widget timeline.
- Widget shows: latest selected album photo (with caption/comment), or a selected
  unlocked/completed capsule memory that rotates over time and surfaces latest
  comment text when available. Sealing holds automatically (sealed media has no
  obtainable URL). Also refresh widget data on every app open (push is best-effort).
- Endpoint: `GET /v1/widget/feed` returns widget-eligible items for the user,
  including optional `caption` and latest `comment` on media-backed entries.
- Photo confirm generates a ~400px JPEG at `media/{user}/{media_id}_thumb.jpg`
  (`media.thumb_bucket_key`). Widget feed signs thumb URLs; videos fall back to
  the full media object in v1 (first-frame thumb deferred).
- Silent widget refresh pushes use Expo `_contentAvailable` + `data.type =
  widget_refresh`, throttled to one per device per 15 minutes
  (`push_tokens.widget_push_at`).

## 9b. Capsule unlock Recap

On first open after unlock (and replay via `?celebrate=1`), the app shows a
horizontal **Recap** story before the vertical memory viewer:

- `GET /v1/capsules/{id}/recap` — narrative facts only (totals, `days_sealed`,
  top contributor + share, `peak_hour_local` / `night_owl_share` in the
  **viewer’s** `users.timezone`, first/last capture, streak, `was_frozen`,
  busiest day, **`viewer`** block, **`facts`** keyed map). Same sealing guards
  as `/stats`. **No media URLs or captions.** See `docs/RECAP_CONTENT_SCHEMA.md`.
- Client builds screens from `content/screens.json` + `composeRecapScreens(facts)`
  (gradients + motifs). Navigation: Recap = left/right; photos = swipe down after CTA.
- Reveal gate unchanged: `capsule_members.unlock_reveal_seen_at` +
  `POST /v1/capsules/{id}/unlock-reveal/seen`.

## 10. Environments & deployment

| Env | Where | Notes |
|---|---|---|
| local | docker-compose (api + postgres + minio) | `make up` |
| staging | Fly.io / Railway / small VPS | real R2 + Resend sandbox |
| production | Hetzner VPS (Docker) or Fly.io | R2 + CDN, daily pg_dump to R2, Sentry |

- Single Go container + managed/co-located Postgres. Media never transits the API.
- Observability: structured logs (slog → stdout), optional Sentry via `SENTRY_DSN`
  (panic capture + Error-level slog forwarding), `/healthz` + `/readyz` endpoints.
- **Product analytics** (see `docs/ANALYTICS.md`): first-party `analytics_events`
  table; non-blocking server `Track()` queue; client batch ingest
  `POST /v1/analytics/events`; founder dashboard `GET /v1/metrics/summary`
  (`X-Metrics-Key` / `METRICS_API_KEY`). Privacy: no captions, media URLs, or emails.
- Rate limits: 600 req/min per IP, 900 req/min per authenticated user; media/voice
  stream redirects excluded; stricter
  auth OTP/login buckets (`internal/auth/limits.go`). Health probes exempt.
- Media signed URLs: `SignedURLTTL = 10 * time.Minute` (`internal/media/store.go`).
- DB pool: `DB_MAX_CONNS` env (default 20) → `pgxpool.MaxConns`.
- Backups: `scripts/backup.sh` → nightly `pg_dump`, upload to private R2 bucket,
  30-day retention — see `docs/RUNBOOK.md`.
- CI: `.github/workflows/backend-ci.yml` — go test (Postgres service), lint, Docker build.

## 11. API conventions

- `/v1` prefix, JSON, `Authorization: Bearer <access>`.
- Errors: `{ "error": { "code": "capsule_limit_reached", "message": "..." } }` —
  stable machine codes the app maps to UI strings.
- Cursor pagination (`?cursor=&limit=`) on all list endpoints.
- Idempotency keys on memory creation (mobile retries).
- OpenAPI spec generated from code (`swaggo` or hand-maintained `openapi.yaml`)
  → TypeScript client types generated for the app (single source of truth).
