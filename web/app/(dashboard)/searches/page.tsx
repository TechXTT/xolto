"use client";

import { useEffect, useRef, useState } from "react";

import { SearchConfigForm } from "../../../components/SearchConfigForm";
import { api, SearchSpec } from "../../../lib/api";
import { formatCompactEuroFromCents, normalizeCheckIntervalMinutes, searchSignature } from "../../../lib/format";

const CONDITION_LABELS: Record<string, string> = {
  new: "New",
  like_new: "Like new",
  good: "Good / Used",
  fair: "Fair",
  // legacy Dutch values from existing DB records
  "Zo goed als nieuw": "Like new",
  "Gebruikt": "Good / Used",
  "Nieuw": "New",
};

function conditionLabel(value: string): string {
  return CONDITION_LABELS[value] ?? value;
}

type GeneratedSearch = SearchSpec & {
  _key: string;
  duplicate: boolean;
};

function normalizeSearch(search: SearchSpec): SearchSpec {
  return {
    ...search,
    Condition: search.Condition?.length ? search.Condition : ["like_new", "good"],
    OfferPercentage: search.OfferPercentage || 72,
    Enabled: search.Enabled ?? true,
  };
}

function GeneratedBadge({ duplicate }: { duplicate: boolean }) {
  if (duplicate) {
    return <span className="warning-badge">Duplicate</span>;
  }
  return <span className="success-badge">Ready to activate</span>;
}

function SearchSummary({ search }: { search: SearchSpec }) {
  return (
    <div className="search-summary">
      <div>
        <h3>{search.Name || search.Query}</h3>
        <p>{search.Query}</p>
      </div>
      <div className="search-summary-meta">
        <span className="market-badge">{search.MarketplaceID}</span>
        {search.MaxPrice > 0 && <span className="subtle-pill">{formatCompactEuroFromCents(search.MaxPrice)} max</span>}
        <span className="subtle-pill">{normalizeCheckIntervalMinutes(search.CheckInterval)} min</span>
        <span className="subtle-pill">{search.OfferPercentage}% offer target</span>
      </div>
    </div>
  );
}

