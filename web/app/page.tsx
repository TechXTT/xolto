"use client";

import Link from "next/link";
import { useEffect, useState } from "react";

import { api } from "../lib/api";

export default function HomePage() {
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    api.auth
      .me()
      .then(() => {
        window.location.replace("/missions");
      })
      .catch(() => {
        setChecking(false);
      });
  }, []);

  if (checking) {
    return (
      <main className="fullscreen-shell landing-loading">
        <div className="loading-orb" />
        <p className="loading-copy">Opening MarktBot…</p>
      </main>
    );
  }

  return (
    <main className="landing-shell">
      <section className="landing-hero">
        <div className="landing-copy">
          <span className="landing-kicker">Premium marketplace intelligence</span>
          <h1>Buy used electronics without overpaying.</h1>
          <p>
            markt scans second-hand listings, estimates fair value, flags risks, and tells you exactly which sellers to contact first.
          </p>
          <div className="hero-actions">
            <Link href="/login" className="btn-primary">
              Start a buy mission
            </Link>
            <Link href="/register" className="btn-secondary">
              Create account
            </Link>
          </div>
        </div>

        <div className="landing-panel">
          <div className="landing-panel-top">
            <span className="market-badge">Live marketplace pulse</span>
            <strong>Fresh opportunities, ranked and ready</strong>
          </div>
          <div className="landing-metrics">
            <div className="stat-card">
              <span className="metric-label">Searches monitored</span>
              <strong>Up to 50</strong>
            </div>
            <div className="stat-card">
              <span className="metric-label">Assistant support</span>
              <strong>Brief-aware</strong>
            </div>
            <div className="stat-card live">
              <span className="metric-label">Signal</span>
              <strong>Live feed</strong>
            </div>
          </div>
          <div className="landing-list">
            <div className="landing-list-item">
              <span>01</span>
              <p>Create a mission for phones, laptops, or cameras.</p>
            </div>
            <div className="landing-list-item">
              <span>02</span>
              <p>Track matches with fair-value and risk analysis.</p>
            </div>
            <div className="landing-list-item">
              <span>03</span>
              <p>Compare saved options and message sellers with confidence.</p>
            </div>
          </div>
        </div>
      </section>
    </main>
  );
}
