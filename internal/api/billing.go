package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TechXTT/xolto/internal/models"
	"github.com/stripe/stripe-go/v81"
	portalsession "github.com/stripe/stripe-go/v81/billingportal/session"
	"github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/customer"
	stripeinvoice "github.com/stripe/stripe-go/v81/invoice"
	"github.com/stripe/stripe-go/v81/subscription"
	"github.com/stripe/stripe-go/v81/webhook"
)

func (s *Server) registerBillingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/billing/checkout", s.requireAuth(s.handleBillingCheckout))
	mux.HandleFunc("/billing/portal", s.requireAuth(s.handleBillingPortal))
	mux.HandleFunc("/billing/webhook", s.handleBillingWebhook)
	mux.HandleFunc("/admin/business/overview", s.requireOperatorOrOwner(s.handleBusinessOverview))
	mux.HandleFunc("/admin/business/subscriptions", s.requireOperatorOrOwner(s.handleBusinessSubscriptions))
	mux.HandleFunc("/admin/business/revenue", s.requireOperatorOrOwner(s.handleBusinessRevenue))
	mux.HandleFunc("/admin/business/funnel", s.requireOperatorOrOwner(s.handleBusinessFunnel))
	mux.HandleFunc("/admin/business/cohorts", s.requireOperatorOrOwner(s.handleBusinessCohorts))
	mux.HandleFunc("/admin/business/alerts", s.requireOperatorOrOwner(s.handleBusinessAlerts))
	mux.HandleFunc("/admin/business/subscriptions/", s.requireOwner(s.handleBusinessSubscriptionMutation))
	mux.HandleFunc("/admin/business/reconcile", s.requireOwner(s.handleBusinessReconcile))
}

