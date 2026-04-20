// Package modelkey provides query-to-model-key normalization for the
// price_history comparables pool. The model_key is a canonical
// "brand:modeltokens" string that groups near-equivalent product queries
// (word-order variations, generic qualifiers) into one pool, while keeping
// clearly distinct models separated.
//
// Conservative design contract:
//   - Wrong key is worse than no key. When in doubt, return "".
//   - An empty string signals "fall back to raw query" to callers.
//   - Incomplete brand coverage is safe; incorrect aliasing is not.
//
// # Algorithm
//
//  1. Tokenize the query using the word-boundary regex [\p{L}\p{N}]+. Lowercase
//     all tokens.
//
//  2. Brand lookup. Check each token against brandTokens. The FIRST brand token
//     found determines the canonical brand. If no brand token is found, return "".
//
//  3. Model token extraction. Scan all tokens for a token that contains at least
//     one digit AND is not in noiseTokens. Take the longest such token. If none
//     is found, return "brand:" (brand-only key).
//
//  4. Family prefix. If the token that triggered brand detection is also in
//     familyPrefixTokens, prepend it to the model number. Additionally, any
//     discriminatorTokens that appear between the family-prefix token and the
//     model-number token (inclusive) are appended in order.
//     For single-letter non-noise tokens (len==1) that appear immediately before
//     the model-number token in the token list, prepend them to the model number
//     (covers Fujifilm X-T series: "x" + "t5" → "xt5").
//
//  5. Discriminator suffixes. Collect discriminatorTokens that appear immediately
//     after the model-number token and append them.
//
//  6. Return "brand:" + modelSegment. All lowercase, no spaces.
//
// # Implementation note — "macbook pro 14"
//
// Because "macbook" is a familyPrefixToken, "pro" appears between the family
// prefix and the model number "14", and "pro" is a discriminatorToken, the
// result is "apple:macbookpro14". This keeps "MacBook Pro 14" and
// "MacBook Air 13" in distinct pools.
package modelkey

import (
	"regexp"
	"unicode"
)

var wordTokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// brandTokens maps each recognizable token to its canonical brand name.
var brandTokens = map[string]string{
	// Sony
	"sony": "sony",
	// Samsung
	"samsung": "samsung", "galaxy": "samsung",
	// Google / Pixel
	"google": "google", "pixel": "google",
	// Apple
	"apple": "apple", "iphone": "apple", "ipad": "apple", "macbook": "apple",
	// Canon
	"canon": "canon",
	// Nikon
	"nikon": "nikon",
	// Fujifilm
	"fujifilm": "fujifilm", "fuji": "fujifilm",
	// Xiaomi
	"xiaomi": "xiaomi", "redmi": "xiaomi",
	// OnePlus
	"oneplus": "oneplus",
	// Motorola
	"motorola": "motorola", "moto": "motorola",
	// Huawei
	"huawei": "huawei",
	// LG
	"lg": "lg",
	// Lenovo
	"lenovo": "lenovo", "thinkpad": "lenovo",
	// Panasonic
	"panasonic": "panasonic",
	// Olympus
	"olympus": "olympus",
	// Leica
	"leica": "leica",
	// BG Cyrillic brand tokens (OLX.bg wedge — mandatory coverage)
	"самсунг":  "samsung",  // Samsung
	"гугъл":    "google",   // Google
	"айфон":    "apple",    // iPhone
	"епъл":     "apple",    // Apple
	"хуауей":   "huawei",   // Huawei
	"моторола":  "motorola", // Motorola
	"сони":     "sony",     // Sony
	"ксиаоми":  "xiaomi",   // Xiaomi
	"пиксел":   "google",   // Pixel in BG
}

// familyPrefixTokens maps a token to its canonical Latin family-prefix string.
// These tokens serve as product-family identifiers and should be prepended to
// the numeric model token (e.g. "pixel" + "9a" → "pixel9a").
// "galaxy", "redmi", "thinkpad" are intentionally excluded: the model tokens
// in those families (s24, note14, x1) are self-identifying without the family name.
var familyPrefixTokens = map[string]string{
	"pixel":   "pixel",   // Google Pixel → "pixel9a"
	"iphone":  "iphone",  // Apple iPhone → "iphone15pro"
	"ipad":    "ipad",    // Apple iPad → "ipad10"
	"macbook": "macbook", // Apple MacBook → "macbookpro14"
	"пиксел":  "pixel",   // BG Cyrillic Pixel alias → canonical "pixel"
}

