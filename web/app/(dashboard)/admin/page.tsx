"use client";

import { useEffect, useMemo, useState } from "react";
import { api, AdminAIStats, AdminUser, AdminUsageEntry } from "../../../lib/api";
import { useDashboardContext } from "../../../components/DashboardContext";

type Tab = "overview" | "users" | "usage";

const TABS: Array<{ id: Tab; label: string; description: string }> = [
  { id: "overview", label: "Overview", description: "Cost and usage pulse" },
  { id: "users", label: "Users", description: "Accounts and plan mix" },
  { id: "usage", label: "Usage log", description: "Recent model calls" },
];

const PERIOD_OPTIONS = [7, 14, 30, 90];

export default function AdminPage() {
  const { user } = useDashboardContext();
  const [tab, setTab] = useState<Tab>("overview");
  const [days, setDays] = useState(30);
  const [stats, setStats] = useState<AdminAIStats | null>(null);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [usageEntries, setUsageEntries] = useState<AdminUsageEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const activeUsers = useMemo(
    () => users.filter((entry) => entry.mission_count > 0 || entry.search_count > 0).length,
    [users],
  );
  const aiUsers = useMemo(
    () => users.filter((entry) => entry.ai_call_count > 0).length,
    [users],
  );
  const topAIUser = useMemo(
    () => [...users].sort((a, b) => b.ai_tokens - a.ai_tokens)[0] ?? null,
    [users],
  );
  const failureRate = stats && stats.TotalCalls > 0
    ? (stats.FailedCalls / stats.TotalCalls) * 100
    : 0;
  const avgTokensPerCall = stats && stats.TotalCalls > 0
    ? Math.round(stats.TotalTokens / stats.TotalCalls)
    : 0;

  useEffect(() => {
    if (!user?.is_admin) return;
    setLoading(true);
    setError("");

    const promises: Promise<void>[] = [
      api.admin.stats(days).then((res) => setStats(res.stats)),
      api.admin.users().then((res) => setUsers(res.users ?? [])),
    ];
    if (tab === "usage") {
      promises.push(api.admin.usage(Math.min(days, 90)).then((res) => setUsageEntries(res.entries ?? [])));
    }

    Promise.all(promises)
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to load admin data"))
      .finally(() => setLoading(false));
  }, [user, days, tab]);

  if (!user?.is_admin) {
    return (
      <div className="page-stack">
        <section className="surface-panel empty-state">
          <div className="empty-icon" aria-hidden="true">
            <svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="var(--brand-700)" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
              <path d="M12 2v4M12 18v4M4.9 4.9l2.8 2.8M16.3 16.3l2.8 2.8M2 12h4M18 12h4M4.9 19.1l2.8-2.8M16.3 7.7l2.8-2.8" />
              <circle cx="12" cy="12" r="3.5" />
            </svg>
          </div>
          <h3>Admin access required</h3>
          <p>Sign in with an email listed in <code>ADMIN_EMAILS</code> to open this workspace.</p>
        </section>
      </div>
    );
  }

  return (
    <div className="page-stack admin-page">
      <section className="hero-panel compact">
        <div>
          <p className="section-kicker">Admin workspace</p>
          <h2>Monitor AI cost, reliability, and user activity</h2>
          <p className="hero-copy">
            This view follows the same mission dashboard patterns, but pivots the data toward platform health:
            who is active, how much AI is being used, and whether failures or spend are trending up.
          </p>
        </div>
        <div className="stats-row">
          <div className="stat-card">
            <span className="metric-label">Accounts</span>
            <strong>{users.length.toLocaleString()}</strong>
          </div>
          <div className="stat-card">
            <span className="metric-label">Active users</span>
            <strong>{activeUsers.toLocaleString()}</strong>
          </div>
          <div className={`stat-card${stats && stats.FailedCalls > 0 ? "" : " live"}`}>
            <span className="metric-label">AI calls</span>
            <strong>{stats ? stats.TotalCalls.toLocaleString() : "—"}</strong>
          </div>
          <div className="stat-card">
            <span className="metric-label">Est. cost</span>
            <strong>{stats ? formatCost(stats.EstimatedCostUSD) : "—"}</strong>
          </div>
        </div>
      </section>

      <section className="surface-panel admin-toolbar-panel">
        <div className="section-heading">
          <div>
            <p className="section-kicker">Inspect data</p>
            <h3>Switch focus without leaving the dashboard flow</h3>
            <p className="section-support">
              Filter the same reporting window across overview, user accounts, and raw usage events.
            </p>
          </div>
        </div>
        <div className="feed-filter-row">
          <div className="feed-filter-group">
            <span className="feed-filter-label">View</span>
            <div className="feed-pill-group">
              {TABS.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  className={`feed-pill${tab === item.id ? " active" : ""}`}
                  onClick={() => setTab(item.id)}
                  title={item.description}
                >
                  {item.label}
                </button>
              ))}
            </div>
          </div>
          <label className="admin-period-picker">
            <span className="feed-filter-label">Period</span>
            <select className="input" value={days} onChange={(e) => setDays(Number(e.target.value))}>
              {PERIOD_OPTIONS.map((value) => (
                <option key={value} value={value}>
                  Last {value} days
                </option>
              ))}
            </select>
          </label>
        </div>
      </section>

      {error && <p className="error-msg">{error}</p>}

      {loading ? (
        <section className="surface-panel empty-state admin-loading-panel">
          <div className="loading-orb" />
          <p>Loading admin analytics…</p>
        </section>
      ) : (
        <>
          {tab === "overview" && stats && (
            <OverviewTab
              stats={stats}
              activeUsers={activeUsers}
              aiUsers={aiUsers}
              topAIUser={topAIUser}
              failureRate={failureRate}
              avgTokensPerCall={avgTokensPerCall}
            />
          )}
          {tab === "users" && <UsersTab users={users} />}
          {tab === "usage" && <UsageTab entries={usageEntries} />}
        </>
      )}
    </div>
  );
}

