# Memoria Frontend — Architecture, Design System & Phased Plan

> The app lives in `memoria/` (Expo SDK 54, expo-router, TypeScript strict).
> Iron rule: **no screen is built from raw RN views.** Every route composes the
> `Screen` shell plus design-system primitives. If a screen needs something the
> system doesn't have, add the primitive first, then use it.
> Mark checkboxes `[x]` as items are completed and update `docs/PROGRESS.md`.

---

## 1. Stack

| Concern | Choice |
|---|---|
| Framework | Expo SDK 54, React Native 0.81, expo-router v6 (file-based routes) |
| Language | TypeScript strict; API types generated from backend OpenAPI |
| Server state | TanStack React Query (all API data; caching, retries, invalidation) |
| Client state | Zustand (auth session, camera draft, UI state) — small and explicit |
| Styling | StyleSheet + design tokens (no inline hex/sizes anywhere only where very required) |
| Animations | Reanimated 4 (already installed) + expo-haptics |
| Safe areas | react-native-safe-area-context via the `Screen` primitive only |
| Media | expo-camera, expo-image, expo-video, expo-audio |
| Widgets | set up expo widgets |
| Push | expo-notifications |
| Storage | expo-secure-store (tokens), MMKV (preferences, last destination) |
| Payments | react-native-purchases (RevenueCat) |
| Builds | EAS Build + dev clients (camera/push/widgets don't run in Expo Go) |

## 2. Folder structure

```
memoria/
├── app/                          # expo-router routes ONLY (thin files)
│   ├── _layout.tsx               # providers: SafeArea, Query, Theme, Auth gate
│   ├── (auth)/                   # signed-out stack
│   │   ├── welcome.tsx  sign-in.tsx  sign-up.tsx  otp.tsx
│   │   ├── username.tsx  password.tsx  onboarding.tsx
│   ├── (app)/                    # signed-in
│   │   ├── (tabs)/
│   │   │   ├── _layout.tsx       # FloatingDock tab bar (reference style)
│   │   │   ├── index.tsx         # Memories: month timeline + [Capsule|Albums] pills
│   │   │   └── camera.tsx
│   │   ├── capsule/[id]/         # index (locked|unlocked switch), viewer, stats, settings
│   │   ├── album/[id]/           # index (grid), photo viewer
│   │   ├── create/               # capsule.tsx, album.tsx (modal group)
│   │   ├── profile/              # index, friends, stats, settings, subscription
│   │   ├── notifications.tsx
│   │   └── user/[username].tsx   # connect screen from invite links
├── src/
│   ├── components/
│   │   ├── ui/                   # PRIMITIVES (see §4) — app-agnostic
│   │   └── domain/               # composed app components (see §5)
│   ├── features/                 # hooks + logic per domain: auth/, capsules/,
│   │   ...                       #   albums/, camera/, friends/, profile/
│   ├── api/                      # generated client + React Query hooks
│   ├── stores/                   # zustand stores
│   ├── theme/                    # tokens.ts, ThemeProvider, useTheme
│   ├── lib/                      # utils: dates, haptics, formatters
│   └── notifications/            # push registration, handlers, deep-link map
└── targets/                      # widget extensions (Phase F8)
```

Route files stay under ~30 lines: they read params, call a feature hook, and
render a domain component. All logic lives in `src/`.

## 3. Design language

**Visual reference: `docs/design/reference-timeline.png` (Locket's timeline UI).
This screenshot is the north star for the app's look and the Memories screen
structure. Every screen should feel like it belongs in that app.**

What we adopt from the reference:

- **Palette**: deep warm espresso/brown backgrounds (`#1C1410` family) with a
  subtle warm radial glow behind content — not flat black. Soft cream text.
  **Amber/gold accent** (`#F5A623` family) for primary actions, badges, the
  "today" tile, and notification counts. Surfaces are slightly lighter warm
  browns with large radii.
- **The month timeline** (Memories screen core): a vertical scroll of rounded
  **month cards** ("May 2026", "June 2026"), each containing a **dot grid** —
  one dot per day, laid out in week columns. Days with memories render a small
  rounded photo thumbnail sitting on the dot; empty past days stay as muted
  dots; future days render as soft rounded squares; **today is a gold-bordered
  `+` tile** that jumps straight to the camera. A dashed path connects month
  cards, giving the feed a journey feel.
  - Memoria mapping: thumbnails on the grid are that day's memories across
    albums + unlocked capsules; sealed capsule days show a sealed marker (tiny
    locket icon, no preview — sealing holds visually too). Capsule unlock dates
    appear on future days as countdown markers, which makes anticipation
    visible on the timeline.
- **Top bar**: centered frosted segmented pill — ours is `[Capsule | Albums]`
  (active segment in a lighter pill with icon) — with the profile avatar in a
  circle top-right and notifications bell top-left.
- **Floating dock tab bar**: not a full-width bar — a floating rounded pill
  dock at the bottom with icon buttons (grid/timeline, camera, notifications)
  and gold count badges. It floats above content with the home-indicator inset
  respected; content scrolls behind it with bottom padding from TabBarInset.
- **Typography**: clean rounded sans (system / Inter) for UI like the
  reference; reserve a serif display face only for capsule names and unlock
  moments so emotional beats stand apart.
- **Shape & depth**: generous radii (16–24 cards, 12 thumbnails), soft
  hairline borders, depth from layered warm tones and glow rather than shadows.
- **Motion**: deliberate. Unlock animation is a hero moment (Reanimated
  scale/blur reveal). Subtle settle animation when thumbnails land on the grid.
  Haptics on capture, send, unlock.
- **Theme switch (F0)**: Light = clean black-on-white (white bg, black text, gray
  borders). Dark = inverted near-black (`#0A0A0A` bg, white text). Gold accent
  (`#F5A623`) reserved for primary CTAs and badges in both modes. The warm
  espresso reference palette returns for timeline chrome in F4+; launch ships
  B&W tokens with user-toggle persistence.
- **Icons**: `@expo/vector-icons` only in UI — never emoji as icons (emoji
  reserved for future `EmojiReactionBar` picker).
- Dark-first default; user can toggle light/dark (persisted).

## 4. Design system — `src/components/ui/` (primitives)

Tokens first (`src/theme/tokens.ts`): `colors`, `spacing` (4-pt scale),
`radii`, `typography` (named text styles), `durations`. Every primitive reads
tokens through `useTheme()`. **No raw values in components or screens.**

### The `Screen` primitive (the safe-area contract)

Every route renders exactly one `Screen`. It is the only place
`useSafeAreaInsets` is called for layout. It guarantees notch/home-indicator/
tab-bar correctness everywhere by construction.

```tsx
<Screen
  variant="scroll"        // "fixed" | "scroll" | "keyboard" (KeyboardAvoiding)
  edges={["top"]}         // safe edges to pad; tab screens omit "bottom"
                          // because the custom tab bar consumes that inset
  header={<Screen.Header title="Memories" left={<IconButton name="bell" />}
          right={<IconButton name="user" />} />}
  background="default"    // token name; "camera" variant = black, no padding
>
  {children}
</Screen>
```

- `Screen.Header`: handles top inset + title/left/right slots; large-title and
  inline modes; transparent mode for viewers.
- Scroll variant wires `contentInsetAdjustment`, bottom padding =
  `tabBarHeight + insets.bottom` automatically (via a TabBarInset context).
- Full-bleed screens (camera, unlock viewer) use `variant="fixed"`,
  `edges={[]}` and place controls with `useScreenInsets()` (exposed by Screen)
  so even immersive screens respect notches without ad-hoc math.

### Primitive inventory

- [x] `Screen` + `Screen.Header` (above)
- [x] `Text` — token-typed variants (`display`, `title`, `body`, `caption`, `mono-counter`)
- [x] `Button` — primary / secondary / ghost / destructive; loading + disabled;
      haptic on press
- [x] `IconButton` (44pt touch target), `Pressable` wrapper with scale feedback
- [x] `Input` — label, error, helper; `OTPInput` (6 cells); `SearchInput`
- [x] `Avatar` — sizes, fallback initials, stacked `AvatarGroup` (+N overflow)
- [x] `Card` — surface container; pressable variant
- [x] `PillTabs` — frosted segmented pill per the reference; used for
      [Capsule|Albums] and [Ongoing|Frozen|Completed]
- [x] `FloatingDock` — the floating pill tab bar (reference style): icon
      buttons, gold count badges, home-indicator inset, content-behind scrolling
- [x] `Sheet` — bottom sheet (destination picker, comments) with snap points,
      keyboard-aware, respects bottom inset
- [ ] `Badge` / `Tag` — plan badge, streak badge, frozen indicator
- [x] `Countdown` — live "Unlocks in 18 days / 03:12:09" with tick management
- [x] `EmptyState` — illustration slot, title, body, CTA (used everywhere)
- [x] `ListItem` — leading avatar/icon, title/subtitle, trailing slot
- [x] `Skeleton` — shimmer placeholders for every list/grid
- [x] `Toast` — success/error feedback layer (provider in root layout)
- [x] `ProgressDots` / `StepIndicator` — onboarding + creation flows
- [x] `EmojiReactionBar` — quick reactions row with counts + burst animation
- [x] `MosaicGrid` — virtualized photo grid (FlashList), used by albums and
      unlocked capsules

## 5. Domain components — `src/components/domain/`

Composed from primitives only:

- [x] `MonthCard` + `DayDotGrid` — the timeline building blocks (reference UI):
      month header, week-column dot grid, day cells rendering as dot (empty) /
      photo thumbnail (memory) / sealed marker (locked capsule day) / countdown
      marker (future unlock) / gold `+` tile (today → camera)
- [x] `TimelinePath` — dashed connector between month cards
- [x] `MemoriesTimeline` — virtualized list of `MonthCard`s powering the
      Memories screen (both Capsule and Albums segments render through it)
- [x] `CapsuleCard` — name, countdown, contributor `AvatarGroup`, streak flame,
      memory count, frozen/unlock-ready states (one component, state-driven)
- [x] `AlbumCard` — cover style, member avatars, count, lifespan countdown
- [x] `MemoryTile` (grid cell) + `MemoryViewer` (TikTok-style vertical pager:
      media, author chip, caption, voice-note player, reactions, comments button)
- [ ] `VoiceNotePlayer` (waveform + play) and `VoiceNoteRecorder` (hold-to-record ring)
- [x] `CameraView` — viewfinder, flash/flip, capture button (tap photo / hold video
      with progress ring), mode hint
- [x] `PostCaptureEditor` — preview, caption field, voice note, retake/send
- [x] `DestinationPicker` — Sheet of active capsules+albums, remembers last used
- [x] `InviteStatusList` — per-member Accepted/Pending/Declined with live updates
- [x] `UnlockCelebration` / `RecapStory` — multi-chapter narrated unlock cards
      (`GET /v1/capsules/{id}/recap`); horizontal story → vertical photos;
      ambient sound; once per member via `unlock_reveal_seen`
- [x] `StatsPanels` — unlock stats, profile heatmap (calendar), lifetime numbers
- [x] `FriendRow`, `RequestRow`, `QRShareCard` (QR render of invite link)
- [x] `CommentsThread` — sheet with list + composer
- [x] `PlanPaywall` — upgrade sheet driven by RevenueCat offerings + limit-hit context

## 6. Data layer conventions

- Generated API types from backend `openapi.yaml` (`make types` in app repo).
- One React Query hook per endpoint in `src/api/hooks/` (e.g. `useCapsule(id)`,
  `useCreateMemory()`); mutations invalidate precisely, optimistic updates for
  reactions/comments.
- Error codes from the API map to user strings in one place
  (`src/lib/errors.ts`) — includes paywall triggers (`capsule_limit_reached` →
  opens `PlanPaywall`).
- Auth: tokens in SecureStore; axios/fetch wrapper auto-refreshes on 401 once;
  Zustand `useSession` drives the root `(auth)`/`(app)` gate.
- Deep links: `memoria://` + universal links for invite links and notification
  taps (route map in `src/notifications/links.ts`).

---

## Phased frontend checklist

Frontend phases track backend phases (F1 needs B1, F4 needs B5, etc.).

### Phase F0 — Foundation & design system core

- [x] Prune Expo template demo screens/components
- [x] Folder structure per §2; path aliases (`@/ui`, `@/features`, ...)
- [x] Theme tokens + ThemeProvider + dark/light (B&W light, inverted dark + gold accent)
- [x] Fonts loaded (Inter sans) with splash holdback
- [x] Primitives: `Screen` + Header, `Text`, `Button`, `IconButton`, `Input`,
      `Card`, `Toast`, `Skeleton`, `EmptyState`, `ListItem`, `OTPInput`, `PillTabs`
- [x] Root `_layout.tsx`: SafeAreaProvider, QueryClient, Theme, Toast, auth gate
- [x] `FloatingDock` tab bar with bottom-inset handling + TabBarInset context

> **No mock data — ever.** Screens and components are only built/wired against
> real API data. Until an endpoint exists, the screen shows its real empty/
> loading state. The timeline components (`MonthCard`, `DayDotGrid`,
> `TimelinePath`, `MemoriesTimeline`) are therefore built in F4/F5 directly
> against `GET /v1/timeline`, matching `docs/design/reference-timeline.png`.
- [x] API client wrapper + hand-written types from openapi + React Query setup
- [x] EAS dev build profile (`eas.json` development)

### Phase F1 — Auth & onboarding (needs B1)

- [x] Welcome screen; sign-up flow: email → OTP (`OTPInput`) → password →
      username (realtime availability with debounce) → display name — `StepIndicator`
      driven (avatar capture deferred to F2/F3)
- [x] 3-screen onboarding (Capsules / Albums / Camera explainers)
- [x] Sign-in + OTP password reset
- [x] Session persistence, auto-refresh, sign-out
- [x] Push permission prompt + token registration (`PUT /v1/me/push-token`; graceful Expo Go skip)

### Phase F2 — Camera & capture (needs B2)

- [x] `CameraView`: rear default, flip, flash, tap/hold capture, 10s boomerang
      recording with progress ring
- [ ] Boomerang playback (ping-pong looping client-side)
- [x] `PostCaptureEditor`: caption, retake/send (voice note deferred)
- [x] Upload flow: presign → direct PUT → confirm → POST memory; toast on failure
- [x] `DestinationPicker` sheet + last-destination memory (AsyncStorage)
- [x] Empty state when no active destinations (CTA → create flows)

### Phase F3 — Friends & profile shell (needs B3)

- [x] Profile screen: avatar (camera or gallery → presign/upload → `PUT /me/avatar`),
      display name (editable), username, member-since, plan badge
- [x] Friends list, incoming/outgoing requests, remove, block (with confirm)
- [x] `QRShareCard` + share-link sheet; deep link → `user/[username]` connect screen
- [x] Notifications screen (in-app list) + unread badge on bell icon

### Phase F4 — Albums (needs B5)

- [x] Create-album modal flow: name → cover style picker → friend picker → pending state
- [x] Invite accept/decline UI (from notification deep link + list)
- [x] Wire `MemoriesTimeline` (Albums segment) to real data: album memories as
      day thumbnails on the month grid
- [x] Album screen: header (cover, avatars, count, lifespan countdown),
      `MosaicGrid`, contribution balance line, author badge per tile
- [x] Photo viewer: full screen, reactions, `CommentsThread`, voice playback
- [x] Active | Archived list views; archived = view-only treatment
- [x] Live-ness: refetch on push + on foreground (sockets later if needed)

### Phase F5 — Capsules (needs B6)

- [x] Create-capsule modal flow: type → name/description → calendar unlock date
      (plan max enforced with paywall upsell) → friend invites
- [x] Pending capsule screen: `InviteStatusList` live, 48h countdown, reinvite flow
- [x] Locked capsule screen: countdown, memory count, contributors with last
      activity, streak flame, frozen state, unfreeze vote UI
- [x] Capsule list with `PillTabs` filter: Ongoing | Frozen | Completed
- [x] Wire `MemoriesTimeline` (Capsule segment): sealed markers on contribution
      days, countdown markers on future unlock dates, photo thumbnails for
      unlocked capsules
- [x] Unlock moment: 3-slide `UnlockStory` (+ ambient sound, media stack,
      highlight; replay from stats) then grid / `MemoryViewer` pager
- [x] Grid toggle inside unlocked capsule; tap-through to viewer position
- [x] Reactions + comments (post-unlock only), stats screen
- [x] Frozen-at-unlock: blocked-member state + admin unblock UI (Plus/Pro)

### Phase F6 — Notifications & polish (needs B7)

- [x] Notification settings screen (8 category toggles)
- [x] Push deep-link routing for every category
- [x] Foreground flush call; badge counts
- [x] Animation/haptics polish pass; loading/error/empty audit on every screen

### Phase F7 — Subscriptions, stats, account (needs B8, B9)

- [x] RevenueCat SDK + `PlanPaywall` (triggered contextually on limit errors +
      from profile)
- [x] Subscription management screen
- [x] Contribution heatmap + lifetime stats screens
- [x] Export flow (request → progress → share sheet)
- [x] Account deletion flow (typed confirmation)

### Phase F8 — Widgets (needs B10)

- [x] iOS widget scaffold (`targets/ios/` + `@bacons/apple-targets` / SDK 56 `expo-widgets` upgrade path documented)
- [x] Android widget scaffold (`targets/android/` + `react-native-android-widget` notes)
- [x] Background refresh: silent push `widget_refresh` → `syncWidgetFeed()` → shared storage; also on app open/foreground
- [x] Widget configuration: focus picker (auto / albums / capsules) in Settings + manual refresh

### Phase F9 — Release readiness

- [x] App icons, splash, store screenshots/copy (assets wired in `app.json`; replace placeholders pre-submit)
- [x] Sentry stub (`EXPO_PUBLIC_SENTRY_DSN` + `src/lib/sentry.ts`); product analytics
      (`src/lib/analytics.ts` → batched `POST /v1/analytics/events`; see `docs/ANALYTICS.md`)
- [x] Performance pass: `MosaicGrid` → FlashList; expo-image caching; cold start waits fonts + session hydrate
- [x] Accessibility: `accessibilityLabel` on IconButtons; `Screen.Header` header role + title label
- [x] EAS production profile + README deploy section
- [x] E2E happy path: Maestro flow + manual checklist in README

### Post-launch — Offline capsule queue (2026-07-12)

- [x] Local outbound queue (`src/features/offlineQueue/`) — copy captures to sandbox, persist metadata
- [x] Capsule-only offline capture; albums stay online-only
- [x] Cached ongoing capsules for offline destination picker
- [x] Sync worker: presign → upload → confirm → POST with `Idempotency-Key` + `captured_at`
- [x] `OfflineQueueButton` beside Capsules `+` (badge + sheet + sync now)
- [x] Auto-sync on app foreground + 30s poll while queue non-empty

### Post-launch — Social polish (2026-07-12)

- [x] Snapchat-style streak: `🔥 N` in ongoing capsule list, detail InfoRow, and stats grid
- [x] Notifications: infinite pagination (20/page + Load more) + swipe-to-delete rows (`NotificationCenterScreen`)
- [x] Reactions: per-emoji counts on chips, my reaction highlighted (server `my_emoji`), tap again to remove, "See who reacted" sheet
- [x] Comment composer: single field with inline Post pill (`Input` `trailing` prop)
