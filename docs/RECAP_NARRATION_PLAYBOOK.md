# Capsule Unlock Recap — Narration Playbook

> How we craft Wrapped-style copy. **Data → segment → screen → line.**
> Visual north star: Reddit Recap — white bubbles, bold black type, numbers in pills.
> Technical wiring: `docs/RECAP_CONTENT_SCHEMA.md` + `content/screens.json`.

---

## 1. How Spotify thinks (and how we adapt it)

Spotify Wrapped is not one script. It is a **priority engine**:

1. **Collect facts** — every measurable thing about this user × this capsule.
2. **Score interest** — rank facts by how surprising or story-worthy they are.
3. **Assign segments** — solo vs crew, slacker group vs everyone showed up, night owl vs early bird.
4. **Walk a fixed spine** — same emotional arc every time (setup → tension → reveal → roast → warmth → CTA).
5. **Pick one variant per beat** — the highest-priority line that passes gates for this capsule.
6. **Never ship a dead slide** — if a beat has no good data, skip it or swap in a fallback from another bucket.

Our spine (7 beats + 2 optional inserts):

| # | Beat | Job | Emotion |
|---|------|-----|---------|
| 1 | **Intro** | Name the capsule + why it was sealed | Anticipation |
| 2 | **Hook** | Create tension — versions of truth will collide | Curiosity |
| 3 | **Soft reveal** | Drop the scale (totals) + one format roast | Surprise |
| 4 | **Punch I** | Call out a person or effort imbalance | Humor / roast |
| 5 | **Punch II** | Call out time-of-day or chaos day | Humor / roast |
| 6 | **Quiet beat** | Release tension — warmth, no roast | Warmth |
| 7 | **CTA** | Open the capsule | Suspense → action |

**Optional inserts** (only when data is spicy):

| Insert | When | Where |
|--------|------|-------|
| **Perspective** | `memories_by_others ≥ 1` and group | After hook or after soft reveal |
| **Viewer mirror** | viewer is NOT top contributor but took ≥1 | Replace or augment Punch I |
| **Social beat** | `most_reacted_count ≥ 3` | Before quiet beat |
| **Frozen beat** | `was_frozen` | Layer onto intro aside, not its own slide |

Target runtime: **6–8 slides**, ~7s each. Skip beats that fail the “would I screenshot this?” test.

### Layout variety (critical)

Copy alone is not enough. Each beat uses a **`layout`** template independent of words:

| Beat | Layout | Reddit analog |
|------|--------|---------------|
| Intro | `poster_bubbles` | Screen 1 — stacked bubbles |
| Hook | `editorial_punch` | Bold editorial, no boxes |
| Reveal | `stat_hero` OR `format_list` | Screen 2 (big number) OR Screen 3 (list) |
| Perspective | `sentence_pill` | Inline pill number |
| Punch I | `contributor_rank` OR `stat_hero` | Screen 3 list OR big stat |
| Punch II | `time_poster` | Giant hour / % |
| Warm | `warm_minimal` | Breathing room, minimal |
| CTA | `cta_poster` | Bubble + command |

Implemented in `layouts/story/` + assigned in `content/composeStory.ts`.

---

## 2. Voice & language guide

### Tone ladder

```
Playful setup → Suspicious hook → Scale shock → Gentle roast → Time roast → Warm landing → Command
```

Never start with roast. Never end on roast. The quiet beat exists so the CTA feels earned.

### Rules (non-negotiable)

| Do | Don't |
|----|-------|
| Roast **behavior** — photo count, timing, effort, format choice | Roast appearance, body, relationships, drama |
| Use **first names** from `display_name` (first token only) | Use @username in story copy |
| Put **numbers in pills** — they are the hero | Write paragraphs |
| One idea per screen | Stack two roasts on one card |
| Sound like a friend who was there | Sound like a corporate dashboard |
| Use contractions (`couldn't`, `let's`) | Use “memories were captured by users” |

### Typography mapping (Reddit-style UI)

| Field | UI role | Example |
|-------|---------|---------|
| `bubble` | White speech bubble, top | `Capsule recap` · `{{capsule_name}}` |
| `headline` | Bold black, main read | `You scrolled {{n}} bananas` → `{{memories_total}} moments got locked in here.` |
| `twist` | Second line or pill-heavy sentence | `Everyone else captured {{memories_by_others}} you never saw.` |
| `aside` | Small roast / connective tissue | `Same trip. Different eyes.` |