export default function HuntsPage() {
  const [savedSearches, setSavedSearches] = useState<SearchSpec[]>([]);
  const [generatedSearches, setGeneratedSearches] = useState<GeneratedSearch[]>([]);
  const [error, setError] = useState("");
  const [generatorWarning, setGeneratorWarning] = useState("");
  const [topic, setTopic] = useState("");
  const [generating, setGenerating] = useState(false);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [editingID, setEditingID] = useState<number | null>(null);
  const [runningAll, setRunningAll] = useState(false);
  const nextKey = useRef(0);

  useEffect(() => {
    void load();
  }, []);

  async function load() {
    try {
      const res = await api.searches.list();
      const searches = Array.isArray(res.searches) ? res.searches : [];
      setSavedSearches(searches.map(normalizeSearch));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load hunts");
    }
  }

  function refreshDuplicateFlags(nextGenerated: GeneratedSearch[], nextSaved = savedSearches) {
    const savedSignatures = new Set(nextSaved.map((search) => searchSignature(search)));
    const seenGenerated = new Set<string>();
    return nextGenerated.map((search) => {
      const signature = searchSignature(search);
      const duplicate = savedSignatures.has(signature) || seenGenerated.has(signature);
      seenGenerated.add(signature);
      return { ...search, duplicate };
    });
  }

  async function handleGenerate() {
    setError("");
    setGeneratorWarning("");
    setGenerating(true);
    try {
      const res = await api.searches.generate(topic.trim());
      const searches = Array.isArray(res.searches) ? (res.searches as SearchSpec[]) : [];
      const generated = searches.map((search) => ({
        ...normalizeSearch(search),
        _key: `gen-${nextKey.current++}`,
        duplicate: false,
      }));
      setGeneratedSearches((prev) => refreshDuplicateFlags([...generated, ...prev]));
      setGeneratorWarning(res.warning || "");
      setTopic("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to generate hunts");
    } finally {
      setGenerating(false);
    }
  }

  async function saveGenerated(search: GeneratedSearch) {
    setError("");
    try {
      const created = await api.searches.create(search);
      const nextSaved = [normalizeSearch(created), ...savedSearches];
      setSavedSearches(nextSaved);
      setGeneratedSearches((prev) => refreshDuplicateFlags(prev.filter((item) => item._key !== search._key), nextSaved));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to activate hunt");
    }
  }

  async function toggleSearch(search: SearchSpec) {
    setError("");
    const nextSearch = normalizeSearch({ ...search, Enabled: !search.Enabled });
    try {
      await api.searches.update(search.ID, nextSearch);
      setSavedSearches((prev) => prev.map((item) => (item.ID === search.ID ? nextSearch : item)));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update hunt");
    }
  }

  async function deleteSearch(searchID: number) {
    setError("");
    try {
      await api.searches.delete(searchID);
      const nextSaved = savedSearches.filter((item) => item.ID !== searchID);
      setSavedSearches(nextSaved);
      setGeneratedSearches((prev) => refreshDuplicateFlags(prev, nextSaved));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete hunt");
    }
  }

  async function runAllSearches() {
    setError("");
    setRunningAll(true);
    try {
      await api.searches.runAll();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to trigger hunts");
    } finally {
      setRunningAll(false);
    }
  }

  const activeCount = savedSearches.filter((s) => s.Enabled).length;

  return (
    <div className="page-stack">
      <section className="hero-panel compact">
        <div>
          <p className="section-kicker">Hunt configuration</p>
          <h2>Deploy AI across the market</h2>
          <p className="hero-copy">
            Each hunt is a live monitor. MarktBot polls the market on your schedule, scores every new listing, and surfaces only what meets your criteria.
          </p>
        </div>
        <div className="stats-row">
          <div className="stat-card live">
            <span className="metric-label">Active hunts</span>
            <strong>{activeCount}</strong>
          </div>
          <div className="stat-card">
            <span className="metric-label">Total saved</span>
            <strong>{savedSearches.length}</strong>
          </div>
          <div className="hero-actions" style={{ alignSelf: "flex-end" }}>
            <button type="button" className="btn-secondary" onClick={() => setShowCreateForm((open) => !open)}>
              {showCreateForm ? "Hide form" : "Manual setup"}
            </button>
            <button type="button" className="btn-primary" onClick={() => void runAllSearches()} disabled={runningAll || activeCount === 0}>
              {runningAll ? "Scanning…" : "Scan now"}
            </button>
          </div>
        </div>
      </section>

      {error && <div className="error-msg">{error}</div>}
      {generatorWarning && <div className="notice-msg">{generatorWarning}</div>}

      {showCreateForm && (
        <SearchConfigForm
          onSaved={(search) => {
            const nextSaved = [normalizeSearch(search), ...savedSearches];
            setSavedSearches(nextSaved);
            setGeneratedSearches((prev) => refreshDuplicateFlags(prev, nextSaved));
            setShowCreateForm(false);
          }}
          onCancel={() => setShowCreateForm(false)}
        />
      )}

      {/* AI Generator — primary feature */}
      <section className="surface-panel">
        <div className="section-heading">
          <div>
            <p className="section-kicker">AI hunt generator</p>
            <h3>Describe a category, get a full set of hunts</h3>
          </div>
        </div>
        <p className="section-support">
          Tell the AI what you're after — it will generate a batch of targeted hunts covering different angles, price points, and keywords across the market.
        </p>

        <div className="generator-bar">
          <input
            className="input"
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            placeholder="e.g. mirrorless cameras, vintage denim, espresso machines"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void handleGenerate();
              }
            }}
          />
          <button type="button" className="btn-primary" disabled={generating || !topic.trim()} onClick={() => void handleGenerate()}>
            {generating ? "Generating…" : "Generate hunts"}
          </button>
        </div>

        {generatedSearches.length === 0 ? (
          <div className="empty-inline">
            <p>No AI suggestions yet. Describe a product category above to generate a batch of targeted hunts.</p>
          </div>
        ) : (
          <div className="search-card-list">
            {generatedSearches.map((search) => (
              <article key={search._key} className="search-card generated">
                <div className="search-card-header">
                  <SearchSummary search={search} />
                  <GeneratedBadge duplicate={search.duplicate} />
                </div>
                {search.Condition.length > 0 && (
                  <p className="search-card-copy">Conditions: {search.Condition.join(", ")}</p>
                )}
                <div className="search-card-actions">
                  <button
                    type="button"
                    className="btn-ghost"
                    onClick={() => setGeneratedSearches((prev) => prev.filter((item) => item._key !== search._key))}
                  >
                    Dismiss
                  </button>
                  <button
                    type="button"
                    className="btn-primary"
                    disabled={search.duplicate}
                    onClick={() => void saveGenerated(search)}
                  >
                    Activate hunt
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}
      </section>

      {/* Saved hunts */}
      <section className="surface-panel">
        <div className="section-heading">
          <div>
            <p className="section-kicker">Active hunts</p>
            <h3>
              {savedSearches.length === 0
                ? "No hunts deployed yet"
                : `${savedSearches.length} hunt${savedSearches.length === 1 ? "" : "s"} configured`}
            </h3>
          </div>
        </div>
        <p className="section-support">
          Adjust budget, timing, and conditions without resetting any hunt history. Pause to stop scanning without deleting.
        </p>

        {savedSearches.length === 0 ? (
          <div className="empty-inline">
            <p>Generate your first hunts with the AI above, or use the manual setup form to build one precisely.</p>
          </div>
        ) : (
          <div className="search-card-list">
            {savedSearches.map((search) => {
              const editing = editingID === search.ID;
              return (
                <article key={search.ID} className="search-card saved">
                  <div className="search-card-header">
                    <SearchSummary search={search} />
                    <span className={`status-pill${search.Enabled ? " on" : ""}`}>
                      {search.Enabled ? "Active" : "Paused"}
                    </span>
                  </div>
                  <p className="search-card-copy">
                    Scanning {search.Condition.map(conditionLabel).join(", ")} listings every {normalizeCheckIntervalMinutes(search.CheckInterval)} minutes.
                  </p>

                  <div className="search-card-actions">
                    <button type="button" className="btn-ghost" onClick={() => setEditingID(editing ? null : search.ID)}>
                      {editing ? "Close" : "Edit"}
                    </button>
                    <button type="button" className="btn-secondary" onClick={() => void toggleSearch(search)}>
                      {search.Enabled ? "Pause" : "Resume"}
                    </button>
                    <button type="button" className="btn-secondary danger" onClick={() => void deleteSearch(search.ID)}>
                      Delete
                    </button>
                  </div>

                  {editing && (
                    <div className="search-card-editor">
                      <SearchConfigForm
                        initialValue={search}
                        mode="edit"
                        onCancel={() => setEditingID(null)}
                        onSaved={(updated) => {
                          const nextSaved = savedSearches.map((item) => (item.ID === updated.ID ? normalizeSearch(updated) : item));
                          setSavedSearches(nextSaved);
                          setGeneratedSearches((prev) => refreshDuplicateFlags(prev, nextSaved));
                          setEditingID(null);
                        }}
                      />
                    </div>
                  )}
                </article>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
