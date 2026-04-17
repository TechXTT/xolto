// Package listingfetcher resolves a public marketplace URL into a
// models.Listing by scraping schema.org JSON-LD metadata or (for OLX BG)
// calling the public offer API. This is used by the ad-hoc "analyze this
// listing" endpoint so a user can paste any URL and get an AI verdict
// without having to wait for the scheduled crawler to find it.
package listingfetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/marketplace/olxbg"
	"github.com/TechXTT/xolto/internal/models"
)

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

var (
	// Matches <script type="application/ld+json">...</script> blocks.
	jsonLDRe = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	// olxBGIDRe captures the trailing "IDxxxx.html" OLX uses in every listing URL.
	olxBGIDRe = regexp.MustCompile(`(?:ID([A-Za-z0-9]+)\.html|/([0-9]+)(?:/|$))`)
)

// Fetcher resolves a listing URL. One Fetcher is safe for concurrent use.
type Fetcher struct {
	http *http.Client
}

func New() *Fetcher {
	return &Fetcher{
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// Fetch returns a populated models.Listing for the given URL. The returned
// listing always has ItemID, Title, Price, URL and MarketplaceID set when the
// page parses successfully. Any parse failure is surfaced as a wrapped error
// so the caller can show a helpful message to the user.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (models.Listing, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return models.Listing{}, fmt.Errorf("url is empty")
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return models.Listing{}, fmt.Errorf("url must start with http:// or https://")
	}

	marketplace := detectMarketplace(rawURL)

	// OLX BG exposes a clean JSON API; prefer it over HTML scraping since the
	// HTML is client-rendered and ships little useful metadata.
	if marketplace == "olxbg" {
		if listing, err := f.fetchOLXByID(ctx, rawURL); err == nil {
			return listing, nil
		}
		// Fall through to the generic JSON-LD path on API failure.
	}

	return f.fetchJSONLD(ctx, rawURL, marketplace)
}

