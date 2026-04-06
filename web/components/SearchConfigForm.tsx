"use client";

import { useState } from "react";

import { api, SearchSpec } from "../lib/api";

type Props = {
  onSaved?: (search: SearchSpec) => void;
};

export function SearchConfigForm({ onSaved }: Props) {
  const [name, setName] = useState("");
  const [query, setQuery] = useState("");
  const [marketplaceID, setMarketplaceID] = useState("marktplaats");
  const [maxPrice, setMaxPrice] = useState("");
  const [categoryID, setCategoryID] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  async function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    setSaving(true);
    try {
      const search = await api.searches.create({
        Name: name,
        Query: query,
        MarketplaceID: marketplaceID,
        MaxPrice: maxPrice ? Math.round(Number(maxPrice) * 100) : 0,
        CategoryID: categoryID ? Number(categoryID) : 0,
        Condition: [],
        OfferPercentage: 70,
        AutoMessage: false,
        MessageTemplate: "",
        Enabled: true,
        CheckInterval: 300,
      });
      setName("");
      setQuery("");
      setMaxPrice("");
      setCategoryID("");
      onSaved?.(search);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save search");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="card p-5">
      <h3 className="text-sm font-semibold text-gray-900 mb-4">New search</h3>
      {error && <p className="error-msg mb-3">{error}</p>}
      <form onSubmit={onSubmit} className="space-y-3">
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="label">Name</label>
            <input
              className="input"
              placeholder="Sony A6700"
              value={name}
              onChange={(e) => setName(e.target.value)}
              required
            />
          </div>
          <div>
            <label className="label">Search query</label>
            <input
              className="input"
              placeholder="sony a6700 camera"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              required
            />
          </div>
        </div>
        <div className="grid grid-cols-3 gap-3">
          <div>
            <label className="label">Marketplace</label>
            <select
              className="input"
              value={marketplaceID}
              onChange={(e) => setMarketplaceID(e.target.value)}
            >
              <option value="marktplaats">Marktplaats</option>
              <option value="vinted">Vinted</option>
            </select>
          </div>
          <div>
            <label className="label">Max price (€)</label>
            <input
              className="input"
              placeholder="900"
              type="number"
              min="0"
              value={maxPrice}
              onChange={(e) => setMaxPrice(e.target.value)}
            />
          </div>
          <div>
            <label className="label">Category ID</label>
            <input
              className="input"
              placeholder="optional"
              type="number"
              min="0"
              value={categoryID}
              onChange={(e) => setCategoryID(e.target.value)}
            />
          </div>
        </div>
        <button type="submit" disabled={saving} className="btn-primary">
          {saving ? "Saving…" : "Save search"}
        </button>
      </form>
    </div>
  );
}
