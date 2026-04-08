"use client";

import { useState } from "react";

import { Listing } from "../lib/api";
import { formatEuroFromCents } from "../lib/format";

interface Props {
  listing: Listing;
  onShortlist?: (itemID: string) => Promise<void>;
  isSaved?: boolean;
}

const MARKETPLACE_LABELS: Record<string, string> = {
  marktplaats: "Marktplaats",
  vinted: "Vinted",
  olxbg: "OLX BG",
};

const RISK_FLAG_LABELS: Record<string, string> = {
  anomaly_price: "Anomaly price",
  vague_condition: "Vague condition",
  unclear_bundle: "Unclear bundle",
  no_model_id: "No model identified",
  missing_key_photos: "Too few photos",
  no_battery_health: "Battery health missing",
  refurbished_ambiguity: "Refurbished details unclear",
};

export function ListingCard({ listing, onShortlist, isSaved = false }: Props) {
  const item = listing;
  const score = (listing.Score ?? 0) > 0 ? listing.Score : undefined;
  const fairPrice = (listing.FairPrice ?? 0) > 0 ? listing.FairPrice : undefined;
  const confidence = listing.Confidence ?? 0;
  const reason = listing.Reason || undefined;
  const verdict = verdictLabel(score, listing.RiskFlags ?? []);
  const confidenceLabel = confidenceCopy(confidence);
  const suggestedQuestion = firstSuggestedQuestion(listing.RiskFlags ?? []);

  const [saving, setSaving] = useState(false);

  async function handleShortlist() {
    if (!onShortlist || saving || isSaved) return;
    setSaving(true);
    try {
      await onShortlist(item.ItemID);
    } finally {
      setSaving(false);
    }
  }

  return (
    <article className="listing-card">
      <div className="listing-media">
        {item.ImageURLs?.[0] ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img src={item.ImageURLs[0]} alt={item.Title} className="listing-image" />
        ) : (
          <div className="listing-image listing-image-fallback">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="var(--brand-600)" strokeWidth="1.5" strokeLinecap="round" opacity="0.5">
              <rect x="3" y="3" width="18" height="18" rx="3" />
              <circle cx="8.5" cy="8.5" r="1.5" />
              <path d="M21 15l-5-5L5 21" />
            </svg>
          </div>
        )}
      </div>

      <div className="listing-content">
        <div className="listing-head">
          <div className="listing-copy">
            <div className="listing-meta-row">
              {item.MarketplaceID && (
                <span className="market-badge">{MARKETPLACE_LABELS[item.MarketplaceID] || item.MarketplaceID}</span>
              )}
              {item.Condition && <span className="subtle-pill">{item.Condition}</span>}
            </div>
            <a href={item.URL || "#"} target="_blank" rel="noopener noreferrer" className="listing-title">
              {item.Title}
            </a>
            <div className="listing-meta-row" style={{ marginTop: 8 }}>
              <span className="subtle-pill">{verdict}</span>
              {score !== undefined && <span className="subtle-pill">Score {score.toFixed(1)}</span>}
              <span className="subtle-pill">{confidenceLabel}</span>
            </div>
          </div>

          {onShortlist && (
            <button
              type="button"
              className={`save-chip${isSaved ? " saved" : ""}`}
              onClick={handleShortlist}
              disabled={saving || isSaved}
            >
              {isSaved ? "Saved" : saving ? "Saving…" : "Save"}
            </button>
          )}
        </div>

        <div className="listing-price-row">
          <span className="listing-price">
            Ask: {formatEuroFromCents(item.Price)}
          </span>
          <span className="price-caption">
            Fair: {fairPrice ? formatEuroFromCents(fairPrice) : "Unknown"}
          </span>
        </div>

        {reason && <p className="listing-reason">{reason}</p>}
        {(item.RiskFlags?.length ?? 0) > 0 && (
          <div className="risk-flags">
            {item.RiskFlags!.map((flag) => (
              <span key={flag} className="risk-flag">
                {RISK_FLAG_LABELS[flag] ?? flag}
              </span>
            ))}
          </div>
        )}
        {suggestedQuestion && <p className="shortlist-question">Ask seller: {suggestedQuestion}</p>}
        {item.URL && (
          <div className="shortlist-actions">
            <a href={item.URL} target="_blank" rel="noopener noreferrer" className="btn-secondary">
              Ask seller
            </a>
          </div>
        )}
      </div>
    </article>
  );
}

function verdictLabel(score: number | undefined, riskFlags: string[]) {
  if (riskFlags.includes("anomaly_price") || riskFlags.includes("refurbished_ambiguity")) {
    return "Suspicious";
  }
  if (!score) return "Fair price";
  if (score >= 8) return "Strong buy";
  if (score >= 7) return "Good deal";
  if (score >= 5.5) return "Fair price";
  if (score >= 4) return "Overpriced";
  return "Suspicious";
}

function confidenceCopy(confidence: number) {
  if (confidence >= 0.75) return "Confidence: High";
  if (confidence >= 0.5) return "Confidence: Medium";
  return "Confidence: Low";
}

function firstSuggestedQuestion(riskFlags: string[]) {
  if (riskFlags.includes("no_battery_health")) {
    return "Could you share battery health / cycle count?";
  }
  if (riskFlags.includes("missing_key_photos")) {
    return "Could you send close-up photos of all sides and ports?";
  }
  if (riskFlags.includes("refurbished_ambiguity")) {
    return "Is this refurbished by an official partner, and is there warranty?";
  }
  if (riskFlags.includes("vague_condition")) {
    return "Can you confirm there are no faults and everything works fully?";
  }
  return "Can you confirm condition and included accessories?";
}
