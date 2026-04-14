ALTER TABLE users
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT '';

UPDATE users
SET role = 'admin'
WHERE is_admin = TRUE
  AND COALESCE(role, '') = '';

UPDATE users
SET role = 'user'
WHERE is_admin = FALSE
  AND COALESCE(role, '') = '';

ALTER TABLE admin_audit_log
    ADD COLUMN IF NOT EXISTS actor_role TEXT NOT NULL DEFAULT '';

ALTER TABLE admin_audit_log
    ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS stripe_webhook_events (
    id BIGSERIAL PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    event_type TEXT NOT NULL DEFAULT '',
    object_id TEXT NOT NULL DEFAULT '',
    api_account TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'received',
    error_message TEXT NOT NULL DEFAULT '',
    attempt_count INTEGER NOT NULL DEFAULT 1,
    payload_json TEXT NOT NULL DEFAULT '',
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_received
    ON stripe_webhook_events(received_at DESC);

CREATE INDEX IF NOT EXISTS idx_stripe_webhook_events_status
    ON stripe_webhook_events(status, received_at DESC);

CREATE TABLE IF NOT EXISTS stripe_subscription_snapshots (
    subscription_id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    plan_price_id TEXT NOT NULL DEFAULT '',
    plan_interval TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    unit_amount BIGINT NOT NULL DEFAULT 0,
    quantity BIGINT NOT NULL DEFAULT 0,
    current_period_start TIMESTAMPTZ NULL,
    current_period_end TIMESTAMPTZ NULL,
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
    canceled_at TIMESTAMPTZ NULL,
    paused BOOLEAN NOT NULL DEFAULT FALSE,
    latest_invoice_id TEXT NOT NULL DEFAULT '',
    default_payment_method TEXT NOT NULL DEFAULT '',
    raw_json TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_customer
    ON stripe_subscription_snapshots(customer_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_status
    ON stripe_subscription_snapshots(status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_stripe_subscription_snapshots_user
    ON stripe_subscription_snapshots(user_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS stripe_subscription_history (
    id BIGSERIAL PRIMARY KEY,
    subscription_id TEXT NOT NULL DEFAULT '',
    event_id TEXT NOT NULL DEFAULT '',
    event_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    plan_price_id TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    unit_amount BIGINT NOT NULL DEFAULT 0,
    quantity BIGINT NOT NULL DEFAULT 0,
    period_start TIMESTAMPTZ NULL,
    period_end TIMESTAMPTZ NULL,
    cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
    raw_json TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stripe_subscription_history_sub
    ON stripe_subscription_history(subscription_id, created_at DESC);

CREATE TABLE IF NOT EXISTS stripe_invoice_summaries (
    invoice_id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL DEFAULT '',
    customer_id TEXT NOT NULL DEFAULT '',
    user_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    currency TEXT NOT NULL DEFAULT '',
    amount_due BIGINT NOT NULL DEFAULT 0,
    amount_paid BIGINT NOT NULL DEFAULT 0,
    amount_remaining BIGINT NOT NULL DEFAULT 0,
    attempt_count BIGINT NOT NULL DEFAULT 0,
    paid BOOLEAN NOT NULL DEFAULT FALSE,
    hosted_invoice_url TEXT NOT NULL DEFAULT '',
    invoice_pdf TEXT NOT NULL DEFAULT '',
    period_start TIMESTAMPTZ NULL,
    period_end TIMESTAMPTZ NULL,
    due_date TIMESTAMPTZ NULL,
    finalized_at TIMESTAMPTZ NULL,
    raw_json TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_customer
    ON stripe_invoice_summaries(customer_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_status
    ON stripe_invoice_summaries(status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_stripe_invoice_summaries_subscription
    ON stripe_invoice_summaries(subscription_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS stripe_mutation_log (
    idempotency_key TEXT PRIMARY KEY,
    actor_user_id TEXT NOT NULL DEFAULT '',
    actor_role TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL DEFAULT '',
    target_id TEXT NOT NULL DEFAULT '',
    request_json TEXT NOT NULL DEFAULT '',
    response_json TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_stripe_mutation_log_actor
    ON stripe_mutation_log(actor_user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS billing_reconcile_runs (
    id BIGSERIAL PRIMARY KEY,
    triggered_by TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT '',
    summary_json TEXT NOT NULL DEFAULT '',
    error_json TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_billing_reconcile_runs_started
    ON billing_reconcile_runs(started_at DESC);