Numbers and names **always** get pill treatment in UI even if copy is plain text in JSON.

### Phrasing pantry

**Setup:** sealed away · locked in · off-limits · nobody could peek · smart move  
**Tension:** different versions · camera roll wins · about to get exposed · not aligned  
**Scale:** between all of you · nobody's memory is complete · waiting for you  
**Roast verbs:** carried · out here doing the most · on vacation from documenting · zero chill  
**Warmth:** worth opening · made it matter · no roast here · just —  
**CTA:** enough talking · let's go see it · the movie starts now  

### Words to retire (too plain / generic)

- “You thought you'd seen this whole trip.” (overused)
- “You hadn't.” (without context)
- “Apparently,” as a crutch on every slide
- “Smart.” alone as a punchline
- Any line that works without `{{capsule_name}}` or a number

---

## 3. Segments (filters)

Every capsule gets tagged with **one primary segment** + **flags**. Composer picks copy from the matching bucket.

### Primary segment (`group_shape`)

| Segment | Gate | Story angle |
|---------|------|-------------|
| `solo` | `member_count == 1` | Letter to future self; no “everyone” copy |
| `duo` | `member_count == 2` | Two versions of truth; intimate tension |
| `small_crew` | `member_count 3–5` | Traceable; nobody hides |
| `crowd` | `member_count >= 6` | Chaos; someone definitely carried |

### Duration flags

| Flag | Gate | Copy flavor |
|------|------|-------------|
| `flash_seal` | `days_sealed <= 1` | “Barely had time to forget” |
| `week_seal` | `days_sealed 2–6` | Standard |
| `long_seal` | `days_sealed >= 7` | “A whole era” |
| `marathon_seal` | `days_sealed >= 30` | “This capsule aged like wine” |

### Effort flags

| Flag | Gate | Unlocks |
|------|------|---------|
| `everyone_contributed` | `contributor_count == member_count` | Positive intro layer |
| `slackers_present` | `contributor_count < member_count` | Slacker roast (`slackers_count = member_count - contributor_count`) |
| `one_person_show` | `top_contributor_share_pct >= 0.45` and `member_count >= 2` | Punch I priority |
| `balanced_crew` | top share `< 0.35` and `contributor_count >= 3` | “Democracy” copy |
| `viewer_carried` | `viewer_is_top_contributor` | Second-person roast |
| `viewer_ghost` | `memories_by_viewer == 0` and group | “You didn't add a single thing” |

### Format flags (computed in composer)

| Flag | Gate | Roast target |
|------|------|--------------|
| `photo_heavy` | `photos_total / memories_total >= 0.75` | Photo-first crowd |
| `video_shy` | `videos_total / photos_total < 0.15` and `photos_total >= 5` | Pictures not clips |
| `voice_silent` | `voice_notes_total == 0` | Nobody wanted to be heard |
| `voice_rare` | `voice_notes_total 1–2` and `memories_total >= 5` | Caps-lock typists |
| `voice_chaos` | `voice_notes_total / memories_total >= 0.25` | Voice note people |

### Time flags

| Flag | Gate | Punch II bucket |
|------|------|-----------------|
| `night_owl_group` | `group_night_owl_share >= 0.25` | Late night copy |
| `peak_hour_*` | derive from `group_peak_hour_local` | See bucket table §5 |

### Streak / freeze flags

| Flag | Gate |
|------|------|
| `streak_new` | `streak_current <= 1` |
| `streak_warming` | `streak_current 2–3` |
| `streak_tradition` | `streak_current >= 4` |
| `was_frozen` | `was_frozen == true` |
| `streak_perfect` | `streak_perfect == true` |

---

## 4. Derived facts (add to composer)

These are computed client-side from API `facts` — no new SQL needed:

```
slackers_count          = member_count - contributor_count
top_contributor_pct     = round(top_contributor_share_pct * 100)
photo_ratio             = photos_total / max(memories_total, 1)
video_ratio             = videos_total / max(memories_total, 1)
voice_ratio             = voice_notes_total / max(memories_total, 1)
peak_hour_bucket        = night | early_bird | morning | midday | evening
is_solo / is_duo        = member_count checks
memories_per_person     = memories_total / max(contributor_count, 1)
contributor_gap         = top_contributor_count - second_contributor_count
busiest_day_label       = formatted busiest_day ("March 14")
```

