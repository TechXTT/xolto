# Test and Verify — markt (xolto backend)

Run this skill after making changes to verify nothing is broken.

## Steps

1. Build the server:
   ```
   go build ./cmd/server
   ```
   If it fails, fix the compilation errors before proceeding.

2. Run all tests:
   ```
   go test ./...
   ```
   If tests fail, fix them before proceeding.

3. Run static analysis:
   ```
   go vet ./...
   ```

4. If any step fails, fix the issue and re-run from the beginning.

5. If models were changed, verify:
   - Store interface in `internal/store/iface.go` is updated
   - Both SQLite (`store.go`) and Postgres (`postgres.go`) implementations are updated
   - A new migration was added (not an edit to an existing one)
   - Tests cover the new/changed store methods

6. If handlers were changed, verify:
   - Route registration in the appropriate `internal/api/*.go` file
   - Request validation is present
   - Error responses use consistent format
   - No breaking changes to existing response shapes

7. If billing code was changed, verify:
   - Webhook handler still processes all expected event types
   - Idempotency keys are used for mutations
   - Reconciliation logic is not broken

## When done

Report which checks passed and any issues found.
