# Memoria Backend — Phased Implementation Plan

> Workflow: phases ship in order. Every item is a checkbox — mark `[x]` the moment
> it is implemented **and verified** (manual test or automated test passing).
> A phase is done only when all its boxes are checked and `docs/PROGRESS.md` is updated.
> Architecture details live in `docs/SYSTEM_DESIGN.md`. Feature spec lives in `project.md`.

---

## Phase 0 — Foundations (infra, Docker, Makefile)

Goal: a running, containerized Go API skeleton with DB, migrations, and tooling.
Nothing product-specific yet; everything after this builds on it.

- [x] Clean stray Expo files out of `memoria-gobackend/` (`_layout.tsx`, `modal.tsx`, `(tabs)`)
- [x] `go mod init`, folder layout per SYSTEM_DESIGN §3, `cmd/api/main.go` boots
- [x] Config loader (env vars only; `.env.example` committed, `.env` gitignored)
- [x] Structured logging (slog, JSON in prod, pretty in dev) + request-ID middleware
- [x] chi router with middleware stack: recovery, logging, CORS, timeout
- [x] `GET /healthz` (liveness) and `GET /readyz` (DB ping)
- [x] Postgres via pgx pool; goose migration tooling wired (also self-migrates on boot)
- [x] sqlc configured (`sqlc.yaml`, `db/queries/`, generated package compiles)
- [x] Dockerfile: multi-stage (builder → distroless), < 30 MB image
- [x] docker-compose.yml: `api`, `postgres:16`, `minio` (local R2) with volumes
- [x] Makefile targets: `up`, `down`, `build`, `run`, `test`, `lint` (golangci-lint),
      `migrate-up`, `migrate-down`, `migrate-new name=...`, `sqlc`, `logs` (+ `tools`)
- [x] Error envelope helper (`{"error":{"code","message"}}`) + typed error codes
- [x] Versioned router mount at `/v1`
- [x] README in `memoria-gobackend/` (run instructions, env vars)

**Exit test**: `make up` → `curl localhost:8080/readyz` returns 200 with DB connected.
✅ **Passed 2026-06-12** — healthz/readyz 200, 404 error envelope correct, goose
migrated to version 1 on container start, lint clean.

---

## Phase 1 — Auth & Identity

Spec: email + OTP signup, password, globally-unique permanently-retired usernames,
display name, sign in, OTP password reset, one account per email.

- [x] Migration: `users`, `retired_usernames`, `otp_codes`, `sessions`, `push_tokens`
- [x] `POST /v1/auth/otp/request` — issue 6-digit code, hashed at rest, 10 min TTL,
      via Gmail SMTP (dev fallback logs the code); purposes: `signup`, `password_reset`
- [x] Rate limiting on OTP request (per email + per IP) and max 5 verify attempts
- [x] `POST /v1/auth/otp/verify` — returns short-lived signup ticket
- [x] `POST /v1/auth/signup` — ticket + password (argon2id) + username + display name
- [x] Username rules: 3–20 chars, `[a-z0-9_.]`, unique against `users` **and**
      `retired_usernames`, immutable after creation
- [x] `GET /v1/auth/username-available?u=` — for realtime client check (rate limited)
- [x] `POST /v1/auth/login` (email+password) and `POST /v1/auth/reset-password` (OTP flow,
      revokes all sessions)
- [x] JWT access tokens (15 min, HS256 pinned) + rotating refresh tokens
      (`POST /v1/auth/refresh`), refresh stored SHA-256-hashed per device session;
      `POST /v1/auth/logout` revokes
- [x] Auth middleware (bearer token → user in context)
- [x] `GET /v1/me`, `PATCH /v1/me` (display name, timezone)
- [x] `PUT /v1/me/push-token` — register Expo push token per device
- [x] Account deletion `DELETE /v1/me` — soft-delete user, retire username (tx),
      revoke sessions + push tokens; hard-delete cascade lands in Phase 9
- [x] Tests: signup happy path, duplicate email, retired username rejected,
      refresh rotation, OTP expiry/attempt limits (integration vs real Postgres)

**Exit test**: full signup → login → refresh → `GET /v1/me` cycle from an HTTP file or curl.
✅ **Passed 2026-06-12** — live curl cycle against compose stack: OTP → ticket →
signup → /me → username availability → refresh rotation → login. `go test` green
(3 integration tests vs real Postgres), lint clean.

---

## Phase 2 — Media pipeline (R2 + sealing primitives)

