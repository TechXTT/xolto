package messenger

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/store"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const loginURL = "https://www.marktplaats.nl/account/login"

type Messenger struct {
	cfg    config.MessengerConfig
	store  store.Store
	dryRun bool

	browser *rod.Browser

	mu        sync.Mutex
	sentCount int
	hourStart time.Time
}

func New(cfg config.MessengerConfig, s store.Store, dryRun bool) *Messenger {
	return &Messenger{
		cfg:       cfg,
		store:     s,
		dryRun:    dryRun,
		hourStart: time.Now(),
	}
}

func (m *Messenger) Enabled() bool {
	return m.cfg.Enabled && !m.dryRun
}

// Init launches the browser and logs in.
func (m *Messenger) Init() error {
	if !m.cfg.Enabled {
		return nil
	}

	l := launcher.New()
	if m.cfg.Headless {
		l = l.Headless(true)
	} else {
		l = l.Headless(false)
	}

	u, err := l.Launch()
	if err != nil {
		return fmt.Errorf("launching browser: %w", err)
	}

	m.browser = rod.New().ControlURL(u)
	if err := m.browser.Connect(); err != nil {
		return fmt.Errorf("connecting to browser: %w", err)
	}

	if err := m.login(); err != nil {
		return fmt.Errorf("logging in: %w", err)
	}

	return nil
}

func (m *Messenger) Close() {
	if m.browser != nil {
		m.browser.Close()
	}
}

func (m *Messenger) login() error {
	page, err := m.browser.Page(proto.TargetCreateTarget{URL: loginURL})
	if err != nil {
		return fmt.Errorf("opening login page: %w", err)
	}
	defer page.Close()

	// Wait for page to load
	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("waiting for login page: %w", err)
	}

	// Dismiss cookie consent if present
	dismissCookieConsent(page)

	// Fill in credentials
	emailInput, err := page.Timeout(10 * time.Second).Element("input[name='email'], input[type='email'], #email")
	if err != nil {
		return fmt.Errorf("finding email input: %w", err)
	}
	if err := emailInput.Input(m.cfg.Username); err != nil {
		return fmt.Errorf("typing email: %w", err)
	}

	passwordInput, err := page.Element("input[name='password'], input[type='password'], #password")
	if err != nil {
		return fmt.Errorf("finding password input: %w", err)
	}
	if err := passwordInput.Input(m.cfg.Password); err != nil {
		return fmt.Errorf("typing password: %w", err)
	}

	// Click login button
	loginBtn, err := page.Element("button[type='submit'], input[type='submit']")
	if err != nil {
		return fmt.Errorf("finding login button: %w", err)
	}
	if err := loginBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("clicking login: %w", err)
	}

	// Wait for navigation after login
	time.Sleep(3 * time.Second)

	slog.Info("logged in to Marktplaats")
	return nil
}

// SendMessage sends an offer message to a seller for the given listing.
func (m *Messenger) SendMessage(sl models.ScoredListing, search models.SearchSpec) error {
	if !m.cfg.Enabled {
		return nil
	}

	// Check if already offered
	offered, err := m.store.WasOffered("", sl.Listing.ItemID)
	if err != nil {
		return fmt.Errorf("checking offer status: %w", err)
	}
	if offered {
		slog.Debug("already offered", "item", sl.Listing.ItemID)
		return nil
	}

	// Rate limit check
	if err := m.checkRate(); err != nil {
		return err
	}

	// Render message
	msg, err := renderMessage(search.MessageTemplate, sl)
	if err != nil {
		return fmt.Errorf("rendering message: %w", err)
	}

	if m.dryRun {
		slog.Info("DRY RUN: would send message",
			"item", sl.Listing.ItemID,
			"title", sl.Listing.Title,
			"message", msg,
		)
		return nil
	}

	// Navigate to listing and send message
	if err := m.sendViaPage(sl.Listing.URL, msg); err != nil {
		return fmt.Errorf("sending message for %s: %w", sl.Listing.ItemID, err)
	}

	// Mark as offered
	if err := m.store.MarkOffered("", sl.Listing.ItemID); err != nil {
		slog.Warn("failed to mark as offered", "item", sl.Listing.ItemID, "error", err)
	}

	m.mu.Lock()
	m.sentCount++
	m.mu.Unlock()

	slog.Info("message sent",
		"item", sl.Listing.ItemID,
		"title", sl.Listing.Title,
		"offer", fmt.Sprintf("€%.2f", float64(sl.OfferPrice)/100),
	)

	return nil
}

