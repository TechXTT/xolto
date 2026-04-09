"use client";

import Link from "next/link";
import { useState } from "react";

import { ShortlistEntry } from "../lib/api";
import { formatEuroFromCents } from "../lib/format";
import { ScoreBar } from "./ScoreBar";

type Props = {
  items: ShortlistEntry[];
  onRemove?: (itemID: string) => Promise<void>;
  draftStates?: Record<string, { loading: boolean; text: string | null }>;
  onDraftOffer?: (itemID: string) => Promise<void>;
  comparisonMode?: boolean;
  selectedIDs?: string[];
  onToggleSelect?: (itemID: string) => void;
};

const LABEL_CONFIG: Record<string, { label: string; color: string; bg: string }> = {
  buy_now: { label: "Buy now", color: "var(--brand-700)", bg: "var(--brand-100)" },
  worth_watching: { label: "Worth watching", color: "var(--warning-500)", bg: "rgba(245,158,11,0.1)" },
  ask_questions: { label: "Ask questions", color: "var(--fg-700)", bg: "rgba(15,23,42,0.07)" },
  skip: { label: "Skip", color: "var(--danger-600)", bg: "rgba(220,38,38,0.08)" },
};

export function ShortlistTable({
  items,
  onRemove,
  draftStates = {},
  onDraftOffer,
  comparisonMode = false,
  selectedIDs = [],
  onToggleSelect,
}: Props) {
  const [removingID, setRemovingID] = useState<string | null>(null);

  if (items.length === 0) {
    return (
      <div className="surface-panel empty-state">
        <div className="empty-icon">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#94a3b8" strokeWidth="1.5" strokeLinejoin="round">
            <path d="M6 3.5h12a.5.5 0 0 1 .5.5v16L12 17l-6.5 3.5V4a.5.5 0 0 1 .5-.5z" />
          </svg>
        </div>
        <h3>No saved comparisons yet</h3>
        <p>Save promising listings from the live feed to compare price, verdict, and fair value side by side.</p>
        <Link href="/matches" className="btn-primary">
          Browse matches
        </Link>
      </div>
    );
  }

  if (comparisonMode) {
    return (
      <div className="surface-panel">
        <div style={{ overflowX: "auto" }}>
          <table className="shortlist-compare-table">
            <thead>
              <tr>
                <th>Select</th>
                <th>Name</th>
                <th>Ask price</th>
                <th>Fair price</th>
                <th>Condition</th>
                <th>Verdict</th>
                <th>Risks</th>
                <th>Fit score</th>
                <th>Suggested offer</th>
                <th>Next action</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => {
                const savings = item.FairPrice > 0 && item.AskPrice > 0 ? item.FairPrice - item.AskPrice : 0;
                const strongBuy = isStrongBuy(item);
                return (
                  <tr key={item.ItemID} className={strongBuy ? "strong-buy-row" : ""}>
                    <td>
                      <input
                        type="checkbox"
                        checked={selectedIDs.includes(item.ItemID)}
                        onChange={() => onToggleSelect?.(item.ItemID)}
                        disabled={!selectedIDs.includes(item.ItemID) && selectedIDs.length >= 4}
                      />
                    </td>
                    <td>
                      <a href={item.URL} target="_blank" rel="noopener noreferrer">{item.Title}</a>
                    </td>
                    <td>{formatEuroFromCents(item.AskPrice)}</td>
                    <td>{formatEuroFromCents(item.FairPrice)}</td>
                    <td>—</td>
                    <td>{item.Verdict || "—"}</td>
                    <td>{item.Concerns?.length ? item.Concerns.join(" | ") : "—"}</td>
                    <td>{item.RecommendationScore > 0 ? item.RecommendationScore.toFixed(1) : "—"}</td>
                    <td>{savings > 0 ? formatEuroFromCents(item.AskPrice + Math.floor(savings/2)) : "—"}</td>
                    <td>{item.SuggestedQuestions?.[0] || "Ask for recent photos and condition details."}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>
    );
  }

  return (
    <div className="surface-panel">
      <div className="shortlist-grid">
        {items.map((item) => {
          const config = LABEL_CONFIG[item.RecommendationLabel] ?? null;
          const savings = item.AskPrice > 0 && item.FairPrice > 0 ? item.FairPrice - item.AskPrice : 0;
          const strongBuy = isStrongBuy(item);

          return (
            <article key={item.ItemID} className={`shortlist-card${strongBuy ? " strong-buy" : ""}`}>
              <div className="shortlist-card-top">
                <div>
                  <a href={item.URL} target="_blank" rel="noopener noreferrer" className="shortlist-title">
                    {item.Title}
                  </a>
                  <p className="shortlist-verdict">{item.Verdict}</p>
                </div>
                {config && (
                  <span className="shortlist-badge" style={{ color: config.color, background: config.bg }}>
                    {config.label}
                  </span>
                )}
              </div>

              {item.RecommendationScore > 0 && <ScoreBar score={item.RecommendationScore} />}

              <div className="shortlist-metrics">
                <div>
                  <span className="metric-label">Ask</span>
                  <strong>{formatEuroFromCents(item.AskPrice)}</strong>
                </div>
                <div>
                  <span className="metric-label">Fair value</span>
                  <strong>{formatEuroFromCents(item.FairPrice)}</strong>
                </div>
                <div>
                  <span className="metric-label">Opportunity</span>
                  <strong className={savings > 0 ? "metric-positive" : ""}>{savings > 0 ? formatEuroFromCents(savings) : "Watch"}</strong>
                </div>
              </div>

              {item.Concerns?.[0] && <p className="shortlist-concern">Flag: {item.Concerns[0]}</p>}
              {item.SuggestedQuestions?.length > 0 && (
                <div className="shortlist-question">
                  <strong>Suggested questions</strong>
                  <ul>
                    {item.SuggestedQuestions.map((question) => (
                      <li key={question}>{question}</li>
                    ))}
                  </ul>
                </div>
              )}

              {onDraftOffer && (
                <div className="offer-draft-row">
                  <button
                    type="button"
                    className="btn-primary"
                    disabled={draftStates[item.ItemID]?.loading}
                    onClick={() => void onDraftOffer(item.ItemID)}
                  >
                    {draftStates[item.ItemID]?.loading ? "Drafting..." : "Draft offer"}
                  </button>
                  {draftStates[item.ItemID]?.text && (
                    <div className="offer-draft-block">
                      <p>{draftStates[item.ItemID]!.text}</p>
                      <button
                        type="button"
                        className="btn-copy"
                        onClick={() => {
                          const text = draftStates[item.ItemID]?.text || "";
                          if (!text) return;
                          void navigator.clipboard.writeText(text);
                        }}
                      >
                        Copy
                      </button>
                    </div>
                  )}
                </div>
              )}

              {onRemove && (
                <div className="shortlist-actions">
                  <button
                    type="button"
                    className="btn-secondary"
                    onClick={async () => {
                      if (removingID) return;
                      setRemovingID(item.ItemID);
                      try {
                        await onRemove(item.ItemID);
                      } finally {
                        setRemovingID(null);
                      }
                    }}
                    disabled={removingID === item.ItemID}
                  >
                    {removingID === item.ItemID ? "Removing..." : "Remove"}
                  </button>
                </div>
              )}
            </article>
          );
        })}
      </div>
    </div>
  );
}

function isStrongBuy(item: ShortlistEntry) {
  if (item.RecommendationLabel === "buy_now") return true;
  return item.Verdict.toLowerCase().includes("strong buy");
}
