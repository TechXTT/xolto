package scorer

import (
	"testing"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

// TestComputeComparableStatsOddCount verifies the odd-length median (middle value).
func TestComputeComparableStatsOddCount(t *testing.T) {
	now := time.Now()
	// Ages will be: 10, 3, 7 days → sorted: 3, 7, 10 → median = 7
	comparables := []models.ComparableDeal{
		{ItemID: "a", LastSeen: now.Add(-10 * 24 * time.Hour)},
		{ItemID: "b", LastSeen: now.Add(-3 * 24 * time.Hour)},
		{ItemID: "c", LastSeen: now.Add(-7 * 24 * time.Hour)},
	}
	count, median := computeComparableStats(comparables)
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	if median != 7 {
		t.Fatalf("median = %d, want 7", median)
	}
}

// TestComputeComparableStatsEvenCount verifies the even-length median
// (average of two middle values, rounded to nearest int).
func TestComputeComparableStatsEvenCount(t *testing.T) {
	now := time.Now()
	// Ages: 4, 12, 6, 20 days → sorted: 4, 6, 12, 20 → median = (6+12)/2 = 9
	comparables := []models.ComparableDeal{
		{ItemID: "a", LastSeen: now.Add(-4 * 24 * time.Hour)},
		{ItemID: "b", LastSeen: now.Add(-12 * 24 * time.Hour)},
		{ItemID: "c", LastSeen: now.Add(-6 * 24 * time.Hour)},
		{ItemID: "d", LastSeen: now.Add(-20 * 24 * time.Hour)},
	}
	count, median := computeComparableStats(comparables)
	if count != 4 {
		t.Fatalf("count = %d, want 4", count)
	}
	if median != 9 {
		t.Fatalf("median = %d, want 9 (average of 6 and 12)", median)
	}
}

// TestComputeComparableStatsEmpty verifies that both fields are 0 when there
// are no comparables.
func TestComputeComparableStatsEmpty(t *testing.T) {
	count, median := computeComparableStats(nil)
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
	if median != 0 {
		t.Fatalf("median = %d, want 0", median)
	}
}

// TestComputeComparableStatsSingleItem verifies count=1, median=age of that item.
func TestComputeComparableStatsSingleItem(t *testing.T) {
	now := time.Now()
	comparables := []models.ComparableDeal{
		{ItemID: "only", LastSeen: now.Add(-5 * 24 * time.Hour)},
	}
	count, median := computeComparableStats(comparables)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if median != 5 {
		t.Fatalf("median = %d, want 5", median)
	}
}

// TestComputeComparableStatsZeroLastSeenExcluded verifies that comparables with
// zero LastSeen are excluded from both count and median.
func TestComputeComparableStatsZeroLastSeenExcluded(t *testing.T) {
	now := time.Now()
	comparables := []models.ComparableDeal{
		{ItemID: "valid", LastSeen: now.Add(-8 * 24 * time.Hour)},
		{ItemID: "zero", LastSeen: time.Time{}}, // zero — must be excluded
	}
	count, median := computeComparableStats(comparables)
	if count != 1 {
		t.Fatalf("count = %d, want 1 (zero LastSeen must be excluded)", count)
	}
	if median != 8 {
		t.Fatalf("median = %d, want 8", median)
	}
}

// TestComputeComparableStatsAllZeroLastSeen verifies that when all comparables
// have zero LastSeen, both fields are 0.
func TestComputeComparableStatsAllZeroLastSeen(t *testing.T) {
	comparables := []models.ComparableDeal{
		{ItemID: "z1", LastSeen: time.Time{}},
		{ItemID: "z2", LastSeen: time.Time{}},
	}
	count, median := computeComparableStats(comparables)
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
	if median != 0 {
		t.Fatalf("median = %d, want 0", median)
	}
}

// TestComputeComparableStatsFutureLastSeen verifies that a future LastSeen
// (clock skew) is treated as 0 days and is still counted.
func TestComputeComparableStatsFutureLastSeen(t *testing.T) {
	now := time.Now()
	comparables := []models.ComparableDeal{
		{ItemID: "future", LastSeen: now.Add(48 * time.Hour)},
	}
	count, median := computeComparableStats(comparables)
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	if median != 0 {
		t.Fatalf("median = %d, want 0 (future treated as 0 days)", median)
	}
}

// TestComputeComparableStatsFiveComparables exercises the 5-item case from the
// brief: known LastSeen times → known median.
// Ages (days): 2, 5, 10, 15, 20 → sorted: 2, 5, 10, 15, 20 → median = 10
func TestComputeComparableStatsFiveComparables(t *testing.T) {
	now := time.Now()
	comparables := []models.ComparableDeal{
		{ItemID: "a", LastSeen: now.Add(-2 * 24 * time.Hour)},
		{ItemID: "b", LastSeen: now.Add(-5 * 24 * time.Hour)},
		{ItemID: "c", LastSeen: now.Add(-10 * 24 * time.Hour)},
		{ItemID: "d", LastSeen: now.Add(-15 * 24 * time.Hour)},
		{ItemID: "e", LastSeen: now.Add(-20 * 24 * time.Hour)},
	}
	count, median := computeComparableStats(comparables)
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}
	if median != 10 {
		t.Fatalf("median = %d, want 10", median)
	}
}