func (s *Server) handleBillingCheckout(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if s.cfg.StripeSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe is not configured")
		return
	}
	var req struct {
		PriceID string `json:"price_id" validate:"required"`
	}
	if err := Decode(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	priceID := strings.TrimSpace(req.PriceID)
	if priceID == "" {
		writeError(w, http.StatusBadRequest, "price_id is required")
		return
	}
	tier, ok := s.subscriptionTier(priceID)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown price_id")
		return
	}
	stripe.Key = s.cfg.StripeSecret

	customerID := strings.TrimSpace(user.StripeCustomer)
	if customerID == "" {
		cust, err := customer.New(&stripe.CustomerParams{
			Email: stripe.String(user.Email),
			Name:  stripe.String(user.Name),
			Metadata: map[string]string{
				"user_id": user.ID,
				"tier":    tier,
			},
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		customerID = cust.ID
		if err := s.db.UpdateStripeCustomer(user.ID, customerID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	successURL := strings.TrimRight(s.cfg.AppBaseURL, "/") + "/settings?checkout=success"
	cancelURL := strings.TrimRight(s.cfg.AppBaseURL, "/") + "/settings?checkout=cancelled"
	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		SuccessURL: stripe.String(successURL),
		CancelURL:  stripe.String(cancelURL),
		Customer:   stripe.String(customerID),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		Metadata: map[string]string{
			"user_id": user.ID,
			"tier":    tier,
		},
	}
	sess, err := session.New(params)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": sess.URL, "id": sess.ID})
}

func (s *Server) handleBillingPortal(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if s.cfg.StripeSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe is not configured")
		return
	}
	stripe.Key = s.cfg.StripeSecret
	customerID := strings.TrimSpace(user.StripeCustomer)
	if customerID == "" {
		writeError(w, http.StatusBadRequest, "no billing account found")
		return
	}
	returnURL := strings.TrimRight(s.cfg.AppBaseURL, "/") + "/settings"
	sess, err := portalsession.New(&stripe.BillingPortalSessionParams{
		Customer:  stripe.String(customerID),
		ReturnURL: stripe.String(returnURL),
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": sess.URL})
}

func (s *Server) handleBillingWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var event stripe.Event
	if s.cfg.StripeWebhookSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe webhook not configured")
		return
	}
	signature := r.Header.Get("Stripe-Signature")
	event, err = webhook.ConstructEventWithOptions(body, signature, s.cfg.StripeWebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid stripe webhook payload")
		return
	}
	webhookEntry := models.StripeWebhookEventLog{
		EventID:      event.ID,
		EventType:    string(event.Type),
		ObjectID:     "",
		RequestID:    requestIDFromRequest(r),
		Status:       "received",
		ReceivedAt:   time.Now().UTC(),
		AttemptCount: 1,
		PayloadJSON:  string(body),
	}
	if err := s.db.UpsertStripeWebhookEvent(webhookEntry); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if event.ID != "" {
		if err := s.db.RecordStripeEvent(event.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	var processingErr error
	switch event.Type {
	case "checkout.session.completed":
		var checkoutSession stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &checkoutSession); err == nil {
			customerID := ""
			if checkoutSession.Customer != nil {
				customerID = checkoutSession.Customer.ID
			}
			tier, ok := s.subscriptionTierFromMetadata(checkoutSession.Metadata)
			if ok && customerID != "" {
				_ = s.db.UpdateUserTierByStripeCustomer(customerID, tier)
			}
		}
	case "customer.subscription.created", "customer.subscription.updated":
		var sub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &sub); err == nil {
			priceID := ""
			if len(sub.Items.Data) > 0 && sub.Items.Data[0].Price != nil {
				priceID = sub.Items.Data[0].Price.ID
			}
			tier, ok := s.subscriptionTier(priceID)
			customerID := ""
			if sub.Customer != nil {
				customerID = sub.Customer.ID
			}
			if ok && customerID != "" {
				_ = s.db.UpdateUserTierByStripeCustomer(customerID, tier)
			}
		}
		if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
			processingErr = err
		} else if err := s.persistStripeSubscriptionEvent(event.ID, string(event.Type), &sub); err != nil {
			processingErr = err
		}
	case "customer.subscription.deleted":
		var sub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &sub); err == nil && sub.Customer != nil && sub.Customer.ID != "" {
			_ = s.db.UpdateUserTierByStripeCustomer(sub.Customer.ID, "free")
		}
		if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
			processingErr = err
		} else if err := s.persistStripeSubscriptionEvent(event.ID, string(event.Type), &sub); err != nil {
			processingErr = err
		}
	case "invoice.payment_succeeded", "invoice.payment_failed", "invoice.finalized", "invoice.paid", "invoice.updated":
		var inv stripe.Invoice
		if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
			processingErr = err
		} else if err := s.persistStripeInvoiceEvent(event.ID, string(event.Type), &inv); err != nil {
			processingErr = err
		}
	}

	webhookEntry.Status = "processed"
	webhookEntry.ProcessedAt = time.Now().UTC()
	webhookEntry.AttemptCount = 2
	if processingErr != nil {
		webhookEntry.Status = "failed"
		webhookEntry.ErrorMessage = processingErr.Error()
	}
	_ = s.db.UpsertStripeWebhookEvent(webhookEntry)

	if processingErr != nil {
		writeError(w, http.StatusInternalServerError, processingErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- Admin endpoints ---

func (s *Server) handleBusinessOverview(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	overview, err := s.db.GetBusinessOverview(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"overview": overview,
		"days":     days,
	})
}

func (s *Server) handleBusinessSubscriptions(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	filter := models.BusinessSubscriptionFilter{
		Limit:       limit,
		Status:      strings.TrimSpace(r.URL.Query().Get("status")),
		PlanPriceID: strings.TrimSpace(r.URL.Query().Get("plan")),
		UserID:      strings.TrimSpace(r.URL.Query().Get("user")),
		CountryCode: strings.TrimSpace(r.URL.Query().Get("country")),
	}
	subscriptions, err := s.db.ListBusinessSubscriptions(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"subscriptions": subscriptions,
		"limit":         limit,
		"filters": map[string]any{
			"status": filter.Status,
			"plan":   filter.PlanPriceID,
			"user":   filter.UserID,
			"country": strings.ToUpper(
				strings.TrimSpace(filter.CountryCode),
			),
		},
	})
}

func (s *Server) handleBusinessRevenue(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	points, err := s.db.GetBusinessRevenue(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"points": points,
		"days":   days,
	})
}

func (s *Server) handleBusinessFunnel(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	days := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	funnel, err := s.db.GetBusinessFunnel(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"funnel": funnel,
		"days":   days,
	})
}

func (s *Server) handleBusinessCohorts(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	months := 6
	if raw := strings.TrimSpace(r.URL.Query().Get("months")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 24 {
			months = parsed
		}
	}
	cohorts, err := s.db.GetBusinessCohorts(months)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"cohorts": cohorts,
		"months":  months,
	})
}

