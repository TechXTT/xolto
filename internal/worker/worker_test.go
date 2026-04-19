package worker

import (
	"testing"

	"github.com/TechXTT/xolto/internal/models"
)

func TestListingMatchesSearchRejectsCameraAccessoryOnlyTitles(t *testing.T) {
	spec := models.SearchSpec{Query: "sony a6000 under 500 euro"}
	listing := models.Listing{
		Title: "Чанта Sony LCS-EJA / калъф за Sony A6000",
	}
	if listingMatchesSearch(listing, spec) {
		t.Fatalf("expected accessory-only listing to be rejected")
	}
}

func TestListingMatchesSearchKeepsCameraBodyAndKitTitles(t *testing.T) {
	spec := models.SearchSpec{Query: "sony a6000 under 500 euro"}
	listing := models.Listing{
		Title: "Sony A6000 + 2 обектива",
	}
	if !listingMatchesSearch(listing, spec) {
		t.Fatalf("expected camera body/kit listing to be kept")
	}
}

func TestListingMatchesSearchKeepsAccessoryQueries(t *testing.T) {
	spec := models.SearchSpec{Query: "sony a6000 bag"}
	listing := models.Listing{
		Title: "Sony A6000 camera bag and strap",
	}
	if !listingMatchesSearch(listing, spec) {
		t.Fatalf("expected accessory listing to pass when accessory is in query")
	}
}

func TestShouldRejectAccessoryListingPhone(t *testing.T) {
	cases := []struct {
		title       string
		query       string
		wantReject  bool
	}{
		{
			title:      "[Калъф за телефон] Otterbox Commuter, Google Pixel 9a",
			query:      "google pixel 9a",
			wantReject: true,
		},
		{
			title:      "Pixel 9a case Black",
			query:      "google pixel 9a",
			wantReject: true,
		},
		{
			title:      "Samsung Galaxy S24 charger USB-C",
			query:      "samsung galaxy s24",
			wantReject: true,
		},
		{
			title:      "iPhone 15 Pro калъф кожен",
			query:      "iphone 15 pro",
			wantReject: true,
		},
		{
			title:      "Google Pixel 9a 128GB Unlocked Black",
			query:      "google pixel 9a",
			wantReject: false,
		},
		{
			title:      "Samsung Galaxy S24 256GB",
			query:      "samsung galaxy s24",
			wantReject: false,
		},
		{
			title:      "Google Pixel 9a",
			query:      "google pixel 9a",
			wantReject: false,
		},
		{
			title:      "Sony A6000 case",
			query:      "google pixel 9a",
			wantReject: false,
		},
		// Regression: camera filter still works
		{
			title:      "Sony A6000 camera bag",
			query:      "sony a6000 camera",
			wantReject: true,
		},
	}

	for _, tc := range cases {
		got := shouldRejectAccessoryListing(tc.title, tc.query)
		if got != tc.wantReject {
			t.Errorf("shouldRejectAccessoryListing(%q, %q) = %v, want %v",
				tc.title, tc.query, got, tc.wantReject)
		}
	}
}
