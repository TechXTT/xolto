# markt

![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Next.js](https://img.shields.io/badge/Next.js-14-black?logo=nextdotjs)
![Status](https://img.shields.io/badge/status-active%20prototype-orange)

`markt` is a used electronics copilot.

It helps you run a mission-driven buying loop:

1. Define a buy mission
2. Monitor mission-scoped matches
3. Save and compare top options
4. Draft seller outreach with context

The repo contains:

- a Go API server (`cmd/server`)
- a Go CLI worker/runtime (`cmd/marktbot`)
- the split product app repo (`../xolto-app`)
- the split landing site repo (`../xolto-landing`)

## At A Glance

- Mission-first workflow: Missions -> Matches -> Saved -> Seller outreach
- Marketplace integrations with scoring and risk flags
- AI-assisted reasoning with rule-based fallback
- Multi-user auth/session flow in server mode
- SQLite local default, Postgres support for server mode
- Optional Discord assistant and webhook alerts

## Product Flow

The primary app flow is now:

1. Create a mission (example: "iPhone 15 Pro under EUR 850")
2. Review mission-scoped matches with verdict + confidence + risk flags
3. Save promising listings to comparison
4. Draft a seller message when you are ready to act

App routes:

- `/missions`
- `/matches`
- `/saved`
- `/settings`

Legacy listings with no mission link (`profile_id = 0`) still appear in "All missions" mode on Matches.

## Repository Layout

```text
markt/
|-- cmd/
|   |-- marktbot/   # CLI entrypoint
|   `-- server/     # HTTP API server entrypoint
|-- internal/
|   |-- api/
|   |-- assistant/
|   |-- auth/
|   |-- billing/
|   |-- cliapp/
|   |-- config/
|   |-- discordbot/
|   |-- marketplace/
|   |-- messenger/
|   |-- models/
|   |-- notify/
|   |-- reasoner/
|   |-- scheduler/
|   |-- scorer/
|   |-- store/
|   `-- worker/
|-- migrations/
|-- config.yaml.example
|-- .env.example
`-- README.md
```

## Requirements

- Go `1.25.0+`
- Node.js `18+`
- npm
- Chrome/Chromium only if you enable browser messaging automation

## Quick Start

1. Install dependencies:

```bash
go mod download
```

2. Create local config files:

```bash
cp config.yaml.example config.yaml
cp .env.example .env
```

3. Set at least `JWT_SECRET` in `.env` (32+ random chars).

4. Run API server:

```bash
go run ./cmd/server
```

Defaults:

- API server: `http://localhost:8000`

The split frontends now live in sibling repos:

- `../xolto-app`
- `../xolto-landing`

## CLI Mode

Use CLI mode when you want local polling/automation workflows without the web stack.

```bash
go run ./cmd/marktbot --config config.yaml
```

Useful flags:

- `--once`
- `--dry-run`
- `--verbose`
- `--generate-searches "<topic>"`
- `--cleanup listings|history|all`

Examples:

```bash
go run ./cmd/marktbot --config config.yaml --once --dry-run --verbose
go run ./cmd/marktbot --config config.yaml --generate-searches "sony cameras"
go run ./cmd/marktbot --config config.yaml --cleanup all
```

## Config Notes

- `config.yaml` uses whole-euro prices for `min_price`/`max_price`; backend converts to cents.
- If `discord.assistant_enabled: true`, `discord.bot_token` is required.
- If `messenger.enabled: true`, messaging credentials are required.
- If `ai.enabled: true`, `api_key` and `model` are required.

## Environment Notes

Important `.env` values:

- `JWT_SECRET` (required)
- `DATABASE_URL` (default SQLite file)
- `APP_BASE_URL` (must match app origin for auth cookies/CORS)
- `SERVER_ADDR`
- `AI_API_KEY` / `AI_BASE_URL` / `AI_MODEL`
- `STRIPE_SECRET_KEY` / `STRIPE_WEBHOOK_SECRET`
- `STRIPE_PRO_PRICE_ID` / `STRIPE_TEAM_PRICE_ID`

`DATABASE_URL` behavior:

- File path -> SQLite
- `postgres://` or `postgresql://` -> Postgres

## API Surface

Current HTTP endpoints include:

- `GET /healthz`
- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/refresh`
- `POST /auth/logout`
- `GET /users/me`

- `GET /missions`
- `POST /missions`
- `GET /missions/{id}`
- `PUT /missions/{id}`
- `PUT /missions/{id}/status`
- `GET /missions/{id}/matches`

- `GET /searches`
- `POST /searches`
- `POST /searches/run`
- `POST /searches/generate`
- `PUT /searches/{id}`
- `DELETE /searches/{id}`
- `POST /searches/{id}/run`

- `GET /listings/feed`
- `GET /shortlist`
- `POST /shortlist/{itemID}`
- `DELETE /shortlist/{itemID}`
- `POST /shortlist/{itemID}/draft`

- `POST /assistant/converse`
- `GET /assistant/session`
- `GET /actions`
- `GET /events`

- `POST /billing/checkout`
- `GET /billing/portal`
- `POST /billing/webhook`

## Discord Assistant

Discord assistant mode is optional and still supported.

Typical commands:

- `/brief`
- `/profile`
- `/matches`
- `/why`
- `/shortlist`
- `/shortlist-add`
- `/compare`
- `/draft`
- `/chat`
- `/help`

## Development Commands

Backend:

```bash
go test ./...
go build ./...
go run ./cmd/server
go run ./cmd/marktbot --once --dry-run --verbose
```

## Status

The mission-centric product shape is implemented and usable:

- mission creation and mission-scoped matching
- shortlist/save and comparison flow
- seller draft generation
- dashboard + API + billing skeleton

Still in active hardening:

- lifecycle edge cases across mission state transitions
- scoring/verdict trust calibration
- production polish and release readiness

## Roadmap

Near-term priorities:

- tighten mission lifecycle consistency
- improve verdict/risk consistency and explainability
- strengthen onboarding and first-run flow
- harden integrations and observability

Later expansion:

- additional marketplaces
- richer collaboration workflows
- deeper AI-assisted buying guidance

## Security

- Never commit real secrets.
- Rotate keys that have been committed previously.
- Treat Discord tokens, webhook URLs, AI keys, and Stripe secrets as sensitive.