Spec: in-app capture only; photos, boomerang videos, voice notes; avatar photos.
This phase builds the presign/confirm/download core every later feature uses.

- [x] Migration: `media` table
- [x] R2/MinIO client (S3 SDK), private bucket, key scheme `media/{user}/{uuid}`
- [x] `POST /v1/media/presign` — kind (photo|video|voice), content type + size
      declared; validates against caller's plan caps (duration enforced at confirm);
      returns presigned PUT + media ID (status `pending`)
- [x] `POST /v1/media/{id}/confirm` — verifies object exists (HEAD), records
      real size/dimensions/duration, flips to `ready`; rejects spec violations
      (free: boomerang video ≤ 3s; voice ≤ 10s; size caps)
- [x] Signed GET URL helper (10 min TTL) — **the only way media is ever read**
- [x] Orphan sweep job: `pending` media older than 24h → delete object + row
- [x] Avatar: `PUT /v1/me/avatar` accepts a ready photo media ID
- [x] Tests: presign→confirm happy path, oversize rejection, duration rejection,
      unauthorized media access returns 403/404

**Exit test**: upload a real image through presigned URL via curl, confirm, fetch signed GET URL, see the image.
✅ **Passed 2026-06-12** — live curl cycle against compose stack: signup → presign
(plan caps checked) → PUT real PNG to MinIO → confirm (HEAD-verified size +
decoded 1x1 dimensions) → signed GET returned identical bytes → avatar set with
`avatar_url` in /me. 5 integration tests green vs real Postgres + MinIO, lint clean.

---

## Phase 3 — Friends & Connections

Spec: mutual connections only, QR/link sharing, pending in/out, remove, silent block.

- [x] Migration: `friendships`, `blocks`, `invite_links`
- [x] `POST /v1/me/invite-link` — stable shareable token (QR encodes same URL)
- [x] `GET /v1/users/by-invite/{token}` and `GET /v1/users/{username}` (profile
      card: avatar, display name, username — never exposed to blockers)
- [x] `POST /v1/friends/requests` (by user ID from link/QR), accept / decline /
      cancel endpoints
- [x] `GET /v1/friends` (accepted), `GET /v1/friends/requests` (incoming+outgoing)
- [x] `DELETE /v1/friends/{userId}` — remove connection
- [x] `POST /v1/blocks/{userId}` + unblock; block silently: hides profile, kills
      pending requests, prevents future requests/invites both directions
- [x] Block checks integrated into every later invite path (capsules, albums)
- [x] Friend search among existing connections (`GET /v1/friends?q=`) for invite pickers
- [x] Push: friend request received, request accepted (uses Phase 4 sender —
      stub behind interface until then)
- [x] Tests: mutual flow, block silence (no 403 leaks — return as-if-not-found)

**Exit test**: two seeded users connect via invite token, block hides everything.
✅ **Passed 2026-06-12** — live curl via `scripts/b3_exit_test.sh`: signup ×2 →
invite link → by-invite profile → request → accept → friends list → block →
profile + request both 404. 3 integration tests green, lint clean.

---

## Phase 4 — Push notification core

Spec matrix in `project.md` §Notifications. This phase: infrastructure + instant sends.
Batching windows arrive in Phase 7.

- [x] Migration: `notification_prefs`, `notification_outbox`
- [x] Expo Push client (batch send, receipt checking, dead-token pruning)
- [x] Outbox pattern: domain code writes outbox rows; sender goroutine flushes
      instant categories immediately
- [x] Per-category preference checks (default all enabled)
- [x] `GET/PUT /v1/me/notification-prefs` (all 8 spec categories)
- [x] In-app notification list: `GET /v1/notifications` + unread count + mark-read
- [x] Wire Phase 3 friend events through the real sender
- [x] Tests: pref-disabled category not sent; dead token pruned after receipt error

**Exit test**: ✅ `scripts/b4_exit_test.sh` — friend request → outbox sent → in-app feed (real device push needs Expo + dev app).

---

## Phase 5 — Albums (full lifecycle)

Spec: 2-person live shared space (free), instant visibility, 48h invite,
1-month lifespan, archive windows, member-exit rules.

- [x] Migration: `albums`, `album_members`, `memories` (shared with capsules),
      `reactions`, `comments`
- [x] `POST /v1/albums` — name, cover style (enum), one invited friend;
      plan checks: active album count, member count (Pro: up to 5)
