package scorer

import (
	"math"
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

const floatTolerance = 1e-6

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) <= floatTolerance
}

// TestComputeScoreContributions_SumMatchesFinalScore verifies the core invariant:
// the sum of all contributions must equal the final (clamped) score within
// float tolerance. We exercise several representative listing configurations.
func TestComputeScoreContributions_SumMatchesFinalScore(t *testing.T) {
	cases := []struct {
		desc      string
		listing   models.Listing
		search    models.SearchSpec
		analysis  models.DealAnalysis
		refPrice  int
		wantScore float64
	}{
		{
			desc: "fair price, high confidence, negotiable",
			listing: models.Listing{
				Price:     45000, // 450 EUR (asking)
				PriceType: "negotiable",
				Condition: "good",
			},
			search:    models.SearchSpec{},
			analysis:  models.DealAnalysis{FairPrice: 50000, Confidence: 0.80},
			refPrice:  50000,
			wantScore: func() float64 {
				// 10 - 10*(0.9) + 5 = 6.0; confidence +0.4; negotiable +1.0 → 7.4
				s := clamp(10.0-10.0*float64(45000)/float64(50000)+5.0, 1, 10) + 0.4 + 1.0
				return clamp(s, 1, 10)
			}(),
		},
		{
			desc: "overpriced, low confidence, fair condition, camera",
			listing: models.Listing{
				Price:     70000,
				PriceType: "fixed",
				Condition: "fair",
			},
			search:   models.SearchSpec{Category: "camera"},
			analysis: models.DealAnalysis{FairPrice: 50000, Confidence: 0.30},
			refPrice: 50000,
			wantScore: func() float64 {
				// 10 - 10*(1.4) + 5 = 1.0 (clamped); confidence -0.3; condition -0.3; cat_condition -0.3 → 0.1 → clamped to 1.0
				s := clamp(10.0-10.0*float64(70000)/float64(50000)+5.0, 1, 10) - 0.3 - 0.3 - 0.3
				return clamp(s, 1, 10)
			}(),
		},
		{
			desc: "like_new, high confidence, fresh",
			listing: models.Listing{
				Price:     40000,
				PriceType: "fixed",
				Condition: "like_new",
				Date:      time.Now().Add(-10 * time.Minute), // within 1 hour
			},
			search:   models.SearchSpec{},
			analysis: models.DealAnalysis{FairPrice: 50000, Confidence: 0.85},
			refPrice: 50000,
			wantScore: func() float64 {
				s := clamp(10.0-10.0*float64(40000)/float64(50000)+5.0, 1, 10) + 0.4 + 0.5 + 0.5
				return clamp(s, 1, 10)
			}(),
		},
		{
			desc: "no reference price, fallback to max budget",
			listing: models.Listing{
				Price:     30000,
				PriceType: "fixed",
				Condition: "good",
			},
			search:   models.SearchSpec{MaxPrice: 50000},
			analysis: models.DealAnalysis{Confidence: 0.50},
			refPrice: 0,
			wantScore: func() float64 {
				s := clamp(10.0-8.0*float64(30000)/float64(50000), 1, 10)
				return clamp(s, 1, 10)
			}(),
		},
		{
			desc: "no reference price, no max price — flat 5.0",
			listing: models.Listing{
				Price:     30000,
				PriceType: "fixed",
				Condition: "good",
			},
			search:    models.SearchSpec{},
			analysis:  models.DealAnalysis{Confidence: 0.50},
			refPrice:  0,
			wantScore: 5.0,
		},
		{
			desc: "for_parts condition",
			listing: models.Listing{
				Price:     10000,
				PriceType: "fixed",
				Condition: "for_parts",
			},
			search:   models.SearchSpec{},
			analysis: models.DealAnalysis{FairPrice: 40000, Confidence: 0.60},
			refPrice: 40000,
			wantScore: func() float64 {
				s := clamp(10.0-10.0*float64(10000)/float64(40000)+5.0, 1, 10) - 2.0
				return clamp(s, 1, 10)
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			contribs := computeScoreContributions(tc.listing, tc.search, tc.analysis, tc.refPrice, tc.wantScore)

			// Invariant: sum of all contributions == wantScore within tolerance.
			var sum float64
			for _, v := range contribs {
				sum += v
			}
			if !approxEqual(sum, tc.wantScore) {
				t.Errorf("sum of contributions %.6f != final score %.6f (diff %.9f)",
					sum, tc.wantScore, math.Abs(sum-tc.wantScore))
			}

			// Required components must exist.
			for _, key := range []string{"comparables", "confidence", "negotiable", "recency", "condition", "category_condition"} {
				if _, ok := contribs[key]; !ok {
					t.Errorf("missing required contribution key %q", key)
				}
			}
		})
	}
}

