package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TechXTT/xolto/internal/assistant"
	"github.com/TechXTT/xolto/internal/config"
	"github.com/TechXTT/xolto/internal/marketplace/listingfetcher"
	"github.com/TechXTT/xolto/internal/models"
	"github.com/TechXTT/xolto/internal/plain"
	"github.com/TechXTT/xolto/internal/replycopilot"
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
)

type SearchRunner interface {
	RunAllNow(ctx context.Context) error
	RunUserNow(ctx context.Context, userID string) error
}

// PlainAPIClient is the subset of the Plain GraphQL client used by the support
// handlers. Expressed as an interface so tests can substitute a mock transport.
type PlainAPIClient interface {
	UpsertCustomer(ctx context.Context, input plain.UpsertCustomerInput) (plain.UpsertCustomerResult, error)
	CreateThread(ctx context.Context, input plain.CreateThreadInput) (plain.CreateThreadResult, error)
	AddLabel(ctx context.Context, threadID, labelTypeID string) error
	AddNote(ctx context.Context, threadID, body string) error
	SetPriority(ctx context.Context, threadID string, priority plain.Priority) error
}

type Server struct {
	cfg                    config.ServerConfig
	db                     store.Store
	assistant              *assistant.Assistant
	broker                 *SSEBroker
	runner                 SearchRunner
	scorer                 *scorer.Scorer
	fetcher                *listingfetcher.Fetcher
	mux                    *http.ServeMux
	adminAllowlistWarnOnce sync.Once
	routesOnce             sync.Once
	// plainClient is used by the support handlers to call Plain GraphQL.
	// It is set to a real *plain.Client in production and can be overridden
	// in tests by substituting an httptest.Server via plain.Client.HTTPClient.
	plainClient PlainAPIClient
	// supportEvents is the in-process channel that the webhook handler pushes
	// events onto for downstream consumption by the classifier worker (SUP-4).
	// The channel is buffered with a small capacity; events are dropped if no
	// consumer is reading (Phase 1 — no DB-backed queue).
	supportEvents chan store.SupportEvent
	// mustHaveEvaluator is the optional semantic LLM evaluator for must-have
	// matching (XOL-22). When nil the /matches handler uses the tokenizer-only
	// path via ScoreMustHaves. Set at startup via SetMustHaveEvaluator.
	mustHaveEvaluator scorer.MustHaveEvaluator
	// replyClassifier is the LLM classifier for POST /reply-copilot (XOL-73).
	// When nil the handler returns 503 "classifier not configured".
	replyClassifier replycopilot.Classifier
}

type googleTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int    `json:"expires_in"`
	RefreshToken     string `json:"refresh_token"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type googleUserInfo struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

func NewServer(cfg config.ServerConfig, db store.Store, asst *assistant.Assistant, broker *SSEBroker, runner SearchRunner, sc *scorer.Scorer) *Server {
	if broker == nil {
		broker = NewSSEBroker()
	}
	mux := http.NewServeMux()
	// Initialise the Plain client. When PLAIN_API_KEY is empty (dev / test
	// environments) the client is still created; callers that require the API
	// key will return errors rather than panicking.
	plainClient := plain.New(cfg.PlainAPIKey)
	var rc replycopilot.Classifier
	if cfg.AIAPIKey != "" && cfg.AIModel != "" {
		rc = replycopilot.NewLLMClassifier(cfg.AIBaseURL, cfg.AIAPIKey, cfg.AIModel)
	}
	s := &Server{
		cfg:             cfg,
		db:              db,
		assistant:       asst,
		broker:          broker,
		runner:          runner,
		scorer:          sc,
		fetcher:         listingfetcher.New(),
		mux:             mux,
		plainClient:     plainClient,
		supportEvents:   make(chan store.SupportEvent, 64),
		replyClassifier: rc,
	}
	return s
}

func (s *Server) Handler() http.Handler {
	s.routesOnce.Do(func() {
		s.registerHealthRoutes(s.mux)
		s.registerAuthRoutes(s.mux)
		s.registerMissionRoutes(s.mux)
		s.registerListingRoutes(s.mux)
		s.registerBillingRoutes(s.mux)
		s.registerAdminRoutes(s.mux)
		s.registerOutreachRoutes(s.mux)
		s.registerSupportRoutes(s.mux)
		s.registerInternalRoutes(s.mux)
	})

	handler := http.Handler(s.mux)
	handler = s.adminIPAllowlistMiddleware(handler)
	handler = s.corsMiddleware(handler)
	handler = s.requestLoggingMiddleware(handler)
	handler = s.requestIDMiddleware(handler)
	// sentryMiddleware is outermost so it catches panics from every inner layer.
	handler = s.sentryMiddleware(handler)
	return handler
}

// SupportEvents returns the read-only channel of support events emitted by the
// Plain webhook handler. The classifier worker (SUP-4) consumes from this
// channel. The channel is never closed by the server; callers drain it until
// their context is cancelled.
func (s *Server) SupportEvents() <-chan store.SupportEvent {
	return s.supportEvents
}

// SetMustHaveEvaluator wires the optional LLM-backed must-have evaluator
// (XOL-22) into the /matches handler. When e is nil the handler falls back to
// tokenizer-only scoring. Must be called before Handler().
func (s *Server) SetMustHaveEvaluator(e scorer.MustHaveEvaluator) {
	s.mustHaveEvaluator = e
}

func (s *Server) StartBillingReconcileLoop(ctx context.Context, interval time.Duration) {
	if strings.TrimSpace(s.cfg.StripeSecret) == "" {
		return
	}
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runID, err := s.db.StartBillingReconcileRun(models.BillingReconcileRun{
					TriggeredBy: "system",
					Status:      "running",
					StartedAt:   time.Now().UTC(),
				})
				if err != nil {
					slog.Default().Error("billing reconcile run start failed", "op", "billing.reconcile.start_run", "error", err)
					continue
				}
				summary, reconcileErr := s.runStripeReconcile(ctx)
				if reconcileErr != nil {
					slog.Default().Error("billing reconcile run failed", "op", "billing.reconcile.run", "error", reconcileErr, "run_id", runID)
					if err := s.db.FinishBillingReconcileRun(runID, "failed", mustJSON(summary), mustJSON(map[string]any{"error": reconcileErr.Error()})); err != nil {
						slog.Default().Error("billing reconcile run finalize failed", "op", "billing.reconcile.finish", "error", err, "run_id", runID, "status", "failed")
					}
					continue
				}
				if err := s.db.FinishBillingReconcileRun(runID, "success", mustJSON(summary), ""); err != nil {
					slog.Default().Error("billing reconcile run finalize failed", "op", "billing.reconcile.finish", "error", err, "run_id", runID, "status", "success")
				}
			}
		}
	}()
}

// corsMiddleware adds CORS headers for requests from the configured app origin.
// It handles preflight OPTIONS requests and allows credentials.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigin, ok := s.allowedCORSOrigin(origin); ok {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ListenAndServe() error {
	server := &http.Server{
		Addr:              s.cfg.Address,
		Handler:           s.Handler(),
		ReadTimeout:       s.cfg.HTTPTimeouts.ReadTimeout,
		WriteTimeout:      s.cfg.HTTPTimeouts.WriteTimeout,
		IdleTimeout:       s.cfg.HTTPTimeouts.IdleTimeout,
		ReadHeaderTimeout: s.cfg.HTTPTimeouts.ReadHeaderTimeout,
		BaseContext: func(net.Listener) context.Context {
			return context.Background()
		},
	}
	return server.ListenAndServe()
}