### Peak hour buckets

| Bucket | Hour (`group_peak_hour_local`) |
|--------|--------------------------------|
| `night` | 22–23 or 0–4 |
| `early_bird` | 5–8 |
| `morning` | 9–11 |
| `midday` | 12–16 |
| `evening` | 17–21 |

---

## 5. Screen library (crafted copy)

Priority = higher number wins when multiple variants pass gates.

---

### Screen 1 — INTRO (`type: intro`)

**Visual:** Bubble = capsule name. Headline = seal context. Aside = layered flag.

#### 1A — Group base (default)

**Gates:** `member_count >= 2`, `memories_total >= 1`  
**Priority:** 10

```
bubble:   "{{capsule_name}}"
headline: Sealed away {{days_sealed}} days ago. {{member_count}} people. Zero trust.
twist:    {{memories_total}} moments inside that none of you were allowed to peek at.
aside:    Smart system. Unhinged group.
```

#### 1B — Flash seal (short trip)

**Gates:** `days_sealed <= 1`, `member_count >= 2`  
**Priority:** 20

```
bubble:   "{{capsule_name}}"
headline: This capsule didn't even have time to collect dust.
twist:    {{member_count}} of you still couldn't be trusted to open it early.
aside:    {{memories_total}} moments. One day. Maximum paranoia.
```

#### 1C — Long seal

**Gates:** `days_sealed >= 14`  
**Priority:** 15

```
bubble:   "{{capsule_name}}"
headline: {{days_sealed}} days ago this got locked.
twist:    That's not a capsule. That's a whole era you agreed not to look at.
aside:    {{memories_total}} moments aged in there. You're about to find out how.
```

#### 1D — Solo

**Gates:** `member_count == 1`  
**Priority:** 100 (always wins solo)

```
bubble:   "{{capsule_name}}"
headline: You sealed this away from yourself {{days_sealed}} days ago.
twist:    Past you thought future you could handle the truth.
aside:    {{memories_total}} moments. No one else to blame.
```

#### 1E — Duo

**Gates:** `member_count == 2`  
**Priority:** 25

```
bubble:   "{{capsule_name}}"
headline: Two people. One capsule. {{days_sealed}} days of mutual restraint.
twist:    {{memories_total}} moments you both hid from each other on purpose.
aside:    Intimacy or insanity. Possibly both.
```

#### Layered asides (append to whichever intro wins — max 1)

| Flag | Aside layer |
|------|-------------|
| `was_frozen` | `This one got frozen mid-way. It's been through something. So have you.` |
| `slackers_present` | `Only {{contributor_count}} of {{member_count}} actually showed up with content. The rest know who they are.` |
| `everyone_contributed` | `Every single person contributed. Unusually responsible for this app.` |
| `streak_tradition` | `{{streak_current}} capsules deep with this crew. A tradition nobody voted on.` |
| `streak_warming` | `Capsule #{{streak_current}} with this group. You're building a habit.` |
| `small_crew` (3–5) | `Just {{member_count}} of you. No crowd to hide in. Everything is traceable.` |

---

### Screen 2 — HOOK (`type: hook`)

**Job:** Tension. Viewer should feel something is about to be contradicted.

#### 2A — Group memory collision (default)

**Gates:** `member_count >= 2`  
**Priority:** 10

```
headline: Everyone in this capsule remembers it differently.
twist:    That's about to become a problem.
aside:    The group chat version and the camera roll version are never the same file.
```

#### 2B — Duo standoff

**Gates:** `member_count == 2`  
**Priority:** 30

```
headline: You both think you know what happened.
twist:    You both only saw half of it.
aside:    Someone's timeline is about to get fact-checked by the other.
```

#### 2C — Camera roll vs consensus

**Gates:** `memories_total >= 3`, `member_count >= 2`  
**Priority:** 15

```
headline: There's the version everyone agreed on.
twist:    And then there's what actually got captured.
aside:    Spoiler: the camera roll doesn't care about your group narrative.
```

#### 2D — Exposure threat

**Gates:** `contributor_count >= 2`, `memories_total >= 4`  
**Priority:** 12

```
headline: Somebody's about to get exposed.
twist:    Could be you. Could be {{top_contributor_name}}. Could be everyone.
aside:    {{memories_total}} pieces of evidence. No alibis.
```

#### 2E — Solo hook

**Gates:** `member_count == 1`  
**Priority:** 100