// TestComputeScoreContributions_ComponentValues verifies that each component's
// delta has the correct sign and magnitude for known inputs.
func TestComputeScoreContributions_ComponentValues(t *testing.T) {
	t.Run("high confidence adds 0.4", func(t *testing.T) {
		listing := models.Listing{Price: 50000, PriceType: "fixed", Condition: "good"}
		search := models.SearchSpec{}
		analysis := models.DealAnalysis{FairPrice: 50000, Confidence: 0.80}
		finalScore := clamp(10.0-10.0+5.0, 1, 10) + 0.4
		finalScore = clamp(finalScore, 1, 10)
		contribs := computeScoreContributions(listing, search, analysis, 50000, finalScore)
		if !approxEqual(contribs["confidence"], 0.4) {
			t.Errorf("expected confidence=0.4, got %.4f", contribs["confidence"])
		}
	})

	t.Run("low confidence subtracts 0.3", func(t *testing.T) {
		listing := models.Listing{Price: 50000, PriceType: "fixed", Condition: "good"}
		search := models.SearchSpec{}
		analysis := models.DealAnalysis{FairPrice: 50000, Confidence: 0.30}
		finalScore := clamp(10.0-10.0+5.0, 1, 10) - 0.3
		finalScore = clamp(finalScore, 1, 10)
		contribs := computeScoreContributions(listing, search, analysis, 50000, finalScore)
		if !approxEqual(contribs["confidence"], -0.3) {
			t.Errorf("expected confidence=-0.3, got %.4f", contribs["confidence"])
		}
	})

	t.Run("negotiable adds 1.0", func(t *testing.T) {
		listing := models.Listing{Price: 50000, PriceType: "negotiable", Condition: "good"}
		search := models.SearchSpec{}
		analysis := models.DealAnalysis{FairPrice: 50000, Confidence: 0.60}
		finalScore := clamp(10.0-10.0+5.0+1.0, 1, 10)
		finalScore = clamp(finalScore, 1, 10)
		contribs := computeScoreContributions(listing, search, analysis, 50000, finalScore)
		if !approxEqual(contribs["negotiable"], 1.0) {
			t.Errorf("expected negotiable=1.0, got %.4f", contribs["negotiable"])
		}
	})

	t.Run("fresh listing adds 0.5", func(t *testing.T) {
		listing := models.Listing{
			Price: 50000, PriceType: "fixed", Condition: "good",
			Date: time.Now().Add(-5 * time.Minute),
		}
		search := models.SearchSpec{}
		analysis := models.DealAnalysis{FairPrice: 50000, Confidence: 0.60}
		finalScore := clamp(10.0-10.0+5.0+0.5, 1, 10)
		finalScore = clamp(finalScore, 1, 10)
		contribs := computeScoreContributions(listing, search, analysis, 50000, finalScore)
		if !approxEqual(contribs["recency"], 0.5) {
			t.Errorf("expected recency=0.5, got %.4f", contribs["recency"])
		}
	})

	t.Run("for_parts subtracts 2.0 from condition", func(t *testing.T) {
		listing := models.Listing{Price: 20000, PriceType: "fixed", Condition: "for_parts"}
		search := models.SearchSpec{}
		analysis := models.DealAnalysis{FairPrice: 40000, Confidence: 0.60}
		rawBase := 10.0 - 10.0*float64(20000)/float64(40000) + 5.0
		finalScore := clamp(clamp(rawBase, 1, 10)-2.0, 1, 10)
		contribs := computeScoreContributions(listing, search, analysis, 40000, finalScore)
		if !approxEqual(contribs["condition"], -2.0) {
			t.Errorf("expected condition=-2.0, got %.4f", contribs["condition"])
		}
	})

	t.Run("camera fair condition adds category_condition=-0.3", func(t *testing.T) {
		listing := models.Listing{Price: 40000, PriceType: "fixed", Condition: "fair"}
		search := models.SearchSpec{Category: "camera"}
		analysis := models.DealAnalysis{FairPrice: 40000, Confidence: 0.60}
		rawBase := clamp(10.0-10.0+5.0, 1, 10)
		finalScore := clamp(rawBase-0.3-0.3, 1, 10)
		contribs := computeScoreContributions(listing, search, analysis, 40000, finalScore)
		if !approxEqual(contribs["category_condition"], -0.3) {
			t.Errorf("expected category_condition=-0.3 for camera+fair, got %.4f", contribs["category_condition"])
		}
	})
}

