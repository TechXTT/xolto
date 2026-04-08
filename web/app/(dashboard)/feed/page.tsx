"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";

import { useDashboardContext } from "../../../components/DashboardContext";
import { ListingCard } from "../../../components/ListingCard";
import { api, Listing } from "../../../lib/api";
import { connectDealStream } from "../../../lib/sse";

type SortKey = "score" | "price_asc" | "price_desc" | "newest";
type MarketplaceFilter = "all" | "marktplaats" | "vinted" | "olxbg";
type ConditionFilter = "all" | "new" | "like_new" | "good" | "fair";

const MARKETPLACE_LABELS: Record<string, string> = {
  all: "All markets",
  marktplaats: "Marktplaats",
  vinted: "Vinted",
  olxbg: "OLX BG",
};

const CONDITION_LABELS: Record<string, string> = {
  all: "Any condition",
  new: "New",
  like_new: "Like new",
  good: "Good",
  fair: "Fair",
};

const SORT_LABELS: Record<SortKey, string> = {
  score: "Best score",
  price_asc: "Price: low → high",
  price_desc: "Price: high → low",
  newest: "Newest first",
};

const MIN_SCORE_OPTIONS = [
  { value: 0, label: "Any score" },
  { value: 6, label: "Score ≥ 6" },
  { value: 7, label: "Score ≥ 7" },
  { value: 8, label: "Score ≥ 8" },
  { value: 9, label: "Score ≥ 9" },
];

