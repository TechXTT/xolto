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
	"github.com/TechXTT/xolto/internal/scorer"
	"github.com/TechXTT/xolto/internal/store"
)

type SearchRunner interface {
	RunAllNow(ctx context.Context) error
	RunUserNow(ctx context.Context, userID string) error
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
	s := &Server{
		cfg:       cfg,
		db:        db,
		assistant: asst,
		broker:    broker,
		runner:    runner,
		scorer:    sc,
		fetcher:   listingfetcher.New(),
		mux:       mux,
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