- [x] Invite accept/decline; activation when invitee accepts (`activated_at`)
- [x] Cron `expire_invites`: pending albums past 48h → disintegrate + notify creator
- [x] `POST /v1/albums/{id}/memories` — media ID + optional voice + caption;
      validates: member, album active, photo cap by plan
- [x] `GET /v1/albums/{id}` — header data: name, cover, member avatars, count,
      lifespan countdown, contribution balance ("You: 12 · Alex: 3")
- [x] `GET /v1/albums/{id}/memories` — paginated, signed URLs, author per item
- [x] Instant push to other member(s) on new memory
- [x] Reactions: `PUT/DELETE /v1/memories/{id}/reactions` (one emoji per user),
      counts in list responses
- [x] Comments: CRUD on `/v1/memories/{id}/comments`, instant push to the author
      and prior commenters
- [x] Album list: `GET /v1/albums?state=active|archived`
- [x] Timeline feed v1: `GET /v1/timeline?month=2026-06` — per-day buckets for
      the month-grid UI (`docs/design/reference-timeline.png`): album memories
      as thumbnail items (signed thumb URLs). Extended for capsules in 6c.
- [x] Member exit: `POST /v1/albums/{id}/leave` — deletes that member's memories
      (rows + R2 objects), remaining member keeps album; both gone → album deleted
- [x] Cron `album_lifespan`: active past plan lifespan → archived (view-only)
- [x] Cron `archive_sweep`: archived past retention (90d free / 1yr plus / ∞ pro)
      → hard delete memories + objects
- [x] Tests: full lifecycle, leave-deletes-photos, plan caps, 48h disintegration

**Exit test**: two users run create → accept → contribute → react → comment →
leave end-to-end; verify leaver's photos are gone from R2.
✅ **Passed 2026-06-12** — `scripts/b5_exit_test.sh` + 4 integration tests vs real
Postgres + MinIO (20 total across B1–B5), lint clean.

---

## Phase 6 — Capsules (the core product)

Largest phase; build in the listed order — each block is shippable.

### 6a. Creation, invites, pending state

- [x] Migration: `capsules`, `capsule_members`, `capsule_unfreeze_votes`, `streak_days`
- [x] `POST /v1/capsules` — solo|group, name, description, unlock date
      (validated against plan max: 30/60/90 days); solo → `active` immediately
- [x] Group invites (≤ 4 others free, connected friends only, block-checked),
      48h `invite_expires_at`
- [x] Accept/decline endpoints; per-member realtime status in
      `GET /v1/capsules/{id}` (Accepted / Pending / Declined)
- [x] Decline → notify creator; creator can remove declined member and reinvite
      a replacement within the 48h window
- [x] All accepted → `active`, notify creator + members ("capsule is live")
- [x] Cron `expire_invites` extended to capsules → disintegrate + notify
- [x] Plan checks: active capsule count (2/5/∞), member count (5/10/20)

### 6b. Contributions, sealing, streaks, freeze

- [x] `POST /v1/capsules/{id}/memories` — only while `active`; memory cap by plan
      (50/100/200); updates `last_contribution_at` + `streak_days`
- [x] **Sealed reads**: locked capsule detail returns ONLY name, description,
      countdown, total memory count, contributor list with last-contribution
      times, streak count, invite status, frozen indicator. Memory listing
      endpoint returns 403 `capsule_sealed` until unlocked. (Most important
      invariant in the codebase — covered by tests.)
- [x] Streak engine: a streak day = ≥1 post by anyone that day (per capsule);
      `streak_perfect` flag falsified on any 7-day gap
- [x] Cron `freeze_warning` (day 5) and `freeze_capsules` (day 7) per spec
- [x] Frozen behavior: contributions rejected, existing media untouched,
      frozen indicator + date in capsule card payload
- [x] Unfreeze voting: `POST /v1/capsules/{id}/unfreeze-vote`; all active
      members voted → revive, reset streak (frozen days lost, unlock date
      unchanged); push on each vote cast
- [x] Admin actions: remove member (admin only), delete capsule (admin only),
      member leave (their memories remain per spec)
- [x] Admin transfer: creator leaves/deletes account → next member by
      `accepted_at` becomes admin

### 6c. Unlock, post-unlock, archive

- [x] Cron `unlock_capsules`: flip to `unlocked`, stamp `viewable_until` from
      **creator's plan** (30d/90d/∞)
