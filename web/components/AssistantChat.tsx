"use client";

import Link from "next/link";
import { useEffect, useRef, useState } from "react";

import { api, AssistantReply, Mission, Recommendation } from "../lib/api";
import { formatEuroFromCents } from "../lib/format";

type Message = {
  role: "user" | "assistant";
  text: string;
  mission?: Mission | null;
  recommendations?: Recommendation[];
};

const PROMPTS = [
  "I want a Sony A6700, good condition, under EUR900",
  "Looking for a MacBook Pro 14 M3, like new, max EUR1600",
  "Canon RF 50mm f/1.8 lens, any condition, under EUR220",
  "Gaming laptop RTX 4060, good condition, budget EUR750",
];

const LABEL_COPY: Record<string, { label: string; color: string; bg: string }> = {
  buy_now:        { label: "Buy now",     color: "var(--brand-700)",   bg: "var(--brand-100)"          },
  worth_watching: { label: "Watch",       color: "var(--warning-500)", bg: "rgba(245,158,11,0.10)"     },
  ask_questions:  { label: "Ask first",   color: "var(--fg-500)",      bg: "rgba(10,26,18,0.06)"       },
  skip:           { label: "Skip",        color: "var(--danger-600)",  bg: "rgba(220,38,38,0.08)"      },
};

function AIAvatar() {
  return (
    <div className="ai-avatar" aria-hidden>
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none">
        <path d="M12 2l2.5 6.5L21 11l-6.5 2.5L12 20l-2.5-6.5L3 11l6.5-2.5z" stroke="currentColor" strokeWidth="2" strokeLinejoin="round" />
      </svg>
    </div>
  );
}

