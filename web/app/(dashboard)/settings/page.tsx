"use client";

import { useEffect, useState } from "react";

import { api, User } from "../../../lib/api";

const TIER_LABELS: Record<string, string> = { free: "Free", pro: "Pro", team: "Team" };

export default function SettingsPage() {
  const [user, setUser] = useState<User | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api.auth.me().then(setUser).catch((err) => {
      setError(err instanceof Error ? err.message : "Failed to load profile");
    });
  }, []);

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Settings</h1>

      {error && <p className="error-msg mb-4">{error}</p>}

      {user ? (
        <div className="space-y-6 max-w-lg">
          <div className="card p-5">
            <h2 className="text-sm font-semibold text-gray-900 mb-4">Profile</h2>
            <dl className="space-y-2 text-sm">
              <div className="flex justify-between">
                <dt className="text-gray-500">Name</dt>
                <dd className="text-gray-900 font-medium">{user.name}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-gray-500">Email</dt>
                <dd className="text-gray-900">{user.email}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-gray-500">Plan</dt>
                <dd><span className={`badge-${user.tier}`}>{TIER_LABELS[user.tier] ?? user.tier}</span></dd>
              </div>
            </dl>
          </div>

          {user.tier === "free" && (
            <div className="card p-5">
              <h2 className="text-sm font-semibold text-gray-900 mb-1">Upgrade</h2>
              <p className="text-xs text-gray-400 mb-4">
                Unlock more searches, faster intervals, and AI-powered scoring.
              </p>
              <div className="grid grid-cols-2 gap-3">
                <div className="border border-gray-200 rounded-lg p-4">
                  <p className="font-semibold text-sm text-gray-900">Pro</p>
                  <p className="text-2xl font-bold text-gray-900 mt-1">
                    €9<span className="text-sm font-normal text-gray-400">/mo</span>
                  </p>
                  <ul className="text-xs text-gray-500 mt-2 space-y-1 list-disc list-inside">
                    <li>10 searches · 5-min interval</li>
                    <li>Full AI scoring</li>
                    <li>All marketplaces</li>
                  </ul>
                  <button
                    type="button"
                    className="btn-primary w-full mt-4 text-xs"
                    onClick={() => void handleCheckout(process.env.NEXT_PUBLIC_STRIPE_PRO_PRICE_ID ?? "")}
                  >
                    Upgrade to Pro
                  </button>
                </div>
                <div className="border border-brand-200 rounded-lg p-4 bg-brand-50">
                  <p className="font-semibold text-sm text-brand-900">Team</p>
                  <p className="text-2xl font-bold text-brand-900 mt-1">
                    €29<span className="text-sm font-normal text-brand-400">/mo</span>
                  </p>
                  <ul className="text-xs text-brand-600 mt-2 space-y-1 list-disc list-inside">
                    <li>50 searches · 1-min interval</li>
                    <li>5 team members</li>
                    <li>Priority support</li>
                  </ul>
                  <button
                    type="button"
                    className="btn-primary w-full mt-4 text-xs"
                    onClick={() => void handleCheckout(process.env.NEXT_PUBLIC_STRIPE_TEAM_PRICE_ID ?? "")}
                  >
                    Upgrade to Team
                  </button>
                </div>
              </div>
            </div>
          )}
        </div>
      ) : (
        !error && <p className="text-sm text-gray-400">Loading…</p>
      )}
    </div>
  );

  async function handleCheckout(priceID: string) {
    if (!priceID) { alert("Stripe not configured (missing NEXT_PUBLIC_STRIPE_PRO_PRICE_ID)"); return; }
    try {
      const res = await api.billing.createCheckout(priceID);
      window.location.href = res.url;
    } catch (err) {
      setError(err instanceof Error ? err.message : "Checkout failed");
    }
  }
}
