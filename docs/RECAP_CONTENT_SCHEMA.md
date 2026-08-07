# Capsule Unlock Recap — Content Schema

> Step 1 of the Recap redesign: **facts from the API**, **screen definitions in JSON**,
> **composer** picks and resolves copy client-side.

## Data flow

```
GET /v1/capsules/{id}/recap
        │
        ├─ flat fields (backward compat)
        ├─ viewer { … per-member stats for the caller }
        └─ facts { … keyed analytics for story gates + copy interpolation }
                │
                ▼
content/screens.json  (static beat catalog)
                │
                ▼
composeRecapScreens(recap) → ResolvedRecapScreen[]
                │
                ▼
RecapStory UI (next step — wire composer instead of buildPlaylist)
```

**Narration craft:** `docs/RECAP_NARRATION_PLAYBOOK.md` — Spotify-style segments, screen library, voice guide.

Same sealing guards as `/stats`. **No media URLs or captions** in recap responses.

---

## Fact keys (`facts` object)

All values are JSON primitives (number, string, boolean). Keys are stable API contract.

### Capsule

| Key | Type | Source |
|-----|------|--------|
| `capsule_id` | string | capsule |
| `capsule_name` | string | capsule |
| `capsule_type` | string | capsule |
| `member_count` | int | accepted members |
| `contributor_count` | int | distinct authors |
| `days_sealed` | int | created → unlock |
| `streak_current` | int | capsule |
| `streak_perfect` | bool | capsule |
| `was_frozen` | bool | capsule |

### Memories (group)

| Key | Type | Source |
|-----|------|--------|
| `memories_total` | int | all memories |
| `photos_total` | int | photo kind |
| `videos_total` | int | video kind |
| `voice_notes_total` | int | voice attachment |

### Memories (viewer = caller)

| Key | Type | Source |
|-----|------|--------|
| `memories_by_viewer` | int | `author_id = viewer` |
| `photos_by_viewer` | int | viewer photos |
| `videos_by_viewer` | int | viewer videos |
| `voice_notes_by_viewer` | int | viewer voice |
| `viewer_share_pct` | float | viewer / total |
| `memories_by_others` | int | total − viewer (**discovery proxy**) |
| `viewer_contributor_rank` | int | rank by count (1 = most) |
| `viewer_is_top_contributor` | bool | rank == 1 |
| `viewer_peak_hour_local` | int? | 0–23, viewer TZ |
| `viewer_night_owl_share` | float? | 22:00–04:59 share, viewer captures |
| `viewer_reactions_on_others` | int | reactions viewer left on others' memories |
| `viewer_comments_on_others` | int | comments viewer left on others' memories |

### People

| Key | Type | Source |
|-----|------|--------|
| `top_contributor_name` | string | most memories |
| `top_contributor_count` | int | |
| `top_contributor_share_pct` | float | |
| `top_contributor_is_viewer` | bool | |
| `second_contributor_name` | string | 2nd by count |
| `second_contributor_count` | int | |
| `min_contributor_name` | string | fewest (≥1) |
| `min_contributor_count` | int | |

### Time (group, viewer TZ)

| Key | Type | Source |
|-----|------|--------|
| `first_captured_at` | ISO8601 | earliest capture |
| `last_captured_at` | ISO8601 | latest capture |
| `group_peak_hour_local` | int | busiest hour, all members |
| `group_night_owl_share` | float | late-night share, all |
| `busiest_day` | YYYY-MM-DD | |
| `busiest_day_count` | int | captures that day |

### Social

| Key | Type | Source |
|-----|------|--------|
| `most_reacted_count` | int | |
| `most_reacted_author_name` | string | author display name only |

### Not available (v1)

| Desired beat | Why | Use instead |
|--------------|-----|-------------|
| `photos_of_user` | No face/subject tagging or CV | `memories_by_others` + honest copy (“Everyone else captured **{{memories_by_others}}** moments you didn’t.”) |
| Scene labels (“the beach”) | No location/scene classification | Time or contributor beats |

---

## Screen definition (`content/screens.json`)

```jsonc
{
  "id": "perspective_others",
  "type": "perspective",
  "order": 40,
  "analytics_used": ["photos_by_viewer", "memories_by_others", "member_count"],
  "gates": [
    { "fact": "memories_by_others", "op": "gte", "value": 1 },
    { "fact": "member_count", "op": "gte", "value": 2 }
  ],
  "emotion": "surprise",
  "copy": {
    "headline": "You captured {{photos_by_viewer}} photos.",
    "twist": "Everyone else captured {{memories_by_others}} you never saw.",
    "aside": "Same trip. Different eyes."
  },
  "visual": { "chapter": "redditSky", "motif": "Sunset" },
  "show_share": true
}
```

### Screen types

| `type` | Role |
|--------|------|
| `intro` | Capsule title + sealed context |
| `hook` | “You thought you’d seen it” |
| `payoff` | “You hadn’t” |
| `perspective` | Viewer vs group (your example) |
| `confession` | Apparently / time / contributor |
| `stat_hero` | One big number |
| `suspense` | Open Capsule CTA |

### Gate operators

`gte`, `gt`, `lte`, `lt`, `eq`, `neq`, `truthy`, `falsy`

---

## Resolved screen (composer output)

What the UI consumes:

```json
{
  "screen": 3,
  "id": "reveal_format_list",
  "type": "reveal",
  "layout": "format_list",
  "analytics_used": ["photos_total", "videos_total"],
  "headline": "This is what got locked in here.",
  "list_items": [
    { "label": "Photos", "value": "38", "tone": "pink" },
    { "label": "Videos", "value": "4", "tone": "blush" }
  ],
  "emotion": "surprise",
  "visual": { "chapter": "redditBlush", "motif": "FilmStrip" },
  "show_share": true
}
```

### Layout templates (`layout` field)

Independent from copy — each is a distinct composition (Reddit Recap variety):

| `layout` | Visual pattern | Typical beat |
|----------|----------------|--------------|
| `poster_bubbles` | Centered stacked speech bubbles + copy below | Intro |
| `editorial_punch` | Large left-aligned type, no bubbles | Hook |
| `stat_hero` | Giant number pill + minimal text + hero motif | Reveal, punch |
| `sentence_pill` | Inline sentence with pill number | Perspective, punch |
| `format_list` | Icon rows + colored pills (Reddit screen 3) | Reveal |
| `contributor_rank` | Ranked list with name pills | Punch person |
| `time_poster` | Giant hour / % as hero | Punch time |
| `warm_minimal` | Centered soft text, no motif | Warm beat |
| `cta_poster` | Bubble + headline + Open CTA | Final |

Story is composed **once per capsule** via `composeRecapStory(recap)` when recap loads — deterministic from facts (same capsule → same story on replay).

See `docs/RECAP_NARRATION_PLAYBOOK.md` for copy rules + `content/composeStory.ts` for spine logic.

---

## `viewer` block (API)

Nested summary for the authenticated member:

```json
{
  "memories_taken": 4,
  "photos_taken": 4,
  "videos_taken": 0,
  "voice_notes_taken": 0,
  "share_pct": 0.33,
  "memories_waiting": 8,
  "contributor_rank": 2,
  "is_top_contributor": false,
  "peak_hour_local": 19,
  "night_owl_share": 0.12,
  "reactions_on_others": 3,
  "comments_on_others": 1
}
```

`memories_waiting` = `memories_by_others` — moments sealed from the viewer, not “photos of you”.
