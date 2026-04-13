"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";

import { ListingCard } from "../../../components/ListingCard";
import { useDashboardContext } from "../../../components/DashboardContext";
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

const PAGE_SIZE = 20;
const MATCHES_FETCH_LIMIT = 200;

export default function MatchesPage() {
  const [listings, setListings] = useState<Listing[]>([]);
  const [error, setError] = useState("");
  const [draftStates, setDraftStates] = useState<Record<string, { loading: boolean; text: string | null }>>({});
  const [newCount, setNewCount] = useState(0);
  const { missions, activeMissionId, setActiveMission, shortlist, addToShortlist, isShortlisted, refreshMissions } = useDashboardContext();

  const [sort, setSort] = useState<SortKey>("score");
  const [marketplace, setMarketplace] = useState<MarketplaceFilter>("all");
  const [condition, setCondition] = useState<ConditionFilter>("all");
  const [minScore, setMinScore] = useState(0);
  const [page, setPage] = useState(1);

  const [analyzeURL, setAnalyzeURL] = useState("");
  const [analyzeLoading, setAnalyzeLoading] = useState(false);
  const [analyzeError, setAnalyzeError] = useState("");
  const [analyzeResult, setAnalyzeResult] = useState<Listing | null>(null);
  const [analyzeSource, setAnalyzeSource] = useState("");

  useEffect(() => {
    if (missions.length === 0) {
      void refreshMissions();
    }
  }, [missions.length, refreshMissions]);

  useEffect(() => {
    let disconnect: (() => void) | undefined;
    let cancelled = false;
    const selectedMissionStatus = missions.find((mission) => mission.ID === activeMissionId)?.Status?.toLowerCase() ?? "";
    const shouldStream = activeMissionId === 0 || selectedMissionStatus === "" || selectedMissionStatus === "active";

    async function load() {
      setError("");
      setListings([]);
      setNewCount(0);
      setDraftStates({});
      try {
        const nextListings = activeMissionId > 0
          ? (await api.missions.matches(activeMissionId, { limit: MATCHES_FETCH_LIMIT })).listings ?? []
          : (await api.listings.feed()).listings ?? [];
        if (cancelled) return;
        setListings(nextListings);
        if (!shouldStream) return;
        disconnect = connectDealStream((payload) => {
          if (!payload || typeof payload !== "object") return;
          const event = payload as {
            type?: string;
            missionID?: number;
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
          if (event.type !== "deal_found" || !event.deal?.Listing?.ItemID) return;
          if (activeMissionId > 0 && Number(event.missionID || 0) !== activeMissionId) return;
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
        });
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Failed to load matches");
        }
      }
    }

    void load();
    return () => {
      cancelled = true;
      disconnect?.();
    };
  }, [activeMissionId, missions]);

  const filtered = useMemo(() => {
    let result = listings;
    if (marketplace !== "all") result = result.filter((l) => l.MarketplaceID === marketplace);
    if (condition !== "all") result = result.filter((l) => l.Condition === condition);
    if (minScore > 0) result = result.filter((l) => (l.Score ?? 0) >= minScore);

    return [...result].sort((a, b) => {
      switch (sort) {
        case "score":
          return (b.Score ?? 0) - (a.Score ?? 0);
        case "price_asc":
          return (a.Price ?? 0) - (b.Price ?? 0);
        case "price_desc":
          return (b.Price ?? 0) - (a.Price ?? 0);
        case "newest":
          return 0;
      }
    });
  }, [listings, sort, marketplace, condition, minScore]);

  const totalPages = Math.max(1, Math.ceil(filtered.length / PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pageStart = (currentPage - 1) * PAGE_SIZE;
  const pageEnd = pageStart + PAGE_SIZE;
  const pagedListings = filtered.slice(pageStart, pageEnd);

  // Reset to page 1 whenever filters, sort, or the active mission change so the
  // user isn't stranded on a page that no longer exists after the list shrinks.
  useEffect(() => {
    setPage(1);
  }, [activeMissionId, sort, marketplace, condition, minScore]);

  const hasActiveFilters = marketplace !== "all" || condition !== "all" || minScore > 0 || sort !== "score";
  const currentMission = missions.find((mission) => mission.ID === activeMissionId) ?? null;
  const showLegacyFeedWithoutMissions = missions.length === 0 && listings.length > 0;
  const currentMissionStatus = (currentMission?.Status || "active").toLowerCase();
  const missionPaused = activeMissionId > 0 && currentMissionStatus === "paused";
  const missionCompleted = activeMissionId > 0 && currentMissionStatus === "completed";

  function resetFilters() {
    setSort("score");
    setMarketplace("all");
    setCondition("all");
    setMinScore(0);
  }

  async function approveMatch(itemID: string) {
    setError("");
    // Optimistically flag as approved so the badge lights up immediately.
    const prev = listings;
    setListings((list) =>
      list.map((l) => (l.ItemID === itemID ? { ...l, Feedback: "approved" } : l))
    );
    try {
      await api.matches.feedback(itemID, "approve");
    } catch (err) {
      setListings(prev);
      setError(err instanceof Error ? err.message : "Failed to approve match");
    }
  }

  async function dismissMatch(itemID: string) {
    setError("");
    // Optimistically remove it — dismissed listings are excluded from the feed
    // server-side anyway, so we just match that here.
    const prev = listings;
    setListings((list) => list.filter((l) => l.ItemID !== itemID));
    try {
      await api.matches.feedback(itemID, "dismiss");
    } catch (err) {
      setListings(prev);
      setError(err instanceof Error ? err.message : "Failed to dismiss match");
    }
  }

  async function analyzeListingURL() {
    const url = analyzeURL.trim();
    if (!url || analyzeLoading) return;
    setAnalyzeLoading(true);
    setAnalyzeError("");
    setAnalyzeResult(null);
    setAnalyzeSource("");
    try {
      const res = await api.matches.analyze(url, activeMissionId);
      setAnalyzeResult(res.listing);
      setAnalyzeSource(res.reasoning_source || "");
    } catch (err) {
      setAnalyzeError(err instanceof Error ? err.message : "Failed to analyze listing");
    } finally {
      setAnalyzeLoading(false);
    }
  }

  async function draftOffer(itemID: string) {
    setDraftStates((prev) => ({ ...prev, [itemID]: { loading: true, text: prev[itemID]?.text ?? null } }));
    setError("");
    try {
      const res = await api.shortlist.draftOffer(itemID);
      setDraftStates((prev) => ({ ...prev, [itemID]: { loading: false, text: res.Content || "" } }));
    } catch (err) {
      setDraftStates((prev) => ({ ...prev, [itemID]: { loading: false, text: prev[itemID]?.text ?? null } }));
      setError(err instanceof Error ? err.message : "Failed to draft seller message");
    }
  }

  return (
    <div className="page-stack">
      <section className="hero-panel compact">
        <div>
          <p className="section-kicker">Mission matches</p>
          <h2>Live feed scoped to your active mission</h2>
          <p className="hero-copy">
            Pick a mission to narrow deals to one buying goal. Keep mission set to All to view your combined feed.
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

      <section className="surface-panel analyze-panel">
        <div>
          <p className="section-kicker">Analyze any listing</p>
          <p className="section-support">
            Paste a Marktplaats, Vinted, or OLX BG URL and xolto will score it with the same AI that ranks your mission feed.
            {activeMissionId > 0 && " The active mission's goal and approved comparables will be used as context."}
          </p>
        </div>
        <div className="generator-bar">
          <input
            type="url"
            className="input"
            placeholder="https://www.marktplaats.nl/v/.../m1234567890-..."
            value={analyzeURL}
            onChange={(e) => setAnalyzeURL(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void analyzeListingURL();
            }}
            disabled={analyzeLoading}
          />
          <button
            type="button"
            className="btn-primary"
            onClick={() => void analyzeListingURL()}
            disabled={analyzeLoading || !analyzeURL.trim()}
          >
            {analyzeLoading ? "Analyzing…" : "Run analysis"}
          </button>
        </div>
        {analyzeError && <p className="error-msg" style={{ marginTop: 12 }}>{analyzeError}</p>}
        {analyzeResult && (
          <div style={{ marginTop: 16 }}>
            {analyzeSource && (
              <p className="section-support" style={{ marginBottom: 8 }}>
                Reasoning source: <strong>{analyzeSource === "ai" ? "AI" : "Heuristic fallback"}</strong>
              </p>
            )}
            <ListingCard listing={analyzeResult} />
          </div>
        )}
      </section>


      {(missionPaused || missionCompleted) && (
        <section className="surface-panel status-banner">
          <p className="section-kicker">Mission status</p>
          <p className="section-support">
            {missionPaused
              ? "This mission is paused. Monitors are not actively hunting until you resume it."
              : "This mission is completed. Start or resume another mission to keep getting active matches."}
          </p>
          <Link href="/missions" className="btn-secondary">
            Manage missions
          </Link>
        </section>
      )}

      {showLegacyFeedWithoutMissions && (
        <section className="surface-panel">
          <p className="section-kicker">Legacy feed mode</p>
          <p className="section-support">
            You have listings from older searches without mission links. Create a mission to scope new matches.
          </p>
          <Link href="/missions" className="btn-secondary">
            Create mission
          </Link>
        </section>
      )}

      <section className="surface-panel">
        <div className="feed-filter-group">
          <label className="feed-filter-label">Mission</label>
          <div className="generator-bar">
            <select
              className="input"
              value={activeMissionId}
              onChange={(e) => setActiveMission(Number(e.target.value))}
            >
              <option value={0}>All missions (legacy compatible)</option>
              {missions.map((mission) => (
                <option key={mission.ID} value={mission.ID}>
                  {mission.Name} ({mission.Status || "active"})
                </option>
              ))}
            </select>
            <Link href="/missions" className="btn-secondary">Manage missions</Link>
          </div>
          {currentMission && <p className="section-support">Active mission: {currentMission.Name}</p>}
        </div>
      </section>

      {listings.length > 0 && (
        <div className="feed-filter-bar">
          <div className="feed-filter-group">
            <label className="feed-filter-label">Sort</label>
            <div className="feed-pill-group">
              {(Object.keys(SORT_LABELS) as SortKey[]).map((key) => (
                <button key={key} type="button" className={`feed-pill${sort === key ? " active" : ""}`} onClick={() => setSort(key)}>
                  {SORT_LABELS[key]}
                </button>
              ))}
            </div>
          </div>

          <div className="feed-filter-group">
            <label className="feed-filter-label">Market</label>
            <div className="feed-pill-group">
              {(Object.keys(MARKETPLACE_LABELS) as MarketplaceFilter[]).map((key) => (
                <button key={key} type="button" className={`feed-pill${marketplace === key ? " active" : ""}`} onClick={() => setMarketplace(key)}>
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
                  <button key={key} type="button" className={`feed-pill${condition === key ? " active" : ""}`} onClick={() => setCondition(key)}>
                    {CONDITION_LABELS[key]}
                  </button>
                ))}
              </div>
            </div>

            <div className="feed-filter-group">
              <label className="feed-filter-label">Min score</label>
              <div className="feed-pill-group">
                {MIN_SCORE_OPTIONS.map((opt) => (
                  <button key={opt.value} type="button" className={`feed-pill${minScore === opt.value ? " active" : ""}`} onClick={() => setMinScore(opt.value)}>
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

      {missions.length === 0 && listings.length === 0 ? (
        <div className="surface-panel empty-state">
          <h3>No missions yet</h3>
          <p>Create a mission first to scope and prioritize your matches.</p>
          <Link href="/missions" className="btn-primary">
            Start a mission
          </Link>
        </div>
      ) : listings.length === 0 && (missionPaused || missionCompleted) && !error ? (
        <div className="surface-panel empty-state">
          <h3>{missionPaused ? "Mission is paused" : "Mission is completed"}</h3>
          <p>
            {missionPaused
              ? "Resume this mission to start collecting fresh matches again."
              : "Reactivate this mission or switch to an active mission to keep monitoring the market."}
          </p>
        </div>
      ) : listings.length === 0 && !error ? (
        <div className="surface-panel empty-state">
          <h3>No matches yet for this mission</h3>
          <p>Keep monitors running or broaden budget/condition constraints in your mission.</p>
        </div>
      ) : filtered.length === 0 ? (
        <div className="surface-panel empty-state">
          <h3>No matches fit these filters</h3>
          <p>Try relaxing score threshold or condition filters.</p>
          <button type="button" className="btn-ghost" onClick={resetFilters}>
            Clear filters
          </button>
        </div>
      ) : (
        <>
          <div className="listing-stack">
            {pagedListings.map((listing) => (
              <ListingCard
                key={listing.ItemID}
                listing={listing}
                isSaved={isShortlisted(listing.ItemID)}
                onShortlist={addToShortlist}
                onDraftOffer={draftOffer}
                onApprove={activeMissionId > 0 ? approveMatch : undefined}
                onDismiss={activeMissionId > 0 ? dismissMatch : undefined}
                draftState={draftStates[listing.ItemID]}
              />
            ))}
          </div>
          {totalPages > 1 && (
            <nav className="pagination-bar" aria-label="Matches pagination">
              <button
                type="button"
                className="feed-pill"
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={currentPage === 1}
              >
                ← Prev
              </button>
              <span className="pagination-status">
                Page {currentPage} of {totalPages} · showing {pageStart + 1}–{Math.min(pageEnd, filtered.length)} of {filtered.length}
              </span>
              <button
                type="button"
                className="feed-pill"
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                disabled={currentPage === totalPages}
              >
                Next →
              </button>
            </nav>
          )}
        </>
      )}
    </div>
  );
}
