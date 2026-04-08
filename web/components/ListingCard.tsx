"use client";

import { useState } from "react";

import { Listing } from "../lib/api";
import { formatEuroFromCents } from "../lib/format";
import { ScoreBar } from "./ScoreBar";

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
};

export function ListingCard({ listing, onShortlist, isSaved = false }: Props) {
  const item = listing;
  const score = (listing.Score ?? 0) > 0 ? listing.Score : undefined;
  const offerPrice = (listing.OfferPrice ?? 0) > 0 ? listing.OfferPrice : undefined;
  const fairPrice = (listing.FairPrice ?? 0) > 0 ? listing.FairPrice : undefined;
  const reason = listing.Reason || undefined;

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
        {/* Score bar at the very top when scored */}
        {score !== undefined && <ScoreBar score={score} className="listing-score" />}

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
          <span className="listing-price">{formatEuroFromCents(item.Price)}</span>
          {offerPrice ? (
            <span className="price-callout">Offer {formatEuroFromCents(offerPrice)}</span>
          ) : null}
          {fairPrice ? (
            <span className="price-caption">Fair value {formatEuroFromCents(fairPrice)}</span>
          ) : null}
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
      </div>
    </article>
  );
}