export default function FeedPage() {
  const [listings, setListings] = useState<Listing[]>([]);
  const [error, setError] = useState("");
  const [newCount, setNewCount] = useState(0);
  const { shortlist, addToShortlist, isShortlisted } = useDashboardContext();

  // Filter / sort state
  const [sort, setSort] = useState<SortKey>("score");
  const [marketplace, setMarketplace] = useState<MarketplaceFilter>("all");
  const [condition, setCondition] = useState<ConditionFilter>("all");
  const [minScore, setMinScore] = useState(0);

  useEffect(() => {
    let disconnect: (() => void) | undefined;

    async function load() {
      try {
        const res = await api.listings.feed();
        setListings(res.listings ?? []);
        disconnect = connectDealStream((payload) => {
          if (!payload || typeof payload !== "object") return;
          const event = payload as {
            type?: string;
            deal?: {
              Listing?: Listing;
              Score?: number;
              OfferPrice?: number;
              FairPrice?: number;
              Confidence?: number;
              Reason?: string;
              RiskFlags?: string[];
            };
          };
          if (event.type === "deal_found" && event.deal?.Listing?.ItemID) {
            const listing: Listing = {
              ...event.deal.Listing,
              Score: event.deal.Score ?? 0,
              OfferPrice: event.deal.OfferPrice ?? 0,
              FairPrice: event.deal.FairPrice ?? 0,
              Confidence: event.deal.Confidence ?? 0,
              Reason: event.deal.Reason ?? "",
              RiskFlags: event.deal.RiskFlags ?? [],
            };
            setListings((prev) => [listing, ...prev.filter((item) => item.ItemID !== listing.ItemID)]);
            setNewCount((count) => count + 1);
          }
        });
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load feed");
      }
    }

    void load();
    return () => disconnect?.();
  }, []);

  const filtered = useMemo(() => {
    let result = listings;

    if (marketplace !== "all") {
      result = result.filter((l) => l.MarketplaceID === marketplace);
    }

    if (condition !== "all") {
      result = result.filter((l) => l.Condition === condition);
    }

    if (minScore > 0) {
      result = result.filter((l) => (l.Score ?? 0) >= minScore);
    }

    return [...result].sort((a, b) => {
      switch (sort) {
        case "score":
          return (b.Score ?? 0) - (a.Score ?? 0);
        case "price_asc":
          return (a.Price ?? 0) - (b.Price ?? 0);
        case "price_desc":
          return (b.Price ?? 0) - (a.Price ?? 0);
        case "newest":
          return 0; // arrival order preserved from SSE / DB newest-first
      }
    });
  }, [listings, sort, marketplace, condition, minScore]);

  const hasActiveFilters = marketplace !== "all" || condition !== "all" || minScore > 0 || sort !== "score";

  function resetFilters() {
    setSort("score");
    setMarketplace("all");
    setCondition("all");
    setMinScore(0);
  }

  return (
    <div className="page-stack">
      <section className="hero-panel compact">
        <div>
          <p className="section-kicker">Deal radar</p>
          <h2>AI-surfaced deals, live</h2>
          <p className="hero-copy">
            Every listing here was found, fetched, and scored by MarktBot. Deals are ranked by how far below fair market value the asking price sits.
          </p>
        </div>
        <div className="stats-row">
          <div className="stat-card">
            <span className="metric-label">Deals found</span>
            <strong>{listings.length}</strong>
          </div>
          <div className="stat-card">
            <span className="metric-label">Showing</span>
            <strong>{filtered.length}</strong>
          </div>
          <div className="stat-card">
            <span className="metric-label">Shortlisted</span>
            <strong>{shortlist.length}</strong>
          </div>
          <div className="stat-card live">
            <span className="metric-label">New since open</span>
            <strong>{newCount > 0 ? newCount : "—"}</strong>
          </div>
        </div>
      </section>

      {error && <div className="error-msg">{error}</div>}

      {listings.length > 0 && (
        <div className="feed-filter-bar">
          <div className="feed-filter-group">
            <label className="feed-filter-label">Sort</label>
            <div className="feed-pill-group">
              {(Object.keys(SORT_LABELS) as SortKey[]).map((key) => (
                <button
                  key={key}
                  type="button"
                  className={`feed-pill${sort === key ? " active" : ""}`}
                  onClick={() => setSort(key)}
                >
                  {SORT_LABELS[key]}
                </button>
              ))}
            </div>
          </div>

          <div className="feed-filter-group">
            <label className="feed-filter-label">Market</label>
            <div className="feed-pill-group">
              {(Object.keys(MARKETPLACE_LABELS) as MarketplaceFilter[]).map((key) => (
                <button
                  key={key}
                  type="button"
                  className={`feed-pill${marketplace === key ? " active" : ""}`}
                  onClick={() => setMarketplace(key)}
                >
                  {MARKETPLACE_LABELS[key]}
                </button>
              ))}
            </div>
          </div>

          <div className="feed-filter-row">
            <div className="feed-filter-group">
              <label className="feed-filter-label">Condition</label>
              <div className="feed-pill-group">
                {(Object.keys(CONDITION_LABELS) as ConditionFilter[]).map((key) => (
                  <button
                    key={key}
                    type="button"
                    className={`feed-pill${condition === key ? " active" : ""}`}
                    onClick={() => setCondition(key)}
                  >
                    {CONDITION_LABELS[key]}
                  </button>
                ))}
              </div>
            </div>

            <div className="feed-filter-group">
              <label className="feed-filter-label">Min score</label>
              <div className="feed-pill-group">
                {MIN_SCORE_OPTIONS.map((opt) => (
                  <button
                    key={opt.value}
                    type="button"
                    className={`feed-pill${minScore === opt.value ? " active" : ""}`}
                    onClick={() => setMinScore(opt.value)}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>
            </div>

            {hasActiveFilters && (
              <button type="button" className="feed-reset-btn" onClick={resetFilters}>
                Reset
              </button>
            )}
          </div>
        </div>
      )}

      {listings.length === 0 && !error ? (
        <div className="surface-panel empty-state">
          <div className="empty-icon">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" style={{ color: "var(--brand-600)" }}>
              <circle cx="8" cy="8" r="4" />
              <path d="M8 4a4 4 0 0 1 4 4" opacity="0.5" />
              <path d="M8 1a7 7 0 0 1 7 7" opacity="0.3" />
            </svg>
          </div>
          <h3>No deals yet - tell the AI what you want</h3>
          <p>
            Describe what you are after in the Brief and MarktBot will activate monitors and stream scored deals here.
          </p>
          <Link href="/assistant" className="btn-primary" style={{ marginTop: 12 }}>
            Set up your brief
          </Link>
        </div>
      ) : filtered.length === 0 ? (
        <div className="surface-panel empty-state">
          <h3>No deals match these filters</h3>
          <p>Try relaxing your score threshold or selecting a different marketplace.</p>
          <button type="button" className="btn-ghost" onClick={resetFilters} style={{ marginTop: 12 }}>
            Clear filters
          </button>
        </div>
      ) : (
        <div className="listing-stack">
          {filtered.map((listing) => (
            <ListingCard
              key={listing.ItemID}
              listing={listing}
              isSaved={isShortlisted(listing.ItemID)}
              onShortlist={addToShortlist}
            />
          ))}
        </div>
      )}
    </div>
  );
}