func (m *Messenger) sendViaPage(listingURL, message string) error {
	page, err := m.browser.Page(proto.TargetCreateTarget{URL: listingURL})
	if err != nil {
		return fmt.Errorf("opening listing page: %w", err)
	}
	defer page.Close()

	if err := page.WaitLoad(); err != nil {
		return fmt.Errorf("waiting for listing page: %w", err)
	}

	dismissCookieConsent(page)

	// Click "Bericht sturen" / "Send message" button
	msgBtn, err := page.Timeout(10 * time.Second).Element(
		"button[data-testid='send-message-button'], a[href*='bericht'], button:has-text('Bericht'), button:has-text('bericht')",
	)
	if err != nil {
		return fmt.Errorf("finding message button (listing may be removed or seller disabled messages): %w", err)
	}
	if err := msgBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("clicking message button: %w", err)
	}

	time.Sleep(2 * time.Second)

	// Find and fill message textarea
	textarea, err := page.Timeout(10 * time.Second).Element("textarea")
	if err != nil {
		return fmt.Errorf("finding message textarea: %w", err)
	}
	if err := textarea.Input(message); err != nil {
		return fmt.Errorf("typing message: %w", err)
	}

	// Click send
	sendBtn, err := page.Element("button[type='submit'], button:has-text('Verstuur'), button:has-text('Send')")
	if err != nil {
		return fmt.Errorf("finding send button: %w", err)
	}
	if err := sendBtn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("clicking send: %w", err)
	}

	time.Sleep(2 * time.Second)
	return nil
}

func (m *Messenger) checkRate() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if now.Sub(m.hourStart) > time.Hour {
		m.sentCount = 0
		m.hourStart = now
	}

	if m.sentCount >= m.cfg.MaxMessagesPerHour {
		return fmt.Errorf("rate limit reached: %d messages per hour", m.cfg.MaxMessagesPerHour)
	}

	return nil
}

func renderMessage(tmpl string, sl models.ScoredListing) (string, error) {
	if tmpl == "" {
		return fmt.Sprintf("Hoi! Ik ben geïnteresseerd in %s. Zou je €%.2f willen accepteren?",
			sl.Listing.Title, float64(sl.OfferPrice)/100), nil
	}

	t, err := template.New("msg").Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing message template: %w", err)
	}

	data := map[string]string{
		"Title":      sl.Listing.Title,
		"OfferPrice": fmt.Sprintf("%.2f", float64(sl.OfferPrice)/100),
		"AskPrice":   fmt.Sprintf("%.2f", float64(sl.Listing.Price)/100),
		"Score":      fmt.Sprintf("%.1f", sl.Score),
	}

	var buf strings.Builder
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing message template: %w", err)
	}

	return buf.String(), nil
}

func dismissCookieConsent(page *rod.Page) {
	// Try common cookie consent selectors
	selectors := []string{
		"button#gdpr-consent-accept-btn",
		"button[data-testid='accept-cookies']",
		"button.accept-cookies",
		"#onetrust-accept-btn-handler",
		"button:has-text('Accepteren')",
		"button:has-text('Alles accepteren')",
	}

	for _, sel := range selectors {
		el, err := page.Timeout(2 * time.Second).Element(sel)
		if err == nil {
			_ = el.Click(proto.InputMouseButtonLeft, 1)
			time.Sleep(500 * time.Millisecond)
			return
		}
	}
}
