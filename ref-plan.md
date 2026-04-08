# Plan: Transform markt into a Used Electronics Buying Copilot

## Context

The markt project has the right building blocks (brief creation, scoring, shortlist, comparison, seller drafting) but the product loop is fragmented. Users can see pieces of value, but the app doesn't carry them from intent to decision in one coherent flow. This plan implements the product strategy's Phase 1 (Days 1-30): make the core loop work around a single concept — the **Buy Mission**.

The central shift: everything revolves around a Mission (e.g., "iPhone 15 Pro, 256GB, under €850"). Creating a mission starts monitoring. Matches are scoped to a mission. Shortlisting, comparing, and contacting sellers all happen in context of that mission.

---

## Key Architectural Decisions

1. **Mission = renamed & extended ShoppingProfile** — not a new model. The `shopping_profiles` DB table keeps its name; the Go struct becomes `Mission` with new fields. Avoids destructive migration.

2. **Listings scoped to missions via `profile_id`** — add `profile_id` column to `search_configs` and `listings` tables. Creates a clean Mission → Searches → Listings hierarchy. The alternative (filtering by query text match) is fragile.

3. **Nav restructure, not just rename** — `/missions`, `/matches`, `/saved`, `/settings`. The assistant chat embeds into the missions page for natural-language creation rather than being a separate top-level route.

4. **"team" tier → "power"** — aligns with strategy's pricing tiers. Add `MaxMissions` to billing limits.

---

## Phase 1 Implementation Steps

### Step 1: Backend Data Model Changes

**1a. Rename ShoppingProfile → Mission, add fields**

- [internal/models/listing.go](internal/models/listing.go) — Rename `ShoppingProfile` to `Mission`. Add fields:
  - `Status string` (active/paused/completed)
  - `Urgency string` (urgent/flexible/no-rush)
  - `AvoidFlags []string`
  - `TravelRadius int` (km)
  - `Category string` (phone/laptop/camera/other)
- Update all references: `AssistantSession.DraftProfile` → `DraftMission`, `Recommendation.Profile` → `Recommendation.Mission`, `ShortlistEntry.ProfileID` → `MissionID`
- [internal/models/search.go](internal/models/search.go) — Add `ProfileID int64` to `SearchSpec`

**1b. Database migration**

- New file: `migrations/000002_missions.up.sql`
  - ALTER shopping_profiles: add `status`, `urgency`, `avoid_flags`, `travel_radius`, `category` columns
  - ALTER search_configs: add `profile_id BIGINT DEFAULT 0`
  - ALTER listings: add `profile_id BIGINT DEFAULT 0`
  - Create indexes on `profile_id`

**1c. Update store interface**

- [internal/store/iface.go](internal/store/iface.go):
  - `GetActiveShoppingProfile` → `GetActiveMission`
  - Add: `GetMission(id int64)`, `ListMissions(userID string)`, `UpdateMissionStatus(id int64, status string)`
  - `UpsertShoppingProfile` → `UpsertMission`
  - `ListRecentListings` gains optional `missionID int64` param (0 = all, for backward compat)

**1d. Update store implementations**

- [internal/store/store.go](internal/store/store.go) (SQLite) — implement new interface methods, update queries for new columns
- [internal/store/postgres.go](internal/store/postgres.go) — mirror changes

### Step 2: Backend Business Logic Updates

**2a. Update assistant** — [internal/assistant/assistant.go](internal/assistant/assistant.go)
- All `ShoppingProfile` references → `Mission`
- `autoDeployHunts` sets `profile_id` on created search configs
- `FindMatches` accepts optional `missionID` to scope searches
- `DraftSellerMessage` generates category-specific questions (battery health for phones, shutter count for cameras, keyboard/screen for laptops)

**2b. Expand risk flags** — [internal/scorer/scorer.go](internal/scorer/scorer.go)
- New flags: `missing_key_photos` (< 3 images for electronics), `no_battery_health` (phone/laptop without mention), `refurbished_ambiguity`
- Broader electronics keyword detection

**2c. Update billing** — [internal/billing/limits.go](internal/billing/limits.go)
- Rename `team` → `power`
- Add `MaxMissions int` to `Limits`: Free=1, Pro=10, Power=unlimited(0)

**2d. New API routes** — [internal/api/server.go](internal/api/server.go)
- `GET /missions` — list user's missions
- `POST /missions` — create mission (structured form)
- `GET /missions/{id}` — mission detail
- `PUT /missions/{id}` — update mission
- `PUT /missions/{id}/status` — pause/resume/complete
- `GET /missions/{id}/matches` — listings scoped to mission
- Existing `/searches` and `/listings/feed` endpoints remain working (backward compat)

### Step 3: Frontend Navigation & Routing

**3a. Update nav** — [web/app/(dashboard)/layout.tsx](web/app/(dashboard)/layout.tsx)
- NAV: Missions (`/missions`) → Matches (`/matches`) → Saved (`/saved`) → Settings (`/settings`)
- Branding: "markt" / "Used electronics copilot"
- Add active mission context to DashboardProvider

**3b. Extend dashboard context** — [web/components/DashboardContext.tsx](web/components/DashboardContext.tsx)
- Add: `activeMissionId`, `setActiveMission`, `missions[]`, `refreshMissions()`
- Persist active mission ID in localStorage

**3c. API client** — [web/lib/api.ts](web/lib/api.ts)
- Add `Mission` type
- Add `api.missions` namespace: `list()`, `create()`, `get(id)`, `update(id)`, `updateStatus(id, status)`, `matches(id, params)`

