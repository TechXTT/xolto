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