```
headline: You remember how this felt.
twist:    Your camera remembers the parts you chose to forget.
aside:    {{memories_total}} receipts from past you.
```

#### 2F — Viewer ghost (spicy)

**Gates:** `memories_by_viewer == 0`, `member_count >= 2`, `memories_total >= 1`  
**Priority:** 40

```
headline: You didn't add a single moment to this capsule.
twist:    You're still here for the reveal.
aside:    That's either confidence or denial. We're about to find out which.
```

---

### Screen 3 — SOFT REVEAL (`type: reveal`)

**Job:** Scale shock + one format roast. Pick **one** main line + **one** format aside by priority.

#### Main line (pick highest priority that passes)

| Pri | Gates | Copy |
|-----|-------|------|
| 20 | `memories_total >= 10` | `Between all of you, {{memories_total}} moments got locked away in here.` |
| 15 | `contributor_count >= 2` | `{{contributor_count}} people. {{memories_total}} moments. Nobody's version of this is complete without the others.` |
| 10 | solo | `You left yourself {{memories_total}} moments to reopen.` |
| 10 | default | `{{memories_total}} moments. All of them off-limits until right now.` |

#### Format roast aside (pick highest priority — one only)

| Pri | Gates | Aside |
|-----|-------|-------|
| 30 | `voice_silent` and group | `Zero voice notes. Nobody wanted to be heard — only seen. Weird.` |
| 25 | `voice_rare` | `Only {{voice_notes_total}} voice note(s). Everyone else chose captions and chaos.` |
| 25 | `voice_chaos` | `{{voice_notes_total}} voice notes. This group communicates like a podcast.` |
| 20 | `video_shy` and `photos_total >= 5` | `{{photos_total}} photos, {{videos_total}} videos. Pictures now, argue later.` |
| 18 | `photo_heavy` | `{{photos_total}} photos. Everything else barely showed up. Photo-first crowd.` |
| 15 | `videos_total >= photos_total` and `videos_total >= 3` | `More videos than photos. Unusual. Documentary energy.` |
| 10 | `memories_total == 1` | `One moment. One capsule. Maximum drama per memory.` |

---

### Screen 4 — PUNCH I (`type: punch_person`)

**Job:** Contributor imbalance. Skip entirely if solo or balanced with no clear lead.

**Skip gates:** `member_count == 1` OR (`top_contributor_share_pct < 0.35` AND `contributor_count < 3`)

#### 4A — Dominant carrier

**Gates:** `top_contributor_share_pct >= 0.45`, `top_contributor_is_viewer == false`  
**Priority:** 20

```
headline: {{top_contributor_name}} carried this entire capsule.
twist:    {{top_contributor_count}} moments. {{top_contributor_pct}}% of everything in here.
aside:    Everyone else was along for the ride and somehow still tired.
```

#### 4B — Short roast

**Gates:** `top_contributor_share_pct >= 0.40`  
**Priority:** 15

```
headline: One person took nearly half of what's in here.
twist:    {{top_contributor_name}}, we need to talk.
aside:    {{top_contributor_pct}}% is not a collaboration. That's a solo project with witnesses.
```

#### 4C — Versus the room

**Gates:** `top_contributor_share_pct >= 0.35`, `member_count >= 3`  
**Priority:** 12

```
headline: {{top_contributor_name}} vs. the other {{member_count}}.
twist:    {{top_contributor_pct}}% to their name.
aside:    It was not close. It was not subtle.
```

#### 4D — Second place respect

**Gates:** `second_contributor_name` exists, gap meaningful (`contributor_gap >= 2`)  
**Priority:** 18

```
headline: {{top_contributor_name}} out here doing the most.
twist:    {{second_contributor_name}} quietly held second place.
aside:    Everyone else was on vacation from documenting their own vacation.
```

#### 4E — Viewer carried (you're the problem)

**Gates:** `viewer_is_top_contributor`, `member_count >= 2`  
**Priority:** 25

```
headline: You carried this capsule.
twist:    {{memories_by_viewer}} moments. {{top_contributor_pct}}% of everything in here.
aside:    The group owes you an apology or a drink. Possibly both.
```

#### 4F — Balanced democracy (fallback when no dominator)

**Gates:** `contributor_count >= 3`, top share `< 0.35`  
**Priority:** 5

```
headline: Nobody ran away with it.
twist:    {{contributor_count}} people split {{memories_total}} moments like civilized humans.
aside:    Suspiciously fair. Are you sure you know each other?
```

