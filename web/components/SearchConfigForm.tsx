"use client";

import { useEffect, useState } from "react";

import { api, SearchSpec } from "../lib/api";
import { intervalMinutesToDurationNs, normalizeCheckIntervalMinutes } from "../lib/format";

const CONDITION_OPTIONS = [
  { value: "new", label: "New" },
  { value: "like_new", label: "Like new" },
  { value: "good", label: "Good / Used" },
];

type Props = {
  initialValue?: Partial<SearchSpec>;
  onSaved?: (search: SearchSpec) => void;
  onCancel?: () => void;
  mode?: "create" | "edit";
  title?: string;
  description?: string;
};

type DraftState = {
  name: string;
  query: string;
  marketplaceID: string;
  maxPrice: string;
  categoryID: string;
  checkInterval: string;
  offerPercentage: string;
  enabled: boolean;
  condition: string[];
};

function draftFromSearch(search?: Partial<SearchSpec>): DraftState {
  return {
    name: search?.Name || "",
    query: search?.Query || "",
    marketplaceID: search?.MarketplaceID || "marktplaats",
    maxPrice: search?.MaxPrice ? String(Math.round(search.MaxPrice / 100)) : "",
    categoryID: search?.CategoryID ? String(search.CategoryID) : "",
    checkInterval: String(normalizeCheckIntervalMinutes(search?.CheckInterval)),
    offerPercentage: String(search?.OfferPercentage || 72),
    enabled: search?.Enabled ?? true,
    condition: search?.Condition?.length ? search.Condition : ["like_new", "good"],
  };
}

export function SearchConfigForm({
  initialValue,
  onSaved,
  onCancel,
  mode = "create",
  title,
  description,
}: Props) {
  const [draft, setDraft] = useState<DraftState>(() => draftFromSearch(initialValue));
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setDraft(draftFromSearch(initialValue));
    setError("");
  }, [initialValue]);

  function toggleCondition(condition: string) {
    setDraft((current) => ({
      ...current,
      condition: current.condition.includes(condition)
        ? current.condition.filter((item) => item !== condition)
        : [...current.condition, condition],
    }));
  }

  async function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    setSaving(true);

    const payload: Partial<SearchSpec> = {
      Name: draft.name.trim(),
      Query: draft.query.trim(),
      MarketplaceID: draft.marketplaceID,
      MaxPrice: draft.maxPrice ? Math.round(Number(draft.maxPrice) * 100) : 0,
      CategoryID: draft.categoryID ? Number(draft.categoryID) : 0,
      Condition: draft.condition,
      OfferPercentage: Math.min(95, Math.max(40, Number(draft.offerPercentage) || 72)),
      Enabled: draft.enabled,
      MinPrice: initialValue?.MinPrice || 0,
      AutoMessage: initialValue?.AutoMessage ?? false,
      MessageTemplate: initialValue?.MessageTemplate || "",
      Attributes: initialValue?.Attributes || {},
      CheckInterval: intervalMinutesToDurationNs(Number(draft.checkInterval) || 5),
    };

    try {
      if (mode === "edit" && initialValue?.ID) {
        await api.searches.update(initialValue.ID, payload);
        onSaved?.({
          ...(initialValue as SearchSpec),
          ...payload,
          ID: initialValue.ID,
        });
      } else {
        const search = await api.searches.create(payload);
        onSaved?.(search);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save search");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="surface-panel form-panel">
      <div className="section-heading">
        <div>
          <p className="section-kicker">{mode === "edit" ? "Tune search" : "Manual setup"}</p>
          <h3>{title || (mode === "edit" ? "Edit saved search" : "Create a precise hunt")}</h3>
        </div>
        {onCancel && (
          <button type="button" className="btn-ghost" onClick={onCancel}>
            Cancel
          </button>
        )}
      </div>
      <p className="section-support">
        {description || "Set the marketplace, budget, timing, and condition guardrails you want markt to follow."}
      </p>

      {error && <div className="error-msg">{error}</div>}

      <form onSubmit={onSubmit} className="search-form-grid">
        <div className="input-stack">
          <label className="label" htmlFor="search-name">
            Search name
          </label>
          <input
            id="search-name"
            className="input"
            placeholder="Sony A6700 body"
            value={draft.name}
            onChange={(e) => setDraft((current) => ({ ...current, name: e.target.value }))}
            required
          />
        </div>

        <div className="input-stack">
          <label className="label" htmlFor="search-query">
            Query
          </label>
          <input
            id="search-query"
            className="input"
            placeholder="sony a6700 camera"
            value={draft.query}
            onChange={(e) => setDraft((current) => ({ ...current, query: e.target.value }))}
            required
          />
        </div>

        <div className="input-stack">
          <label className="label" htmlFor="search-marketplace">
            Marketplace
          </label>
          <select
            id="search-marketplace"
            className="input"
            value={draft.marketplaceID}
            onChange={(e) => setDraft((current) => ({ ...current, marketplaceID: e.target.value }))}
          >
            <option value="marktplaats">Marktplaats</option>
            <option value="vinted">Vinted</option>
            <option value="olxbg">OLX Bulgaria</option>
          </select>
        </div>

        <div className="input-stack">
          <label className="label" htmlFor="search-max-price">
            Max price (€)
          </label>
          <input
            id="search-max-price"
            className="input"
            type="number"
            min="0"
            step="1"
            placeholder="900"
            value={draft.maxPrice}
            onChange={(e) => setDraft((current) => ({ ...current, maxPrice: e.target.value }))}
          />
        </div>

        <div className="input-stack">
          <label className="label" htmlFor="search-category">
            Category ID
          </label>
          <input
            id="search-category"
            className="input"
            type="number"
            min="0"
            placeholder="Optional"
            value={draft.categoryID}
            onChange={(e) => setDraft((current) => ({ ...current, categoryID: e.target.value }))}
          />
        </div>

        <div className="input-stack">
          <label className="label" htmlFor="search-interval">
            Check interval (minutes)
          </label>
          <input
            id="search-interval"
            className="input"
            type="number"
            min="1"
            step="1"
            value={draft.checkInterval}
            onChange={(e) => setDraft((current) => ({ ...current, checkInterval: e.target.value }))}
          />
        </div>

        <div className="input-stack">
          <label className="label" htmlFor="search-offer-percentage">
            Offer target (% of fair value)
          </label>
          <input
            id="search-offer-percentage"
            className="input"
            type="number"
            min="40"
            max="95"
            step="1"
            value={draft.offerPercentage}
            onChange={(e) => setDraft((current) => ({ ...current, offerPercentage: e.target.value }))}
          />
        </div>

        <div className="input-stack search-form-full">
          <label className="label">Preferred condition</label>
          <div className="choice-row">
            {CONDITION_OPTIONS.map(({ value, label }) => {
              const active = draft.condition.includes(value);
              return (
                <button
                  key={value}
                  type="button"
                  className={`choice-pill${active ? " active" : ""}`}
                  onClick={() => toggleCondition(value)}
                >
                  {label}
                </button>
              );
            })}
          </div>
        </div>

        <div className="search-form-full search-form-footer">
          <label className="toggle-field">
            <input
              type="checkbox"
              checked={draft.enabled}
              onChange={(e) => setDraft((current) => ({ ...current, enabled: e.target.checked }))}
            />
            <span>Keep this search active after saving</span>
          </label>

          <button type="submit" disabled={saving} className="btn-primary">
            {saving ? "Saving…" : mode === "edit" ? "Save changes" : "Save search"}
          </button>
        </div>
      </form>
    </div>
  );
}
