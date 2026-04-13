"use client";

import { useState } from "react";

import { useDashboardContext } from "../../../components/DashboardContext";
import { api } from "../../../lib/api";

const TIER_LABELS: Record<string, string> = { free: "Free", pro: "Pro", power: "Power", team: "Power" };

export default function SettingsPage() {
  const { user } = useDashboardContext();
  const [error, setError] = useState("");

  async function handleCheckout(priceID: string) {
    if (!priceID) {
      setError("Stripe is not configured. Set NEXT_PUBLIC_STRIPE_PRO_PRICE_ID / NEXT_PUBLIC_STRIPE_TEAM_PRICE_ID in web/.env.local.");
      return;
    }

    try {
      const res = await api.billing.createCheckout(priceID);
      window.location.href = res.url;
    } catch (err) {
      setError(err instanceof Error ? err.message : "Checkout failed");
    }
  }

  async function openBillingPortal() {
    try {
      const res = await api.billing.portal();
      window.location.href = res.url;
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to open billing portal");
    }
  }

  if (!user) {
    return null;
  }

  return (
    <div className="page-stack">
      <section className="hero-panel compact">
        <div>
          <p className="section-kicker">Settings</p>
          <h2>Account and billing overview</h2>
          <p className="hero-copy">Review who is using the workspace, what plan is active, and where billing controls live.</p>
        </div>
        <div className="stat-card">
          <span className="metric-label">Current plan</span>
          <strong>{TIER_LABELS[user.tier] ?? user.tier}</strong>
        </div>
      </section>

      {error && <div className="error-msg">{error}</div>}

      <div className="settings-grid">
        <section className="surface-panel">
          <div className="profile-card">
            <div className="profile-avatar">{(user.name || user.email).slice(0, 1).toUpperCase()}</div>
            <div>
              <p className="section-kicker">Profile</p>
              <h3>{user.name || "Unnamed account"}</h3>
              <p className="section-support">{user.email}</p>
            </div>
          </div>

          <div className="settings-list">
            <div className="settings-row">
              <span>Name</span>
              <strong>{user.name || "—"}</strong>
            </div>
            <div className="settings-row">
              <span>Email</span>
              <strong>{user.email}</strong>
            </div>
            <div className="settings-row">
              <span>Plan</span>
              <strong>{TIER_LABELS[user.tier] ?? user.tier}</strong>
            </div>
          </div>
        </section>

        {user.tier === "free" ? (
          <section className="surface-panel premium-card">
            <p className="section-kicker">Upgrade</p>
            <h3>Unlock faster scans and more active hunts</h3>
            <p className="section-support">Choose the plan that fits how aggressively you want xolto to monitor the market.</p>

            <div className="plan-grid">
              <PlanCard
                name="Pro"
                price="€9"
                features={["10 active searches", "5 minute polling", "AI search generation", "Full assistant access"]}
                onUpgrade={() => void handleCheckout(process.env.NEXT_PUBLIC_STRIPE_PRO_PRICE_ID ?? "")}
              />
              <PlanCard
                name="Power"
                price="€29"
                highlight
                features={["Unlimited missions", "50 active searches", "1 minute polling", "Auto-messaging"]}
                onUpgrade={() => void handleCheckout(process.env.NEXT_PUBLIC_STRIPE_TEAM_PRICE_ID ?? "")}
              />
            </div>
          </section>
        ) : (
          <section className="surface-panel">
            <p className="section-kicker">Billing</p>
            <h3>Manage your paid workspace</h3>
            <p className="section-support">Open Stripe’s billing portal to update payment details, invoices, and subscription settings.</p>
            <button type="button" className="btn-primary" onClick={() => void openBillingPortal()}>
              Open billing portal
            </button>
          </section>
        )}
      </div>
    </div>
  );
}

function PlanCard({
  name,
  price,
  features,
  highlight = false,
  onUpgrade,
}: {
  name: string;
  price: string;
  features: string[];
  highlight?: boolean;
  onUpgrade: () => void;
}) {
  return (
    <article className={`plan-card${highlight ? " highlight" : ""}`}>
      {highlight && <span className="success-badge">Most popular</span>}
      <h4>{name}</h4>
      <p className="plan-price">
        {price}
        <span>/month</span>
      </p>
      <ul className="plan-feature-list">
        {features.map((feature) => (
          <li key={feature}>{feature}</li>
        ))}
      </ul>
      <button type="button" className={highlight ? "btn-primary" : "btn-secondary"} onClick={onUpgrade}>
        Upgrade to {name}
      </button>
    </article>
  );
}
