"use client";

import { useEffect, useState } from "react";

import { ShortlistTable } from "../../../components/ShortlistTable";
import { api, ShortlistEntry } from "../../../lib/api";

export default function ShortlistPage() {
  const [items, setItems] = useState<ShortlistEntry[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    api.shortlist
      .get()
      .then((res) => setItems(res.shortlist.filter((item) => item.Status !== "removed")))
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load shortlist"));
  }, []);

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Shortlist</h1>
        {items.length > 0 && (
          <span className="text-xs text-gray-400">
            {items.length} item{items.length !== 1 ? "s" : ""}
          </span>
        )}
      </div>

      {error && <p className="error-msg mb-4">{error}</p>}

      <ShortlistTable
        items={items}
        onRemove={async (itemID) => {
          await api.shortlist.remove(itemID);
          setItems((prev) => prev.filter((item) => item.ItemID !== itemID));
        }}
      />
    </div>
  );
}
