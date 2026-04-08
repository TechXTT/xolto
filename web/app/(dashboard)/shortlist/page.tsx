"use client";

import { useState } from "react";

import { useDashboardContext } from "../../../components/DashboardContext";
import { ShortlistTable } from "../../../components/ShortlistTable";
import { api } from "../../../lib/api";
import { formatEuroFromCents } from "../../../lib/format";

export default function ShortlistPage() {
  const { shortlist, removeFromShortlist } = useDashboardContext();
  const [error, setError] = useState("");
  const [draftStates, setDraftStates] = useState<Record<string, { loading: boolean; text: string | null }>>({});

  const totalOpportunity = shortlist.reduce((sum, item) => {
    const savings = item.FairPrice > 0 && item.AskPrice > 0 ? item.FairPrice - item.AskPrice : 0;
    return sum + Math.max(0, savings);
  }, 0);

  const buyNowCount = shortlist.filter((item) => item.RecommendationLabel === "buy_now").length;

  async function draftOffer(itemID: string) {
    setDraftStates((prev) => ({ ...prev, [itemID]: { loading: true, text: prev[itemID]?.text ?? null } }));
    setError("");
    try {
      const res = await api.shortlist.draftOffer(itemID);
      setDraftStates((prev) => ({ ...prev, [itemID]: { loading: false, text: res.Content || "" } }));
    } catch (err) {
      setDraftStates((prev) => ({ ...prev, [itemID]: { loading: false, text: prev[itemID]?.text ?? null } }));
      setError(err instanceof Error ? err.message : "Failed to draft offer");
    }
  }

  return (
    <div className="page-stack">
      <section className="hero-panel compact">
        <div>
          <p className="section-kicker">Deal room</p>
          <h2>Deals worth acting on</h2>
          <p className="hero-copy">
            Every listing here has been AI-scored and analysed. Compare fair values, read the AI verdict, and act on the ones that clear your bar.
          </p>
        </div>
        <div className="stats-row">
          <div className="stat-card">
            <span className="metric-label">Saved deals</span>
            <strong>{shortlist.length}</strong>
          </div>
          {buyNowCount > 0 && (
            <div className="stat-card live">
              <span className="metric-label">Buy now</span>
              <strong>{buyNowCount}</strong>
            </div>
          )}
          {totalOpportunity > 0 && (
            <div className="stat-card">
              <span className="metric-label">Total opportunity</span>
              <strong>{formatEuroFromCents(totalOpportunity)}</strong>
            </div>
          )}
        </div>
      </section>

      {error && <div className="error-msg">{error}</div>}

      <ShortlistTable
        items={shortlist}
        draftStates={draftStates}
        onDraftOffer={draftOffer}
        onRemove={async (itemID) => {
          try {
            await removeFromShortlist(itemID);
          } catch (err) {
            setError(err instanceof Error ? err.message : "Failed to remove item");
          }
        }}
      />
    </div>
  );
}
