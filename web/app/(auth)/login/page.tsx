"use client";

import Link from "next/link";
import { useState } from "react";

import { api } from "../../../lib/api";

export default function LoginPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function onSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      await api.auth.login(email, password);
      window.location.replace("/missions");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="landing-shell">
    <div className="auth-shell">
      <section className="auth-panel auth-panel-dark">
        <span className="landing-kicker">Welcome back</span>
        <h1>Log in and pick up where your market watch left off.</h1>
        <p>
          xolto keeps your missions, matches, saved comparisons, and seller drafts in sync so you can jump straight back into the buying loop.
        </p>
        <p className="auth-panel-switch">
          No account yet?{" "}
          <Link href="/register" className="auth-panel-link">Create one free</Link>
        </p>
      </section>

      <section className="auth-panel">
        <div className="auth-card">
          <div className="section-heading">
            <div>
              <p className="section-kicker">Sign in</p>
              <h2>Access your workspace</h2>
            </div>
          </div>

          {error && <div className="error-msg">{error}</div>}

          <form onSubmit={onSubmit} className="auth-form">
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
                autoFocus
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
                placeholder="Enter your password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
                disabled={loading}
              />
            </div>

            <button type="submit" disabled={loading} className="btn-primary auth-submit">
              {loading ? "Signing in…" : "Sign in"}
            </button>
          </form>

          <p className="auth-footer">
            No account yet?{" "}
            <Link href="/register">
              Create one
            </Link>
          </p>
        </div>
      </section>
    </div>
    </main>
  );
}
