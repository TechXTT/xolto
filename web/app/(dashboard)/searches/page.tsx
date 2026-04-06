"use client";

import { useEffect, useState } from "react";

import { SearchConfigForm } from "../../../components/SearchConfigForm";
import { api, SearchSpec } from "../../../lib/api";

export default function SearchesPage() {
  const [searches, setSearches] = useState<SearchSpec[]>([]);
  const [error, setError] = useState("");
  const [topic, setTopic] = useState("");
  const [generating, setGenerating] = useState(false);

  async function load() {
    try {
      const res = await api.searches.list();
      setSearches(res.searches);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load searches");
    }
  }

  useEffect(() => { void load(); }, []);

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Searches</h1>

      {error && <p className="error-msg mb-4">{error}</p>}

      {/* AI generator */}
      <div className="card p-5 mb-6">
        <h3 className="text-sm font-semibold text-gray-900 mb-1">Generate with AI</h3>
        <p className="text-xs text-gray-400 mb-3">
          Describe what you&apos;re hunting and get search configurations generated automatically.
        </p>
        <div className="flex gap-2">
          <input
            className="input flex-1"
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            placeholder="sony cameras, vintage jackets, mirrorless lenses…"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                void handleGenerate();
              }
            }}
          />
          <button
            type="button"
            disabled={generating || !topic.trim()}
            className="btn-primary"
            onClick={() => void handleGenerate()}
          >
            {generating ? "Generating…" : "Generate"}
          </button>
        </div>
      </div>

      {/* New search form */}
      <div className="mb-6">
        <SearchConfigForm onSaved={(search) => setSearches((prev) => [search, ...prev])} />
      </div>

      {/* Search list */}
      {searches?.length > 0 && (
        <div className="space-y-3">
          {searches.map((search, idx) => (
            <div key={search.ID ?? idx} className="card p-4 flex items-center gap-4">
              <div className="flex-1 min-w-0">
                <p className="font-medium text-sm text-gray-900 truncate">{search.Name}</p>
                <p className="text-xs text-gray-400 truncate">
                  {search.Query} · {search.MarketplaceID}
                  {search.MaxPrice > 0 ? ` · max €${(search.MaxPrice / 100).toFixed(0)}` : ""}
                </p>
              </div>
              <div className="flex gap-2 shrink-0">
                {!search.ID ? (
                  <button
                    type="button"
                    className="btn-primary text-xs"
                    onClick={async () => {
                      const created = await api.searches.create(search);
                      setSearches((prev) => prev.map((item) => (item === search ? created : item)));
                    }}
                  >
                    Save
                  </button>
                ) : (
                  <button
                    type="button"
                    className="btn-secondary text-xs"
                    onClick={async () => { await api.searches.run(search.ID); }}
                  >
                    Run now
                  </button>
                )}
                <button
                  type="button"
                  className="btn-danger text-xs"
                  onClick={async () => {
                    if (search.ID) await api.searches.delete(search.ID);
                    setSearches((prev) => prev.filter((item) => item !== search));
                  }}
                >
                  {search.ID ? "Delete" : "Dismiss"}
                </button>
              </div>
            </div>
          ))}
        </div>
      )}

      {searches?.length === 0 && !error && (
        <p className="text-sm text-gray-400 text-center py-8">
          No searches yet. Create one above or generate with AI.
        </p>
      )}
    </div>
  );

  async function handleGenerate() {
    setError("");
    setGenerating(true);
    try {
      const res = await api.searches.generate(topic.trim());
      setSearches((prev) => [...(res.searches as unknown as SearchSpec[]), ...prev]);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to generate searches");
    } finally {
      setGenerating(false);
    }
  }
}