function OverviewTab({
  stats,
  activeUsers,
  aiUsers,
  topAIUser,
  failureRate,
  avgTokensPerCall,
}: {
  stats: AdminAIStats;
  activeUsers: number;
  aiUsers: number;
  topAIUser: AdminUser | null;
  failureRate: number;
  avgTokensPerCall: number;
}) {
  return (
    <div className="admin-content-stack">
      <section className="surface-panel">
        <div className="section-heading">
          <div>
            <p className="section-kicker">Overview</p>
            <h3>Usage pulse</h3>
            <p className="section-support">
              High-level AI volume, spend, and reliability for the selected period.
            </p>
          </div>
        </div>
        <div className="admin-metric-grid">
          <StatCard label="Total AI calls" value={stats.TotalCalls.toLocaleString()} />
          <StatCard label="Total tokens" value={stats.TotalTokens.toLocaleString()} />
          <StatCard label="Prompt tokens" value={stats.TotalPrompt.toLocaleString()} />
          <StatCard label="Completion tokens" value={stats.TotalCompletion.toLocaleString()} />
          <StatCard label="Failed calls" value={stats.FailedCalls.toLocaleString()} alert={stats.FailedCalls > 0} />
          <StatCard label="Avg tokens / call" value={avgTokensPerCall.toLocaleString()} />
        </div>
      </section>

      <div className="admin-note-grid">
        <article className="surface-panel admin-note-card">
          <div className="admin-note-head">
            <div>
              <p className="section-kicker">Adoption</p>
              <h3>Who is actually using the system</h3>
            </div>
            <span className="success-badge">{aiUsers} AI users</span>
          </div>
          <p className="section-support">
            {activeUsers > 0
              ? `${activeUsers} accounts created missions or searches in this workspace. ${aiUsers} of them triggered AI-assisted flows during the selected window.`
              : "No active missions or AI activity showed up in the selected period."}
          </p>
          <div className="admin-note-meta">
            <span className="subtle-pill">Cost {formatCost(stats.EstimatedCostUSD)}</span>
            <span className="subtle-pill">Failure rate {failureRate.toFixed(1)}%</span>
          </div>
        </article>

        <article className="surface-panel admin-note-card">
          <div className="admin-note-head">
            <div>
              <p className="section-kicker">Heavy usage</p>
              <h3>Top AI consumer</h3>
            </div>
            {topAIUser?.is_admin && <span className="warning-badge">Admin account</span>}
          </div>
          {topAIUser ? (
            <>
              <p className="admin-spotlight-name">{topAIUser.name || topAIUser.email}</p>
              <p className="section-support">{topAIUser.email}</p>
              <div className="admin-note-meta">
                <span className="subtle-pill">{topAIUser.ai_call_count.toLocaleString()} calls</span>
                <span className="subtle-pill">{topAIUser.ai_tokens.toLocaleString()} tokens</span>
                <span className={`tier-badge tier-${topAIUser.tier}`}>{topAIUser.tier}</span>
              </div>
            </>
          ) : (
            <p className="section-support">No AI usage has been recorded yet.</p>
          )}
        </article>
      </div>
    </div>
  );
}

