# xolto — backend

Go API server and worker runtime for the xolto used-electronics buying copilot.

Deploy target: **Railway** behind `https://api.xolto.app`

Frontend repos that call this API:

- `TechXTT/dash.xolto.app` — main buyer app
- `TechXTT/admin.xolto.app` — ops dashboard
- `TechXTT/www.xolto.app` — landing page

---

## Setup

```bash
git clone https://github.com/TechXTT/xolto.git
cd xolto
go mod download
cp .env.example .env          # fill in JWT_SECRET and DATABASE_URL at minimum
go run ./cmd/server
```

Default listen address: `http://localhost:8000`

Default database: `xolto-server.db` (SQLite) when `DATABASE_URL` is not set.

No `config.yaml` is required to run the API server. `config.yaml` / `config.yaml.example` configures the CLI mode only.

**CLI mode** (local polling without the web stack):

```bash
go run ./cmd/xolto --config config.yaml [--once] [--dry-run] [--verbose]
```

---

## Environment Variables

All configuration is via environment variables. Load from `.env` locally (the server calls `godotenv.Load()` on startup).

### Required

| Variable | Description |
|---|---|
| `JWT_SECRET` | HS256 signing secret — minimum 32 random bytes. Generate: `openssl rand -hex 32` |
| `DATABASE_URL` | Postgres DSN (`postgres://...`) or SQLite path. Defaults to `xolto-server.db` when unset. |

### Server

| Variable | Default | Description |
|---|---|---|
| `SERVER_ADDR` | `:8000` | Listen address |
| `APP_BASE_URL` | `http://localhost:3000` | Dash URL — used for Stripe redirect URLs and CORS fallback |
| `ADMIN_BASE_URL` | `http://localhost:3002` | Admin URL — used for CORS fallback |
| `ALLOWED_ORIGINS` | — | Comma-separated CORS allowlist. Falls back to `APP_BASE_URL` + `ADMIN_BASE_URL` when unset. |
| `CORS_ALLOWED_ORIGINS` | — | Deprecated alias for `ALLOWED_ORIGINS`; accepted for one release cycle. |
| `TRUST_PROXY` | `false` | Set `true` when requests pass through a trusted reverse proxy (X-Forwarded-For). |
| `APP_ENV` | — | Controls log format. Set to `production` for JSON logs; any other value uses text. |

### HTTP Timeouts

| Variable | Default | Description |
|---|---|---|
| `SERVER_READ_TIMEOUT` | `15s` | |
| `SERVER_WRITE_TIMEOUT` | `30s` | |
| `SERVER_IDLE_TIMEOUT` | `60s` | |
| `SERVER_READ_HEADER_TIMEOUT` | `5s` | |

### Database Pool

| Variable | Default | Description |
|---|---|---|
| `DB_MAX_OPEN_CONNS` | `25` | Maximum open connections |
| `DB_MAX_IDLE_CONNS` | `5` | Maximum idle connections |
| `DB_CONN_MAX_LIFETIME` | `30m` | Connection max lifetime (Go duration string) |

### AI

| Variable | Default | Description |
|---|---|---|
| `AI_API_KEY` | — | OpenAI-compatible API key. AI features are disabled when unset. |
| `AI_BASE_URL` | `https://api.openai.com/v1` | API base URL — override to use a local LLM or alternative provider. |
| `AI_MODEL` | `gpt-4o-mini` | Model name passed to the AI API. |
| `AI_PROMPT_VERSION` | `1` | Bump to invalidate cached AI scores. |

### Observability

Sentry is a no-op when `SENTRY_DSN` is not set. No side effects in local dev.

| Variable | Default | Description |
|---|---|---|
| `SENTRY_DSN` | — | Sentry Data Source Name. Empty = disabled. |
| `SENTRY_ENVIRONMENT` | `development` | Sentry environment tag (`production`, `staging`, `development`). Also gates the `/debug/panic` route — that route is only registered when this is explicitly `development`, `staging`, `local`, or `test`. |
| `SENTRY_RELEASE` | — | Commit SHA to tag events. Can also be injected at build time via `-ldflags "-X .../observability.Release=<sha>"`. |

### OAuth

