"use client";

import { useMemo, useState } from "react";

import { api, Mission } from "../lib/api";

const CATEGORY_SUGGESTIONS: Record<string, string[]> = {
  phone: ["128GB+", "battery health 85%+", "factory unlocked"],
  laptop: ["16GB RAM+", "battery cycle count", "no dead pixels"],
  camera: ["low shutter count", "sensor clean", "original charger"],
  other: [],
};

const CONDITIONS = ["new", "like_new", "good", "fair"];

export function MissionForm({
  onCreated,
  onCancel,
}: {
  onCreated?: (mission: Mission) => void;
  onCancel?: () => void;
}) {
  const [category, setCategory] = useState<"phone" | "laptop" | "camera" | "other">("phone");
  const [brand, setBrand] = useState("");
  const [model, setModel] = useState("");
  const [budgetMax, setBudgetMax] = useState(900);
  const [conditions, setConditions] = useState<string[]>(["like_new", "good"]);
  const [mustHaveInput, setMustHaveInput] = useState("");
  const [mustHaves, setMustHaves] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const missionName = useMemo(() => {
    return [brand.trim(), model.trim()].filter(Boolean).join(" ").trim();
  }, [brand, model]);

  function toggleCondition(value: string) {
    setConditions((prev) => {
      if (prev.includes(value)) return prev.filter((v) => v !== value);
      return [...prev, value];
    });
  }

  function addMustHave(value: string) {
    const normalized = value.trim();
    if (!normalized) return;
    setMustHaves((prev) => (prev.includes(normalized) ? prev : [...prev, normalized]));
    setMustHaveInput("");
  }

  async function submit() {
    if (!missionName) {
      setError("Add a brand and model first.");
      return;
    }
    setError("");
    setLoading(true);
    try {
      const query = missionName.toLowerCase();
      const mission = await api.missions.create({
        Name: missionName,
        TargetQuery: query,
        BudgetMax: budgetMax,
        BudgetStretch: Math.round(budgetMax * 1.1),
        PreferredCondition: conditions,
        RequiredFeatures: mustHaves,
        SearchQueries: [query, model.toLowerCase().trim(), `${brand.toLowerCase().trim()} ${category}`].filter(Boolean),
        Status: "active",
        Urgency: "flexible",
        Category: category,
      });
      onCreated?.(mission);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create mission");
    } finally {
      setLoading(false);
    }
  }

  return (
    <section className="surface-panel">
      <div className="section-heading">
        <div>
          <p className="section-kicker">Structured mission</p>
          <h3>Tell xolto exactly what to hunt</h3>
        </div>
      </div>

      {error && <div className="error-msg">{error}</div>}

      <div className="feed-pill-group" style={{ marginBottom: 14 }}>
        {(["phone", "laptop", "camera", "other"] as const).map((value) => (
          <button
            key={value}
            type="button"
            className={`feed-pill${category === value ? " active" : ""}`}
            onClick={() => setCategory(value)}
          >
            {value}
          </button>
        ))}
      </div>

      <div className="generator-bar">
        <input className="input" placeholder="Brand (Sony, Apple, Dell)" value={brand} onChange={(e) => setBrand(e.target.value)} />
        <input className="input" placeholder="Model (A6700, iPhone 15 Pro)" value={model} onChange={(e) => setModel(e.target.value)} />
      </div>

      <div style={{ marginTop: 14 }}>
        <label className="feed-filter-label">Budget (EUR): {budgetMax}</label>
        <input
          type="range"
          min={100}
          max={4000}
          step={25}
          value={budgetMax}
          onChange={(e) => setBudgetMax(Number(e.target.value))}
          style={{ width: "100%" }}
        />
      </div>

      <div className="feed-filter-group" style={{ marginTop: 14 }}>
        <label className="feed-filter-label">Condition preference</label>
        <div className="feed-pill-group">
          {CONDITIONS.map((value) => (
            <button
              key={value}
              type="button"
              className={`feed-pill${conditions.includes(value) ? " active" : ""}`}
              onClick={() => toggleCondition(value)}
            >
              {value}
            </button>
          ))}
        </div>
      </div>

      <div className="feed-filter-group" style={{ marginTop: 14 }}>
        <label className="feed-filter-label">Must-haves</label>
        <div className="generator-bar">
          <input
            className="input"
            placeholder="Type a must-have and press Enter"
            value={mustHaveInput}
            onChange={(e) => setMustHaveInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addMustHave(mustHaveInput);
              }
            }}
          />
          <button type="button" className="btn-secondary" onClick={() => addMustHave(mustHaveInput)}>
            Add
          </button>
        </div>
        <div className="feed-pill-group" style={{ marginTop: 10 }}>
          {CATEGORY_SUGGESTIONS[category].map((suggestion) => (
            <button key={suggestion} type="button" className="feed-pill" onClick={() => addMustHave(suggestion)}>
              + {suggestion}
            </button>
          ))}
          {mustHaves.map((value) => (
            <button key={value} type="button" className="feed-pill active" onClick={() => setMustHaves((prev) => prev.filter((v) => v !== value))}>
              {value} ×
            </button>
          ))}
        </div>
      </div>

      <div className="hero-actions" style={{ marginTop: 16 }}>
        <button type="button" className="btn-primary" onClick={() => void submit()} disabled={loading}>
          {loading ? "Creating…" : "Start mission"}
        </button>
        {onCancel && (
          <button type="button" className="btn-ghost" onClick={onCancel}>
            Cancel
          </button>
        )}
      </div>
    </section>
  );
}