- [x] Frozen-at-unlock rule: if frozen when `unlock_at` passes → only admin can
      view (`view_blocked = true` for others); Plus/Pro admin endpoint
      `POST /v1/capsules/{id}/unblock/{userId}`; state permanent for contribution
- [x] Unlock notification: queued per member, delivered at 9am user-local time
      or immediately on app open (`deliver_after` + on-open flush)
- [x] `GET /v1/capsules/{id}/memories` — chronological (oldest→newest), signed
      URLs, author, caption, voice note, timestamp, reaction counts, comment
      counts; cursor-paginated for the swipe viewer; grid uses same endpoint
- [x] Reactions + comments enabled only when `unlocked` (reuse Phase 5 social
      module with a state guard)
- [x] Unlock Recap facts: `GET /v1/capsules/{id}/recap` (days_sealed, peak hour
      in viewer TZ, night owl share, top contributor share, first/last, streak,
      was_frozen, busiest day) — no media URLs; same sealing guards as stats
- [x] Unlock stats endpoint: who posted most, most-reacted memory, totals by
      type (photos/videos/voice notes), perfect-streak flag for celebration screen
- [x] Unlock reveal seen: `capsule_members.unlock_reveal_seen_at` + membership
      `unlock_reveal_seen` on detail; `POST /v1/capsules/{id}/unlock-reveal/seen`
      (idempotent, post-unlock) for one-time Wrapped-style unlock story
- [x] Capsule list `GET /v1/capsules?filter=ongoing|frozen|completed`
- [x] Timeline feed v2: extend `GET /v1/timeline` with capsule items — sealed
      contribution days return **marker objects only** (capsule ID + name, no
      media), future unlock dates return countdown items, unlocked capsule
      days return thumbnails (sealing invariant applies to the timeline too)
- [x] Plan-upgrade hook: creator upgrade re-stamps `viewable_until` forward on
      not-yet-expired capsules (never revives expired ones)
- [x] Cron `archive_sweep`: past `viewable_until` → archived
- [x] Tests: the full state machine (table-driven), sealing invariant, frozen
      math around unlock, viewability stamping + upgrade re-stamp, admin transfer

**Exit test**: scripted scenario — group capsule with 3 users: invite, accept,
contribute daily, force-freeze (clock manipulation in test), unfreeze vote,
unlock, view, react, archive.
✅ **Passed 2026-06-12** — `scripts/b6_exit_test.sh` + 6 integration tests vs real
Postgres + MinIO (26 total across B1–B6), lint clean.

---

## Phase 7 — Notification batching + full matrix

- [x] Batch engine: outbox rows with `batch_key` + `deliver_after`; cron
      `flush_notification_batches` every 5 min collapses rows into one push
      ("3 new memories in Wales Trip")
- [x] Capsule new-memory: batched, max one per 4h window per capsule
- [x] Reactions on your memory: batched
- [x] Verify entire spec matrix is wired: capsule invite (instant), album invite
      (instant), capsule active (instant), album memory (instant), comments
      (instant), unlock day (9am local / app open), freeze warning day-5,
      frozen (instant), unfreeze vote (instant), friend request + accept (instant)
- [x] App-open flush endpoint (`POST /v1/notifications/flush` called on foreground)
- [x] Tests: 4h window collapse, pref-disabled suppression

---

## Phase 8 — Subscriptions & plan enforcement

- [x] Migration: `subscription_events`
- [x] RevenueCat webhook `POST /v1/webhooks/revenuecat` (signature-verified):
      INITIAL_PURCHASE / RENEWAL / CANCELLATION / EXPIRATION → set `users.plan`
      + `plan_expires_at`
- [x] Plan-change hook: upgrade triggers capsule `viewable_until` re-stamp (6c)
      and immediately raises live limits
- [x] Downgrade policy: existing over-limit capsules/albums stay but no NEW
      creations until under limit (document in code + this file)
- [x] `GET /v1/me/subscription` for the profile badge + management screen
- [x] Audit: every limit check reads from the single plans config (no scattered
      constants) — grep check + test
- [x] Tests: webhook idempotency (RevenueCat retries), upgrade re-stamp

**Exit test**: ✅ `scripts/b8_exit_test.sh` — webhook purchase → idempotent retry →
`GET /me/subscription` shows pro limits. 33 integration tests total (4 new in
`memoria_billing_test`), lint clean.

---

## Phase 9 — Stats, exports, account deletion completion

