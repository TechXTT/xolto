package models

import "time"

type StripeSubscriptionSnapshot struct {
	SubscriptionID       string    `json:"subscription_id"`
	CustomerID           string    `json:"customer_id"`
	UserID               string    `json:"user_id"`
	Status               string    `json:"status"`
	PlanPriceID          string    `json:"plan_price_id"`
	PlanInterval         string    `json:"plan_interval"`
	Currency             string    `json:"currency"`
	UnitAmount           int64     `json:"unit_amount"`
	Quantity             int64     `json:"quantity"`
	CurrentPeriodStart   time.Time `json:"current_period_start"`
	CurrentPeriodEnd     time.Time `json:"current_period_end"`
	CancelAtPeriodEnd    bool      `json:"cancel_at_period_end"`
	CanceledAt           time.Time `json:"canceled_at"`
	Paused               bool      `json:"paused"`
	LatestInvoiceID      string    `json:"latest_invoice_id"`
	DefaultPaymentMethod string    `json:"default_payment_method"`
	RawJSON              string    `json:"raw_json"`
	UpdatedAt            time.Time `json:"updated_at"`
	CreatedAt            time.Time `json:"created_at"`
}

type StripeSubscriptionHistoryEntry struct {
	ID             int64     `json:"id"`
	SubscriptionID string    `json:"subscription_id"`
	EventID        string    `json:"event_id"`
	EventType      string    `json:"event_type"`
	Status         string    `json:"status"`
	PlanPriceID    string    `json:"plan_price_id"`
	Currency       string    `json:"currency"`
	UnitAmount     int64     `json:"unit_amount"`
	Quantity       int64     `json:"quantity"`
	PeriodStart    time.Time `json:"period_start"`
	PeriodEnd      time.Time `json:"period_end"`
	CancelAtEnd    bool      `json:"cancel_at_period_end"`
	RawJSON        string    `json:"raw_json"`
	CreatedAt      time.Time `json:"created_at"`
}

type StripeInvoiceSummary struct {
	InvoiceID        string    `json:"invoice_id"`
	SubscriptionID   string    `json:"subscription_id"`
	CustomerID       string    `json:"customer_id"`
	UserID           string    `json:"user_id"`
	Status           string    `json:"status"`
	Currency         string    `json:"currency"`
	AmountDue        int64     `json:"amount_due"`
	AmountPaid       int64     `json:"amount_paid"`
	AmountRemaining  int64     `json:"amount_remaining"`
	AttemptCount     int64     `json:"attempt_count"`
	Paid             bool      `json:"paid"`
	HostedInvoiceURL string    `json:"hosted_invoice_url"`
	InvoicePDF       string    `json:"invoice_pdf"`
	PeriodStart      time.Time `json:"period_start"`
	PeriodEnd        time.Time `json:"period_end"`
	DueDate          time.Time `json:"due_date"`
	FinalizedAt      time.Time `json:"finalized_at"`
	RawJSON          string    `json:"raw_json"`
	UpdatedAt        time.Time `json:"updated_at"`
	CreatedAt        time.Time `json:"created_at"`
}

type StripeWebhookEventLog struct {
	EventID      string    `json:"event_id"`
	EventType    string    `json:"event_type"`
	ObjectID     string    `json:"object_id"`
	APIAccount   string    `json:"api_account"`
	RequestID    string    `json:"request_id"`
	ReceivedAt   time.Time `json:"received_at"`
	ProcessedAt  time.Time `json:"processed_at"`
	Status       string    `json:"status"`
	ErrorMessage string    `json:"error_message"`
	AttemptCount int       `json:"attempt_count"`
	PayloadJSON  string    `json:"payload_json"`
}

