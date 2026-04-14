package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/auth"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/store"
)

type stubRunner struct {
	userID string
	calls  int
}

func (r *stubRunner) RunAllNow(context.Context) error { return nil }

func (r *stubRunner) RunUserNow(_ context.Context, userID string) error {
	r.calls++
	r.userID = userID
	return nil
}

func issueAccessToken(t *testing.T, userID, email string) string {
	t.Helper()
	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     email,
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}
	return token
}

func decodeBodyMap(t *testing.T, res *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return body
}

func newAdminTestServer(t *testing.T) (*store.SQLiteStore, *Server, *stubRunner, string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "api-admin-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	adminID, err := st.CreateUser("admin@example.com", "hash", "Admin User")
	if err != nil {
		t.Fatalf("CreateUser(admin) error = %v", err)
	}
	userID, err := st.CreateUser("member@example.com", "hash", "Member User")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	if err := st.SetUserAdmin(adminID, true); err != nil {
		t.Fatalf("SetUserAdmin(admin) error = %v", err)
	}
	runner := &stubRunner{}
	srv := NewServer(config.ServerConfig{
		JWTSecret:          "test-secret",
		AppBaseURL:         "http://localhost:3000",
		CORSAllowedOrigins: []string{"http://localhost:3000"},
	}, st, nil, nil, runner, nil)
	return st, srv, runner, adminID, userID
}

func TestHandleRunAllSearchesRequiresAuthAndTriggersRunner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "api-server-test.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	userID, err := st.CreateUser("runner@example.com", "hash", "Runner User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	runner := &stubRunner{}
	srv := NewServer(config.ServerConfig{
		JWTSecret:  "test-secret",
		AppBaseURL: "http://localhost:3000",
	}, st, nil, nil, runner, nil)

	unauthorizedReq := httptest.NewRequest(http.MethodPost, "/searches/run", nil)
	unauthorizedRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unauthorizedRes, unauthorizedReq)
	if unauthorizedRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized status, got %d", unauthorizedRes.Code)
	}

	token, err := auth.IssueToken("test-secret", auth.Claims{
		UserID:    userID,
		Email:     "runner@example.com",
		TokenType: "access",
	}, time.Hour)
	if err != nil {
		t.Fatalf("IssueToken() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/searches/run", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected ok status, got %d", res.Code)
	}
	if runner.calls != 1 {
		t.Fatalf("expected runner to be called once, got %d", runner.calls)
	}
	if runner.userID != userID {
		t.Fatalf("expected runner user %q, got %q", userID, runner.userID)
	}

	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("expected ok response body, got %#v", body)
	}
}

func TestAdminEndpointsRejectNonAdmin(t *testing.T) {
	st, srv, _, _, memberID := newAdminTestServer(t)
	defer st.Close()

	memberToken := issueAccessToken(t, memberID, "member@example.com")
	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodGet, path: "/admin/stats"},
		{method: http.MethodGet, path: "/admin/users"},
		{method: http.MethodGet, path: "/admin/usage"},
		{method: http.MethodGet, path: "/admin/search-runs"},
		{method: http.MethodPost, path: "/admin/users/some-user/tier", body: `{"tier":"pro"}`},
		{method: http.MethodPost, path: "/admin/users/some-user/admin", body: `{"is_admin":true}`},
		{method: http.MethodPost, path: "/admin/missions/1/status", body: `{"status":"paused"}`},
		{method: http.MethodPost, path: "/admin/searches/1/enabled", body: `{"enabled":true}`},
		{method: http.MethodPost, path: "/admin/searches/1/run", body: `{}`},
	}

	for _, endpoint := range endpoints {
		req := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
		req.Header.Set("Authorization", "Bearer "+memberToken)
		if endpoint.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("%s %s expected forbidden, got %d", endpoint.method, endpoint.path, res.Code)
		}
	}
}

