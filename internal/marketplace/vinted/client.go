package vinted

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TechXTT/xolto/internal/models"
)

const (
	sessionTTL = 10 * time.Minute
)

type client struct {
	http       *http.Client
	jar        *cookiejar.Jar
	cfg        Config
	mu         sync.Mutex
	sessionAt  time.Time
	hasSession bool
}

type searchResponse struct {
	Items []apiItem `json:"items"`
}

func newClient(cfg Config) *client {
	jar, _ := cookiejar.New(nil)
	return &client{
		cfg: cfg,
		jar: jar,
		http: &http.Client{
			Timeout: 20 * time.Second,
			Jar:     jar,
		},
	}
}

func (c *client) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasSession && time.Since(c.sessionAt) < sessionTTL {
		return nil
	}

	// Visit the homepage to seed anonymous cookies. Vinted's homepage already
	// sets a valid `access_token_web` JWT in the Set-Cookie header, which is
	// enough for the catalog API. We previously also POSTed to
	// /auth/token_refresh, but that endpoint requires an existing refresh token
	// and returns 401 for anonymous sessions, which clobbered the working token
	// we'd just received from the homepage.
	homeReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.HomeURL, nil)
	if err != nil {
		return err
	}
	c.setBrowserHeaders(homeReq)

	homeResp, err := c.http.Do(homeReq)
	if err != nil {
		return fmt.Errorf("vinted home request: %w", err)
	}
	homeResp.Body.Close()
	if homeResp.StatusCode < 200 || homeResp.StatusCode >= 400 {
		return fmt.Errorf("vinted home returned status %d", homeResp.StatusCode)
	}

	c.hasSession = true
	c.sessionAt = time.Now()
	slog.Info("vinted session established", "status", homeResp.StatusCode)
	return nil
}

func (c *client) search(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
	if err := c.ensureSession(ctx); err != nil {
		return nil, fmt.Errorf("vinted session: %w", err)
	}

	listings, err := c.doSearch(ctx, spec)
	if err == nil {
		return listings, nil
	}

	// On 401, refresh session once and retry.
	if strings.Contains(err.Error(), "status 401") {
		c.mu.Lock()
		c.hasSession = false
		c.mu.Unlock()
		if retryErr := c.ensureSession(ctx); retryErr != nil {
			return nil, retryErr
		}
		return c.doSearch(ctx, spec)
	}
	return nil, err
}

func (c *client) doSearch(ctx context.Context, spec models.SearchSpec) ([]models.Listing, error) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	c.setBrowserHeaders(req)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

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
		listing := mapListing(item, c.cfg.ID)
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

func (c *client) setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", c.cfg.AcceptLanguage)
	req.Header.Set("Referer", c.cfg.Referer)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
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
