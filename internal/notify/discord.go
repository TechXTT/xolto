package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TechXTT/xolto/internal/format"
	"github.com/TechXTT/xolto/internal/models"
)

const (
	colorGreen  = 0x2ECC71
	colorYellow = 0xF1C40F
	colorRed    = 0xE74C3C

	maxWebhookRate = 30
	maxEmbedFields = 25
)

type Discord struct {
	webhookURL string
	client     *http.Client

	mu        sync.Mutex
	sent      int
	resetTime time.Time
}

func NewDiscord(webhookURL string) *Discord {
	return &Discord{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 10 * time.Second},
		resetTime:  time.Now().Add(time.Minute),
	}
}

func (d *Discord) Enabled() bool {
	return d.webhookURL != ""
}

// SendDeal sends a rich embed notification for a scored listing.
func (d *Discord) SendDeal(sl models.ScoredListing, queryName string) error {
	if !d.Enabled() {
		return nil
	}
	if err := d.rateLimit(); err != nil {
		return err
	}

	color := colorRed
	if sl.Score >= 8 {
		color = colorGreen
	} else if sl.Score >= 6 {
		color = colorYellow
	}

	fields := []embedField{
		{Name: "Asking Price", Value: format.Euro(sl.Listing.Price), Inline: true},
		{Name: "Suggested Offer", Value: format.Euro(sl.OfferPrice), Inline: true},
		{Name: "Score", Value: fmt.Sprintf("%.1f/10", sl.Score), Inline: true},
	}

	if sl.FairPrice > 0 {
		fields = append(fields, embedField{Name: "Fair Value", Value: format.Euro(sl.FairPrice), Inline: true})
	}
	if sl.Confidence > 0 {
		fields = append(fields, embedField{Name: "Confidence", Value: fmt.Sprintf("%.0f%%", sl.Confidence*100), Inline: true})
	}
	if sl.ReasoningSource != "" {
		fields = append(fields, embedField{Name: "Reasoning", Value: sl.ReasoningSource, Inline: true})
	}
	if sl.Listing.PriceType != "" {
		fields = append(fields, embedField{Name: "Price Type", Value: sl.Listing.PriceType, Inline: true})
	}
	if cond, ok := sl.Listing.Attributes["condition"]; ok {
		fields = append(fields, embedField{Name: "Condition", Value: cond, Inline: true})
	}
	if sl.Listing.Location.City != "" {
		fields = append(fields, embedField{Name: "Location", Value: sl.Listing.Location.City, Inline: true})
	}
	if sl.Listing.Seller.Name != "" {
		fields = append(fields, embedField{Name: "Seller", Value: sl.Listing.Seller.Name, Inline: true})
	}
	if sl.Reason != "" {
		fields = append(fields, embedField{Name: "Why it stands out", Value: truncate(sl.Reason, 1024), Inline: false})
	}
	if sl.SearchAdvice != "" {
		fields = append(fields, embedField{Name: "Search Advice", Value: truncate(sl.SearchAdvice, 1024), Inline: false})
	}
	if len(sl.ComparableDeals) > 0 {
		fields = append(fields, embedField{Name: "Best Comparables", Value: renderComparables(sl.ComparableDeals), Inline: false})
	}
	fields = sanitizeFields(fields)

	embed := discordEmbed{
		Title:     truncate(fmt.Sprintf("[%s] %s", queryName, sl.Listing.Title), 256),
		URL:       sl.Listing.URL,
		Color:     color,
		Fields:    fields,
		Footer:    &embedFooter{Text: truncate(fmt.Sprintf("xolto | %s", queryName), 2048)},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if len(sl.Listing.ImageURLs) > 0 {
		if thumbURL := normalizeAssetURL(sl.Listing.ImageURLs[0]); thumbURL != "" {
			embed.Thumbnail = &embedMedia{URL: thumbURL}
		}
	}

	body, err := json.Marshal(webhookPayload{Embeds: []discordEmbed{embed}})
	if err != nil {
		return fmt.Errorf("marshaling webhook payload: %w", err)
	}

	resp, err := d.client.Post(d.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sending webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		slog.Warn("Discord rate limited, will retry later")
		return fmt.Errorf("discord rate limited")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord webhook returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	slog.Info("Discord notification sent", "title", sl.Listing.Title, "score", sl.Score)
	return nil
}

func (d *Discord) rateLimit() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if now.After(d.resetTime) {
		d.sent = 0
		d.resetTime = now.Add(time.Minute)
	}
	if d.sent >= maxWebhookRate {
		return fmt.Errorf("discord rate limit reached (%d/min), waiting", maxWebhookRate)
	}

	d.sent++
	return nil
}

func renderComparables(comparables []models.ComparableDeal) string {
	limit := len(comparables)
	if limit > 3 {
		limit = 3
	}

	lines := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		comp := comparables[i]
		lines = append(lines, fmt.Sprintf("- %s (%s, %.0f%%)", truncate(comp.Title, 70), format.Euro(comp.Price), comp.Similarity*100))
	}
	return truncate(strings.Join(lines, "\n"), 1024)
}

func sanitizeFields(fields []embedField) []embedField {
	cleaned := make([]embedField, 0, min(len(fields), maxEmbedFields))
	for _, field := range fields {
		name := truncate(strings.TrimSpace(field.Name), 256)
		value := truncate(strings.TrimSpace(field.Value), 1024)
		if name == "" || value == "" {
			continue
		}
		cleaned = append(cleaned, embedField{Name: name, Value: value, Inline: field.Inline})
		if len(cleaned) >= maxEmbedFields {
			break
		}
	}
	return cleaned
}

func normalizeAssetURL(value string) string {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return ""
	case strings.HasPrefix(value, "//"):
		return "https:" + value
	case strings.HasPrefix(value, "http://"), strings.HasPrefix(value, "https://"):
		return value
	default:
		return ""
	}
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return strings.TrimSpace(value[:max-3]) + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type webhookPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title     string       `json:"title"`
	URL       string       `json:"url,omitempty"`
	Color     int          `json:"color"`
	Fields    []embedField `json:"fields,omitempty"`
	Thumbnail *embedMedia  `json:"thumbnail,omitempty"`
	Footer    *embedFooter `json:"footer,omitempty"`
	Timestamp string       `json:"timestamp,omitempty"`
}

type embedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type embedMedia struct {
	URL string `json:"url"`
}

type embedFooter struct {
	Text string `json:"text"`
}