func TestAdminMutationsWriteAuditEntries(t *testing.T) {
	st, srv, runner, adminID, memberID := newAdminTestServer(t)
	defer st.Close()

	missionID, err := st.UpsertMission(models.Mission{
		UserID:        memberID,
		Name:          "Sony Mission",
		TargetQuery:   "sony a6000",
		Status:        "active",
		Urgency:       "flexible",
		Category:      "camera",
		CategoryID:    487,
		SearchQueries: []string{"sony a6000"},
		Active:        true,
	})
	if err != nil {
		t.Fatalf("UpsertMission() error = %v", err)
	}
	searchID, err := st.CreateSearchConfig(models.SearchSpec{
		UserID:        memberID,
		ProfileID:     missionID,
		Name:          "sony a6000",
		Query:         "sony a6000",
		MarketplaceID: "marktplaats",
		CountryCode:   "NL",
		CategoryID:    487,
		Enabled:       true,
		CheckInterval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateSearchConfig() error = %v", err)
	}

	adminToken := issueAccessToken(t, adminID, "admin@example.com")
	requests := []struct {
		path string
		body string
	}{
		{path: "/admin/users/" + memberID + "/tier", body: `{"tier":"pro"}`},
		{path: "/admin/users/" + memberID + "/admin", body: `{"is_admin":true}`},
		{path: "/admin/missions/" + strconv.FormatInt(missionID, 10) + "/status", body: `{"status":"paused"}`},
		{path: "/admin/searches/" + strconv.FormatInt(searchID, 10) + "/enabled", body: `{"enabled":true}`},
		{path: "/admin/searches/" + strconv.FormatInt(searchID, 10) + "/run", body: `{}`},
	}
	for _, request := range requests {
		req := httptest.NewRequest(http.MethodPost, request.path, strings.NewReader(request.body))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s expected status 200, got %d", request.path, res.Code)
		}
		body := decodeBodyMap(t, res)
		if ok, _ := body["ok"].(bool); !ok {
			t.Fatalf("%s expected ok response, got %#v", request.path, body)
		}
	}

	member, err := st.GetUserByID(memberID)
	if err != nil {
		t.Fatalf("GetUserByID() error = %v", err)
	}
	if member == nil {
		t.Fatalf("member user not found")
	}
	if member.Tier != "pro" {
		t.Fatalf("expected member tier pro, got %q", member.Tier)
	}
	if !member.IsAdmin {
		t.Fatalf("expected member to be admin after toggle")
	}
	mission, err := st.GetMission(missionID)
	if err != nil {
		t.Fatalf("GetMission() error = %v", err)
	}
	if mission == nil || mission.Status != "paused" {
		t.Fatalf("expected mission paused, got %#v", mission)
	}
	search, err := st.GetSearchConfigByID(searchID)
	if err != nil {
		t.Fatalf("GetSearchConfigByID() error = %v", err)
	}
	if search == nil {
		t.Fatalf("search not found after mutation")
	}
	if search.NextRunAt.IsZero() {
		t.Fatalf("expected run endpoint to set next_run_at")
	}
	if runner.calls != 1 || runner.userID != memberID {
		t.Fatalf("expected runner to trigger for member once, calls=%d user=%q", runner.calls, runner.userID)
	}

	logs, err := st.ListAdminAuditLog(20)
	if err != nil {
		t.Fatalf("ListAdminAuditLog() error = %v", err)
	}
	if len(logs) != len(requests) {
		t.Fatalf("expected %d admin audit rows, got %d", len(requests), len(logs))
	}
	seen := map[string]bool{}
	for _, entry := range logs {
		seen[entry.Action] = true
		if strings.TrimSpace(entry.ActorUserID) == "" {
			t.Fatalf("expected actor_user_id to be set: %#v", entry)
		}
		if strings.TrimSpace(entry.ActorRole) == "" {
			t.Fatalf("expected actor_role to be set: %#v", entry)
		}
	}
	for _, action := range []string{
		"user_tier_updated",
		"user_admin_updated",
		"mission_status_updated",
		"search_enabled_updated",
		"search_run_triggered",
	} {
		if !seen[action] {
			t.Fatalf("missing audit action %q", action)
		}
	}
}