func (s *Server) handleBusinessAlerts(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	days := 7
	if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 90 {
			days = parsed
		}
	}
	alerts, err := s.db.GetBusinessAlerts(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminOK(w, http.StatusOK, map[string]any{
		"alerts": alerts,
		"days":   days,
	})
}

func (s *Server) handleBusinessSubscriptionMutation(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if strings.TrimSpace(s.cfg.StripeSecret) == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe is not configured")
		return
	}
	rawPath := strings.Trim(strings.TrimPrefix(r.URL.Path, "/admin/business/subscriptions/"), "/")
	parts := strings.Split(rawPath, "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "unknown business subscription action")
		return
	}
	subscriptionID := strings.TrimSpace(parts[0])
	action := strings.TrimSpace(parts[1])
	if subscriptionID == "" || action == "" {
		writeError(w, http.StatusBadRequest, "invalid subscription action path")
		return
	}

	before, err := s.db.GetStripeSubscriptionSnapshot(subscriptionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	idempotencyKey := s.ownerIdempotencyKey(r, action, subscriptionID)

	var updatedSub *stripe.Subscription
	switch action {
	case "plan":
		var req struct {
			PriceID           string `json:"price_id" validate:"required"`
			ProrationBehavior string `json:"proration_behavior" validate:"omitempty,min=1,max=64"`
		}
		if err := Decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		priceID := strings.TrimSpace(req.PriceID)
		if priceID == "" {
			writeError(w, http.StatusBadRequest, "price_id is required")
			return
		}
		if _, ok := s.subscriptionTier(priceID); !ok {
			writeError(w, http.StatusBadRequest, "unknown price_id")
			return
		}
		current, err := subscription.Get(subscriptionID, &stripe.SubscriptionParams{})
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if current == nil || current.Items == nil || len(current.Items.Data) == 0 {
			writeError(w, http.StatusBadRequest, "subscription has no mutable items")
			return
		}
		item := current.Items.Data[0]
		qty := int64(1)
		if item.Quantity > 0 {
			qty = item.Quantity
		}
		prorationBehavior := strings.TrimSpace(req.ProrationBehavior)
		if prorationBehavior == "" {
			prorationBehavior = "create_prorations"
		}
		params := &stripe.SubscriptionParams{
			Items: []*stripe.SubscriptionItemsParams{
				{
					ID:       stripe.String(item.ID),
					Price:    stripe.String(priceID),
					Quantity: stripe.Int64(qty),
				},
			},
			ProrationBehavior: stripe.String(prorationBehavior),
		}
		params.SetIdempotencyKey(idempotencyKey)
		params.AddExpand("latest_invoice")
		updatedSub, err = subscription.Update(subscriptionID, params)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "cancel":
		params := &stripe.SubscriptionParams{
			CancelAtPeriodEnd: stripe.Bool(true),
		}
		params.SetIdempotencyKey(idempotencyKey)
		params.AddExpand("latest_invoice")
		updatedSub, err = subscription.Update(subscriptionID, params)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "resume":
		params := &stripe.SubscriptionParams{
			CancelAtPeriodEnd: stripe.Bool(false),
		}
		params.AddExtra("pause_collection", "")
		params.SetIdempotencyKey(idempotencyKey)
		params.AddExpand("latest_invoice")
		updatedSub, err = subscription.Update(subscriptionID, params)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "pause":
		var req struct {
			Behavior string `json:"behavior" validate:"omitempty,min=1,max=64"`
		}
		if err := Decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		behavior := strings.TrimSpace(req.Behavior)
		if behavior == "" {
			behavior = string(stripe.SubscriptionPauseCollectionBehaviorMarkUncollectible)
		}
		params := &stripe.SubscriptionParams{
			PauseCollection: &stripe.SubscriptionPauseCollectionParams{
				Behavior: stripe.String(behavior),
			},
		}
		params.SetIdempotencyKey(idempotencyKey)
		params.AddExpand("latest_invoice")
		updatedSub, err = subscription.Update(subscriptionID, params)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "sync":
		updatedSub, err = s.syncStripeSubscription(subscriptionID, idempotencyKey)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeError(w, http.StatusNotFound, "unknown business subscription action")
		return
	}

	if updatedSub != nil {
		if err := s.persistStripeSubscriptionEvent("owner_mutation", "owner.subscription."+action, updatedSub); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if updatedSub.LatestInvoice != nil && updatedSub.LatestInvoice.ID != "" {
			_ = s.persistStripeInvoiceEvent("owner_mutation", "owner.subscription."+action, updatedSub.LatestInvoice)
		}
	}
	after, err := s.db.GetStripeSubscriptionSnapshot(subscriptionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	_ = s.db.RecordStripeMutation(models.StripeMutationLog{
		IdempotencyKey: idempotencyKey,
		ActorUserID:    user.ID,
		ActorRole:      models.EffectiveUserRole(*user),
		Action:         "owner_subscription_" + action,
		TargetID:       subscriptionID,
		RequestJSON:    mustJSON(map[string]any{"path": rawPath}),
		ResponseJSON:   mustJSON(map[string]any{"subscription_id": subscriptionID}),
		Status:         "ok",
	})

	_ = s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
		ActorUserID: user.ID,
		ActorRole:   models.EffectiveUserRole(*user),
		RequestID:   requestIDFromRequest(r),
		Action:      "owner_subscription_" + action,
		TargetType:  "subscription",
		TargetID:    subscriptionID,
		BeforeJSON:  mustJSON(before),
		AfterJSON:   mustJSON(after),
	})

	writeAdminOK(w, http.StatusOK, map[string]any{
		"subscription":    after,
		"idempotency_key": idempotencyKey,
	})
}

