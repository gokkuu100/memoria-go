# Memoria Analytics — Architecture & Viewing Guide

## How big companies track product metrics

Production analytics almost never run on the critical request path. The usual pattern:

1. **Server-side facts** (signup, invite accept, memory create, freeze, unlock) are
   recorded in the API after the mutation succeeds — fire-and-forget into a
   buffered queue, never blocking the HTTP response.
2. **Client-side UX** (opened capsule before unlock, viewed someone else's memory,
   finished slide 2 of UnlockStory) is batched on-device and flushed in the
   background. Failures are dropped silently so the app never breaks.
3. **Dashboard metrics** are mostly computed from **operational tables**
   (capsules, members, memories, reactions) — events fill gaps the DB cannot see.
4. An optional product-analytics SaaS (PostHog / Amplitude) can mirror the same
   events later; Memoria stores first-party events so you own the data.

Memoria follows that model. Analytics must not reduce capture latency, break
auth, or store captions / media URLs / emails.

## The 15 metrics

| # | Metric | Primary source |
|---|---|---|
| 1 | Signup → First Capsule (7d) | `users` + `capsule_members` |
| 2 | Invite Conversion Rate | `capsule_members` |
| 3 | Activated Capsule Rate | capsules + memories + members |
| 4 | Disintegration Rate | `capsules.state` |
| 5 | Time to First Memory | `accepted_at` → first memory |
| 6 | Perspective Density | distinct authors / unlocked capsule |
| 7 | Memories per Capsule at Unlock | `memories` count |
| 8 | Pre-Reveal Return Rate | client `capsule_opened` before unlock |
| 9 | Freeze Rate | `capsules.frozen_at` / reached active |
| 10 | Reveal Return Rate (24h) | client open / reveal within 24h of unlock |
| 11 | Time-to-Reveal | unlock → first open / reveal seen |
| 12 | Discovery Index | client `memory_viewed` with `is_own=false` |
| 13 | Reveal Completion Rate | `unlock_reveal_seen_at` |
| 14 | Reveal Day Engagement | reactions + comments on unlock day |
| 15 | Repeat Capsule Rate (90d) | users with ≥2 capsules in 90d |

## Event taxonomy (allowlisted)

### Server (automatic)

| Event | When |
|---|---|
| `signup_complete` | User created |
| `capsule_created` | Capsule created (`capsule_type`) |
| `capsule_invite_sent` | Member invited |
| `capsule_invite_accepted` | Invite accepted |
| `capsule_memory_created` | Memory attached to capsule |
| `capsule_frozen` | Freeze job |
| `capsule_unlocked` | Unlock job |
| `capsule_disintegrated` | Invite expiry / disintegrate |
| `unlock_reveal_completed` | `POST .../unlock-reveal/seen` |
| `memory_reacted` | Reaction upsert |
| `memory_commented` | Comment created |

### Client (batched)

| Event | When |
|---|---|
| `app_opened` | App foreground / cold start (authenticated) |
| `capsule_opened` | Capsule detail viewed (`capsule_id`, `capsule_state`) |
| `memory_viewed` | Memory slide visible (`capsule_id`, `is_own`) — no media id |
| `unlock_story_slide` | Recap slide (`capsule_id`, `slide`, `beat`) |
| `unlock_story_completed` | Story finished (`capsule_id`) |
| `subscription_viewed` | Subscription screen opened |

Property allowlist: `capsule_id`, `capsule_type`, `capsule_state`, `slide`,
`beat`, `is_own`, `plan`, `source`. Everything else is stripped server-side.

## APIs

### Ingest (authenticated)

```http
POST /v1/analytics/events
Authorization: Bearer <access>
Content-Type: application/json

{
  "events": [
    {
      "name": "capsule_opened",
      "occurred_at": "2026-07-15T12:00:00Z",
      "properties": { "capsule_id": "<uuid>", "capsule_state": "active" }
    }
  ]
}
```

Max 50 events per request. Unknown event names are ignored (not 400) so old
clients stay compatible. Response is always fast; persistence is async.

### Dashboard summary (founder / ops)

```http
GET /v1/metrics/summary?days=90
X-Metrics-Key: <METRICS_API_KEY>
```

Returns JSON for all 15 metrics over the lookback window. Set `METRICS_API_KEY`
in the API env. In development the default is `dev-metrics-key` if unset.

## How to view metrics

### 1. Curl the summary (fastest)

```bash
# With stack running (make up):
curl -s -H "X-Metrics-Key: ${METRICS_API_KEY:-dev-metrics-key}" \
  "http://localhost:8080/v1/metrics/summary?days=90" | jq .
```

Each metric includes `value`, `numerator`, `denominator`, and a short `note`.

### 2. SQL / Metabase later

Point Metabase or Grafana at Postgres and chart from `analytics_events` plus
operational tables. The summary endpoint SQL in `internal/analytics/metrics.go`
is the source of truth for definitions.

### 3. Optional SaaS (future)

Wire PostHog/Amplitude as a second sink from the same `trackEvent` client and
server `Track()` calls. Do not make the app depend on SaaS availability.

## Performance & safety guarantees

- Server `Track()` never waits on DB write; queue drop-on-full with a warn log.
- Client batches (~10s or 20 events) and uses fire-and-forget `fetch`.
- Analytics failures never surface as user-facing errors.
- No captions, media URLs, emails, or display names in properties.
