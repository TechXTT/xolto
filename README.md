# MarktBot

![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Next.js](https://img.shields.io/badge/Next.js-14-black?logo=nextdotjs)
![Discord](https://img.shields.io/badge/Discord-Assistant-5865F2?logo=discord&logoColor=white)
![Status](https://img.shields.io/badge/status-active%20prototype-orange)

MarktBot is an AI-assisted shopping agent for second-hand marketplaces, built around Marktplaats first.

It started as a deal-finder bot and has grown into a larger product surface with:

- a Go CLI for polling searches, scoring deals, Discord alerts, and optional browser automation
- a Discord shopping assistant that can ask follow-up questions, maintain briefs, and shortlist listings
- a Go API server and Next.js dashboard for the first SaaS-style version of the product

The product is advisory-first. It can search, compare, explain, shortlist, and draft seller follow-ups. It is not designed to autonomously contact sellers unless you explicitly enable that behavior.

## At a Glance

- Marketplace-first shopping assistant for used goods
- Marktplaats support today, marketplace abstraction for future expansion
- AI-enhanced deal reasoning with rule-based fallback
- Discord-first assistant workflow plus web dashboard
- SQLite for local mode, SQLite or Postgres for server mode
- Explicit safety gates around messaging and automation

## Choose Your Mode

### 1. Personal CLI + Discord bot

Best if you want a local assistant that monitors searches, pushes Discord alerts, and helps shortlist deals.

### 2. Server + web dashboard

Best if you want the multi-user API, dashboard, SSE feed, auth flow, and the emerging SaaS architecture.

## What It Does

- Searches Marktplaats for configured products
- Scores listings using price history, market averages, comparable deals, and optional AI reasoning
- Sends Discord webhook alerts for interesting deals
- Runs a Discord assistant with slash commands and conversational follow-up questions
- Stores shopping briefs, shortlist items, drafts, and seen listings in SQLite or Postgres
- Exposes a multi-user HTTP API for auth, searches, feed, shortlist, assistant chat, SSE, and billing hooks
- Ships with a Next.js dashboard in `web/` and a separate landing site in `landing/`

## Demo Flow

The intended user flow looks like this:

1. Describe what you want in Discord or the web app
2. Let MarktBot turn that into a shopping brief and active search profile
3. Review matches with fair-value reasoning and fit analysis
4. Save promising listings to a shortlist
5. Compare shortlisted items
6. Draft seller questions or an offer when you are ready

## Current Architecture

```text
marktbot/
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
|-- web/            # Next.js product dashboard
|-- landing/        # Next.js marketing/landing site
|-- config.yaml.example
|-- .env.example
`-- README.md
```

## Requirements

- Go 1.25.0+ for the backend and CLI
- Node.js 18+ and npm for `web/` and `landing/`
- Chrome or Chromium if you want Rod-based browser automation
- A Discord webhook for passive alerts
- A Discord bot token for the interactive assistant
- An OpenAI-compatible API key if you want AI reasoning or AI-backed search generation

## Quick Start

### Full local stack

1. Install dependencies:

```bash
go mod download
npm --prefix web install
npm --prefix landing install
```

2. Create local config files from the checked-in examples:

```bash
cp config.yaml.example config.yaml
cp .env.example .env
cp web/.env.example web/.env.local
```

3. Edit `.env` and set at least `JWT_SECRET`.

Local defaults already work for the rest of the core server setup:

- `DATABASE_URL=marktbot-server.db`
- `APP_BASE_URL=http://localhost:3000`
- `SERVER_ADDR=:8000`

4. Start the API server:

```bash
go run ./cmd/server
```

The server loads `.env` automatically and creates the SQLite schema on first start.

5. Start the app dashboard in another terminal:

```bash
npm --prefix web run dev
```

The dashboard runs on `http://localhost:3000`.

6. Start the landing page in another terminal if you want the separate marketing site locally:

```bash
npm --prefix landing run dev
```

The landing page runs on `http://localhost:3001` and links to `http://localhost:3000` by default. If your app dashboard runs elsewhere, set `NEXT_PUBLIC_APP_URL` in `landing/.env.local`.

### CLI-only mode

1. Copy the example config:

```bash
cp config.yaml.example config.yaml
```

2. Edit `config.yaml` with at least one search, or enable Discord assistant-only mode.

3. Run one dry cycle:

```bash
go run ./cmd/marktbot --config config.yaml --once --dry-run --verbose
```

## Screenshots

The repo does not include polished screenshots yet, but the current UI surface includes:

- a live feed page
- saved searches
- shortlist management
- assistant chat
- billing/settings

If you want, the next README pass can add real screenshots or GIFs from the dashboard and Discord assistant.

## CLI Usage

The CLI entrypoint is:

```bash
go run ./cmd/marktbot
```

Flags:

- `--config`: path to the YAML config file, default `config.yaml`
- `--once`: run a single cycle and exit
- `--dry-run`: skip seller messaging
- `--verbose`: enable debug logging
- `--generate-searches "<topic>"`: generate ready-to-paste YAML search entries
- `--cleanup listings|history|all`: clear local SQLite bot state and exit

Examples:

```bash
go run ./cmd/marktbot --config config.yaml
go run ./cmd/marktbot --config config.yaml --once
go run ./cmd/marktbot --config config.yaml --once --dry-run --verbose
go run ./cmd/marktbot --generate-searches "sony cameras"
go run ./cmd/marktbot --cleanup all
```

## Example `config.yaml`

```yaml
searches:
  - name: "Sony A6400 body"
    query: "sony a6400 body"
    marketplace_id: "marktplaats"
    category_id: 487
    max_price: 850
    min_price: 350
    condition: ["Gebruikt", "Zo goed als nieuw"]
    offer_percentage: 75
    auto_message: false
    message_template: "Hoi! Ik ben geinteresseerd in {{.Title}}. Zou je EUR {{.OfferPrice}} accepteren?"

  - name: "Sony FE 50mm f/1.8"
    query: "sony fe 50mm sel50f18f"
    marketplace_id: "marktplaats"
    category_id: 495
    max_price: 220
    min_price: 75
    condition: ["Gebruikt", "Zo goed als nieuw"]
    offer_percentage: 72
    auto_message: false
    message_template: "Hoi! Is {{.Title}} nog beschikbaar? Ik kan EUR {{.OfferPrice}} bieden."

marktplaats:
  zip_code: "1011AB"
  distance: 75000
  check_interval: 5m
  request_delay: 3s

discord:
  webhook_url: ""
  assistant_enabled: true
  bot_token: "YOUR_DISCORD_BOT_TOKEN"
  command_prefix: "!"
  message_content_enabled: false
  guild_ids: ["YOUR_GUILD_ID"]
  allowed_channel_ids: ["YOUR_CHANNEL_ID"]
  allowed_user_ids: ["YOUR_DISCORD_USER_ID"]

messenger:
  enabled: false
  headless: true
  username: ""
  password: ""
  max_messages_per_hour: 5

scoring:
  min_score: 7.2
  market_sample_size: 20

ai:
  enabled: false
  base_url: "https://api.openai.com/v1"
  api_key: ""
  model: ""
  temperature: 0.2
  max_comparables: 8
  min_confidence: 0.55
  search_advice: true
```

Notes:

- For cameras, `487` is the body category and `495` is the lens category.
- `max_price` and `min_price` are whole euros in YAML and are converted internally to cents.
- If `discord.assistant_enabled` is `true`, `discord.bot_token` is required.
- If `messenger.enabled` is `true`, `username` and `password` are required.
- If `ai.enabled` is `true`, `api_key` and `model` are required.
- You can run assistant-only mode with no static `searches` as long as Discord assistant mode is enabled.

## Discord Assistant

MarktBot can run as a Discord-first shopping assistant.

Enable it in `config.yaml`:

```yaml
discord:
  assistant_enabled: true
  bot_token: "YOUR_DISCORD_BOT_TOKEN"
  guild_ids: ["YOUR_GUILD_ID"]
  allowed_channel_ids: ["YOUR_CHANNEL_ID"]
  allowed_user_ids: ["YOUR_DISCORD_USER_ID"]
```

Recommended bot permissions:

- View Channels
- Send Messages
- Read Message History
- Use Application Commands

Slash commands:

- `/brief` create or update a shopping brief
- `/profile` show the active brief
- `/matches` show the best current matches
- `/why` explain a listing
- `/shortlist` show shortlist items
- `/shortlist-add` save a listing
- `/compare` compare shortlisted items
- `/draft` draft a seller follow-up
- `/chat` talk to the assistant directly
- `/help` show available commands

If you want the bot to respond to normal free text in a channel, set:

```yaml
discord:
  message_content_enabled: true
```

and enable `Message Content Intent` in the Discord Developer Portal.

Typical flow:

```text
/brief request:I want a Sony A6400 body under 800 euro
/matches
/why listing_id:...
/shortlist-add listing_id:...
/compare
/draft listing_id:...
```

## Search Generation

You can generate search presets from the CLI:

```bash
go run ./cmd/marktbot --config config.yaml --generate-searches "sony cameras"
```

Behavior:

- if AI is configured, the generator uses the AI provider first
- if AI is unavailable or fails, it falls back to built-in templates
- camera-related results are normalized to the correct Marktplaats category IDs

## AI Features

When AI is enabled, MarktBot can:

- improve fair-value estimation using listing context and comparables
- explain why a listing is or is not attractive
- give search refinement advice
- generate search presets
- support richer shopping-assistant replies

The AI integration uses an OpenAI-compatible `/chat/completions` API.

## Server Mode

The server entrypoint is:

```bash
go run ./cmd/server
```

Setup notes:

- `cmd/server` loads `.env` automatically if it exists in the repo root
- `JWT_SECRET` is required; the rest of the core local settings are prefilled in `.env.example`
- `APP_BASE_URL` must match the dashboard origin for cookie auth and CORS; local default is `http://localhost:3000`
- SQLite and Postgres schemas are created automatically on startup

Required env vars:

- `JWT_SECRET`: required

Common env vars:

- `SERVER_ADDR`: default `:8000`
- `DATABASE_URL`: SQLite file path or Postgres DSN, default `marktbot-server.db`
- `APP_BASE_URL`: dashboard origin, default `http://localhost:3000`
- `AI_API_KEY`
- `AI_BASE_URL`
- `AI_MODEL`
- `STRIPE_SECRET_KEY`
- `STRIPE_WEBHOOK_SECRET`
- `STRIPE_PRO_PRICE_ID`
- `STRIPE_TEAM_PRICE_ID`

Database behavior:

- SQLite is used if `DATABASE_URL` is a file path
- Postgres is used if `DATABASE_URL` starts with `postgres://` or `postgresql://`

## Current API Surface

The HTTP server currently exposes:

- `GET /healthz`
- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/refresh`
- `POST /auth/logout`
- `GET /users/me`
- `GET /searches`
- `POST /searches`
- `POST /searches/run`
- `POST /searches/generate`
- `PUT /searches/:id`
- `DELETE /searches/:id`
- `POST /searches/:id/run`
- `GET /listings/feed`
- `GET /shortlist`
- `POST /shortlist/:id`
- `DELETE /shortlist/:id`
- `POST /assistant/converse`
- `GET /assistant/session`
- `GET /actions`
- `GET /events`
- `POST /billing/checkout`
- `GET /billing/portal`
- `POST /billing/webhook`

Auth notes:

- the web app now uses HttpOnly cookies for session auth
- the server issues separate access and refresh tokens
- `/auth/refresh` is cookie-based and intended for session renewal

## Roadmap

Near-term priorities:

- tighten the first-release SaaS experience
- improve dashboard polish and onboarding
- harden marketplace integrations further
- expand billing and account-management flows
- add more production-grade observability and admin tooling

Likely next product expansions:

- more marketplaces through the registry layer
- stronger listing comparison and buyer-risk analysis
- better shortlist collaboration and team workflows
- richer automation with tighter approval gates

## Frontends

The authenticated app dashboard lives in `web/` and currently covers:

- registration and login
- feed view
- saved searches
- shortlist view
- assistant chat
- billing/settings surface

Run the dashboard locally:

```bash
npm --prefix web run dev
```

Build the dashboard:

```bash
npm --prefix web run build
```

The marketing site lives in `landing/` and runs separately on port `3001`:

```bash
npm --prefix landing run dev
```

If the dashboard is not running on `http://localhost:3000`, set `NEXT_PUBLIC_APP_URL` in `landing/.env.local` so the landing page links to the correct app origin.

## Notifications and Messaging

### Discord webhooks

If `discord.webhook_url` is set, qualifying deals can be sent to Discord as passive notifications.

### Seller messaging

The browser messenger is intentionally behind multiple safety gates:

- `messenger.enabled` must be `true`
- credentials must be configured
- `--dry-run` disables actual sending
- search entries must explicitly opt into `auto_message`

Recommended approach:

1. keep auto-messaging off while tuning searches
2. run with `--once --dry-run`
3. verify scoring and templates
4. only then enable messaging for a narrow set of searches

## Development

Useful commands:

```bash
go test ./...
go build ./...
go run ./cmd/marktbot --once --dry-run --verbose
go run ./cmd/server
```

Frontend dev/build:

```bash
npm --prefix web run dev
npm --prefix web run build
npm --prefix landing run dev
npm --prefix landing run build
```

For local state reset:

```bash
go run ./cmd/marktbot --cleanup all
```

## Security Notes

- Do not commit live secrets in `config.yaml`
- Rotate any keys that have already been pasted into the repo
- Prefer local-only config files or secret injection through your environment
- Treat Discord bot tokens, webhook URLs, AI API keys, and Stripe secrets as compromised if they have ever been committed

## Status

MarktBot is no longer just a polling bot, but it is also not yet a fully finished SaaS product. The current state is:

- the CLI and Discord assistant are usable
- the server and dashboard are real and functional for local development
- the SaaS layer is still evolving toward a fuller production release

## Release Readiness

Good fit today for:

- personal use
- local development
- internal prototyping
- testing shopping-assistant flows

Not fully polished yet for:

- broad public launch
- high-scale production use
- hands-off autonomous negotiation

That said, the repo is already substantial enough to use as:

- a serious personal deal-hunting assistant
- a foundation for a marketplace-shopping SaaS
- a strong prototype for AI-assisted commerce workflows
