package store

// Postgres integration tests for outreach_threads.
//
// These tests execute real SQL against a live Postgres instance. They are
// gated on the TEST_POSTGRES_DSN environment variable and skip silently when
// that variable is unset, so they do not break the standard `go test ./...`
// run on machines without a Postgres instance.
//
// To run:
//
//	TEST_POSTGRES_DSN="postgres://user:pass@host:5432/dbname?sslmode=disable" \
//	  go test ./internal/store/ -run TestPostgresOutreach -v

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newOutreachThread(userID, listingID, marketplace string) OutreachThread {
	return OutreachThread{
		UserID:        userID,
		ListingID:     listingID,
		MarketplaceID: marketplace,
		DraftText:     "Здравейте, бихте ли намалили цената?",
		DraftShape:    "negotiate",
		DraftLang:     "bg",
	}
}

// ---------------------------------------------------------------------------
// AC3: full sent → replied cycle on Postgres
// ---------------------------------------------------------------------------

func TestPostgresOutreachSentRepliedCycle(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()
	userID := createPGUser(t, st)

	// Insert thread via UpsertThreadOnSent.
	thread := newOutreachThread(userID, "olxbg-listing-001", "olxbg")
	saved, err := st.UpsertThreadOnSent(ctx, thread)
	if err != nil {
		t.Fatalf("UpsertThreadOnSent() error = %v", err)
	}
	if saved.State != "awaiting_reply" {
		t.Fatalf("expected state=awaiting_reply after sent, got %q", saved.State)
	}
	if saved.RepliedAt != nil {
		t.Fatalf("expected replied_at=nil after sent")
	}

	// Transition to replied.
	replyText := "Да, мога да намалим до 200 лв."
	replied, err := st.MarkReplied(ctx, userID, "olxbg-listing-001", "olxbg", replyText)
	if err != nil {
		t.Fatalf("MarkReplied() error = %v", err)
	}
	if replied.State != "replied" {
		t.Fatalf("expected state=replied after MarkReplied, got %q", replied.State)
	}
	if replied.RepliedAt == nil {
		t.Fatal("expected replied_at to be set after MarkReplied")
	}
	if replied.ReplyText == nil || *replied.ReplyText != replyText {
		t.Fatalf("expected reply_text=%q, got %v", replyText, replied.ReplyText)
	}
}

// ---------------------------------------------------------------------------
// AC4: duplicate upsert does NOT create two rows
// ---------------------------------------------------------------------------