#### 4G — Min contributor (gentle, only if slackers)

**Gates:** `slackers_count == 0`, `min_contributor_count == 1`, `contributor_count >= 4`  
**Priority:** 8

```
headline: {{min_contributor_name}} showed up once.
twist:    One moment. Technically participated.
aside:    Quality over quantity. They chose quantity: one.
```

---

### Screen 5 — PUNCH II (`type: punch_time`)

**Job:** Time-of-day or chaos day. Skip if no peak hour data.

**Skip gates:** `group_peak_hour_local` missing AND `group_night_owl_share < 0.15`

Pick bucket by `peak_hour_bucket` OR `night_owl_group` flag.

| Bucket | Headline | Aside |
|--------|----------|-------|
| **night** | `{{top_contributor_pct}}% of everything happened after dark.` OR if `group_night_owl_share >= 0.25`: `{{group_night_owl_share_pct}}% captured between 10pm and 5am.` | `This certainly was not a wellness retreat.` |
| **early_bird** | `Peak hour: {{group_peak_hour_local}}:00 AM.` | `Someone was up for sunrise content. The rest of you were just… there.` |
| **morning** | `{{group_peak_hour_local}}:00 AM — phones out, brains optional.` | `Suspiciously well-rested of all of you.` |
| **midday** | `Peak chaos: {{group_peak_hour_local}}:00, broad daylight.` | `Nothing was hidden. Everyone saw everything happen in real time.` |
| **evening** | `{{group_peak_hour_local}}:00 in the evening. Prime time.` | `Every phone came out on cue. Almost like you rehearsed it.` |

#### Busiest day layer (append to aside if passes)

**Gates:** `busiest_day_count >= 3`

```
aside_layer: {{busiest_day_label}} takes the crown — {{busiest_day_count}} moments in one day. Everything else was filler.
```

#### Solo time beat

**Gates:** `member_count == 1`, peak hour exists

```
headline: Your peak capture hour was {{group_peak_hour_local}}:00.
twist:    Past you had a type.
aside:    {{busiest_day_count}} moments on {{busiest_day_label}} if that was your main character day.
```

---

### Screen 6 — QUIET BEAT (`type: warm`)

**Job:** Emotional exhale. Always include unless `memories_total == 0`.

#### 6A — Group warmth (default)

```
headline: No roast here.
twist:    Just everyone who put something in this capsule made it worth opening.
aside:    (none)
```

#### 6B — Solo warmth

**Gates:** solo

```
headline: You did this.
twist:    Past you bothered to save {{memories_total}} moments for a day like today.
aside:    That counts for something.
```

#### 6C — Duo warmth

**Gates:** duo

```
headline: Two people built this.
twist:    {{memories_total}} moments you both decided were worth keeping.
aside:    However it went — you kept it.
```

#### 6D — Social warmth (optional swap)

**Gates:** `most_reacted_count >= 3`

```
headline: One moment got {{most_reacted_count}} reactions.
twist:    {{most_reacted_author_name}} captured the one everyone felt.
aside:    You'll know it when you see it.
```

---

### Screen 7 — CTA (`type: suspense`)

**Always last. No share button.**

#### 7A — Group

```
bubble:   Ready?
headline: Enough talking about it.
twist:    Let's go see it.
cta:      Open Capsule
```

#### 7B — Solo

```
headline: You kept yourself waiting long enough.
twist:    Time to look.
cta:      Open Capsule
```

#### 7C — High discovery (others' memories waiting)

**Gates:** `memories_by_others >= 3`

```
headline: {{memories_by_others}} moments you never saw are one tap away.
twist:    The recap ends here. The actual story starts now.
cta:      Open Capsule
```

---

## 6. Optional inserts

### Perspective (group discovery)

**Gates:** `memories_by_others >= 1`, `member_count >= 2`  
**Insert after:** Screen 3 or 4

```
headline: You captured {{photos_by_viewer}} photos.
twist:    Everyone else captured {{memories_by_others}} you never saw.
aside:    Same trip. Different eyes.
```

Solo replacement:

```
headline: You sealed {{memories_total}} moments away from yourself.
twist:    Some of them you probably forgot you took.
aside:    That's the point.
```

### Frozen standalone (only if `was_frozen` and intro layer wasn't enough)