| Variable | Description |
|---|---|
| `GOOGLE_CLIENT_ID` | Google OAuth 2.0 client ID |
| `GOOGLE_CLIENT_SECRET` | Google OAuth 2.0 client secret |
| `GOOGLE_REDIRECT_URL` | OAuth redirect URI registered in Google Cloud Console |

### Billing (Stripe)

All Stripe vars are optional. When unset the server runs in free-tier-only mode.

| Variable | Description |
|---|---|
| `STRIPE_SECRET_KEY` | Stripe secret key (`sk_live_...` or `sk_test_...`) |
| `STRIPE_WEBHOOK_SECRET` | Stripe webhook signing secret (`whsec_...`) |
| `STRIPE_PRO_PRICE_ID` | Price ID for the Pro plan (`price_...`) |
| `STRIPE_POWER_PRICE_ID` | Price ID for the Power plan (`price_...`) |

### SMTP / Deal Alerts

| Variable | Default | Description |
|---|---|---|
| `SMTP_HOST` | — | SMTP server hostname |
| `SMTP_PORT` | `587` | SMTP server port |
| `SMTP_USER` | — | SMTP username |
| `SMTP_PASS` | — | SMTP password |
| `SMTP_FROM` | `alerts@xolto.app` | From address for outbound alerts |
| `ALERT_SCORE` | `8.0` | Minimum score threshold to trigger an email alert |

### Admin

| Variable | Description |
|---|---|
| `ADMIN_EMAILS` | Comma-separated list of bootstrap admin email addresses |
| `ADMIN_IP_ALLOWLIST` | Comma-separated CIDR ranges / IPs allowed to reach admin routes. Empty = no IP filter. |

### Testing

| Variable | Description |
|---|---|
| `TEST_POSTGRES_DSN` | Postgres DSN for integration tests. When unset, Postgres integration tests skip automatically. |

---

## API Surface

Base URL: `https://api.xolto.app` (production) or `http://localhost:8000` (local).

All authenticated routes require an `Authorization: Bearer <access_token>` header or a valid `xolto_access` cookie.

Error envelope:

```json
{ "ok": false, "error": "<message>" }
```

### Health

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/healthz` | None | Liveness check. Returns `{"ok":true,"service":"xolto-server"}`. |
| GET | `/debug/panic` | None | Test Sentry panic capture. Registered only when `SENTRY_ENVIRONMENT` is explicitly `development`, `staging`, `local`, or `test`. Requires header `X-Sentry-Test-Panic: 1`. |

### Auth

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/auth/providers` | None | Returns enabled auth providers. |
| POST | `/auth/register` | None | Email + password registration. |
| POST | `/auth/login` | None | Email + password login. Returns access + refresh tokens. |
| GET | `/auth/google/start` | None | Redirects to Google OAuth consent screen. |
| GET | `/auth/google/callback` | None | Google OAuth callback — exchanges code for session. |
| POST | `/auth/refresh` | None | Exchange refresh token for a new access token. |
| POST | `/auth/logout` | None | Invalidates the current session. |
| GET | `/users/me` | Required | Returns the authenticated user's profile. |

### Missions + Searches

| Method | Path | Auth | Description |
|---|---|---|---|
| GET/POST | `/missions` | Required | List all missions (GET) or create a mission (POST). |
| GET/PATCH/DELETE | `/missions/` | Required | Get, update, or delete a mission by ID (`/missions/{id}`). |
| GET/POST | `/searches` | Required | List search configs (GET) or create one (POST). |
| POST | `/searches/run` | Required | Trigger an immediate search run across all active searches. |
| POST | `/searches/generate` | Required | AI-generate search queries for the authenticated user's missions. |
| GET/PATCH/DELETE | `/searches/` | Required | Get, update, or delete a search config by ID (`/searches/{id}`). |

### Matches + Listings

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/matches` | Required | Paginated matches for the authenticated user. See contract below. |
| POST | `/matches/feedback` | Required | Record approve/dismiss/clear feedback on a listing. |
| POST | `/matches/analyze` | Required | Fetch and score an arbitrary marketplace URL. |
| GET | `/listings/feed` | Required | Recent listings for the authenticated user, optionally filtered by `mission_id`. |
| GET | `/shortlist` | Required | Get the user's shortlist. |
| POST/DELETE | `/shortlist/` | Required | Add (`POST /shortlist/{item_id}`) or remove (`DELETE /shortlist/{item_id}`) a listing. |
| POST | `/shortlist/{item_id}/draft` | Required | Generate a seller draft message for a shortlisted item. |
| POST | `/draft-note` | Required | Generate a verdict-shaped seller note. See contract below. |
| GET | `/actions` | Required | List action drafts for the authenticated user. |

### Assistant

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/assistant/converse` | Required | Send a message to the assistant. Returns structured `AssistantReply`. Requires Pro or Power tier. |
| GET | `/assistant/session` | Required | Retrieve the current assistant session state. |

