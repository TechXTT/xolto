package modelkey

import "testing"

// TestNormalize verifies the Normalize function against the full near-sibling
// matrix from the XOL-105 spec, plus edge cases.
func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		// --- Sony ---
		{name: "sony a6000", query: "sony a6000", want: "sony:a6000"},
		{name: "a6000 sony mirrorless word-order", query: "a6000 sony mirrorless", want: "sony:a6000"},
		{name: "sony a6700", query: "sony a6700", want: "sony:a6700"},
		{name: "sony a6000 ne a6700", query: "sony a6000", want: "sony:a6000"}, // different from sony:a6700

		// --- Samsung ---
		{name: "samsung galaxy s24", query: "samsung galaxy s24", want: "samsung:s24"},
		{name: "samsung galaxy s24 fe", query: "samsung galaxy s24 fe", want: "samsung:s24fe"},
		{name: "samsung s24", query: "samsung s24", want: "samsung:s24"},
		{name: "samsung s24 fe", query: "samsung s24 fe", want: "samsung:s24fe"},
		{name: "samsung s24 ne s24 fe", query: "samsung s24", want: "samsung:s24"}, // different from samsung:s24fe

		// --- Google Pixel ---
		{name: "google pixel 9a", query: "google pixel 9a", want: "google:pixel9a"},
		{name: "pixel 9a brand alias", query: "pixel 9a", want: "google:pixel9a"},
		{name: "pixel 9", query: "pixel 9", want: "google:pixel9"},
		{name: "pixel 9a ne pixel 9", query: "pixel 9a", want: "google:pixel9a"}, // different from google:pixel9

		// --- Apple iPhone ---
		{name: "iphone 15 pro", query: "iphone 15 pro", want: "apple:iphone15pro"},
		{name: "iphone 15", query: "iphone 15", want: "apple:iphone15"},
		{name: "iphone 15 ne iphone 15 pro", query: "iphone 15", want: "apple:iphone15"}, // different from apple:iphone15pro

		// --- Canon ---
		// "eos" is not a familyPrefixToken and does not have a digit, so it becomes
		// a single-letter-prefix candidate only if length==1. "eos" has length 3,
		// so it is dropped. Model token is "r50".
		{name: "canon eos r50", query: "canon eos r50", want: "canon:r50"},

		// --- Fujifilm ---
		// Tokenizer splits "x-t5" into ["x", "t5"]. "x" is a single-letter token
		// immediately before "t5", so it is prepended.
		{name: "fujifilm x-t5", query: "fujifilm x-t5", want: "fujifilm:xt5"},

		// --- Brand-only (no model token) ---
		{name: "sony camera brand only", query: "sony camera", want: "sony:"},

		// --- No brand ---
		{name: "mirrorless camera no brand", query: "mirrorless camera", want: ""},

		// --- Storage variant stripping ---
		{name: "samsung galaxy s24 256gb storage stripped", query: "samsung galaxy s24 256gb", want: "samsung:s24"},
		{name: "samsung s24 256gb", query: "samsung s24 256gb", want: "samsung:s24"},
		{name: "samsung s24 128gb", query: "samsung s24 128gb", want: "samsung:s24"},
		// 256gb and 128gb must produce the same key
		{name: "storage variants same key 256", query: "samsung s24 256gb", want: "samsung:s24"},
		{name: "storage variants same key 128", query: "samsung s24 128gb", want: "samsung:s24"},

		// --- Cyrillic brand tokens ---
		{name: "самсунг s24 cyrillic brand", query: "самсунг s24", want: "samsung:s24"},
		{name: "пиксел 9a cyrillic pixel alias", query: "пиксел 9a", want: "google:pixel9a"},

		// --- Edge cases ---
		{name: "empty string", query: "", want: ""},
		{name: "only noise words", query: "camera phone laptop", want: ""},
		{name: "only numbers no brand", query: "500", want: ""},

		// --- Apple MacBook ---
		// "macbook" is a familyPrefixToken. "pro" is a discriminator between
		// "macbook" and "14". Result: "apple:macbookpro14".
		{name: "apple macbook pro 14", query: "apple macbook pro 14", want: "apple:macbookpro14"},

		// --- Word-order invariance ---
		{name: "a6000 sony same as sony a6000", query: "a6000 sony", want: "sony:a6000"},

		// --- Discriminator suffix inclusion ---
		{name: "samsung galaxy s24 ultra", query: "samsung galaxy s24 ultra", want: "samsung:s24ultra"},
		{name: "iphone 15 plus", query: "iphone 15 plus", want: "apple:iphone15plus"},

		// --- Additional real-world queries ---
		{name: "sony a7r v", query: "sony a7r v", want: "sony:a7r"},
		{name: "xiaomi redmi note 14 pro", query: "xiaomi redmi note 14 pro", want: "xiaomi:14pro"},
		{name: "nikon z50", query: "nikon z50", want: "nikon:z50"},
		{name: "oneplus 12", query: "oneplus 12", want: "oneplus:12"},
		{name: "huawei p60 pro", query: "huawei p60 pro", want: "huawei:p60pro"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Normalize(tc.query)
			if got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}

// TestNormalizeWordOrderInvariance explicitly verifies that swapping brand and
// model position produces the same key.
func TestNormalizeWordOrderInvariance(t *testing.T) {
	pairs := [][2]string{
		{"sony a6000", "a6000 sony"},
		{"sony a6000 mirrorless", "a6000 sony mirrorless"},
		{"samsung s24", "s24 samsung"},
	}
	for _, pair := range pairs {
		a := Normalize(pair[0])
		b := Normalize(pair[1])
		if a == "" || b == "" {
			t.Errorf("Normalize(%q) or Normalize(%q) returned empty unexpectedly", pair[0], pair[1])
			continue
		}
		if a != b {
			t.Errorf("word-order invariance failed: Normalize(%q)=%q != Normalize(%q)=%q",
				pair[0], a, pair[1], b)
		}
	}
}

// TestNormalizeDistinctModelsHaveDifferentKeys verifies that clearly different
// models within the same brand produce different keys.
func TestNormalizeDistinctModelsHaveDifferentKeys(t *testing.T) {
	pairs := [][2]string{
		{"sony a6000", "sony a6700"},
		{"samsung s24", "samsung s24 fe"},
		{"pixel 9a", "pixel 9"},
		{"iphone 15", "iphone 15 pro"},
	}
	for _, pair := range pairs {
		a := Normalize(pair[0])
		b := Normalize(pair[1])
		if a == "" || b == "" {
			t.Errorf("Normalize(%q) or Normalize(%q) returned empty unexpectedly", pair[0], pair[1])
			continue
		}
		if a == b {
			t.Errorf("expected distinct keys for %q and %q, both got %q",
				pair[0], pair[1], a)
		}
	}
}