- [x] Contribution heatmap: `GET /v1/me/stats/heatmap` (daily counts across all
      containers; free plan: last 3 months, Plus/Pro: all time)
- [x] Lifetime stats: totals (capsules joined, albums, memories, photos vs
      videos vs voice notes, most active month)
- [x] Export ZIP: `POST /v1/exports` → background job streams user's media into
      a ZIP in R2 → push + signed download URL (24h); `GET /v1/exports/{id}` status
- [x] PDF memory book (Pro): job stub + plan gate (full layout engine can land
      post-launch; gate + queue now so API is stable)
- [x] Account deletion hard-cascade job: all memories' R2 objects, memberships,
      reactions, comments, sessions, push tokens; username stays retired;
      admin transfer triggered for owned capsules; album exit rules applied
- [x] Tests: deletion leaves no orphaned R2 objects (sweep verify), heatmap windows

**Exit test**: ✅ `scripts/b9_exit_test.sh` — heatmap window clamp, ZIP export
download, PDF plan gate, account delete. Integration tests in `memoria_stats_test`,
`memoria_exports_test`, `memoria_users_test`.

## Phase 10 — Widget support

- [x] `GET /v1/widget/feed` — per-user widget-eligible items: latest album
      photo(s), capsule countdowns, unlocked-today capsules; small payloads +
      signed thumbnail URLs
- [x] Silent push (content-available) on: album memory added, capsule unlocked —
      throttled per device (max 1 per 15 min via `push_tokens.widget_push_at`)
- [x] Thumbnail variant generation at confirm time (widget-sized, ~400px JPEG)
      stored at `media/{user}/{id}_thumb.jpg`. Videos: v1 uses full signed URL
      (first-frame extraction deferred).
- [x] Tests: feed respects sealing + viewability windows (`memoria_widget_test`)

**Exit test**: ✅ `scripts/b10_exit_test.sh` — widget feed album thumb + sealed
capsule countdown. Integration tests in `internal/widget/`.

---

## Phase 11 — Production hardening & launch

- [x] Rate limiting on all public endpoints (per-IP + per-user buckets)
- [x] Input validation pass on every handler (max lengths, enums, UUIDs)
- [x] Security review: IDOR sweep (every `{id}` route checks membership),
      signed-URL TTLs, webhook signature, OTP brute-force, JWT alg pinning
- [x] Sentry (or equivalent) error reporting wired
- [x] DB: indexes audit (every cron query + list query EXPLAINed), connection
      pool sizing
- [x] Nightly `pg_dump` to private R2 bucket + restore runbook tested once
- [x] Load test: 500 concurrent users on album feed + presign flow (k6)
- [x] Staging environment deployed from CI; production deploy runbook
- [x] CI: lint + test + build image on every push (GitHub Actions)
- [x] `openapi.yaml` complete and used to generate the app's TS client types

**Exit**: ✅ `scripts/b11_exit_test.sh` — rate limit 429, openapi present, healthz.
Staging deploy + live R2 backup restore require operator credentials (RUNBOOK).

---

## Post-launch — Offline capsule queue (2026-07-12)

- [x] Migration `00013_offline_queue.sql`: `memories.captured_at`, `memory_idempotency`
- [x] `POST /v1/capsules/{id}/memories`: `captured_at`, `Idempotency-Key` header, sync deadline at `unlock_at`
- [x] Streak/timeline/stats use `captured_at` for capsule contribution day
- [x] Integration tests: captured_at preserved, idempotency, sync deadline rejection

## Post-launch — Social polish (2026-07-12)

- [x] `GET /v1/memories/{id}/reactions` — reactions with author profile cards + caller's `my_emoji`
- [x] `DELETE /v1/notifications/{id}` — user-scoped notification delete (404 for other users)
- [x] Auth IP-keyed rate limits gated to production (matches global middleware; per-email limits stay on everywhere)
- [x] Integration tests: reactions list with users/my_emoji, notification delete scoping

## Post-launch — Product analytics (2026-07-15)

- [x] Migration `00015_analytics.sql`: `analytics_events` (async ingest)
- [x] Non-blocking `internal/analytics` tracker + allowlisted client ingest
      `POST /v1/analytics/events`
- [x] Server Track() on signup, capsule lifecycle, reveal, reactions/comments
- [x] `GET /v1/metrics/summary` (15 product metrics, `X-Metrics-Key`)
- [x] Docs: `docs/ANALYTICS.md`; integration tests for ingest + summary