```
headline: This capsule got frozen mid-trip.
twist:    Even Memoria didn't trust you people for a minute.
aside:    It thawed. The evidence survived.
```

---

## 7. Edge-case matrix

| Scenario | Intro | Hook | Punch I | Punch II | Notes |
|----------|-------|------|---------|----------|-------|
| Solo, 1 memory | 1D | 2E | skip | optional | Shortest path: 5 slides |
| Solo, many | 1D | 2E | skip | time | |
| Duo, balanced | 1E | 2B | 4F or skip | time | |
| Group, 0 memories | empty intro | skip all | skip | skip | CTA only |
| Group, 1 memory | 1A | 2A short | skip | skip | Soft reveal: “One moment” |
| All slackers but 1 | 1A + slacker layer | 2D | 4A | time | |
| Viewer ghost | 1A | 2F | 4A about top | time | Spicy |
| Viewer carried | 1A | 2A | 4E | time | |
| streak == 1 | no streak layer | — | — | — | Don't mention tradition |
| streak >= 4 | streak_tradition layer | — | — | — | |
| was_frozen | frozen layer | — | — | — | |
| night_owl >= 40% | — | — | — | night priority | |

---

## 8. Selection algorithm (composer v2)

```
1. Tag capsule with segments + flags (derived facts)
2. For each beat in spine order:
   a. Filter variants where gates pass
   b. Sort by priority DESC
   c. If tie: hashPick(capsule_id + beat_id)
   d. If empty: use fallback or skip beat
3. Apply at most ONE intro layer aside
4. Apply at most ONE format roast on reveal
5. Apply busiest_day layer on punch_time if eligible
6. Cap total slides at 8; drop lowest-priority optional first
```

---

## 9. New facts to add (backend v2 — optional)

| Fact | Why |
|------|-----|
| `trip_days` | Calendar span first→last capture (more accurate than `days_sealed`) |
| `viewer_vs_group_peak_delta` | Viewer night owl vs group — “you were up when they weren't” |
| `comments_total` / `reactions_total` | Social beat richness |
| `first_capture_day` / `last_capture_day` | “Started March 1, ended March 9” copy |

`trip_days` can be computed now from `first_captured_at` / `last_captured_at` in composer.

---

## 10. Example resolved playlist (group, spicy)

```json
[
  {
    "screen": 1,
    "type": "intro",
    "headline": "Sealed away 12 days ago. 4 people. Zero trust.",
    "twist": "47 moments inside that none of you were allowed to peek at.",
    "aside": "Only 3 of 4 actually showed up with content. The rest know who they are.",
    "emotion": "anticipation"
  },
  {
    "screen": 2,
    "type": "hook",
    "headline": "Somebody's about to get exposed.",
    "twist": "Could be you. Could be Alex. Could be everyone.",
    "aside": "47 pieces of evidence. No alibis.",
    "emotion": "curiosity"
  },
  {
    "screen": 3,
    "type": "reveal",
    "headline": "Between all of you, 47 moments got locked away in here.",
    "aside": "38 photos. Everything else barely showed up. Photo-first crowd.",
    "emotion": "surprise"
  },
  {
    "screen": 4,
    "type": "punch_person",
    "headline": "Alex carried this entire capsule.",
    "twist": "22 moments. 47% of everything in here.",
    "aside": "Everyone else was along for the ride and somehow still tired.",
    "emotion": "humor"
  },
  {
    "screen": 5,
    "type": "punch_time",
    "headline": "31% captured between 10pm and 5am.",
    "aside": "This was not a wellness retreat. March 14 takes the crown — 11 moments in one day.",
    "emotion": "humor"
  },
  {
    "screen": 6,
    "type": "warm",
    "headline": "No roast here.",
    "twist": "Just — everyone who put something in this capsule made it worth opening.",
    "emotion": "warmth"
  },
  {
    "screen": 7,
    "type": "suspense",
    "headline": "Enough talking about it.",
    "twist": "Let's go see it.",
    "cta_label": "Open Capsule",
    "emotion": "suspense"
  }
]
```

---

## 11. Next implementation steps

1. Add derived facts + `peak_hour_bucket` to `composeScreens.ts`
2. Replace `screens.json` with this library (priorities + layers)
3. Wire `RecapStory` to render `headline` / `twist` / `aside` / `bubble` on Reddit card layouts
4. Playtest with real capsules (solo, duo, slacker group, night owl) and tune priorities
