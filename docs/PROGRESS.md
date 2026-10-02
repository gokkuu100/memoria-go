# Memoria — Progress Dashboard

> Single source of truth for "where are we". Update this file **and** the
> phase checklists (`BACKEND_PLAN.md` / `FRONTEND_PLAN.md`) after every feature.
> Status: `not started` → `in progress` → `done`.

## Backend (`memoria-gobackend/`)

| Phase | Scope | Status | Notes |
|---|---|---|---|
| B0 | Foundations: Go skeleton, Docker, Makefile, migrations | done | Exit test passed 2026-06-12 |
| B1 | Auth & identity (OTP, JWT, usernames) | done | Exit test + integration tests passed 2026-06-12 |
| B2 | Media pipeline (R2 presign, sealing primitives) | done | Exit test + 5 integration tests passed 2026-06-12 |
| B3 | Friends & connections | done | Exit test + 3 integration tests passed 2026-06-12 |
| B4 | Push notification core | done | Exit test + 5 integration tests passed 2026-06-12 |
| B5 | Albums full lifecycle | done | Exit test + 4 integration tests passed 2026-06-12 |
| B6 | Capsules (6a invites / 6b sealing+streaks / 6c unlock) | done | Exit test + 6 integration tests passed 2026-06-12 |
| B7 | Notification batching + full matrix | done | Exit test + 3 integration tests passed 2026-06-12 |
| B8 | Subscriptions (RevenueCat) & plan enforcement | done | Exit test + 4 integration tests passed 2026-06-12 |
| B9 | Stats, exports, account deletion | done | Exit test + 5 integration tests passed 2026-06-12 |
| B10 | Widget support endpoints | done | Exit test + 4 integration tests passed 2026-06-12 |
| B11 | Production hardening & launch | done | Exit test passed 2026-06-12. **Backend complete.** |
| — | Offline capsule queue | done | Migration 00013, captured_at + idempotency, sync deadline; integration tests 2026-07-12 |
| — | Social polish | done | GET reactions (users + my_emoji), DELETE notification, dev-gated auth IP limits; tests 2026-07-12 |
| — | Product analytics | done | Migration 00015, async Track + client ingest, GET /v1/metrics/summary (15 metrics); tests 2026-07-15 |

## Frontend (`memoria/`)

| Phase | Scope | Status | Notes |
|---|---|---|---|
| F0 | Design system core + Screen/safe-area shell + tab bar | done | tsc + lint pass 2026-06-12 |
| F1 | Auth & onboarding | done | Real API auth; OTP from docker logs |
| F2 | Camera & capture | done | tsc + lint pass 2026-06-12. Camera needs EAS dev build (expo-camera). Manual: login → camera → upload to active album if exists |
| F3 | Friends & profile shell | done | Profile, friends, settings, notifications, invite connect screen; unread badge on dock + bell |
| F4 | Albums | done | Timeline, create/list/detail/viewer, invites, reactions/comments; tsc + lint pass 2026-06-12 |
| F5 | Capsules + unlock experience | done | Locked/unlocked/frozen flows, stats, unfreeze vote, 3-slide UnlockStory + reveal-seen; 2026-07-09 |
| F6 | Notifications & polish | done | Deep links, badge, flush on foreground, haptics; tsc + lint pass 2026-06-12 |
| F7 | Subscriptions, stats, account | done | RevenueCat, subscription/stats screens, export + delete account; tsc + lint pass 2026-06-12 |
| F8 | Widgets (iOS + Android) | done | JS feed sync + silent push; native stubs in targets/; tsc + lint pass 2026-06-12 |
| F9 | Release readiness | done | FlashList, a11y, EAS production, Maestro stub, Sentry stub; tsc + lint pass 2026-06-12. **Frontend complete.** |
| — | Offline capsule queue | done | Local queue + sync worker + Capsules retry button; backend captured_at + idempotency; 2026-07-12 |
| — | Social polish | done | 🔥 streaks, notification pagination + swipe delete, per-user reactions UI, inline comment composer; 2026-07-12 |
| — | Product analytics | done | Batched client events + unlock/viewer/subscription wiring; docs/ANALYTICS.md; 2026-07-15 |
| — | Capsule unlock Recap | done | Multi-chapter narrated Recap + `GET /v1/capsules/{id}/recap`; 2026-07-16 |