func (s *Server) handleBusinessReconcile(w http.ResponseWriter, r *http.Request, user *models.User) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	if strings.TrimSpace(s.cfg.StripeSecret) == "" {
		writeError(w, http.StatusServiceUnavailable, "stripe is not configured")
		return
	}

	runID, err := s.db.StartBillingReconcileRun(models.BillingReconcileRun{
		TriggeredBy: user.ID,
		Status:      "running",
		StartedAt:   time.Now().UTC(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	summary, reconcileErr := s.runStripeReconcile(r.Context())
	if reconcileErr != nil {
		_ = s.db.FinishBillingReconcileRun(runID, "failed", mustJSON(summary), mustJSON(map[string]any{"error": reconcileErr.Error()}))
		writeError(w, http.StatusBadGateway, reconcileErr.Error())
		return
	}
	_ = s.db.FinishBillingReconcileRun(runID, "success", mustJSON(summary), "")
	_ = s.db.RecordAdminAuditLog(models.AdminAuditLogEntry{
		ActorUserID: user.ID,
		ActorRole:   models.EffectiveUserRole(*user),
		RequestID:   requestIDFromRequest(r),
		Action:      "owner_reconcile_triggered",
		TargetType:  "billing",
		TargetID:    strconv.FormatInt(runID, 10),
		BeforeJSON:  "{}",
		AfterJSON:   mustJSON(summary),
	})

	writeAdminOK(w, http.StatusOK, map[string]any{
		"run_id":  runID,
		"summary": summary,
	})
}

func (s *Server) runStripeReconcile(ctx context.Context) (map[string]any, error) {
	stripe.Key = s.cfg.StripeSecret
	users, err := s.db.ListUsersWithStripeCustomerIDs()
	if err != nil {
		return nil, err
	}
	summary := map[string]any{
		"customers":      0,
		"subscriptions":  0,
		"invoices":       0,
		"failed_entries": 0,
	}
	customerCount := 0
	subCount := 0
	invoiceCount := 0
	failed := 0

	for _, user := range users {
		select {
		case <-ctx.Done():
			return summary, ctx.Err()
		default:
		}
		customerID := strings.TrimSpace(user.StripeCustomer)
		if customerID == "" {
			continue
		}
		customerCount++

		subParams := &stripe.SubscriptionListParams{
			Customer: stripe.String(customerID),
			Status:   stripe.String("all"),
		}
		subParams.Limit = stripe.Int64(100)
		subParams.AddExpand("data.latest_invoice")
		subIter := subscription.List(subParams)
		for subIter.Next() {
			sub := subIter.Subscription()
			if sub == nil {
				continue
			}
			if err := s.persistStripeSubscriptionEvent("reconcile", "reconcile.subscription", sub); err != nil {
				failed++
				continue
			}
			subCount++
			if sub.LatestInvoice != nil && sub.LatestInvoice.ID != "" {
				if err := s.persistStripeInvoiceEvent("reconcile", "reconcile.invoice", sub.LatestInvoice); err == nil {
					invoiceCount++
				} else {
					failed++
				}
			}
		}
		if err := subIter.Err(); err != nil {
			return nil, err
		}

		invParams := &stripe.InvoiceListParams{
			Customer: stripe.String(customerID),
		}
		invParams.Limit = stripe.Int64(100)
		invIter := stripeinvoice.List(invParams)
		for invIter.Next() {
			inv := invIter.Invoice()
			if inv == nil {
				continue
			}
			if err := s.persistStripeInvoiceEvent("reconcile", "reconcile.invoice", inv); err != nil {
				failed++
				continue
			}
			invoiceCount++
		}
		if err := invIter.Err(); err != nil {
			return nil, err
		}
	}

	summary["customers"] = customerCount
	summary["subscriptions"] = subCount
	summary["invoices"] = invoiceCount
	summary["failed_entries"] = failed
	summary["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	return summary, nil
}

func (s *Server) syncStripeSubscription(subscriptionID, idempotencyKey string) (*stripe.Subscription, error) {
	stripe.Key = s.cfg.StripeSecret
	params := &stripe.SubscriptionParams{}
	params.AddExpand("latest_invoice")
	if strings.TrimSpace(idempotencyKey) != "" {
		params.SetIdempotencyKey(idempotencyKey)
	}
	return subscription.Get(subscriptionID, params)
}

func (s *Server) persistStripeSubscriptionEvent(eventID, eventType string, sub *stripe.Subscription) error {
	if sub == nil {
		return errors.New("stripe subscription payload is nil")
	}
	customerID := ""
	if sub.Customer != nil {
		customerID = strings.TrimSpace(sub.Customer.ID)
	}
	userID, err := s.lookupUserIDByCustomer(customerID)
	if err != nil {
		return err
	}
	snapshot := stripeSubscriptionSnapshotFromStripe(sub, userID)
	if err := s.db.UpsertStripeSubscriptionSnapshot(snapshot); err != nil {
		return err
	}
	_ = s.db.AppendStripeSubscriptionHistory(models.StripeSubscriptionHistoryEntry{
		SubscriptionID: snapshot.SubscriptionID,
		EventID:        strings.TrimSpace(eventID),
		EventType:      strings.TrimSpace(eventType),
		Status:         snapshot.Status,
		PlanPriceID:    snapshot.PlanPriceID,
		Currency:       snapshot.Currency,
		UnitAmount:     snapshot.UnitAmount,
		Quantity:       snapshot.Quantity,
		PeriodStart:    snapshot.CurrentPeriodStart,
		PeriodEnd:      snapshot.CurrentPeriodEnd,
		CancelAtEnd:    snapshot.CancelAtPeriodEnd,
		RawJSON:        snapshot.RawJSON,
	})

	if tier, ok := s.subscriptionTier(snapshot.PlanPriceID); ok && customerID != "" && subscriptionDrivesPaidTier(snapshot.Status) {
		_ = s.db.UpdateUserTierByStripeCustomer(customerID, tier)
	}
	if customerID != "" && subscriptionIsEnded(snapshot.Status) {
		_ = s.db.UpdateUserTierByStripeCustomer(customerID, "free")
	}
	return nil
}

func (s *Server) persistStripeInvoiceEvent(eventID, eventType string, inv *stripe.Invoice) error {
	if inv == nil {
		return errors.New("stripe invoice payload is nil")
	}
	customerID := ""
	if inv.Customer != nil {
		customerID = strings.TrimSpace(inv.Customer.ID)
	}
	userID, err := s.lookupUserIDByCustomer(customerID)
	if err != nil {
		return err
	}
	summary := stripeInvoiceSummaryFromStripe(inv, userID)
	return s.db.UpsertStripeInvoiceSummary(summary)
}

func (s *Server) lookupUserIDByCustomer(customerID string) (string, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return "", nil
	}
	users, err := s.db.ListUsersWithStripeCustomerIDs()
	if err != nil {
		return "", err
	}
	for _, user := range users {
		if strings.TrimSpace(user.StripeCustomer) == customerID {
			return user.ID, nil
		}
	}
	return "", nil
}

func stripeSubscriptionSnapshotFromStripe(sub *stripe.Subscription, userID string) models.StripeSubscriptionSnapshot {
	priceID := ""
	interval := ""
	unitAmount := int64(0)
	quantity := int64(1)
	if sub != nil && sub.Items != nil && len(sub.Items.Data) > 0 {
		item := sub.Items.Data[0]
		if item != nil {
			if item.Price != nil {
				priceID = strings.TrimSpace(item.Price.ID)
				if item.Price.Recurring != nil {
					interval = string(item.Price.Recurring.Interval)
				}
				unitAmount = item.Price.UnitAmount
			}
			if item.Quantity > 0 {
				quantity = item.Quantity
			}
		}
	}
	customerID := ""
	if sub.Customer != nil {
		customerID = strings.TrimSpace(sub.Customer.ID)
	}
	latestInvoiceID := ""
	if sub.LatestInvoice != nil {
		latestInvoiceID = strings.TrimSpace(sub.LatestInvoice.ID)
	}
	defaultPaymentMethod := ""
	if sub.DefaultPaymentMethod != nil {
		defaultPaymentMethod = strings.TrimSpace(sub.DefaultPaymentMethod.ID)
	}
	raw, _ := json.Marshal(sub)
	return models.StripeSubscriptionSnapshot{
		SubscriptionID:       strings.TrimSpace(sub.ID),
		CustomerID:           customerID,
		UserID:               strings.TrimSpace(userID),
		Status:               string(sub.Status),
		PlanPriceID:          priceID,
		PlanInterval:         interval,
		Currency:             strings.ToUpper(string(sub.Currency)),
		UnitAmount:           unitAmount,
		Quantity:             quantity,
		CurrentPeriodStart:   unixToTime(sub.CurrentPeriodStart),
		CurrentPeriodEnd:     unixToTime(sub.CurrentPeriodEnd),
		CancelAtPeriodEnd:    sub.CancelAtPeriodEnd,
		CanceledAt:           unixToTime(sub.CanceledAt),
		Paused:               sub.PauseCollection != nil || string(sub.Status) == "paused",
		LatestInvoiceID:      latestInvoiceID,
		DefaultPaymentMethod: defaultPaymentMethod,
		RawJSON:              string(raw),
	}
}

func stripeInvoiceSummaryFromStripe(inv *stripe.Invoice, userID string) models.StripeInvoiceSummary {
	customerID := ""
	if inv.Customer != nil {
		customerID = strings.TrimSpace(inv.Customer.ID)
	}
	subscriptionID := ""
	if inv.Subscription != nil {
		subscriptionID = strings.TrimSpace(inv.Subscription.ID)
	}
	finalizedAt := int64(0)
	if inv.StatusTransitions != nil {
		finalizedAt = inv.StatusTransitions.FinalizedAt
	}
	raw, _ := json.Marshal(inv)
	return models.StripeInvoiceSummary{
		InvoiceID:        strings.TrimSpace(inv.ID),
		SubscriptionID:   subscriptionID,
		CustomerID:       customerID,
		UserID:           strings.TrimSpace(userID),
		Status:           string(inv.Status),
		Currency:         strings.ToUpper(string(inv.Currency)),
		AmountDue:        inv.AmountDue,
		AmountPaid:       inv.AmountPaid,
		AmountRemaining:  inv.AmountRemaining,
		AttemptCount:     inv.AttemptCount,
		Paid:             inv.Paid,
		HostedInvoiceURL: strings.TrimSpace(inv.HostedInvoiceURL),
		InvoicePDF:       strings.TrimSpace(inv.InvoicePDF),
		PeriodStart:      unixToTime(inv.PeriodStart),
		PeriodEnd:        unixToTime(inv.PeriodEnd),
		DueDate:          unixToTime(inv.DueDate),
		FinalizedAt:      unixToTime(finalizedAt),
		RawJSON:          string(raw),
	}
}

func subscriptionDrivesPaidTier(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "trialing", "past_due", "unpaid":
		return true
	default:
		return false
	}
}

func subscriptionIsEnded(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "canceled", "incomplete_expired":
		return true
	default:
		return false
	}
}

func unixToTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func (s *Server) ownerIdempotencyKey(r *http.Request, action, targetID string) string {
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		return key
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("owner:%s:%s:%d", strings.TrimSpace(action), strings.TrimSpace(targetID), time.Now().UnixNano())
	}
	return fmt.Sprintf("owner:%s:%s:%x", strings.TrimSpace(action), strings.TrimSpace(targetID), buf)
}
