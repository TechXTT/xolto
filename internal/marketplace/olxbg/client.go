package olxbg

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

const (
	olxBGBaseURL = "https://www.olx.bg/api/v1/offers/"
	olxPageLimit = 40 // max items per page for OLX API v1
	olxMaxPages  = 5  // cap at 200 results per search cycle
)

type client struct {
	http *http.Client
}

func newClient() *client {
	return &client{
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *client) search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	var all []models.Listing

	var rawCount, droppedPrice, droppedCondition int
	for page := 0; page < olxMaxPages; page++ {
		batch, total, err := c.fetchPage(ctx, spec, page*olxPageLimit)
		if err != nil {
			if page == 0 {
				return nil, err
			}
			// Partial failure: return what we have so far.
			break
		}
		rawCount += len(batch)
		for _, offer := range batch {
			listing := mapListing(offer)
			if spec.CategoryID > 0 {
				listing.CategoryID = spec.CategoryID
			}
			if !matchesPrice(listing.Price, spec.MinPrice, spec.MaxPrice) {
				droppedPrice++
				continue
			}
			if !matchesCondition(listing.Condition, spec.Condition) {
				droppedCondition++
				continue
			}
			all = append(all, listing)
		}
		if len(all) >= total || len(batch) < olxPageLimit {
			break
		}
	}

	slog.Info("olxbg search completed",
		"query", spec.Query,
		"raw", rawCount,
		"dropped_price", droppedPrice,
		"dropped_condition", droppedCondition,
		"kept", len(all),
	)
	return all, nil
}

func (c *client) fetchPage(ctx context.Context, spec models.SearchSpec, offset int) ([]apiOffer, int, error) {
	params := url.Values{}
	params.Set("query", spec.Query)
	params.Set("offset", strconv.Itoa(offset))
	params.Set("limit", strconv.Itoa(olxPageLimit))

	// spec min/max are EUR cents; OLX API v1 expects whole BGN.
	if spec.MinPrice > 0 {
		params.Set("price[from]", strconv.Itoa(EURCentsToBGNWhole(spec.MinPrice)))
	}
	if spec.MaxPrice > 0 {
		params.Set("price[to]", strconv.Itoa(EURCentsToBGNWhole(spec.MaxPrice)))
	}
	if spec.CategoryID > 0 {
		params.Set("category_id", strconv.Itoa(spec.CategoryID))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, olxBGBaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "bg-BG,bg;q=0.9,en;q=0.8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.olx.bg/")
	req.Header.Set("Origin", "https://www.olx.bg")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("olxbg search returned status %d", resp.StatusCode)
	}

	var payload searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, 0, err
	}

	// Filter out inactive listings. OLX API v1 now exposes the state as a
	// string ("active", "removed_by_user", "outdated", etc.) — earlier it was
	// the `is_active` boolean, which no longer appears in the payload.
	active := payload.Data[:0]
	for _, offer := range payload.Data {
		if offer.Status == "" || offer.Status == "active" {
			active = append(active, offer)
		} else {
			slog.Info("olxbg: dropped non-active listing", "status", offer.Status, "id", string(offer.ID))
		}
	}

	return active, payload.Metadata.TotalElements, nil
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
	for _, v := range allowed {
		if v == condition {
			return true
		}
	}
	return false
}
