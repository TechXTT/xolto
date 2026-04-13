# xolto

![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)
![Status](https://img.shields.io/badge/status-active%20prototype-orange)

`xolto` is a used electronics copilot.

Buy used electronics without overpaying.

This repo now contains the backend/runtime layer:

- Go API server: `cmd/server`
- Go CLI runtime: `cmd/xolto`

The split frontend repos live alongside this repo:

- `../xolto-app`
- `../xolto-landing`

## Product Flow

The product remains mission-first:

1. Create a mission
2. Review mission-scoped matches
3. Save and compare strong options
4. Draft seller outreach

User-facing routes remain:

- `/missions`
- `/matches`
- `/saved`
- `/settings`

## Requirements

- Go `1.25.0+`
- npm / Node.js `18+` only if you also work on the split frontend repos
- Chrome/Chromium only if you enable browser messaging automation

## Quick Start

```bash
go mod download
cp config.yaml.example config.yaml
cp .env.example .env
go run ./cmd/server
```

Defaults:

- API server: `http://localhost:8000`
- `DATABASE_URL`: `xolto-server.db` when unset

## CLI Mode

Use the CLI when you want local polling and automation without the web stack.

```bash
go run ./cmd/xolto --config config.yaml
```

Useful flags:

- `--once`
- `--dry-run`
- `--verbose`
- `--generate-searches "<topic>"`
- `--cleanup listings|history|all`

## Configuration

Important `.env` values:

- `JWT_SECRET`
- `DATABASE_URL`
- `APP_BASE_URL`
- `SERVER_ADDR`
- `AI_API_KEY` / `AI_BASE_URL` / `AI_MODEL`
- `STRIPE_SECRET_KEY` / `STRIPE_WEBHOOK_SECRET`
- `STRIPE_PRO_PRICE_ID` / `STRIPE_POWER_PRICE_ID`
- `ADMIN_EMAILS`

`config.yaml` uses whole-euro prices for `min_price` / `max_price`; the backend converts them to cents.

## Development Commands

Backend:

```bash
GOTOOLCHAIN=auto go test ./...
go build ./...
go run ./cmd/server
go run ./cmd/xolto --once --dry-run --verbose
```

Split frontends:

```bash
cd ../xolto-app && npm run build
cd ../xolto-landing && npm run build
```

## API Surface

Core endpoints include:

- `GET /healthz`
- `POST /auth/register`
- `POST /auth/login`
- `POST /auth/refresh`
- `POST /auth/logout`
- `GET /users/me`
- `GET /missions`
- `POST /missions`
- `GET /listings/feed`
- `GET /shortlist`
- `POST /assistant/converse`
- `POST /billing/checkout`
- `GET /billing/portal`
- `POST /billing/webhook`

## Notes

- DB schemas, table names, and applied migration filenames are intentionally unchanged.
- Session cookie names now use the `xolto_*` prefix.
- Frontend token/storage key changes now live in `../xolto-app`.