**3d. Route files**
- New: `web/app/(dashboard)/missions/page.tsx`
- New: `web/app/(dashboard)/matches/page.tsx`
- New: `web/app/(dashboard)/saved/page.tsx`
- Old routes (`/feed`, `/assistant`, `/shortlist`, `/searches`) redirect to new paths

### Step 4: Missions Page

**4a. Missions list page** — `web/app/(dashboard)/missions/page.tsx`
- Empty state: "Start your first buy mission" with two paths:
  - Structured form (category → brand/model → budget → conditions → must-haves)
  - "Describe what you want" opens embedded assistant chat
- Active missions: cards showing name, status, match count, last match time
- Actions: View Matches, Pause/Resume, Edit
- "View Matches" sets active mission and navigates to `/matches`

**4b. Mission form component** — new `web/components/MissionForm.tsx`
- Category selection (phone/laptop/camera) with visual cards
- Brand + model input
- Budget (euro input/slider)
- Condition preference (pills)
- Must-haves (chips, category-specific suggestions)
- Creates mission via API, triggers `autoDeployHunts` on backend

**4c. Embed assistant chat** — [web/components/AssistantChat.tsx](web/components/AssistantChat.tsx)
- Add `embedded?: boolean` prop (removes full-page chrome)
- On mission creation via chat, emit callback to parent

### Step 5: Matches Page (Mission-Scoped Feed)

**5a. Matches page** — `web/app/(dashboard)/matches/page.tsx`
- Mission selector dropdown at top
- Fetches `api.missions.matches(id)` when mission selected
- Same filter/sort capabilities as current feed but scoped
- Empty state links to mission creation
- SSE stream includes `missionID` for client-side filtering

**5b. Enhanced ListingCard** — [web/components/ListingCard.tsx](web/components/ListingCard.tsx)
- Replace score bar with verdict badge: Strong buy / Good deal / Fair price / Overpriced / Suspicious
- Prominent fair price display: "Ask: €850 | Fair: €920"
- Confidence indicator (low/medium/high)
- Better risk flag labels and grouping
- "Ask seller" button directly on card (not just from shortlist)
- Show first suggested question inline

### Step 6: Saved Page Enhancement

**6a. Comparison view** — `web/app/(dashboard)/saved/page.tsx`
- Toggle between card grid and side-by-side comparison table
- Comparison columns: name, ask price, fair price, condition, verdict, risks, fit score, suggested offer, next action
- Select up to 4 items to compare

**6b. Enhanced ShortlistTable** — [web/components/ShortlistTable.tsx](web/components/ShortlistTable.tsx)
- Comparison mode layout
- All suggested questions visible
- More prominent "Draft offer" button

### Step 7: Worker Pipeline Update

**7a. Link workers to missions** — [internal/worker/worker.go](internal/worker/worker.go)
- Look up `profile_id` from SearchSpec, pass to `SaveListing`
- SSE `deal_found` events include `missionID`

**7b. Respect mission status** — [internal/scheduler/scheduler.go](internal/scheduler/scheduler.go)
- Skip searches whose parent mission is paused/completed

### Step 8: Landing Page

- [web/app/page.tsx](web/app/page.tsx) or [landing/app/page.tsx](landing/app/page.tsx):
  - Headline: "Buy used electronics without overpaying"
  - Sub: "markt scans second-hand listings, estimates fair value, flags risks, and tells you exactly which sellers to contact first."
  - CTA: "Start a buy mission"
  - Category badges: phones, laptops, cameras

---

## Reusable Existing Code

- `autoDeployHunts()` in [assistant.go](internal/assistant/assistant.go) — already creates search configs from profiles, just needs `profile_id` wiring
- `computeRiskFlags()` in [scorer.go](internal/scorer/scorer.go) — extend, don't rewrite
- `DraftSellerMessage()` in [assistant.go](internal/assistant/assistant.go) — already functional, needs category-specific question templates
- `DashboardProvider` in [DashboardContext.tsx](web/components/DashboardContext.tsx) — extend with mission state
- `ListingCard`, `ShortlistTable`, `AssistantChat` — modify, don't replace
- SSE broker in [sse.go](internal/api/sse.go) — add mission ID to events

---

## Implementation Order

```
Step 1 (models + migration + store)  ← foundation, everything depends on this
  ↓
Step 2a-2c (assistant + scorer + billing)  ← can be parallelized
  ↓
Step 2d (API routes)  ← depends on 2a, 2c
  ↓
Step 3 (frontend nav + routing + API client)  ← depends on 2d
  ↓
Steps 4-6 (pages)  ← can be partially parallelized
  ↓
Step 7 (worker pipeline)  ← depends on Step 1
  ↓
Step 8 (landing page)  ← independent, can start anytime
```

---

## Verification

1. **Mission creation flow**: Create mission via form → verify search configs created with correct `profile_id` → verify workers pick up searches → verify listings saved with `profile_id`
2. **Mission-scoped matches**: Create two missions → verify `/missions/{id}/matches` returns only listings for that mission
3. **Verdict display**: Verify ListingCard shows correct verdict based on score/price/risk combinations
4. **Pause/resume**: Pause a mission → verify workers skip its searches → resume → verify they resume
5. **Backward compat**: Existing users with searches (no mission) still see their feed on `/matches` (missionID=0 returns all)
6. **Billing limits**: Free user cannot create more than 1 mission
7. **Seller questions**: Draft message for a phone listing includes battery health question; camera listing includes shutter count
  