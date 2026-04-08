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
        window.location.replace("/feed");
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
          <h1>Find underpriced listings before everyone else does.</h1>
          <p>
            MarktBot watches European marketplaces, scores fresh listings, and helps you turn a vague buying goal into a sharp search workflow.
          </p>
          <div className="hero-actions">
            <Link href="/login" className="btn-primary">
              Sign in
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
              <p>Generate candidate searches from a product idea.</p>
            </div>
            <div className="landing-list-item">
              <span>02</span>
              <p>Track listings across marketplaces and shortlist the best ones.</p>
            </div>
            <div className="landing-list-item">
              <span>03</span>
              <p>Use the assistant to tighten your brief and resume later.</p>
            </div>
          </div>
        </div>
      </section>
    </main>
  );
}
