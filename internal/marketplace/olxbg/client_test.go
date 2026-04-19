package olxbg

import "testing"

// TestMatchesCondition verifies the condition-filter logic for OLX.bg listings
// (XOL-102). Key invariants:
//   - "used" (BG: Употребявана/Използвана) is accepted when the user allows
//     "good" or "fair", since it is OLX.bg's generic pre-owned condition.
//   - "" and "unknown" always pass through; the scorer applies its own penalty.
//   - Explicit mismatches (e.g. "for_parts" against good/fair) are still dropped.
func TestMatchesCondition(t *testing.T) {
	cases := []struct {
		name      string
		condition string
		allowed   []string
		want      bool
	}{
		{
			name:      "used passes when good/fair allowed",
			condition: "used",
			allowed:   []string{"good", "fair"},
			want:      true,
		},
		{
			name:      "used dropped when only new/like_new allowed",
			condition: "used",
			allowed:   []string{"new", "like_new"},
			want:      false,
		},
		{
			name:      "used passes when all four conditions allowed",
			condition: "used",
			allowed:   []string{"new", "like_new", "good", "fair"},
			want:      true,
		},
		{
			name:      "empty condition passes through",
			condition: "",
			allowed:   []string{"good"},
			want:      true,
		},
		{
			name:      "unknown condition passes through",
			condition: "unknown",
			allowed:   []string{"like_new"},
			want:      true,
		},
		{
			name:      "for_parts dropped when only good/fair allowed",
			condition: "for_parts",
			allowed:   []string{"good", "fair"},
			want:      false,
		},
		{
			name:      "exact match good passes",
			condition: "good",
			allowed:   []string{"good"},
			want:      true,
		},
		{
			name:      "new dropped when only like_new/good allowed",
			condition: "new",
			allowed:   []string{"like_new", "good"},
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchesCondition(tc.condition, tc.allowed)
			if got != tc.want {
				t.Errorf("matchesCondition(%q, %v) = %v, want %v",
					tc.condition, tc.allowed, got, tc.want)
			}
		})
	}
}
