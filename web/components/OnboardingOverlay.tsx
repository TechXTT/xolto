"use client";

import { useEffect, useState } from "react";

const STORAGE_KEY = "markt_onboarding_completed";

interface Step {
  title: string;
  description: string;
  icon: React.ReactNode;
}

const STEPS: Step[] = [
  {
    title: "Welcome to markt",
    description:
      "Your AI-powered copilot for buying used electronics. We'll walk you through the core workflow so you can start finding deals in minutes.",
    icon: (
      <svg width="32" height="32" viewBox="0 0 24 24" fill="none">
        <path
          d="M12 2l2.5 6.5L21 11l-6.5 2.5L12 20l-2.5-6.5L3 11l6.5-2.5z"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinejoin="round"
        />
      </svg>
    ),
  },
  {
    title: "Create a Mission",
    description:
      "Start by defining what you're looking for — device type, budget, condition, and must-have features. You can use the structured form or just describe it in plain language.",
    icon: (
      <svg width="32" height="32" viewBox="0 0 24 24" fill="none">
        <path d="M12 2.5 14 7.8 19.5 9.5 14 11.2 12 16.5 10 11.2 4.5 9.5l5.5-1.7z" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
        <circle cx="19" cy="18" r="2" fill="currentColor" opacity="0.4" />
        <circle cx="5" cy="19" r="1.5" fill="currentColor" opacity="0.3" />
      </svg>
    ),
  },
  {
    title: "Review Matches",
    description:
      "markt continuously scans marketplaces for listings that fit your mission. Each match gets an AI-generated score, fair price estimate, and risk flags so you can spot the best deals instantly.",
    icon: (
      <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round">
        <circle cx="12" cy="12" r="3" />
        <path d="M12 5a7 7 0 0 1 7 7" />
        <path d="M12 1.5a10.5 10.5 0 0 1 10.5 10.5" opacity="0.4" />
      </svg>
    ),
  },
  {
    title: "Save & Compare",
    description:
      "Shortlist your top picks and compare them side by side — asking price, fair value, suggested offer, risk concerns, and a clear verdict for each listing.",
    icon: (
      <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round">
        <path d="M6 3.5h12a1 1 0 0 1 1 1v15L12 16 5 19.5v-15a1 1 0 0 1 1-1z" />
      </svg>
    ),
  },
  {
    title: "You're all set",
    description:
      "Create your first mission and let markt do the heavy lifting. The AI keeps hunting 24/7 so you never miss a deal.",
    icon: (
      <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
        <path d="M20 6 9 17l-5-5" />
      </svg>
    ),
  },
];

export function OnboardingOverlay({ onComplete }: { onComplete: () => void }) {
  const [step, setStep] = useState(0);
  const [exiting, setExiting] = useState(false);

  function finish() {
    setExiting(true);
    localStorage.setItem(STORAGE_KEY, "1");
    setTimeout(onComplete, 340);
  }

  function next() {
    if (step < STEPS.length - 1) {
      setStep((s) => s + 1);
    } else {
      finish();
    }
  }

  function prev() {
    if (step > 0) setStep((s) => s - 1);
  }

  function skip() {
    finish();
  }

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "ArrowRight" || e.key === "Enter") next();
      if (e.key === "ArrowLeft") prev();
      if (e.key === "Escape") skip();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  const current = STEPS[step];
  const isLast = step === STEPS.length - 1;

  return (
    <div className={`onboarding-backdrop${exiting ? " exit" : ""}`}>
      <div className="onboarding-card" key={step}>
        <div className="onboarding-icon">{current.icon}</div>

        <div className="onboarding-progress">
          {STEPS.map((_, i) => (
            <span key={i} className={`onboarding-dot${i === step ? " active" : ""}${i < step ? " done" : ""}`} />
          ))}
        </div>

        <h2 className="onboarding-title">{current.title}</h2>
        <p className="onboarding-body">{current.description}</p>

        <div className="onboarding-actions">
          {step > 0 && (
            <button type="button" className="btn-ghost" onClick={prev}>
              Back
            </button>
          )}
          <div className="onboarding-spacer" />
          {!isLast && (
            <button type="button" className="btn-ghost" onClick={skip}>
              Skip
            </button>
          )}
          <button type="button" className="btn-primary" onClick={next}>
            {isLast ? "Get started" : "Next"}
          </button>
        </div>
      </div>
    </div>
  );
}

export function shouldShowOnboarding(): boolean {
  if (typeof window === "undefined") return false;
  return localStorage.getItem(STORAGE_KEY) !== "1";
}