function StatCard({ label, value, alert }: { label: string; value: string; alert?: boolean }) {
  return (
    <div className={`admin-stat-card${alert ? " alert" : ""}`}>
      <p className="admin-stat-value">{value}</p>
      <p className="admin-stat-label">{label}</p>
    </div>
  );
}

function UsersTab({ users }: { users: AdminUser[] }) {
  return (
    <section className="surface-panel">
      <div className="section-heading">
        <div>
          <p className="section-kicker">Users</p>
          <h3>Account activity and plan mix</h3>
          <p className="section-support">
            Newest accounts first, with mission/search footprint and AI usage rolled into a single row.
          </p>
        </div>
        <div className="admin-note-meta">
          <span className="subtle-pill">{users.length} accounts</span>
          <span className="subtle-pill">{users.filter((entry) => entry.is_admin).length} admins</span>
        </div>
      </div>

      <div className="admin-table-wrap">
        <table className="admin-table">
          <thead>
            <tr>
              <th>Account</th>
              <th>Plan</th>
              <th>Activity</th>
              <th>AI usage</th>
              <th>Joined</th>
            </tr>
          </thead>
          <tbody>
            {users.map((entry) => (
              <tr key={entry.id}>
                <td>
                  <div className="admin-user-cell">
                    <strong className="admin-user-name">{entry.name || "Unnamed user"}</strong>
                    <span className="admin-user-meta">
                      {entry.email}
                      {entry.is_admin && <span className="admin-badge">admin</span>}
                    </span>
                  </div>
                </td>
                <td><span className={`tier-badge tier-${entry.tier}`}>{entry.tier}</span></td>
                <td>
                  <div className="admin-stack-cell">
                    <strong>{entry.mission_count}</strong>
                    <span>{entry.search_count} searches</span>
                  </div>
                </td>
                <td>
                  <div className="admin-stack-cell">
                    <strong>{entry.ai_call_count.toLocaleString()} calls</strong>
                    <span>{entry.ai_tokens.toLocaleString()} tokens</span>
                  </div>
                </td>
                <td>{new Date(entry.created_at).toLocaleDateString()}</td>
              </tr>
            ))}
            {users.length === 0 && (
              <tr><td colSpan={5} className="admin-empty">No users found</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function UsageTab({ entries }: { entries: AdminUsageEntry[] }) {
  return (
    <section className="surface-panel">
      <div className="section-heading">
        <div>
          <p className="section-kicker">Usage log</p>
          <h3>Recent model calls</h3>
          <p className="section-support">
            Raw call stream for debugging spikes, latency issues, and error bursts.
          </p>
        </div>
        <div className="admin-note-meta">
          <span className="subtle-pill">{entries.length} events</span>
          <span className="subtle-pill">{entries.filter((entry) => !entry.Success).length} failures</span>
        </div>
      </div>

      <div className="admin-table-wrap">
        <table className="admin-table">
          <thead>
            <tr>
              <th>Time</th>
              <th>Call</th>
              <th>User</th>
              <th>Tokens</th>
              <th>Latency</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((entry) => (
              <tr key={entry.ID} className={entry.Success ? "" : "row-error"}>
                <td>{new Date(entry.CreatedAt).toLocaleString()}</td>
                <td>
                  <div className="admin-stack-cell">
                    <strong>{formatCallType(entry.CallType)}</strong>
                    <span>{entry.Model}</span>
                  </div>
                </td>
                <td><code className="admin-inline-code">{entry.UserID || "system"}</code></td>
                <td>
                  <div className="admin-stack-cell">
                    <strong>{entry.TotalTokens.toLocaleString()}</strong>
                    <span>{entry.PromptTokens.toLocaleString()} in / {entry.CompletionTokens.toLocaleString()} out</span>
                  </div>
                </td>
                <td>{entry.LatencyMs}ms</td>
                <td>
                  {entry.Success ? (
                    <span className="success-badge">OK</span>
                  ) : (
                    <span className="warning-badge" title={entry.ErrorMsg || "Unknown error"}>Failed</span>
                  )}
                </td>
              </tr>
            ))}
            {entries.length === 0 && (
              <tr><td colSpan={6} className="admin-empty">No usage data in this period</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function formatCost(value: number) {
  if (value === 0) return "$0.00";
  return `$${value < 0.01 ? value.toFixed(4) : value.toFixed(2)}`;
}

function formatCallType(value: string) {
  return value
    .split("_")
    .filter(Boolean)
    .map((part) => part[0]?.toUpperCase() + part.slice(1))
    .join(" ");
}
