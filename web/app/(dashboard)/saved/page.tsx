"use client";

import { useMemo, useState } from "react";

import { useDashboardContext } from "../../../components/DashboardContext";
import { ShortlistTable } from "../../../components/ShortlistTable";
import { api } from "../../../lib/api";
import { formatEuroFromCents } from "../../../lib/format";

export default function SavedPage() {
  const { shortlist, removeFromShortlist } = useDashboardContext();
  const [error, setError] = useState("");
  const [draftStates, setDraftStates] = useState<Record<string, { loading: boolean; text: string | null }>>({});
  const [comparisonMode, setComparisonMode] = useState(false);
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);

  const selectedItems = useMemo(
    () => shortlist.filter((item) => selectedIDs.includes(item.ItemID)),
    [shortlist, selectedIDs],
  );

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

  function toggleSelect(itemID: string) {
    setSelectedIDs((prev) => {
      if (prev.includes(itemID)) return prev.filter((id) => id !== itemID);
      if (prev.length >= 4) return prev;
      return [...prev, itemID];
    });
  }

  return (
    <div className="page-stack">
      <section className="hero-panel compact">
        <div>
          <p className="section-kicker">Saved comparisons</p>
          <h2>Compare top candidates before messaging sellers</h2>
          <p className="hero-copy">
            Switch between card view and side-by-side comparison mode. Select up to 4 listings for direct evaluation.
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
          <button type="button" className="btn-secondary" onClick={() => setComparisonMode((v) => !v)}>
            {comparisonMode ? "Card view" : "Comparison view"}
          </button>
        </div>
      </section>

      {error && <div className="error-msg">{error}</div>}

      {comparisonMode && (
        <section className="surface-panel">
          <p className="section-kicker">Selected for comparison</p>
          <p className="section-support">{selectedItems.length}/4 selected</p>
        </section>
      )}

      <ShortlistTable
        items={shortlist}
        draftStates={draftStates}
        onDraftOffer={draftOffer}
        comparisonMode={comparisonMode}
        selectedIDs={selectedIDs}
        onToggleSelect={toggleSelect}
        onRemove={async (itemID) => {
          try {
            await removeFromShortlist(itemID);
            setSelectedIDs((prev) => prev.filter((id) => id !== itemID));
          } catch (err) {
            setError(err instanceof Error ? err.message : "Failed to remove item");
          }
        }}
      />
    </div>
  );
}