type StripeMutationLog struct {
	IdempotencyKey string    `json:"idempotency_key"`
	ActorUserID    string    `json:"actor_user_id"`
	ActorRole      string    `json:"actor_role"`
	Action         string    `json:"action"`
	TargetID       string    `json:"target_id"`
	RequestJSON    string    `json:"request_json"`
	ResponseJSON   string    `json:"response_json"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type BillingReconcileRun struct {
	ID          int64     `json:"id"`
	TriggeredBy string    `json:"triggered_by"`
	Status      string    `json:"status"`
	SummaryJSON string    `json:"summary_json"`
	ErrorJSON   string    `json:"error_json"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
}

type BusinessOverview struct {
	WindowDays          int     `json:"window_days"`
	MRR                 float64 `json:"mrr"`
	ARR                 float64 `json:"arr"`
	ActivePaidAccounts  int     `json:"active_paid_accounts"`
	ChurnRatePct        float64 `json:"churn_rate_pct"`
	FailedPayments      int     `json:"failed_payments"`
	RevenueEUR30d       float64 `json:"revenue_eur_30d"`
	RevenueTrendPct     float64 `json:"revenue_trend_pct"`
	SubscriptionsTotal  int     `json:"subscriptions_total"`
	SubscriptionsActive int     `json:"subscriptions_active"`
	WebhookLagMinutes   int     `json:"webhook_lag_minutes"`
	ReconcileLagMinutes int     `json:"reconcile_lag_minutes"`
}

type BusinessSubscriptionFilter struct {
	Limit       int
	Status      string
	PlanPriceID string
	UserID      string
	CountryCode string
}

type BusinessSubscriptionRow struct {
	SubscriptionID     string    `json:"subscription_id"`
	CustomerID         string    `json:"customer_id"`
	UserID             string    `json:"user_id"`
	UserEmail          string    `json:"user_email"`
	UserTier           string    `json:"user_tier"`
	Status             string    `json:"status"`
	PlanPriceID        string    `json:"plan_price_id"`
	PlanInterval       string    `json:"plan_interval"`
	Currency           string    `json:"currency"`
	UnitAmount         int64     `json:"unit_amount"`
	Quantity           int64     `json:"quantity"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
	CancelAtPeriodEnd  bool      `json:"cancel_at_period_end"`
	Paused             bool      `json:"paused"`
	LatestInvoiceID    string    `json:"latest_invoice_id"`
	InvoiceStatus      string    `json:"invoice_status"`
	AmountDue          int64     `json:"amount_due"`
	AmountPaid         int64     `json:"amount_paid"`
	AmountRemaining    int64     `json:"amount_remaining"`
	AttemptCount       int64     `json:"attempt_count"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type BusinessRevenuePoint struct {
	BucketStart time.Time `json:"bucket_start"`
	Currency    string    `json:"currency"`
	AmountPaid  int64     `json:"amount_paid"`
	Invoices    int       `json:"invoices"`
}

type BusinessFunnel struct {
	WindowDays       int     `json:"window_days"`
	Signups          int     `json:"signups"`
	Activated        int     `json:"activated"`
	Paid             int     `json:"paid"`
	SignupToPaidPct  float64 `json:"signup_to_paid_pct"`
	ActivationToPaid float64 `json:"activation_to_paid_pct"`
}

type BusinessCohortRow struct {
	CohortMonth       string  `json:"cohort_month"`
	Users             int     `json:"users"`
	PaidMonth0        int     `json:"paid_month_0"`
	PaidMonth1        int     `json:"paid_month_1"`
	PaidMonth2        int     `json:"paid_month_2"`
	RetentionMonth1   float64 `json:"retention_month_1_pct"`
	RetentionMonth2   float64 `json:"retention_month_2_pct"`
	ChurnBucketEarly  int     `json:"churn_bucket_early"`
	ChurnBucketMiddle int     `json:"churn_bucket_middle"`
	ChurnBucketLate   int     `json:"churn_bucket_late"`
}

type BusinessAlert struct {
	Key         string `json:"key"`
	Severity    string `json:"severity"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Value       string `json:"value"`
	Threshold   string `json:"threshold"`
}