### Billing

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/billing/checkout` | Required | Create a Stripe Checkout session. Returns a redirect URL. |
| GET | `/billing/portal` | Required | Create a Stripe Customer Portal session. Returns a redirect URL. |
| POST | `/billing/webhook` | None | Stripe webhook receiver. Validates `Stripe-Signature` header. |

### Realtime

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/events` | Required | SSE stream. Validates JWT on connect; then pushes `deal_found` frames as new matches are scored. |

### Admin

All admin routes require operator or owner role unless noted. Access is controlled by `ADMIN_EMAILS`, `ADMIN_IP_ALLOWLIST`, and the user's role in the database.

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/admin/stats` | Operator+ | AI usage stats. Optional `?days=N` (default 30, max 365). |
| GET | `/admin/users` | Operator+ | List all users (sanitized — no password hashes). |
| POST | `/admin/users/{id}/tier` | Operator+ | Update a user's subscription tier. |
| POST | `/admin/users/{id}/role` | Owner | Update a user's role. |
| POST | `/admin/users/{id}/admin` | Operator+ | Set/unset admin flag on a user. |
| POST | `/admin/missions/{id}/status` | Operator+ | Update a mission's status. |
| POST | `/admin/searches/{id}/enabled` | Operator+ | Enable or disable a search config. |
| POST | `/admin/searches/{id}/run` | Operator+ | Trigger an immediate run of a specific search. |
| GET | `/admin/usage` | Operator+ | AI usage timeline. Optional `?days=N` (default 7, max 90). |
| GET | `/admin/search-runs` | Operator+ | Search run log. Optional `?days=N&status=&marketplace=&country=&user=&limit=N`. |
| GET | `/admin/business/overview` | Operator+ | Business metrics overview. |
| GET | `/admin/business/subscriptions` | Operator+ | Subscription list. |
| GET | `/admin/business/revenue` | Operator+ | Revenue timeline. |
| GET | `/admin/business/funnel` | Operator+ | Funnel metrics. |
| GET | `/admin/business/cohorts` | Operator+ | Cohort retention. |
| GET | `/admin/business/alerts` | Operator+ | Business alert feed. |
| POST | `/admin/business/subscriptions/{id}` | Owner | Mutate a subscription record. |
| POST | `/admin/business/reconcile` | Owner | Trigger Stripe reconciliation. |

---

## Contracts

This section is the canonical contract reference for `dash.xolto.app`, `admin.xolto.app`, and any new API caller. Do not re-derive or re-implement the logic described here.

### Verdict Enum

Canonical source: [`internal/scorer/verdict.go`](internal/scorer/verdict.go)

Four values — exhaustive, stable:

| Value | Meaning |
|---|---|
| `buy` | Strong signal to purchase at or below asking price |
| `negotiate` | Asking price is above fair value but within negotiable range (price_ratio 1.00–1.30, confidence medium/high, no risk flags) |
| `ask_seller` | Evidence is thin or signals are missing; seek clarification before committing |
| `skip` | Clear disqualifier: overpriced (price_ratio > 1.30), hard risk flag, or condition + price mismatch |

Fallthrough default (when no rule matches): `ask_seller` (trust-preservation bias).

The dash must treat these as opaque enum strings. Do not hardcode scoring thresholds in frontend code.

### Risk Flag Set

Seven stable keys, priority-ordered (highest priority first):

| Key | Signal |
|---|---|
| `anomaly_price` | Asking price suspiciously low vs fair value — possible fraud/theft signal |
| `vague_condition` | Listing contains "as-is", "untested", or equivalent |
| `no_battery_health` | Phone/laptop listing with no battery health signal |
| `missing_key_photos` | Fewer than 3 photos |
| `no_model_id` | No model number in title |
| `unclear_bundle` | Bundle/lot listing with unclear item scope |
| `refurbished_ambiguity` | Refurb listing without grading or warranty signal |

`anomaly_price` is a hard risk flag: any listing with this flag routes to `skip` regardless of other signals. All other listed flags are soft: they route to `ask_seller` unless a harder rule already disqualifies.

### `GET /matches` — Dual-Envelope Contract

Query parameters:

| Parameter | Type | Default | Allowed Values |
|---|---|---|---|
| `limit` | int | `20` | 1–100 |
| `offset` | int | `0` | >= 0 |
| `mission_id` | int64 | `0` (all missions) | |
| `sort` | string | `newest` | `newest` \| `score` \| `price_asc` \| `price_desc` |
| `market` | string | `all` | `all` \| `marktplaats` \| `vinted` \| `vinted_nl` \| `vinted_dk` \| `olx_bg` \| `olxbg` |
| `condition` | string | `all` | `all` \| `new` \| `like_new` \| `good` \| `fair` |
| `min_score` | int | `0` | 0–10 |

Notes: `vinted` normalizes to `vinted_nl`. `olx_bg` (dash vocabulary) normalizes to `olxbg` (stored). Sort `newest` = `last_seen DESC, item_id ASC`.

Response envelope:

```json
{
  "items":   [ <enriched Listing>, ... ],
  "matches": [ <enriched Listing>, ... ],
  "limit":   20,
  "offset":  0,
  "total":   142
}
```

**Invariant:** `items` and `matches` carry byte-identical enriched payloads. Both keys exist on every response. New fields added to the wrapper struct must appear on both keys. This invariant was established to fix INC-20260417c; do not break it.

`total` is the filtered count independent of `limit`/`offset` — use it for pagination UI.

Each enriched listing item carries the standard `Listing` fields plus:

| Field | Type | Description |
|---|---|---|
| `MustHaves` | `[]MustHaveMatch` | One entry per mission must-have, in source order. Empty slice (never null) when no mission is specified or mission has no required features. |

### `ScoredListing` Shape

Returned by `POST /matches/analyze`. All fields:

| Field | Type | Description |
|---|---|---|
| `Listing` | object | Embedded `Listing` struct (see below) |
| `Score` | float64 | 0–10 quality score |
| `OfferPrice` | int | Suggested offer price in cents |
| `FairPrice` | int | Estimated fair market price in cents |
| `MarketAverage` | int | Market average price in cents |
| `Confidence` | float64 | 0–1 confidence in the fair price estimate |
| `Reason` | string | Human-readable explanation of the score |
| `ReasoningSource` | string | Which reasoning path produced the score |
| `SearchAdvice` | string | Suggestions for improving the search |
| `ComparableDeals` | `[]ComparableDeal` | Comparable listings used for pricing |
| `RiskFlags` | `[]string` | Subset of the 7 stable risk flag keys |
| `RecommendedAction` | string | One of the 4 verdict enum values |
| `ComparablesCount` | int | Number of comparable deals used |
| `ComparablesMedianAgeDays` | int | Median age of comparables in days; 0 if none |
| `MustHaves` | `[]MustHaveMatch` | Must-have match results (when mission_id provided) |

`Listing` fields included in the embed:

| Field | Type | Description |
|---|---|---|
| `MarketplaceID` | string | e.g. `marktplaats`, `vinted_nl`, `olxbg` |
| `ItemID` | string | Marketplace-specific listing identifier |
| `Title` | string | |
| `Description` | string | |
| `Price` | int | Asking price in cents |
| `PriceType` | string | `fixed` \| `negotiable` \| `bidding` \| `free` \| `see-description` \| `exchange` \| `reserved` \| `fast-bid` |
| `Condition` | string | `new` \| `like_new` \| `good` \| `fair` |
| `Seller` | object | `{ID, Name}` |
| `Location` | object | `{City, Distance}` (Distance in metres) |
| `Date` | string | ISO 8601 listing date |
| `URL` | string | Original marketplace listing URL |
| `ImageURLs` | `[]string` | |
| `Score` | float64 | |
| `FairPrice` | int | cents |
| `OfferPrice` | int | cents |
| `Confidence` | float64 | |
| `Reason` | string | |
| `RiskFlags` | `[]string` | |
| `RecommendedAction` | string | |
| `ComparablesCount` | int | |
| `ComparablesMedianAgeDays` | int | |
| `Feedback` | string | `""` \| `"approved"` \| `"dismissed"` |

### `POST /draft-note` — Contract

Request body:

```json
{
  "verdict":    "buy",
  "listing_id": "<item_id>",
  "mission_id": 0
}
```

| Field | Type | Required | Allowed Values |
|---|---|---|---|
| `verdict` | string | Yes | `buy` \| `negotiate` \| `ask_seller` \| `skip` |
| `listing_id` | string | Yes | Item ID of a listing in the user's store |
| `mission_id` | int64 | No | Mission ID for context (0 = no mission) |

Response (plain object, not an envelope):

```json
{
  "text":  "<plain-text seller note>",
  "shape": "buy",
  "lang":  "nl"
}
```

| Field | Type | Values |
|---|---|---|
| `text` | string | Plain text (no markdown). Ready to copy-paste to a seller. |
| `shape` | string | `buy` \| `negotiate` \| `ask_seller` \| `generic` |
| `lang` | string | `nl` \| `en` |

Shape derivation: `buy → buy`, `negotiate → negotiate`, `ask_seller → ask_seller`, `skip → generic`.

Lang detection: defaults to `nl`. Falls back to `en` when the listing title/description contains no Dutch stop-word hits.

### `GET /events` — SSE Contract

Stream type: `text/event-stream`

On connect, the server immediately emits a connected frame:

```
data: {"type":"connected"}

