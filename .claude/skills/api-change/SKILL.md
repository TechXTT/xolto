# API Change — markt (xolto backend)

Use this skill when adding or modifying API endpoints, models, or store methods. Ensures all layers stay in sync and nothing breaks.

## Before making changes

1. Identify all layers that need updating:
   - **Models** (`internal/models/`) — struct definitions
   - **Store interface** (`internal/store/iface.go`) — Reader/Writer methods
   - **SQLite store** (`internal/store/store.go`) — implementation
   - **Postgres store** (`internal/store/postgres.go`) — implementation
   - **Handlers** (`internal/api/`) — HTTP route handlers
   - **Migrations** (`migrations/`) — schema changes
   - **Tests** — for any changed layer

2. Check existing API shape:
   - Read the current handler to understand request/response format
   - Read the store interface to understand data access patterns
   - Check if any frontend repos depend on the response shape

## Implementation order

Follow this order to avoid partial states:

1. **Migration first** (if schema change needed)
   - Add a new migration file — never edit existing migrations
   - Number it sequentially after the last one
   - Include both up and down SQL

2. **Models**
   - Update or add structs in `internal/models/`
   - Keep JSON tags consistent with existing conventions

3. **Store interface**
   - Add/update method signatures in `internal/store/iface.go`

4. **Store implementations**
   - Update BOTH `store.go` (SQLite) and `postgres.go` (Postgres)
   - Keep SQL queries consistent between both
   - Handle NULL columns defensively

5. **Handlers**
   - Add/update in the appropriate `internal/api/*.go` file
   - Register routes in the same file's setup function
   - Validate request input
   - Return consistent error responses

6. **Tests**
   - Add/update tests for new store methods and handlers

## Backward compatibility

- Do NOT remove or rename existing response fields
- Adding new fields is safe
- If a breaking change is explicitly requested, document what breaks
- Changing response shapes requires coordinated frontend updates

## Verification

After implementation:

```
go build ./cmd/server
go test ./...
go vet ./...
```

All three must pass before declaring done.

## Checklist

- [ ] Migration added (if schema change)
- [ ] Models updated
- [ ] Store interface updated
- [ ] SQLite implementation updated
- [ ] Postgres implementation updated
- [ ] Handler added/updated with route registration
- [ ] Input validation present
- [ ] Tests cover new/changed behavior
- [ ] `go build ./cmd/server` passes
- [ ] `go test ./...` passes
- [ ] No breaking changes to existing API responses (or explicitly flagged)
