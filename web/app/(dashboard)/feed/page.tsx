"use client";

import { useEffect, useState } from "react";

import { ListingCard } from "../../../components/ListingCard";
import { api, Listing } from "../../../lib/api";
import { connectDealStream } from "../../../lib/sse";

export default function FeedPage() {
  const [listings, setListings] = useState<Listing[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    let disconnect: (() => void) | undefined;

    async function load() {
      try {
        const res = await api.listings.feed();
        setListings(res.listings);
        disconnect = connectDealStream((payload) => {
          if (!payload || typeof payload !== "object") {
            return;
          }
          const event = payload as {
            type?: string;
            deal?: { Listing?: Listing };
            Listing?: Listing;
          };
          const listing = event.deal?.Listing ?? event.Listing;
          if (event.type === "deal_found" && listing?.ItemID) {
            setListings((prev) => [listing, ...prev.filter((item) => item.ItemID !== listing.ItemID)]);
          }
        });
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load feed");
      }
    }

    void load();
    return () => disconnect?.();
  }, []);

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Feed</h1>
        <span className="flex items-center gap-1.5 text-xs text-green-600">
          <span className="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse" />
          Live
        </span>
      </div>

      {error && <p className="error-msg mb-4">{error}</p>}

      {listings?.length === 0 && !error ? (
        <div className="card p-12 text-center text-gray-400 text-sm">
          No deals yet. Make sure you have{" "}
          <a href="/searches" className="underline">searches configured</a> and the background worker is running.
        </div>
      ) : (
        <div className="space-y-3">
          {listings?.map((listing) => (
            <ListingCard
              key={listing.ItemID}
              listing={listing}
              onShortlist={async (itemID) => { await api.shortlist.add(itemID); }}
            />
          ))}
        </div>
      )}
    </div>
  );
}