func TestPostgresOutreachUpsertNoDuplicate(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()
	userID := createPGUser(t, st)

	thread := newOutreachThread(userID, "olxbg-listing-dup", "olxbg")

	first, err := st.UpsertThreadOnSent(ctx, thread)
	if err != nil {
		t.Fatalf("UpsertThreadOnSent(first) error = %v", err)
	}

	// Call again with updated draft_text — should update, not insert.
	thread.DraftText = "Updated draft"
	second, err := st.UpsertThreadOnSent(ctx, thread)
	if err != nil {
		t.Fatalf("UpsertThreadOnSent(second) error = %v", err)
	}

	// Same primary key.
	if first.ID != second.ID {
		t.Fatalf("expected same ID on re-upsert; got first.ID=%d, second.ID=%d", first.ID, second.ID)
	}

	// Draft text should be updated.
	if second.DraftText != "Updated draft" {
		t.Fatalf("expected updated draft_text after re-upsert, got %q", second.DraftText)
	}

	// Verify via list — should have exactly one thread.
	threads, err := st.ListThreadsByUser(ctx, userID, nil)
	if err != nil {
		t.Fatalf("ListThreadsByUser() error = %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("expected 1 thread after two upserts, got %d", len(threads))
	}
}

// ---------------------------------------------------------------------------
// AC5: UpsertThreadOnSent on a replied thread does NOT reset state
// ---------------------------------------------------------------------------

func TestPostgresOutreachUpsertDoesNotResetReplied(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()
	userID := createPGUser(t, st)

	thread := newOutreachThread(userID, "olxbg-listing-noreset", "olxbg")
	_, err := st.UpsertThreadOnSent(ctx, thread)
	if err != nil {
		t.Fatalf("UpsertThreadOnSent() error = %v", err)
	}
	_, err = st.MarkReplied(ctx, userID, "olxbg-listing-noreset", "olxbg", "reply text")
	if err != nil {
		t.Fatalf("MarkReplied() error = %v", err)
	}

	// Now re-upsert — should NOT reset state to awaiting_reply.
	thread.DraftText = "New draft after reply"
	reupserted, err := st.UpsertThreadOnSent(ctx, thread)
	if err != nil {
		t.Fatalf("UpsertThreadOnSent(after reply) error = %v", err)
	}
	if reupserted.State != "replied" {
		t.Fatalf("expected state=replied to be preserved, got %q", reupserted.State)
	}
	// Draft text should NOT have been updated.
	if reupserted.DraftText == "New draft after reply" {
		t.Fatal("expected draft_text to be unchanged when re-upsert is on a replied thread")
	}
}

// ---------------------------------------------------------------------------
// Stale transition
// ---------------------------------------------------------------------------

func TestPostgresOutreachStaleTransition(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()
	userID := createPGUser(t, st)

	// Insert two threads.
	for _, id := range []string{"stale-001", "stale-002"} {
		_, err := st.UpsertThreadOnSent(ctx, newOutreachThread(userID, id, "olxbg"))
		if err != nil {
			t.Fatalf("UpsertThreadOnSent(%s) error = %v", id, err)
		}
	}

	// Backdating last_state_transition_at to 8 days ago so they exceed the 7-day cutoff.
	_, err := st.db.ExecContext(ctx,
		`UPDATE outreach_threads
		 SET last_state_transition_at = NOW() - interval '8 days'
		 WHERE user_id = $1`,
		userID,
	)
	if err != nil {
		t.Fatalf("backdating last_state_transition_at error = %v", err)
	}

	count, err := st.TransitionStaleThreads(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("TransitionStaleThreads() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 threads transitioned to stale, got %d", count)
	}

	// Verify both are stale.
	threads, err := st.ListThreadsByUser(ctx, userID, nil)
	if err != nil {
		t.Fatalf("ListThreadsByUser() error = %v", err)
	}
	for _, thr := range threads {
		if thr.State != "stale" {
			t.Errorf("expected state=stale for listing %q, got %q", thr.ListingID, thr.State)
		}
	}
}

// ---------------------------------------------------------------------------
// ListThreadStatesForListings batch load
// ---------------------------------------------------------------------------

func TestPostgresOutreachListThreadStatesForListings(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()
	userID := createPGUser(t, st)

	// Insert thread for listingA only.
	_, err := st.UpsertThreadOnSent(ctx, newOutreachThread(userID, "listing-A", "olxbg"))
	if err != nil {
		t.Fatalf("UpsertThreadOnSent() error = %v", err)
	}

	keys := []ListingKey{
		{ListingID: "listing-A", MarketplaceID: "olxbg"},
		{ListingID: "listing-B", MarketplaceID: "olxbg"},
	}
	states, err := st.ListThreadStatesForListings(ctx, userID, keys)
	if err != nil {
		t.Fatalf("ListThreadStatesForListings() error = %v", err)
	}

	// listing-A has a thread.
	if _, ok := states[ListingKey{ListingID: "listing-A", MarketplaceID: "olxbg"}]; !ok {
		t.Fatal("expected outreach state for listing-A")
	}
	// listing-B has no thread.
	if _, ok := states[ListingKey{ListingID: "listing-B", MarketplaceID: "olxbg"}]; ok {
		t.Fatal("expected no outreach state for listing-B")
	}
}

// ---------------------------------------------------------------------------
// MarkReplied on stale thread (stale → replied)
// ---------------------------------------------------------------------------

func TestPostgresOutreachStaleToReplied(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()
	userID := createPGUser(t, st)

	_, err := st.UpsertThreadOnSent(ctx, newOutreachThread(userID, "late-reply-001", "olxbg"))
	if err != nil {
		t.Fatalf("UpsertThreadOnSent() error = %v", err)
	}

	// Backdate and force stale.
	_, err = st.db.ExecContext(ctx,
		`UPDATE outreach_threads
		 SET last_state_transition_at = NOW() - interval '8 days'
		 WHERE user_id = $1 AND listing_id = 'late-reply-001'`,
		userID,
	)
	if err != nil {
		t.Fatalf("backdating error = %v", err)
	}
	count, err := st.TransitionStaleThreads(ctx, 7*24*time.Hour)
	if err != nil || count != 1 {
		t.Fatalf("TransitionStaleThreads() error=%v count=%d", err, count)
	}

	// Verify stale state.
	thr, err := st.GetThreadForListing(ctx, userID, "late-reply-001", "olxbg")
	if err != nil || thr == nil {
		t.Fatalf("GetThreadForListing() error=%v thr=%v", err, thr)
	}
	if thr.State != "stale" {
		t.Fatalf("expected state=stale, got %q", thr.State)
	}

	// Now apply a late reply — stale → replied is allowed.
	replied, err := st.MarkReplied(ctx, userID, "late-reply-001", "olxbg", "Late reply!")
	if err != nil {
		t.Fatalf("MarkReplied() on stale thread error = %v", err)
	}
	if replied.State != "replied" {
		t.Fatalf("expected state=replied after late MarkReplied, got %q", replied.State)
	}
}

// ---------------------------------------------------------------------------
// MarkReplied returns ErrOutreachThreadNotFound when thread does not exist
// ---------------------------------------------------------------------------

func TestPostgresOutreachMarkRepliedNotFound(t *testing.T) {
	st := openTestPostgres(t)
	defer st.Close()

	ctx := context.Background()
	userID := createPGUser(t, st)

	_, err := st.MarkReplied(ctx, userID, "nonexistent-listing", "olxbg", "reply")
	if err != ErrOutreachThreadNotFound {
		t.Fatalf("expected ErrOutreachThreadNotFound, got %v", err)
	}
}