```

Subsequent frames are pushed when the worker pool scores new matches above the configured threshold:

```
data: {"type":"deal_found","listing_id":"<item_id>","score":<float>,"mission_id":<int>}

```

The connection is kept alive with periodic comment frames (`:heartbeat`). Clients should reconnect on disconnect.

Authentication: JWT validated on connect via `Authorization: Bearer <token>` header or `xolto_access` cookie. A missing or invalid token returns HTTP 401 before the SSE stream starts.

### `MustHaveMatch` Shape

| Field | Type | Values |
|---|---|---|
| `Text` | string | The must-have requirement text |
| `Status` | string | `met` \| `missed` \| `unknown` |

`unknown` is the safe default when listing text is silent on a requirement. `met` and `missed` require clear textual evidence in the listing.

---

## Observability

Sentry is initialized at startup via `internal/observability.Init()`. When `SENTRY_DSN` is empty (local dev, unset Railway env), `Init` returns immediately — no Sentry calls are made anywhere in the process.

When enabled, the `BeforeSend` scrubber runs on every event before transmission. It filters:

- **Headers:** `Authorization`, `Cookie`, `Set-Cookie` — values replaced with `[Filtered]`
- **Query parameters:** `password`, `token`, `secret`, `api_key`, `apikey`, `access_token`, `refresh_token`, `client_secret` — values replaced with `[Filtered]`
- **Request body:** entire body replaced with `[Filtered]` to prevent credential leakage from POST payloads

Release can be injected at build time:

```bash
go build -ldflags "-X github.com/TechXTT/xolto/cmd/server/main.release=$(git rev-parse --short HEAD)" ./cmd/server
```

When not injected, the server falls back to `SENTRY_RELEASE`.

---

## Testing

```bash
# Full test suite (SQLite-backed, no external deps):
go test ./...

# With race detector:
go test -race ./...

# Postgres integration tests (requires a running Postgres):
TEST_POSTGRES_DSN="postgres://user:pass@localhost:5432/dbname?sslmode=disable" \
  go test -v -race ./internal/store/...
```

Integration tests gated on `TEST_POSTGRES_DSN` skip automatically when the variable is unset. They run against a live Postgres instance to catch SQL-level bugs (e.g. positional-placeholder errors) that SQLite tests cannot detect.

`internal/api/sse_smoke_test.go` is a regression gate for the SSE Flusher passthrough (INC-20260417b). It uses `httptest.NewServer` (real listener) rather than `httptest.NewRecorder` so the full middleware stack is exercised.

CI runs on every push and pull request to `main` via `.github/workflows/ci.yml`. The CI job spins up a Postgres 16 service container and sets `TEST_POSTGRES_DSN` automatically.

---

## Development

```bash
go build ./...        # compile all packages
go vet ./...          # static analysis
go test -race ./...   # full test suite with race detector
go run ./cmd/server   # start API server
```

Build without flags for local development. Production Railway builds inject the release SHA via `-ldflags`.
