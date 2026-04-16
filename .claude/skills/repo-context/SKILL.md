# Repo Context — markt (xolto backend)

## What this repo is

Go backend and runtime for xolto — an AI copilot for buying used electronics. Serves the HTTP API, runs background search workers, handles billing, and integrates with marketplace APIs and Claude for AI features.

## Stack

- Go 1.25, module: `github.com/TechXTT/xolto`
- SQLite (default) or PostgreSQL (via DATABASE_URL)
- pgx/v5 for Postgres, modernc.org/sqlite for SQLite
- Stripe for billing
- go-rod for browser automation (marketplace scraping)
- Claude API for AI reasoning/scoring/assistant
- Discord webhooks + SMTP for notifications
- JWT auth with Google OAuth

## Entrypoints

- `cmd/server/main.go` — HTTP API server (default :8000)
- `cmd/xolto/main.go` — CLI tool (--once, --dry-run, --verbose, --generate-searches, --cleanup)
- `main.go` — delegates to CLI app

## Internal structure

```
internal/
  api/          — HTTP handlers & routes (server.go, auth.go, missions.go, listings.go, billing.go, admin.go, sse.go)
  store/        — data persistence (iface.go, store.go for SQLite, postgres.go for PG)
  models/       — core types (user.go, search.go, listing.go, business.go)
  auth/         — JWT issuance/validation, Google OAuth
  assistant/    — AI copilot (converse, briefs, comparisons)
  reasoner/     — LLM integration (Claude API calls, token tracking)
  scorer/       — deal scoring engine (fair price, offer price, confidence, reasoning)
  marketplace/  — providers (marktplaats/, vinted/, olxbg/, registry.go)
  worker/       — background search polling & dispatch
  billing/      — Stripe integration (subscriptions, webhooks, reconciliation)
  notify/       — SSE broker, Discord, email, dispatcher
  config/       — .env + config.yaml loading
  scheduler/    — search scheduling
  generator/    — AI search generation
```

## Database

- 8 migrations in `migrations/`
- Core tables: users, search_configs, listings, shopping_profiles (missions)
- Auth: user_auth_identities
- AI: ai_usage_log, ai_score_cache
- Ops: search_run_log
- Billing: stripe_webhook_events, stripe_subscription_snapshots, stripe_subscription_history, stripe_invoice_summaries, stripe_mutation_log, stripe_processed_events, billing_reconcile_runs
- Admin: admin_audit_log

## Key API routes (39 endpoints)

- Auth: register, login, Google OAuth, refresh, logout, /users/me
- Missions: CRUD on /missions
- Searches: CRUD + /searches/run, /searches/generate
- Listings: /listings/feed, /matches/analyze, /matches/feedback
- Shortlist: CRUD on /shortlist
- Assistant: /assistant/converse, /assistant/session, /actions
- SSE: /events
- Billing: /billing/checkout, /billing/portal, /billing/webhook
- Admin: /admin/stats, /admin/users, /admin/usage, /admin/search-runs, /admin/business/*

## Commands

```
go build ./cmd/server      # build API server
go test ./...              # run all tests
go run ./cmd/server        # run API server
go run ./cmd/xolto --once --dry-run --verbose  # CLI dry run
go vet ./...               # static analysis
go mod tidy                # clean dependencies
```

## Conventions

- Mission-first product model — shopping_profiles table = "missions" in UI
- Store interface in store/iface.go — both SQLite and Postgres implement it
- Handlers in internal/api/ — one file per domain
- Models in internal/models/ — shared across all layers
- Config via .env + config.yaml
- Marketplace providers registered in cmd/server/main.go
- Tiers: free, pro, power — enforced in billing + search limits

## Do not

- Break API backward compatibility unless explicitly asked
- Modify migrations that have already run in production — add new ones
- Hardcode secrets or API keys
- Skip updating tests when changing store/handler logic
- Modify Stripe webhook handling casually — billing bugs are expensive

## Definition of done

1. `go build ./cmd/server` succeeds
2. `go test ./...` passes
3. No breaking API response changes unless explicitly requested
4. Migrations, store methods, and tests updated together when models change
5. Config changes reflected in config.yaml.example