// noiseTokens are words that must not be selected as model identifier tokens.
var noiseTokens = map[string]bool{
	// Generic category words
	"camera": true, "phone": true, "smartphone": true, "laptop": true,
	"mirrorless": true, "dslr": true, "body": true, "kit": true,
	"notebook": true, "tablet": true, "computer": true, "desktop": true,
	// Common qualifiers
	"new": true, "used": true, "good": true, "like": true, "mint": true,
	"unlocked": true, "factory": true, "sealed": true,
	// Storage sizes (not model discriminators for this pool)
	"128gb": true, "256gb": true, "64gb": true, "512gb": true,
	"16gb": true, "32gb": true, "1tb": true, "2tb": true,
	// Colors
	"black": true, "white": true, "silver": true, "gold": true,
	"blue": true, "red": true, "green": true, "pink": true, "grey": true, "gray": true,
	// BG Cyrillic noise
	"смартфон": true, "телефон": true, "лаптоп": true, "фотоапарат": true,
	"камера": true, "апарат": true,
}

// discriminatorTokens are version/variant qualifiers that differentiate models
// within a product line and are concatenated onto the model token.
var discriminatorTokens = map[string]bool{
	"pro": true, "plus": true, "ultra": true, "fe": true,
	"max": true, "mini": true, "lite": true,
}

// tokenHasDigit reports whether a token contains at least one Unicode digit.
func tokenHasDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// Normalize converts a search query to a canonical model key of the form
// "brand:modeltokens". Returns "" when the query does not match a known brand
// (signal to callers to fall back to raw-query pooling).
func Normalize(query string) string {
	tokens := wordTokenRe.FindAllString(query, -1)
	for i, t := range tokens {
		tokens[i] = toLower(t)
	}

	if len(tokens) == 0 {
		return ""
	}

	// Step 2: find brand — first token that matches brandTokens.
	brand := ""
	brandTriggerToken := ""
	brandTriggerIdx := -1
	for i, tok := range tokens {
		if canonical, ok := brandTokens[tok]; ok {
			brand = canonical
			brandTriggerToken = tok
			brandTriggerIdx = i
			break
		}
	}
	if brand == "" {
		return ""
	}

	// Step 3: find model number token — longest token with a digit, not in
	// noiseTokens. If the brand trigger token itself has a digit (e.g. "oneplus"),
	// it is excluded from model token candidates.
	modelTok := ""
	modelTokIdx := -1
	for i, tok := range tokens {
		if i == brandTriggerIdx {
			continue
		}
		if noiseTokens[tok] {
			continue
		}
		if !tokenHasDigit(tok) {
			continue
		}
		if len(tok) > len(modelTok) {
			modelTok = tok
			modelTokIdx = i
		}
	}

	// Step 4: build model segment.
	var modelSegment string
	if modelTok == "" {
		// Brand-only key: brand trigger found but no numeric model token.
		return brand + ":"
	}

	// Find family prefix: scan ALL tokens for a familyPrefixToken (not just the
	// brand trigger). This handles cases like "google pixel 9a" where brand is
	// triggered by "google" but "pixel" is the family prefix, and "apple macbook
	// pro 14" where brand is "apple" but "macbook" is the family prefix.
	familyPrefix := ""
	familyPrefixIdx := -1
	for i, tok := range tokens {
		if canonical, ok := familyPrefixTokens[tok]; ok {
			familyPrefix = canonical
			familyPrefixIdx = i
			break
		}
	}

	// Check for single-letter non-noise token immediately before the model token.
	// Only used when no family prefix is found (e.g. "fujifilm x-t5" → "xt5").
	singleLetterPrefix := ""
	if familyPrefix == "" && modelTokIdx > 0 {
		prev := tokens[modelTokIdx-1]
		if len([]rune(prev)) == 1 && !noiseTokens[prev] && !tokenHasDigit(prev) {
			// Only prepend if this token is not the brand trigger and not a discriminator
			if prev != brandTriggerToken && !discriminatorTokens[prev] {
				singleLetterPrefix = prev
			}
		}
	}

	// Collect discriminators between family prefix and model number (when family
	// prefix comes before model number in the token list).
	midDiscriminators := ""
	if familyPrefix != "" && familyPrefixIdx >= 0 && familyPrefixIdx < modelTokIdx {
		for i := familyPrefixIdx + 1; i < modelTokIdx; i++ {
			if discriminatorTokens[tokens[i]] {
				midDiscriminators += tokens[i]
			}
		}
	}

	// Collect discriminators immediately after the model number.
	postDiscriminators := ""
	for i := modelTokIdx + 1; i < len(tokens); i++ {
		if discriminatorTokens[tokens[i]] {
			postDiscriminators += tokens[i]
		} else {
			// Stop at the first non-discriminator token after the model number.
			break
		}
	}

	// Assemble the model segment.
	if familyPrefix != "" {
		// Family prefix takes priority over single-letter prefix.
		modelSegment = familyPrefix + midDiscriminators + modelTok + postDiscriminators
	} else if singleLetterPrefix != "" {
		modelSegment = singleLetterPrefix + modelTok + postDiscriminators
	} else {
		modelSegment = modelTok + postDiscriminators
	}

	return brand + ":" + modelSegment
}

// toLower lowercases a string handling both ASCII and Unicode.
func toLower(s string) string {
	runes := []rune(s)
	for i, r := range runes {
		runes[i] = unicode.ToLower(r)
	}
	return string(runes)
}
