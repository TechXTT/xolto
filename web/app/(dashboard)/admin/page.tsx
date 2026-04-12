"use client";

import { useEffect, useState } from "react";
import { api, AdminAIStats, AdminUser, AdminUsageEntry } from "../../../lib/api";
import { useDashboardContext } from "../../../components/DashboardContext";

type Tab = "overview" | "users" | "usage";

export default function AdminPage() {
  const { user } = useDashboardContext();
  const [tab, setTab] = useState<Tab>("overview");
  const [days, setDays] = useState(30);
  const [stats, setStats] = useState<AdminAIStats | null>(null);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [usageEntries, setUsageEntries] = useState<AdminUsageEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

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
      <div className="page-empty">
        <p>Admin access required.</p>
      </div>
    );
  }

  return (
    <div className="admin-dashboard">
      <div className="admin-header">
        <div className="admin-tabs">
          {(["overview", "users", "usage"] as Tab[]).map((t) => (
            <button
              key={t}
              type="button"
              className={`admin-tab${tab === t ? " active" : ""}`}
              onClick={() => setTab(t)}
            >
              {t.charAt(0).toUpperCase() + t.slice(1)}
            </button>
          ))}
        </div>
        <div className="admin-period">
          <label htmlFor="admin-days">Period</label>
          <select id="admin-days" value={days} onChange={(e) => setDays(Number(e.target.value))}>
            <option value={7}>7 days</option>
            <option value={14}>14 days</option>
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
          </select>
        </div>
      </div>

      {error && <p className="error-banner">{error}</p>}
      {loading && <div className="loading-orb" />}

      {!loading && tab === "overview" && stats && <OverviewTab stats={stats} userCount={users.length} />}
      {!loading && tab === "users" && <UsersTab users={users} />}
      {!loading && tab === "usage" && <UsageTab entries={usageEntries} />}
    </div>
  );
}

function OverviewTab({ stats, userCount }: { stats: AdminAIStats; userCount: number }) {
  return (
    <div className="admin-grid">
      <StatCard label="Total AI calls" value={stats.TotalCalls.toLocaleString()} />
      <StatCard label="Total tokens" value={stats.TotalTokens.toLocaleString()} />
      <StatCard label="Prompt tokens" value={stats.TotalPrompt.toLocaleString()} />
      <StatCard label="Completion tokens" value={stats.TotalCompletion.toLocaleString()} />
      <StatCard label="Failed calls" value={stats.FailedCalls.toLocaleString()} alert={stats.FailedCalls > 0} />
      <StatCard label="Est. cost" value={`$${stats.EstimatedCostUSD.toFixed(4)}`} />
      <StatCard label="Registered users" value={userCount.toLocaleString()} />
      <StatCard
        label="Avg tokens/call"
        value={stats.TotalCalls > 0 ? Math.round(stats.TotalTokens / stats.TotalCalls).toLocaleString() : "0"}
      />
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
    <div className="admin-table-wrap">
      <table className="admin-table">
        <thead>
          <tr>
            <th>Email</th>
            <th>Name</th>
            <th>Tier</th>
            <th>Missions</th>
            <th>Searches</th>
            <th>AI calls</th>
            <th>Tokens</th>
            <th>Joined</th>
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>
                {u.email}
                {u.is_admin && <span className="admin-badge">admin</span>}
              </td>
              <td>{u.name || "---"}</td>
              <td><span className={`tier-badge tier-${u.tier}`}>{u.tier}</span></td>
              <td>{u.mission_count}</td>
              <td>{u.search_count}</td>
              <td>{u.ai_call_count.toLocaleString()}</td>
              <td>{u.ai_tokens.toLocaleString()}</td>
              <td>{new Date(u.created_at).toLocaleDateString()}</td>
            </tr>
          ))}
          {users.length === 0 && (
            <tr><td colSpan={8} className="admin-empty">No users found</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function UsageTab({ entries }: { entries: AdminUsageEntry[] }) {
  return (
    <div className="admin-table-wrap">
      <table className="admin-table">
        <thead>
          <tr>
            <th>Time</th>
            <th>Type</th>
            <th>Model</th>
            <th>Prompt</th>
            <th>Completion</th>
            <th>Total</th>
            <th>Latency</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          {entries.map((e) => (
            <tr key={e.ID} className={e.Success ? "" : "row-error"}>
              <td>{new Date(e.CreatedAt).toLocaleString()}</td>
              <td><span className="call-type-badge">{e.CallType}</span></td>
              <td>{e.Model}</td>
              <td>{e.PromptTokens.toLocaleString()}</td>
              <td>{e.CompletionTokens.toLocaleString()}</td>
              <td>{e.TotalTokens.toLocaleString()}</td>
              <td>{e.LatencyMs}ms</td>
              <td>
                {e.Success ? (
                  <span className="status-ok">OK</span>
                ) : (
                  <span className="status-err" title={e.ErrorMsg}>FAIL</span>
                )}
              </td>
            </tr>
          ))}
          {entries.length === 0 && (
            <tr><td colSpan={8} className="admin-empty">No usage data in this period</td></tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
