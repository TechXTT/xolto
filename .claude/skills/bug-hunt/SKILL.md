# Bug Hunt — markt (xolto backend)

Use this skill to investigate and fix bugs. Follows a reproduce-diagnose-patch-verify cycle.

## Steps

### 1. Reproduce
- Understand the reported behavior and expected behavior
- Identify which layer is involved: handler, store, model, worker, billing, marketplace
- Check logs or error output for stack traces

### 2. Diagnose
- Read the relevant handler in `internal/api/`
- Trace to store methods in `internal/store/` — check both SQLite and Postgres implementations
- Check model definitions in `internal/models/`
- If it's a marketplace issue, check the provider in `internal/marketplace/`
- If it's a billing issue, check `internal/billing/` and webhook handling
- If it's a worker issue, check `internal/worker/` scheduling and dispatch
- If it's an AI issue, check `internal/reasoner/` or `internal/scorer/`

### 3. Summarize
Before patching, state:
- **Root cause**: what exactly is wrong and why
- **Affected files**: which files need changes
- **Risk**: what else could break from the fix
- **DB impact**: does this need a migration?

### 4. Patch
- Make the minimal fix that addresses the root cause
- Update BOTH SQLite and Postgres store implementations if store code changed
- Add a migration if the schema needs to change (never edit existing migrations)
- Update tests for changed behavior
- Don't refactor unrelated code

### 5. Verify
```
go build ./cmd/server
go test ./...
go vet ./...
```
All three must pass before declaring done.
