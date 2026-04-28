package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/TechXTT/xolto/internal/aibudget"
	"github.com/TechXTT/xolto/internal/store"
)

// TestHealthzReportsAIBudgetWiringStatus is the W19-24 regression check.
// /healthz must surface tracker presence + audit-table readiness so a future
// silent-migration miss (the 2026-04-27 incident class) is observable from a
// one-line curl. Returns 200 either way; the JSON tells the story.
func TestHealthzReportsAIBudgetWiringStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "healthz-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	// Install a fresh global aibudget tracker so the test exercises the
	// tracker_present = true branch deterministically.
	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })
	aibudget.SetGlobal(aibudget.New())

	srv := &Server{db: st}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(context.Background())
	rr := httptest.NewRecorder()
	srv.handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body=%s)", err, rr.Body.String())
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if body["service"] != "xolto-server" {
		t.Errorf("service = %v, want xolto-server", body["service"])
	}
	wiring, ok := body["ai_budget"].(map[string]any)
	if !ok {
		t.Fatalf("ai_budget block missing or wrong shape: %v", body["ai_budget"])
	}
	if wiring["tracker_present"] != true {
		t.Errorf("ai_budget.tracker_present = %v, want true (Global() was set)", wiring["tracker_present"])
	}
	if wiring["audit_table_ready"] != true {
		t.Errorf("ai_budget.audit_table_ready = %v, want true (SQLite store creates the table)", wiring["audit_table_ready"])
	}
	// Snapshot fields render only when tracker is present.
	if _, has := wiring["cap_usd"]; !has {
		t.Errorf("ai_budget.cap_usd missing")
	}
}

// TestHealthzWithoutTracker verifies the tracker_present=false branch when
// aibudget.Global() returns nil — exercises the fail-soft path the handler
// follows so /healthz never 5xxs over wiring drift.
func TestHealthzWithoutTracker(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "healthz-notracker.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	origTracker := aibudget.Global()
	t.Cleanup(func() { aibudget.SetGlobal(origTracker) })
	aibudget.SetGlobal(nil)

	srv := &Server{db: st}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil).WithContext(context.Background())
	rr := httptest.NewRecorder()
	srv.handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with tracker absent", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	wiring := body["ai_budget"].(map[string]any)
	if wiring["tracker_present"] != false {
		t.Errorf("ai_budget.tracker_present = %v, want false", wiring["tracker_present"])
	}
}