// fetchJSONLD grabs the HTML page and extracts the first schema.org Product
// JSON-LD block. Marktplaats and Vinted both emit one on every listing page,
// which gives us name, description, price, currency and image URLs without
// site-specific parsers.
func (f *Fetcher) fetchJSONLD(ctx context.Context, rawURL, marketplace string) (models.Listing, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return models.Listing{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,nl;q=0.8")

	resp, err := f.http.Do(req)
	if err != nil {
		return models.Listing{}, fmt.Errorf("fetch url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return models.Listing{}, fmt.Errorf("listing url returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return models.Listing{}, fmt.Errorf("read body: %w", err)
	}

	product, err := extractProductJSONLD(body)
	if err != nil {
		return models.Listing{}, err
	}

	priceCents, currency := product.priceCents()
	if marketplace == "olxbg" && strings.EqualFold(currency, "BGN") {
		priceCents = olxbg.BGNStotinkiToEURCents(priceCents)
		currency = "EUR"
	}
	listing := models.Listing{
		MarketplaceID: marketplace,
		Title:         strings.TrimSpace(product.Name),
		Description:   strings.TrimSpace(product.Description),
		Price:         priceCents,
		PriceType:     "fixed",
		URL:           rawURL,
		ImageURLs:     product.normalizedImages(),
		Attributes:    map[string]string{},
	}
	if currency != "" {
		listing.Attributes["currency"] = currency
	}

	id := extractIDFromURL(marketplace, rawURL)
	if id == "" {
		id = rawURL // fall back so downstream callers still get a stable key
	}
	listing.ItemID = fmt.Sprintf("%s_adhoc_%s", marketplace, id)
	listing.CanonicalID = fmt.Sprintf("%s:%s", marketplace, id)

	if listing.Title == "" {
		return models.Listing{}, fmt.Errorf("listing page did not expose a product title")
	}
	if listing.Price <= 0 {
		return models.Listing{}, fmt.Errorf("listing page did not expose a numeric price")
	}
	return listing, nil
}

// fetchOLXByID calls the OLX BG offers API directly for the listing ID we
// parsed out of the URL. This is faster and more reliable than scraping the
// client-rendered HTML.
func (f *Fetcher) fetchOLXByID(ctx context.Context, rawURL string) (models.Listing, error) {
	match := olxBGIDRe.FindStringSubmatch(rawURL)
	if len(match) < 3 {
		return models.Listing{}, fmt.Errorf("could not extract olx id from url")
	}
	id := match[1]
	if id == "" {
		id = match[2]
	}

	endpoint := "https://www.olx.bg/api/v1/offers/" + id + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.Listing{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://www.olx.bg/")

	resp, err := f.http.Do(req)
	if err != nil {
		return models.Listing{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return models.Listing{}, fmt.Errorf("olx offer api returned status %d", resp.StatusCode)
	}

	var payload struct {
		Data struct {
			ID     json.Number `json:"id"`
			URL    string      `json:"url"`
			Title  string      `json:"title"`
			Status string      `json:"status"`
			Params []struct {
				Key   string `json:"key"`
				Value struct {
					Value      float64 `json:"value"`
					Currency   string  `json:"currency"` // "EUR" | "BGN" | ""
					Negotiable bool    `json:"negotiable"`
					Type       string  `json:"type"` // "price" | "free" | "exchange"
					Label      string  `json:"label"`
				} `json:"value"`
			} `json:"params"`
			Photos []struct {
				Link string `json:"link"`
			} `json:"photos"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return models.Listing{}, err
	}

	var rawPrice float64
	var apiCurrency string
	priceType := "fixed"
	for _, p := range payload.Data.Params {
		if p.Key == "price" {
			rawPrice = p.Value.Value
			apiCurrency = p.Value.Currency
			switch {
			case p.Value.Negotiable:
				priceType = "negotiable"
			case p.Value.Type == "free":
				priceType = "free"
			case p.Value.Type == "exchange":
				priceType = "negotiable"
			}
			break
		}
	}
	// Apply currency-aware conversion matching mapper.go (XOL-31).
	// The JSON-LD fallback (fetchJSONLD) already handles this correctly; this
	// path is the direct API path and must mirror that logic.
	eurCents, currencyStatus := olxbg.CurrencyStatusFromAPI(rawPrice, apiCurrency, payload.Data.ID.String())
	var imageURLs []string
	for _, photo := range payload.Data.Photos {
		if photo.Link != "" {
			imageURLs = append(imageURLs, photo.Link)
		}
	}

	listing := models.Listing{
		MarketplaceID: "olxbg",
		ItemID:        fmt.Sprintf("olxbg_%s", payload.Data.ID.String()),
		CanonicalID:   fmt.Sprintf("olxbg:%s", payload.Data.ID.String()),
		Title:         strings.TrimSpace(payload.Data.Title),
		Description:   strings.TrimSpace(payload.Data.Description),
		Price:         eurCents,
		PriceType:     priceType,
		URL:           payload.Data.URL,
		ImageURLs:     imageURLs,
		Attributes: map[string]string{
			"currency":        "EUR",
			"price_local":     fmt.Sprintf("%.2f", rawPrice),
			"price_local_ccy": strings.ToUpper(strings.TrimSpace(apiCurrency)),
			"currency_status": currencyStatus,
		},
	}
	if listing.URL == "" {
		listing.URL = rawURL
	}
	if listing.Title == "" {
		return models.Listing{}, fmt.Errorf("olx offer api returned no title")
	}
	if listing.Price <= 0 {
		return models.Listing{}, fmt.Errorf("olx offer api returned no price")
	}
	return listing, nil
}

func detectMarketplace(rawURL string) string {
	lower := strings.ToLower(rawURL)
	switch {
	case strings.Contains(lower, "marktplaats."):
		return "marktplaats"
	case strings.Contains(lower, "vinted.dk"):
		return "vinted_dk"
	case strings.Contains(lower, "vinted.nl"):
		return "vinted_nl"
	case strings.Contains(lower, "vinted."):
		return "vinted_nl"
	case strings.Contains(lower, "olx.bg"):
		return "olxbg"
	default:
		return "unknown"
	}
}

// extractIDFromURL returns a stable identifier from the listing URL.
// It's a best-effort helper: we just grab the last path segment and trim a
// .html suffix, which works for Marktplaats (m2380271704-sony-a6000),
// Vinted (8610035327-sony-alpha-a6000), and OLX BG (…-CID632-ID9Zs7B.html).
func extractIDFromURL(marketplace, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil {
		if marketplace == "olxbg" {
			if match := olxBGIDRe.FindStringSubmatch(parsed.EscapedPath()); len(match) >= 3 {
				if match[1] != "" {
					return match[1]
				}
				if match[2] != "" {
					return match[2]
				}
			}
		}

		trimmedPath := strings.TrimRight(parsed.Path, "/")
		lastSlash := strings.LastIndex(trimmedPath, "/")
		if lastSlash < 0 {
			return ""
		}
		segment := trimmedPath[lastSlash+1:]
		segment = strings.TrimSuffix(segment, ".html")
		if dash := strings.Index(segment, "-"); dash > 0 {
			return segment[:dash]
		}
		return segment
	}

	trimmed := strings.TrimRight(rawURL, "/")
	lastSlash := strings.LastIndex(trimmed, "/")
	if lastSlash < 0 {
		return ""
	}
	segment := trimmed[lastSlash+1:]
	segment = strings.TrimSuffix(segment, ".html")
	if q := strings.Index(segment, "?"); q >= 0 {
		segment = segment[:q]
	}
	if hash := strings.Index(segment, "#"); hash >= 0 {
		segment = segment[:hash]
	}
	if dash := strings.Index(segment, "-"); dash > 0 {
		return segment[:dash]
	}
	return segment
}

// productJSONLD is the minimal subset of schema.org/Product we need. Both
// Marktplaats and Vinted emit a block that looks like:
//
//	{"@type":"Product","name":"...","description":"...",
//	 "offers":{"@type":"Offer","price":"320","priceCurrency":"EUR"},
//	 "image":["..."]}
type productJSONLD struct {
	Type        json.RawMessage `json:"@type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Offers      json.RawMessage `json:"offers"`
	Image       json.RawMessage `json:"image"`
}

type offerJSONLD struct {
	Price         json.RawMessage `json:"price"`
	PriceCurrency string          `json:"priceCurrency"`
}

func extractProductJSONLD(body []byte) (*productJSONLD, error) {
	matches := jsonLDRe.FindAllSubmatch(body, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := unescapeHTMLEntities(string(m[1]))
		// JSON-LD entries can be objects, arrays, or @graph-wrapped. Try
		// unmarshalling each shape in turn until we find a Product.
		if product := findProductNode([]byte(raw)); product != nil {
			return product, nil
		}
	}
	return nil, fmt.Errorf("no schema.org Product JSON-LD block found on page")
}

// findProductNode walks a JSON-LD blob (which may be a single node, an array,
// or a @graph object) and returns the first @type == "Product" entry.
func findProductNode(raw []byte) *productJSONLD {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	if trimmed[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil
		}
		for _, item := range arr {
			if p := findProductNode(item); p != nil {
				return p
			}
		}
		return nil
	}

	var node productJSONLD
	if err := json.Unmarshal([]byte(trimmed), &node); err == nil {
		if isProductType(node.Type) && node.Name != "" {
			return &node
		}
	}

	// Handle @graph wrappers: {"@context":"...","@graph":[{...},{...}]}.
	var graphWrapper struct {
		Graph []json.RawMessage `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(trimmed), &graphWrapper); err == nil && len(graphWrapper.Graph) > 0 {
		for _, item := range graphWrapper.Graph {
			if p := findProductNode(item); p != nil {
				return p
			}
		}
	}
	return nil
}

func isProductType(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return strings.EqualFold(single, "Product")
	}
	var multi []string
	if err := json.Unmarshal(raw, &multi); err == nil {
		for _, t := range multi {
			if strings.EqualFold(t, "Product") {
				return true
			}
		}
	}
	return false
}

// priceCents normalizes the schema.org Offer price — which can be either a
// number or a string — into integer cents.
func (p *productJSONLD) priceCents() (int, string) {
	if len(p.Offers) == 0 {
		return 0, ""
	}

	// offers may itself be a single object or an array of offers.
	var offer offerJSONLD
	if err := json.Unmarshal(p.Offers, &offer); err != nil {
		var offers []offerJSONLD
		if err := json.Unmarshal(p.Offers, &offers); err != nil || len(offers) == 0 {
			return 0, ""
		}
		offer = offers[0]
	}

	var priceValue float64
	if len(offer.Price) > 0 {
		var asFloat float64
		if err := json.Unmarshal(offer.Price, &asFloat); err == nil {
			priceValue = asFloat
		} else {
			var asString string
			if err := json.Unmarshal(offer.Price, &asString); err == nil {
				asString = strings.ReplaceAll(asString, ",", ".")
				if f, err := strconv.ParseFloat(asString, 64); err == nil {
					priceValue = f
				}
			}
		}
	}
	return int(priceValue * 100), offer.PriceCurrency
}

// normalizedImages returns the product's image URLs as a clean []string,
// tolerating both string and []string forms and stripping protocol-relative
// prefixes.
func (p *productJSONLD) normalizedImages() []string {
	if len(p.Image) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(p.Image, &single); err == nil && single != "" {
		return []string{normalizeImageURL(single)}
	}
	var multi []string
	if err := json.Unmarshal(p.Image, &multi); err == nil {
		out := make([]string, 0, len(multi))
		for _, img := range multi {
			if img = normalizeImageURL(img); img != "" {
				out = append(out, img)
			}
		}
		return out
	}
	return nil
}

func normalizeImageURL(img string) string {
	img = strings.TrimSpace(img)
	if strings.HasPrefix(img, "//") {
		return "https:" + img
	}
	return img
}

// unescapeHTMLEntities handles the small number of HTML escapes we see inside
// JSON-LD blocks on Marktplaats (&#x27; for apostrophes inside product names).
// We do not use net/html to keep the dependency graph small.
func unescapeHTMLEntities(s string) string {
	replacements := []struct{ from, to string }{
		{"&amp;", "&"},
		{"&quot;", `"`},
		{"&#x27;", "'"},
		{"&#39;", "'"},
		{"&lt;", "<"},
		{"&gt;", ">"},
	}
	for _, r := range replacements {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	return s
}
