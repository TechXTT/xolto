package marktplaats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/models"
	"golang.org/x/time/rate"
)

const (
	baseURL                = "https://www.marktplaats.nl/lrp/api/search"
	maxLimit               = 100
	maxResults             = 5000
	audioTVPhotoCategoryID = 31
)

type httpClient struct {
	http     *http.Client
	limiter  *rate.Limiter
	zipCode  string
	distance int
}

func (c *httpClient) search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	var all []models.Listing
	offset := 0

	for {
		if err := c.limiter.Wait(ctx); err != nil {
			return all, err
		}

		listings, total, err := c.fetchPage(ctx, spec, offset)
		if err != nil {
			return all, fmt.Errorf("fetching page at offset %d: %w", offset, err)
		}

		all = append(all, listings...)
		offset += maxLimit

		if offset >= total || offset >= maxResults || len(listings) == 0 {
			break
		}
	}

	slog.Info("search completed", "query", spec.Query, "results", len(all))
	return all, nil
}

func (c *httpClient) fetchPage(ctx context.Context, spec models.SearchSpec, offset int) ([]models.Listing, int, error) {
	params := url.Values{}
	params.Set("query", spec.Query)
	params.Set("limit", strconv.Itoa(maxLimit))
	params.Set("offset", strconv.Itoa(offset))
	params.Set("sortBy", "SORT_INDEX")
	params.Set("sortOrder", "DECREASING")
	params.Set("viewOptions", "list-view")

	if spec.CategoryID > 0 {
		l1, l2 := resolveCategoryParams(spec.CategoryID)
		params.Set("l1CategoryId", strconv.Itoa(l1))
		if l2 > 0 {
			params.Set("l2CategoryId", strconv.Itoa(l2))
		}
	}
	if c.zipCode != "" {
		params.Set("postcode", c.zipCode)
		params.Set("distanceMeters", strconv.Itoa(c.distance))
	}

	reqURL := baseURL + "?" + params.Encode()

	var resp *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, 0, err
		}
		setMarktplaatsHeaders(req)

		resp, err = c.http.Do(req)
		if err != nil {
			slog.Warn("request failed, retrying", "attempt", attempt+1, "error", err)
			time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			resp.Body.Close()
			slog.Warn("retryable status", "status", resp.StatusCode, "attempt", attempt+1)
			time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
			continue
		}
		break
	}
	if err != nil {
		return nil, 0, err
	}
	if resp == nil {
		return nil, 0, fmt.Errorf("all retries exhausted")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("reading response: %w", err)
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, 0, fmt.Errorf("parsing response: %w", err)
	}

	listings := make([]models.Listing, 0, len(apiResp.Listings))
	for _, l := range apiResp.Listings {
		listing := convertListing(l, spec)
		if listing == nil {
			continue
		}
		listings = append(listings, *listing)
	}

	return listings, apiResp.TotalResultCount, nil
}

func convertListing(l apiListing, spec models.SearchSpec) *models.Listing {
	price := parsePriceCents(l.PriceInfo)
	priceType := parsePriceType(l.PriceInfo)

	if spec.MinPrice > 0 && price < spec.MinPrice {
		return nil
	}
	if spec.MaxPrice > 0 && price > spec.MaxPrice {
		return nil
	}

	if len(spec.Condition) > 0 {
		condition := ""
		for _, attr := range l.Attributes {
			if attr.Key == "condition" {
				condition = normalizeCondition(attr.Value)
				break
			}
		}
		if condition != "" && !containsIgnoreCase(spec.Condition, condition) {
			return nil
		}
	}

	listing := &models.Listing{
		MarketplaceID: "marktplaats",
		CanonicalID:   "marktplaats:" + l.ItemID,
		ItemID:        l.ItemID,
		Title:         l.Title,
		Description:   l.Description,
		Price:         price,
		PriceType:     priceType,
		Seller: models.Seller{
			ID:   strconv.FormatInt(l.SellerInformation.SellerID, 10),
			Name: l.SellerInformation.SellerName,
		},
		Location: models.Location{
			City: l.Location.CityName,
		},
		URL:        fmt.Sprintf("https://www.marktplaats.nl%s", l.VIPUrl),
		CategoryID: l.CategoryID,
		Attributes: make(map[string]string),
	}

	if l.Date != "" {
		if t, err := time.Parse(time.RFC3339, l.Date); err == nil {
			listing.Date = t
		}
	}

	for _, img := range l.ImageUrls {
		if img != "" {
			listing.ImageURLs = append(listing.ImageURLs, img)
		}
	}

	for _, attr := range l.Attributes {
		listing.Attributes[attr.Key] = attr.Value
		if attr.Key == "condition" {
			listing.Condition = normalizeCondition(attr.Value)
		}
	}

	return listing
}

func parsePriceCents(info apiPriceInfo) int {
	if info.PriceCents > 0 {
		return info.PriceCents
	}
	return 0
}

func parsePriceType(info apiPriceInfo) string {
	switch info.PriceType {
	case "FIXED":
		return "fixed"
	case "NEGOTIABLE":
		return "negotiable"
	case "BIDDING":
		return "bidding"
	case "FREE":
		return "free"
	case "SEE_DESCRIPTION":
		return "see-description"
	case "EXCHANGE":
		return "exchange"
	case "RESERVED":
		return "reserved"
	case "FAST_BID":
		return "fast-bid"
	default:
		return strings.ToLower(info.PriceType)
	}
}

func normalizeCondition(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "nieuw", "new":
		return "new"
	case "zo goed als nieuw", "like new", "as good as new":
		return "like_new"
	case "gebruikt", "used", "good":
		return "good"
	case "fair":
		return "fair"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func containsIgnoreCase(slice []string, s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	for _, v := range slice {
		if strings.ToLower(strings.TrimSpace(v)) == lower {
			return true
		}
	}
	return false
}

func setMarktplaatsHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "nl-NL,nl;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Referer", "https://www.marktplaats.nl/")
	req.Header.Set("Origin", "https://www.marktplaats.nl")
}

func resolveCategoryParams(categoryID int) (int, int) {
	switch categoryID {
	case 487, 495, 1360, 1400, 1484, 500, 501:
		return audioTVPhotoCategoryID, categoryID
	default:
		if categoryID < 100 {
			return categoryID, 0
		}
		return categoryID, 0
	}
}

// API response types

type apiResponse struct {
	Listings         []apiListing `json:"listings"`
	TotalResultCount int          `json:"totalResultCount"`
}

type apiListing struct {
	ItemID            string         `json:"itemId"`
	Title             string         `json:"title"`
	Description       string         `json:"description"`
	PriceInfo         apiPriceInfo   `json:"priceInfo"`
	SellerInformation apiSellerInfo  `json:"sellerInformation"`
	Location          apiLocation    `json:"location"`
	Date              string         `json:"date"`
	VIPUrl            string         `json:"vipUrl"`
	ImageUrls         []string       `json:"imageUrls"`
	CategoryID        int            `json:"categoryId"`
	Attributes        []apiAttribute `json:"attributes"`
}

type apiPriceInfo struct {
	PriceCents int    `json:"priceCents"`
	PriceType  string `json:"priceType"`
}

type apiSellerInfo struct {
	SellerID   int64  `json:"sellerId"`
	SellerName string `json:"sellerName"`
}

type apiLocation struct {
	CityName string `json:"cityName"`
}

type apiAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