## Decision log

| Date | Decision |
|------|----------|
| 2026-07-22 | Expo Go paywall testing via RevenueCat Test Store (`EXPO_PUBLIC_REVENUECAT_TEST_STORE_KEY`). Production/TestFlight always use `appl_`/`goog_` keys — Test Store cannot validate App Store Connect / StoreKit. |
| 2026-07-22 | Paywall “Loading plans…” forever: production had RC keys + product IDs (`memoria_plus`/`memoria_pro`) but `getOfferings` failures/hangs left the sheet stuck. Timeout (15s), request-id loading cleanup, Sentry on failure, subscription “Store connection” diagnostics. Root cause is almost always ASC: products Approved + Paid Apps Agreement Active + RC Offering Current + bundle `com.pilar.memoria.app`. |
| 2026-07-20 | Media load / rate-limit incident: ClientIP now uses X-Forwarded-For (was docker bridge → one shared 100/min bucket). Media streams excluded from rate limits; limits raised to 600/900. Caddy serves MinIO on `:443` path `/memoria-media` (no gzip); `S3_PUBLIC_ENDPOINT` drops `:9000`. App prefers signed `media_url` over auth→302 hop. Host remains t3.micro — upgrade if concurrent viewers grow. |
| 2026-07-16 | Recap redesign step 1: declarative `screens.json` + `composeRecapScreens(facts)`; API adds `viewer` + `facts` keyed analytics (memories_by_viewer/others, rank, engagement). `photos_of_user` deferred — no CV; use `memories_by_others` as discovery proxy. Schema: `docs/RECAP_CONTENT_SCHEMA.md`. |
| 2026-07-16 | Capsule unlock Recap: Reddit/Spotify-style horizontal story cards (multi-chapter gradients + SVG motifs + narration templates) before vertical photo viewer. New `GET /v1/capsules/{id}/recap` for fun facts (peak hour in viewer TZ, night owl share, days sealed, top contributor share). Analytics allowlist adds `beat`. Skia deferred — Reanimated + SVG + LinearGradient for v1. |
| 2026-07-15 | Product analytics: first-party hybrid model (server Track queue + client batch ingest). 15 metrics via `GET /v1/metrics/summary` (`METRICS_API_KEY`). No captions/media URLs in props. PostHog/Amplitude optional later as a second sink — not required for v1. |
| 2026-07-14 | Spark boomerang: 3s cap with **sound on**; preview uses expo-video seek ping-pong (expo-av reverse rate was stuck on iOS). Client clamps Spark to 3s even if API cache still says 10s; boomerang mode keyed off `plan === 'spark'`, not `video_sound`. |
| 2026-07-12 | Offline capsule queue: client saves photo/video to sandbox when offline; sync on reconnect with `Idempotency-Key` + `captured_at` (capture time, not sync time). Server rejects POST when `now >= unlock_at` or `captured_at >= unlock_at`. Capsules home shows cloud-upload retry badge beside `+`. Albums remain online-only. |
| 2026-07-12 | Reactions UI fix: optimistic cache updates + refetch on open; PUT returns `my_emoji`; GET list drives chip highlight and sheet. Reaction notifications now instant (were 4h batched). Comment/reaction copy says "your photo". Capsule unlock reminder: scheduled at create/activate for unlock_at − 10m (`capsule_unlock_soon` push); migration 00014 + minute job backup. Notification delete uses optimistic list removal. |
| 2026-07-12 | Social polish: `GET /v1/memories/{id}/reactions` returns each reaction with author profile card plus caller's `my_emoji`. `DELETE /v1/notifications/{id}` user-scoped. Notifications feed paginates (20/page) with Load more + swipe-to-delete. Streaks render Snapchat-style `🔥 N`. Comment composer is one field with inline Post pill. Auth IP-keyed rate limits production-only. |
| 2026-07-12 | Connectivity probe fix: `checkOnline` now hits `/healthz` at the server root (was wrongly probing `/v1/healthz` → 404 → app thought it was always offline) and treats any HTTP response as online. Capture path forces a fresh probe (`checkOnline(true)`) so a stale offline flag can never queue an online capture; mid-upload network failure falls back to the queue. Queue sheet shows one summary card per capsule (count + unlock date), not per-item rows. |
| 2026-07-09 | Capsule unlock Wrapped: 3-slide `UnlockStory` (Reanimated + SVG layers + expo-av ambient) on first open after unlock; durable `capsule_members.unlock_reveal_seen_at` + `POST /v1/capsules/{id}/unlock-reveal/seen`; replay via stats `?celebrate=1` without clearing seen. No Rive/Lottie for v1. |
| 2026-06-17 | Widget upgrade for album/capsule personalization: Settings now supports selecting a specific album or unlocked/completed capsule as widget source. Backend widget feed now includes album/capsule memory `caption` and latest `comment`, and returns unlocked capsule memories across visible unlocked/archived capsules (not only unlock-day). iOS widget timeline now rotates capsule memories every ~30 minutes. |
| 2026-06-17 | Added authenticated debug push path `POST /v1/notifications/test-push` to send a real test notification to the caller's own devices and flush immediately. Added Settings action "Send test push notification" so production users can validate banner + sound on phone directly. |
| 2026-06-16 | RevenueCat verification hardening: `/v1/me/subscription` now includes optional `last_event` metadata (latest processed webhook event id/type/plan/timestamp). Frontend subscription screen now shows SDK readiness, active entitlements/products, webhook receipt status, and a manual "Refresh subscription status" action for sandbox QA. |
| 2026-06-16 | Added `DELETE /v1/albums/{id}` (creator-only, purges all memory media from R2 then marks album deleted). Extended `DELETE /v1/capsules/{id}` to also purge S3 media via new `purgeCapsuleMemories` before marking disintegrated. `ListCapsuleMemoryMediaKeys` query hand-added to dbgen (mirrors `ListAlbumMemoryMediaKeys`). Frontend: `useDeleteAlbum` hook, confirm-sheet UI for delete/leave on both `CapsuleDetailContent` and `AlbumDetailContent`. |