// TestComputeAttributionFromListing_SumMatchesScore verifies the read-path
// helper always satisfies: sum(contributions) == listing.Score within tolerance.
func TestComputeAttributionFromListing_SumMatchesScore(t *testing.T) {
	cases := []struct {
		desc           string
		listing        models.Listing
		searchCategory string
	}{
		{
			desc: "good condition, medium confidence, fixed price",
			listing: models.Listing{
				Score: 7.2, Confidence: 0.65, Condition: "good", PriceType: "fixed",
			},
		},
		{
			desc: "like_new, high confidence, negotiable",
			listing: models.Listing{
				Score: 9.0, Confidence: 0.80, Condition: "like_new", PriceType: "negotiable",
			},
		},
		{
			desc: "fair condition, camera category, low confidence",
			listing: models.Listing{
				Score: 3.1, Confidence: 0.25, Condition: "fair", PriceType: "fixed",
			},
			searchCategory: "camera",
		},
		{
			desc: "for_parts, medium confidence",
			listing: models.Listing{
				Score: 1.0, Confidence: 0.50, Condition: "for_parts", PriceType: "fixed",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			contribs := ComputeAttributionFromListing(tc.listing, tc.searchCategory)

			var sum float64
			for _, v := range contribs {
				sum += v
			}
			if !approxEqual(sum, tc.listing.Score) {
				t.Errorf("sum of contributions %.6f != listing.Score %.6f (diff %.9f)",
					sum, tc.listing.Score, math.Abs(sum-tc.listing.Score))
			}

			// All required keys must be present.
			for _, key := range []string{"comparables", "confidence", "negotiable", "recency", "condition", "category_condition"} {
				if _, ok := contribs[key]; !ok {
					t.Errorf("missing contribution key %q", key)
				}
			}
		})
	}
}

// TestComputeAttributionFromListing_StoredListingRecency verifies that recency
// is always 0 for stored listings (posting date equals last_seen, always > 1h).
func TestComputeAttributionFromListing_StoredListingRecency(t *testing.T) {
	listing := models.Listing{
		Score:     7.0,
		Confidence: 0.60,
		Condition: "good",
		PriceType: "fixed",
		// Date set to >1 hour ago, simulating last_seen from the store.
		Date: time.Now().Add(-2 * time.Hour),
	}
	contribs := ComputeAttributionFromListing(listing, "")
	// ComputeAttributionFromListing ignores Date — recency is always 0 for stored listings.
	if !approxEqual(contribs["recency"], 0.0) {
		t.Errorf("expected recency=0 for stored listing (no posting date), got %.4f", contribs["recency"])
	}
}