func TestBusinessEndpointsRoleAccess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "api-business-role-access.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	ownerID, err := st.CreateUser("owner@example.com", "hash", "Owner User")
	if err != nil {
		t.Fatalf("CreateUser(owner) error = %v", err)
	}
	operatorID, err := st.CreateUser("operator@example.com", "hash", "Operator User")
	if err != nil {
		t.Fatalf("CreateUser(operator) error = %v", err)
	}
	memberID, err := st.CreateUser("member@example.com", "hash", "Member User")
	if err != nil {
		t.Fatalf("CreateUser(member) error = %v", err)
	}
	if err := st.UpdateUserRole(ownerID, string(models.UserRoleOwner)); err != nil {
		t.Fatalf("UpdateUserRole(owner) error = %v", err)
	}
	if err := st.SetUserAdmin(ownerID, true); err != nil {
		t.Fatalf("SetUserAdmin(owner) error = %v", err)
	}
	if err := st.UpdateUserRole(operatorID, string(models.UserRoleOperator)); err != nil {
		t.Fatalf("UpdateUserRole(operator) error = %v", err)
	}
	if err := st.SetUserAdmin(operatorID, true); err != nil {
		t.Fatalf("SetUserAdmin(operator) error = %v", err)
	}

	srv := NewServer(config.ServerConfig{
		JWTSecret:          "test-secret",
		AppBaseURL:         "http://localhost:3000",
		CORSAllowedOrigins: []string{"http://localhost:3000"},
	}, st, nil, nil, &stubRunner{}, nil)

	ownerToken := issueAccessToken(t, ownerID, "owner@example.com")
	operatorToken := issueAccessToken(t, operatorID, "operator@example.com")
	memberToken := issueAccessToken(t, memberID, "member@example.com")

	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		return res
	}

	operatorOverview := request(http.MethodGet, "/admin/business/overview?days=30", operatorToken, "")
	if operatorOverview.Code != http.StatusOK {
		t.Fatalf("operator expected 200 on business overview, got %d", operatorOverview.Code)
	}

	memberOverview := request(http.MethodGet, "/admin/business/overview?days=30", memberToken, "")
	if memberOverview.Code != http.StatusForbidden {
		t.Fatalf("member expected 403 on business overview, got %d", memberOverview.Code)
	}

	operatorReconcile := request(http.MethodPost, "/admin/business/reconcile", operatorToken, `{}`)
	if operatorReconcile.Code != http.StatusForbidden {
		t.Fatalf("operator expected 403 on owner reconcile, got %d", operatorReconcile.Code)
	}

	ownerReconcile := request(http.MethodPost, "/admin/business/reconcile", ownerToken, `{}`)
	if ownerReconcile.Code != http.StatusServiceUnavailable {
		t.Fatalf("owner expected 503 reconcile without stripe secret, got %d", ownerReconcile.Code)
	}
}

