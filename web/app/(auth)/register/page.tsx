"use client";

import Link from "next/link";
import { useState } from "react";

import { api } from "../../../lib/api";

export default function RegisterPage() {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      await api.auth.register(email, password, name);
      window.location.replace("/feed");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Registration failed");
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="landing-shell">
    <div className="auth-shell">
      <section className="auth-panel auth-panel-dark">
        <span className="landing-kicker">Create your workspace</span>
        <h1>Set up a premium deal-hunting cockpit in minutes.</h1>
        <p>
          Start with a few searches, let the assistant sharpen your buying brief, and build a shortlist of the listings worth acting on.
        </p>
        <p className="auth-panel-switch">
          Already have an account?{" "}
          <Link href="/login" className="auth-panel-link">Sign in</Link>
        </p>
      </section>

      <section className="auth-panel">
        <div className="auth-card">
          <div className="section-heading">
            <div>
              <p className="section-kicker">Create account</p>
              <h2>Start tracking better deals</h2>
            </div>
          </div>

          {error && <div className="error-msg">{error}</div>}

          <form onSubmit={onSubmit} className="auth-form">
            <div className="input-stack">
              <label htmlFor="name" className="label">
                Name
              </label>
              <input
                id="name"
                type="text"
                className="input"
                placeholder="Your name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoComplete="name"
                required
                autoFocus
                disabled={loading}
              />
            </div>

            <div className="input-stack">
              <label htmlFor="email" className="label">
                Email
              </label>
              <input
                id="email"
                type="email"
                className="input"
                placeholder="you@example.com"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoComplete="email"
                required
                disabled={loading}
              />
            </div>

            <div className="input-stack">
              <label htmlFor="password" className="label">
                Password
              </label>
              <input
                id="password"
                type="password"
                className="input"
                placeholder="Minimum 8 characters"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                required
                minLength={8}
                disabled={loading}
              />
            </div>

            <button type="submit" disabled={loading} className="btn-primary auth-submit">
              {loading ? "Creating account…" : "Create account"}
            </button>
          </form>

          <p className="auth-footer">
            Already have an account?{" "}
            <Link href="/login">
              Sign in
            </Link>
          </p>
        </div>
      </section>
    </div>
    </main>
  );
}