function BriefProgress({ mission }: { mission: Mission }) {
  const fields: { label: string; value: string }[] = [];
  if (mission.Name) fields.push({ label: "Mission", value: mission.Name });
  if ((mission.BudgetMax ?? 0) > 0) fields.push({ label: "Budget", value: `€${mission.BudgetMax}` });
  if (mission.PreferredCondition?.length) fields.push({ label: "Condition", value: mission.PreferredCondition.join(", ") });
  if ((mission.SearchQueries?.length ?? 0) > 0)
    fields.push({ label: "Queries", value: `${mission.SearchQueries!.length} search term${mission.SearchQueries!.length === 1 ? "" : "s"}` });

  if (fields.length === 0) return null;

  return (
    <div className="brief-card">
      <p className="brief-card-label">Mission draft</p>
      <div className="brief-fields">
        {fields.map((f) => (
          <div key={f.label} className="brief-field">
            <span className="brief-field-key">{f.label}</span>
            <span className="brief-field-val">{f.value}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function RecCard({ rec }: { rec: Recommendation }) {
  const cfg = LABEL_COPY[rec.Label] ?? LABEL_COPY.skip;
  return (
    <a
      href={rec.Listing.URL || "#"}
      target="_blank"
      rel="noopener noreferrer"
      className="rec-card"
    >
      <div className="rec-card-top">
        <span className="rec-card-title">{rec.Listing.Title}</span>
        <span className="rec-badge" style={{ color: cfg.color, background: cfg.bg }}>
          {cfg.label}
        </span>
      </div>
      <div className="rec-card-prices">
        <strong>{formatEuroFromCents(rec.Listing.Price)}</strong>
        {(rec.Scored?.FairPrice ?? 0) > 0 && (
          <span className="rec-fair">fair value {formatEuroFromCents(rec.Scored!.FairPrice)}</span>
        )}
        {(rec.SuggestedOffer ?? 0) > 0 && (
          <span className="rec-offer">offer {formatEuroFromCents(rec.SuggestedOffer!)}</span>
        )}
      </div>
      {rec.Verdict && <p className="rec-verdict">{rec.Verdict}</p>}
      {rec.Concerns?.[0] && <p className="rec-concern">{rec.Concerns[0]}</p>}
    </a>
  );
}

export function AssistantChat({
  embedded = false,
  onMissionCreated,
}: {
  embedded?: boolean;
  onMissionCreated?: (mission: Mission) => void;
}) {
  const [message, setMessage] = useState("");
  const [history, setHistory] = useState<Message[]>([]);
  const [loading, setLoading] = useState(false);
  const [hydrating, setHydrating] = useState(true);
  const [error, setError] = useState("");
  const [draftHint, setDraftHint] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let cancelled = false;

    async function hydrateSession() {
      try {
        const res = await api.assistant.session();
        if (cancelled || !res.session) return;
        if (res.session.LastAssistantMsg) {
          setHistory([
            {
              role: "assistant",
              text: res.session.LastAssistantMsg,
              mission: res.session.DraftMission,
            },
          ]);
        }
        if (res.session.DraftMission?.Name) {
          setDraftHint(`Resuming: ${res.session.DraftMission.Name}`);
        } else if (res.session.PendingQuestion) {
          setDraftHint("Continuing your mission");
        }
      } catch {
        // Keep the assistant usable even if session hydration fails.
      } finally {
        if (!cancelled) setHydrating(false);
      }
    }

    void hydrateSession();
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [history, loading]);

  async function sendMessage() {
    const trimmed = message.trim();
    if (!trimmed || loading) return;

    setError("");
    setHistory((prev) => [...prev, { role: "user", text: trimmed }]);
    setMessage("");
    setLoading(true);

    try {
      const reply: AssistantReply = await api.assistant.converse(trimmed);
      setHistory((prev) => [
        ...prev,
        {
          role: "assistant",
          text: reply.Message,
          mission: reply.Mission,
          recommendations: reply.Recommendations,
        },
      ]);
      if (reply.Mission && !reply.Expecting) {
        onMissionCreated?.(reply.Mission);
      }
      setDraftHint(
        reply.Mission?.Name
          ? `Working mission: ${reply.Mission.Name}`
          : reply.Expecting
            ? "Tell me more…"
            : ""
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Something went wrong — try again.");
    } finally {
      setLoading(false);
      inputRef.current?.focus();
    }
  }

  const content = (
    <section className="assistant-shell">
        {/* Header */}
        <div className="assistant-header">
          <div>
            <p className="section-kicker">{embedded ? "Describe your mission" : "AI buying assistant"}</p>
            <h2>{embedded ? "Create a buy mission with natural language" : "Your personal shopper, powered by AI"}</h2>
          </div>
          <div className="assistant-header-right">
            {draftHint && <span className="topbar-chip">{draftHint}</span>}
            {!embedded && history.length > 0 && (
              <Link href="/matches" className="btn-ghost" style={{ fontSize: "0.84rem", minHeight: 36 }}>
                View matches
              </Link>
            )}
          </div>
        </div>

        {/* Chat card */}
        <div className="assistant-card">
          <div className="assistant-stream">
            {hydrating ? (
              <div className="assistant-empty">
                <div className="loading-orb" />
                <p>Loading your previous session…</p>
              </div>
            ) : history.length === 0 ? (
              <div className="assistant-empty">
                <div className="assistant-welcome">
                  <AIAvatar />
                  <div>
                    <h3>Hi, I'm your personal buyer.</h3>
                    <p>
                      Tell me what you're after — item, budget, condition. I'll do the hunting and tell you which
                      listings are actually worth your time.
                    </p>
                  </div>
                </div>

                <div className="assistant-prompts">
                  {PROMPTS.map((prompt) => (
                    <button
                      key={prompt}
                      type="button"
                      className="prompt-pill"
                      onClick={() => { setMessage(prompt); inputRef.current?.focus(); }}
                    >
                      {prompt}
                    </button>
                  ))}
                </div>
              </div>
            ) : (
              history.map((item, index) => (
                <div key={`${item.role}-${index}`} className={`assistant-row ${item.role}`}>
                  {item.role === "assistant" && <AIAvatar />}
                  <div className="assistant-bubble-group">
                    <div className={`assistant-bubble ${item.role}`}>{item.text}</div>

                    {/* Brief progress tracker */}
                    {item.role === "assistant" && item.mission && (item.mission.BudgetMax ?? 0) > 0 && (
                      <BriefProgress mission={item.mission} />
                    )}

                    {/* Inline recommendation cards */}
                    {item.role === "assistant" && item.recommendations && item.recommendations.length > 0 && (
                      <>
                        <div className="rec-list">
                          {item.recommendations.map((rec) => (
                            <RecCard key={rec.Listing.ItemID} rec={rec} />
                          ))}
                        </div>
                        <div className="chat-feed-cta">
                          <p>Your monitors are scanning. New deals appear in real time.</p>
                          <Link href="/matches" className="btn-primary" style={{ fontSize: "0.84rem" }}>
                            Open matches
                          </Link>
                        </div>
                      </>
                    )}
                  </div>
                </div>
              ))
            )}

            {loading && (
              <div className="assistant-row assistant">
                <AIAvatar />
                <div className="assistant-bubble assistant assistant-typing">
                  <span />
                  <span />
                  <span />
                </div>
              </div>
            )}
            <div ref={bottomRef} />
          </div>

          {error && <div className="error-msg assistant-error">{error}</div>}

          <div className="assistant-composer">
            <input
              ref={inputRef}
              className="input"
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              placeholder="What are you buying? Item, budget, condition…"
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  void sendMessage();
                }
              }}
              disabled={loading}
              autoFocus
            />
            <button
              type="button"
              className="btn-primary"
              onClick={() => void sendMessage()}
              disabled={loading || !message.trim()}
            >
              {loading ? "Thinking…" : "Send"}
            </button>
          </div>
        </div>
      </section>
  );

  if (embedded) {
    return content;
  }

  return (
    <div className="page-stack">
      {content}
    </div>
  );
}