| Date | Decision |
|---|---|
| 2026-06-12 | Stack locked: Go + chi + sqlc + Postgres + R2 + Expo Push + RevenueCat; Expo SDK 54 + expo-router; docs structure created. |
| 2026-06-12 | UI north star set: Locket timeline screenshot (`docs/design/reference-timeline.png`). Memories screen = month-card dot-grid timeline; floating dock tab bar; espresso + gold palette. Added `GET /v1/timeline` to backend plan (B5/B6c). |

| 2026-06-12 | No-mock-data rule adopted (user): screens only wired to real API data; timeline components moved from F0 to F4/F5. |
| 2026-06-12 | B0 done. Go 1.26.3 installed at `~/.local/go` (PATH in ~/.bashrc); module name `memoria-backend`; dropped chi `middleware.RealIP` (deprecated, IP-spoofing CVEs) — client IP will come from reverse proxy config in B11. Docker pruned (~19GB) to free disk. |
| 2026-06-12 | Email provider switched Resend → Gmail SMTP (user decision). `net/smtp` + app password, STARTTLS :587; dev fallback logs OTP codes. `.env` created locally with SMTP placeholders. |
| 2026-06-12 | B1 done. Email/username uniqueness via partial unique indexes (live rows only) — deleted accounts free their email, usernames blocked forever via `retired_usernames`. Password reset revokes all sessions. Integration tests run against real Postgres `memoria_test` DB (created/migrated by test setup), skip if DB down. |
| 2026-06-12 | B2 done. All plan limits centralized in `internal/billing/plans.go` (incl. capsule/album caps for later phases). Upload byte caps chosen: 10 MB photo / 60 MB video / 2 MB voice. |
| 2026-06-12 | B2: video/voice duration stays client-declared (capped by plan at presign + confirm); server-side probing needs ffmpeg — out of scope until there's evidence of abuse. Real byte size IS verified via S3 HEAD at confirm (mismatch → object + row deleted, `media_size_mismatch`). Photo dimensions decoded server-side (`image.DecodeConfig`, jpeg/png/webp). |
| 2026-06-12 | B2: split `S3_ENDPOINT` (in-network, e.g. `http://minio:9000`) from `S3_PUBLIC_ENDPOINT` (host clients hit, e.g. `http://localhost:9000`) — SigV4 signs the Host header, so presigned URLs are signed by a second client configured against the public endpoint. Bucket auto-created on startup (idempotent). |
| 2026-06-12 | B2: job runner (`internal/jobs`, robfig/cron) wraps every job in `pg_try_advisory_lock` so a second API instance is safe; orphan sweep (pending media > 24h → delete object then row) runs hourly + at startup. Media GET is owner-only until albums/capsules add membership-based access; non-owners get 404 (not 403) to avoid existence leaks. |
| 2026-06-12 | B3 done. Friendships stored with canonical pair ordering (`user_a < user_b`); silent block returns 404 (never 403) on profile lookup and friend requests. `internal/friends/guard.go` exported for B5/B6 invite paths. Notification events stubbed via `internal/notifications.LogSender` until B4. Integration tests use `memoria_friends_test` DB. |
| 2026-06-12 | B4 done. Outbox pattern with `notification_outbox` (title/body/data/jsonb, `read_at` for in-app feed) + `notification_prefs`. Expo client (`EXPO_PUSH_URL`, optional `EXPO_ACCESS_TOKEN`); instant flush on enqueue + 30s goroutine; dead tokens pruned via receipt poll. Friend events wired through `OutboxService`. Integration tests use `memoria_notifications_test` DB + httptest Expo mock. |
| 2026-06-12 | B5 done. Migration `00006_albums.sql`: albums (incl. `invite_expires_at`), album_members, shared `memories` table, reactions, comments. `internal/albums` + `internal/social` packages; cron jobs `expire_invites` (15m), `album_lifespan` (hourly), `archive_sweep` (daily). Timeline v1 returns per-day buckets with full signed media URLs (thumbnail variants deferred to B10). Album decline → `deleted`; leave purges author media from R2 + rows. 20 integration tests total (4 new in `memoria_albums_test`). |
| 2026-06-12 | B6 done. Migration `00007_capsules.sql`; `internal/capsules` package with full state machine, sealing guards, streak/freeze/unfreeze jobs, timeline v2 capsule items. `POST /v1/notifications/flush` for app-open unlock delivery. Admin transfer on account delete inlined in `users` (avoids import cycle). Plan re-stamp via `capsules.RestampViewableForCreator` (B8 webhook will call). 26 integration tests total (6 new in `memoria_capsules_test`). |
| 2026-06-12 | B7 done. Batch engine: `batch_key` + 4h window per capsule/recipient (`capsule:{id}:{user}`) and reaction batch (`reaction:{memory}:{user}`); cron `flush_notification_batches` every 5 min; `FlushOnAppOpen` also flushes due user batches. Capsule memory contribute wired. 29 integration tests total (3 new batch tests in `memoria_notifications_test`). |
| 2026-06-12 | B8 done. Migration `00008_subscriptions.sql`; RevenueCat webhook (`Authorization` header verify, idempotent `subscription_events`); `GET /v1/me/subscription`; upgrade hook calls `capsules.RestampViewableForCreator` via server callback (avoids import cycle). Downgrade policy: over-limit existing resources kept, new creates blocked at handler count checks. 33 integration tests total (4 new in `memoria_billing_test`). |
| 2026-06-12 | B9 done. Migration `00009_exports.sql`; `internal/stats` (heatmap + lifetime, spark 90-day window from `plans.go`); `internal/exports` (ZIP job → R2 `exports/{user}/{job}.zip`, 24h signed URL, PDF Pro gate + `not_implemented` stub); account hard-delete cascade goroutine after soft-delete (album exit rules, R2 purge, social cleanup). Cron `process_exports` every minute + immediate goroutine on POST. 38 integration tests total. |
| 2026-06-12 | B10 done. Migration `00010_widget.sql` (`media.thumb_bucket_key`, `push_tokens.widget_push_at`). Photo confirm generates ~400px JPEG thumb; `GET /v1/widget/feed` (album latest-per-album, sealed countdowns, unlocked-today memories); silent widget push via `RefreshWidgetForUsers` on album memory + capsule unlock (15 min/device throttle). Video widget thumbs deferred (full URL fallback). 42 integration tests total (4 new in `memoria_widget_test`). |
| 2026-06-12 | B11 done — **backend complete**. Global rate limits (100/min IP, 300/min user) + unified auth OTP limits; `httpx` validation caps; IDOR sweep in `docs/SECURITY.md` + comment-delete `memory_id` fix; optional Sentry (`SENTRY_DSN`); migration `00011_indexes.sql`; `DB_MAX_CONNS` pool sizing; `scripts/backup.sh` + `docs/RUNBOOK.md`; k6 load script; CI `.github/workflows/backend-ci.yml`; hand-maintained `openapi.yaml`. Staging/R2 backup restore documented for operator credentials. |
| 2026-06-12 | F0+F1 done. No-emoji-icons rule: UI uses `@expo/vector-icons` only. Theme switch: light=B&W, dark=inverted B&W + gold accent (`#F5A623`), persisted via AsyncStorage. Global `ToastProvider` maps `error.code` via `src/lib/errors.ts`. API client hand-typed from `openapi.yaml`; Memories tab wired to real `GET /v1/timeline`. |
| 2026-06-12 | F2+F3 done. Camera flow: presign → PUT → confirm → POST memory; destination picker reads active albums + ongoing capsules; last destination in AsyncStorage. Profile/friends/notifications wired to B3/B4/B7 APIs; invite link copy via `expo-clipboard`; connect screen at `user/[username]?invite=`. Boomerang ping-pong + VoiceNoteRecorder deferred (PostCaptureEditor is caption-only for now). |
| 2026-06-12 | F4+F5 done. Memories tab: PillTabs [Capsule|Albums], `MemoriesTimeline` wired to `GET /v1/timeline` (3-month window, refetch on focus). Albums/capsules CRUD hooks, MosaicGrid, MemoryViewer + EmojiReactionBar + CommentsThread. Create modals, invite accept/decline, capsule frozen/unfreeze/view_blocked, stats, PlanPaywall stub on limit errors. `POST /v1/notifications/flush` on app foreground. MosaicGrid uses FlatList (FlashList deferred to F9). Voice playback in viewer deferred. |
| 2026-06-12 | F6+F7 done. `src/notifications/links.ts` maps all B7 payload types to routes (memory social resolves via timeline). Expo notification handler + tap routing in app layout; badge from unread count. RevenueCat via `react-native-purchases` + hybrid `PlanPaywall`. Profile subscription/stats screens; settings export (ZIP poll + clipboard) + typed DELETE account. Spark heatmap 90-day clamp shown in UI. |
| 2026-06-12 | F8+F9 done — **frontend complete**. Widget feed sync (`GET /v1/widget/feed` → AsyncStorage) on app open/foreground + silent `widget_refresh` push; Settings focus picker; native stubs in `targets/`. F9: FlashList MosaicGrid, splash holds until session hydrate, Screen.Header a11y, EAS production profile, Maestro happy-path stub, optional Sentry DSN stub. |
| 2026-06-12 | Local ops config: Gmail SMTP wired in a gitignored `.env`; frontend Sentry DSN + `@sentry/react-native` init; RevenueCat SDK keys + webhook secret stay in local env only. |
| 2026-06-12 | Dev connectivity + UX: `src/lib/apiUrl.ts` + platform `.env.*` templates + `scripts/detect-api-url.sh`; API client network toast includes configured URL; `Screen` dismisses keyboard on outside tap (all variants incl. `keyboard`). |
| 2026-06-13 | Album pending UX: `GET /v1/albums?state=active` includes `pending` albums for accepted members (creator sees album immediately); camera destination picker filters to `state=active` only; album detail shows accept/decline for invitee and waiting state for creator. |
| 2026-06-14 | Profile photo upload: Profile → Change photo → camera or gallery → presign/confirm → `PUT /v1/me/avatar`; session + friend/user queries invalidated on success. |

## Known issues / debt

- Native widget UI requires EAS build + `@bacons/apple-targets` (SDK 54) or Expo SDK 56+
  upgrade for official `expo-widgets`. JS data bridge is complete; see `targets/README.md`.
- Disk was at 100% before Docker prune; keep an eye on free space (22GB now).
