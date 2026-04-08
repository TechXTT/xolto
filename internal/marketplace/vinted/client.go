package vinted

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/marktbot/internal/models"
)

const vintedBaseURL = "https://www.vinted.nl/api/v2/catalog/items"

type client struct {
	http *http.Client
}

type searchResponse struct {
	Items []apiItem `json:"items"`
}

func newClient() *client {
	return &client{
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *client) search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	params := url.Values{}
	params.Set("search_text", spec.Query)
	params.Set("per_page", "24")
	params.Set("page", "1")
	if min, ok := euroString(spec.MinPrice); ok {
		params.Set("price_from", min)
	}
	if max, ok := euroString(spec.MaxPrice); ok {
		params.Set("price_to", max)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, vintedBaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "nl-NL,nl;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://www.vinted.nl/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vinted search returned status %d", resp.StatusCode)
	}

	var payload searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	listings := make([]models.Listing, 0, len(payload.Items))
	for _, item := range payload.Items {
		listing := mapListing(item)
		if !matchesPrice(listing.Price, spec.MinPrice, spec.MaxPrice) {
			continue
		}
		if !matchesCondition(listing.Condition, spec.Condition) {
			continue
		}
		listings = append(listings, listing)
	}
	return listings, nil
}

func euroString(cents int) (string, bool) {
	if cents <= 0 {
		return "", false
	}
	return strconv.FormatFloat(float64(cents)/100, 'f', 2, 64), true
}

func matchesPrice(price, min, max int) bool {
	if min > 0 && price < min {
		return false
	}
	if max > 0 && price > max {
		return false
	}
	return true
}

func matchesCondition(condition string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		if strings.ToLower(strings.TrimSpace(value)) == condition {
			return true
		}
	}
	return false
}