func TestAdminSearchRunsEndpointFilters(t *testing.T) {
	st, srv, _, adminID, memberID := newAdminTestServer(t)
	defer st.Close()

	missionID, err := st.UpsertMission(models.Mission{
		UserID:        memberID,
		Name:          "Sony Mission",
		TargetQuery:   "sony a6000",
		Status:        "active",
		Urgency:       "flexible",
		Category:      "camera",
		CategoryID:    487,
		SearchQueries: []string{"sony a6000"},
		Active:        true,
	})
	if err != nil {
		t.Fatalf("UpsertMission() error = %v", err)
	}
	searchID, err := st.CreateSearchConfig(models.SearchSpec{
		UserID:        memberID,
		ProfileID:     missionID,
		Name:          "sony a6000",
		Query:         "sony a6000",
		MarketplaceID: "marktplaats",
		CountryCode:   "NL",
		CategoryID:    487,
		Enabled:       true,
		CheckInterval: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateSearchConfig() error = %v", err)
	}

	now := time.Now().UTC()
	if err := st.RecordSearchRun(models.SearchRunLog{
		SearchConfigID: searchID,
		UserID:         memberID,
		MissionID:      missionID,
		Plan:           "pro",
		MarketplaceID:  "marktplaats",
		CountryCode:    "NL",
		StartedAt:      now.Add(-3 * time.Minute),
		FinishedAt:     now.Add(-2 * time.Minute),
		Status:         "success",
		ResultsFound:   3,
		NewListings:    2,
		DealHits:       1,
	}); err != nil {
		t.Fatalf("RecordSearchRun(success) error = %v", err)
	}
	if err := st.RecordSearchRun(models.SearchRunLog{
		SearchConfigID: searchID,
		UserID:         memberID,
		MissionID:      missionID,
		Plan:           "pro",
		MarketplaceID:  "marktplaats",
		CountryCode:    "NL",
		StartedAt:      now.Add(-90 * time.Second),
		FinishedAt:     now.Add(-60 * time.Second),
		Status:         "search_failed",
		ErrorCode:      "search_failed",
	}); err != nil {
		t.Fatalf("RecordSearchRun(search_failed) error = %v", err)
	}

	adminToken := issueAccessToken(t, adminID, "admin@example.com")
	req := httptest.NewRequest(http.MethodGet, "/admin/search-runs?days=30&status=success&user="+memberID+"&limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.Code)
	}
	body := decodeBodyMap(t, res)
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("expected ok response, got %#v", body)
	}
	entries, ok := body["entries"].([]any)
	if !ok {
		t.Fatalf("expected entries array, got %#v", body["entries"])
	}
	if len(entries) != 1 {
		t.Fatalf("expected one filtered entry, got %d", len(entries))
	}
	entry, ok := entries[0].(map[string]any)
	if !ok {
		t.Fatalf("expected entry object, got %#v", entries[0])
	}
	if entry["status"] != "success" {
		t.Fatalf("expected status=success, got %#v", entry["status"])
	}
	if entry["user_id"] != memberID {
		t.Fatalf("expected user_id=%q, got %#v", memberID, entry["user_id"])
	}
}

func TestAdminIPAllowlist(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "api-admin-ip-allowlist.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	defer st.Close()

	adminID, err := st.CreateUser("admin@example.com", "hash", "Admin User")
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if err := st.SetUserAdmin(adminID, true); err != nil {
		t.Fatalf("SetUserAdmin() error = %v", err)
	}

	srv := NewServer(config.ServerConfig{
		JWTSecret:          "test-secret",
		AppBaseURL:         "http://localhost:3000",
		CORSAllowedOrigins: []string{"http://localhost:3000"},
		AdminIPAllowlist:   []string{"203.0.113.0/24"},
	}, st, nil, nil, &stubRunner{}, nil)
	adminToken := issueAccessToken(t, adminID, "admin@example.com")

	blockedReq := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	blockedReq.Header.Set("Authorization", "Bearer "+adminToken)
	blockedRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(blockedRes, blockedReq)
	if blockedRes.Code != http.StatusForbidden {
		t.Fatalf("expected blocked admin IP request, got %d", blockedRes.Code)
	}

	allowedReq := httptest.NewRequest(http.MethodGet, "/admin/stats", nil)
	allowedReq.Header.Set("Authorization", "Bearer "+adminToken)
	allowedReq.Header.Set("X-Forwarded-For", "203.0.113.42")
	allowedRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(allowedRes, allowedReq)
	if allowedRes.Code != http.StatusOK {
		t.Fatalf("expected allowed admin IP request, got %d", allowedRes.Code)
	}
}
