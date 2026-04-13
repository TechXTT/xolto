"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type { ReactNode } from "react";
import { useEffect, useState } from "react";

import { DashboardProvider } from "../../components/DashboardContext";
import { OnboardingOverlay, shouldShowOnboarding } from "../../components/OnboardingOverlay";
import { api, Mission, ShortlistEntry, User } from "../../lib/api";

function IconAI() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
      <path d="M8 1.5 9.3 5.2 13 6.5 9.3 7.8 8 11.5 6.7 7.8 3 6.5l3.7-1.3z" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <circle cx="13" cy="12" r="1.5" fill="currentColor" opacity="0.5" />
      <circle cx="3.5" cy="12.5" r="1" fill="currentColor" opacity="0.4" />
    </svg>
  );
}

function IconRadar() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
      <circle cx="8" cy="8" r="2" />
      <path d="M8 3a5 5 0 0 1 5 5" />
      <path d="M8 0.5a7.5 7.5 0 0 1 7.5 7.5" opacity="0.5" />
      <circle cx="8" cy="8" r="2" fill="currentColor" opacity="0.15" />
    </svg>
  );
}

function IconSaved() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round">
      <path d="M4 2.5h8a.5.5 0 0 1 .5.5v10.5L8 11 3.5 13.5V3a.5.5 0 0 1 .5-.5z" />
    </svg>
  );
}

function IconSettings() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
      <path d="M2 5h12M2 11h12" />
      <circle cx="5.5" cy="5" r="1.5" fill="currentColor" stroke="none" />
      <circle cx="10.5" cy="11" r="1.5" fill="currentColor" stroke="none" />
    </svg>
  );
}

function IconAdmin() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
      <path d="M8 1v2M8 13v2M1 8h2M13 8h2" />
      <circle cx="8" cy="8" r="3" />
      <path d="M4.2 4.2l1.4 1.4M10.4 10.4l1.4 1.4M4.2 11.8l1.4-1.4M10.4 5.6l1.4-1.4" opacity="0.5" />
    </svg>
  );
}

const NAV = [
  { href: "/missions", label: "Missions", description: "Define what to buy", Icon: IconAI },
  { href: "/matches", label: "Matches", description: "Mission-scoped deals", Icon: IconRadar },
  { href: "/saved", label: "Saved", description: "Compare top picks", Icon: IconSaved },
  { href: "/settings", label: "Settings", description: "Account and billing", Icon: IconSettings },
];

const ADMIN_NAV = { href: "/admin", label: "Admin", description: "Usage & users", Icon: IconAdmin };

function initialsForUser(user: User | null) {
  if (!user?.name) return user?.email.slice(0, 1).toUpperCase() || "?";
  return user.name
    .split(" ")
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}

function normalizeShortlist(items: ShortlistEntry[]) {
  return items.filter((item) => item.Status !== "removed");
}

function resolveActiveMissionID(missions: Mission[], currentID: number, fallbackStoredID = 0) {
  if (missions.length === 0) return 0;
  const preferred = currentID > 0 ? currentID : fallbackStoredID;
  if (preferred > 0 && missions.some((mission) => mission.ID === preferred)) {
    return preferred;
  }
  return missions[0].ID ?? 0;
}

export default function DashboardLayout({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [missions, setMissions] = useState<Mission[]>([]);
  const [activeMissionId, setActiveMissionId] = useState(0);
  const [shortlist, setShortlist] = useState<ShortlistEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [menuOpen, setMenuOpen] = useState(false);
  const [showOnboarding, setShowOnboarding] = useState(false);
  const pathname = usePathname();

  useEffect(() => {
    if (typeof window === "undefined") return;
    const raw = window.localStorage.getItem("markt_active_mission_id");
    if (!raw) return;
    const parsed = Number(raw);
    if (Number.isFinite(parsed) && parsed > 0) {
      setActiveMissionId(parsed);
    }
  }, []);

  useEffect(() => {
    if (typeof window === "undefined") return;
    if (activeMissionId > 0) {
      window.localStorage.setItem("markt_active_mission_id", String(activeMissionId));
    } else {
      window.localStorage.removeItem("markt_active_mission_id");
    }
  }, [activeMissionId]);

  useEffect(() => {
    let cancelled = false;

    async function bootstrap() {
      try {
        const [me, shortlistRes, missionsRes] = await Promise.all([
          api.auth.me(),
          api.shortlist.get().catch(() => ({ shortlist: [] as ShortlistEntry[] })),
          api.missions.list().catch(() => ({ missions: [] as Mission[] })),
        ]);
        if (cancelled) return;
        setUser(me);
        setShortlist(normalizeShortlist(shortlistRes.shortlist));
        const loadedMissions = Array.isArray(missionsRes.missions) ? missionsRes.missions : [];
        setMissions(loadedMissions);
        const storedMissionRaw = typeof window !== "undefined" ? window.localStorage.getItem("markt_active_mission_id") : "";
        const storedMissionID = Number(storedMissionRaw);
        setActiveMissionId((current) =>
          resolveActiveMissionID(
            loadedMissions,
            current,
            Number.isFinite(storedMissionID) && storedMissionID > 0 ? storedMissionID : 0,
          ),
        );
      } catch (err) {
        if (!cancelled) {
          const msg = err instanceof Error ? err.message : "";
          if (msg.includes("401") || msg.toLowerCase().includes("unauthorized") || msg.toLowerCase().includes("missing") || msg.toLowerCase().includes("invalid token")) {
            window.location.replace("/login");
          }
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
          setShowOnboarding(shouldShowOnboarding());
        }
      }
    }

    void bootstrap();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    setMenuOpen(false);
  }, [pathname]);

  async function refreshShortlist() {
    const res = await api.shortlist.get();
    setShortlist(normalizeShortlist(res.shortlist));
  }

  async function refreshMissions() {
    const res = await api.missions.list();
    const next = Array.isArray(res.missions) ? res.missions : [];
    setMissions(next);
    setActiveMissionId((current) => resolveActiveMissionID(next, current));
  }

  const shortlistIDs = new Set(shortlist.map((item) => item.ItemID));

  async function addToShortlist(itemID: string) {
    if (shortlistIDs.has(itemID)) return;
    const entry = await api.shortlist.add(itemID);
    setShortlist((prev) => normalizeShortlist([entry, ...prev.filter((item) => item.ItemID !== itemID)]));
  }

  async function removeFromShortlist(itemID: string) {
    await api.shortlist.remove(itemID);
    setShortlist((prev) => prev.filter((item) => item.ItemID !== itemID));
  }

  const allNav = user?.is_admin ? [...NAV, ADMIN_NAV] : NAV;
  const currentNav = allNav.find((item) => pathname === item.href || pathname?.startsWith(`${item.href}/`)) ?? NAV[0];

  if (loading) {
    return (
      <div className="fullscreen-shell">
        <div className="loading-orb" />
        <p className="loading-copy">Initialising your AI workspace…</p>
      </div>
    );
  }

  return (
    <DashboardProvider
      value={{
        user,
        missions,
        activeMissionId,
        setActiveMission: (missionID: number) => setActiveMissionId(missionID > 0 ? missionID : 0),
        refreshMissions,
        shortlist,
        shortlistIDs,
        refreshShortlist,
        addToShortlist,
        removeFromShortlist,
        isShortlisted: (itemID: string) => shortlistIDs.has(itemID),
      }}
    >
      {showOnboarding && <OnboardingOverlay onComplete={() => setShowOnboarding(false)} />}
      <div className="app-shell">
        {menuOpen && <button type="button" className="app-overlay" aria-label="Close navigation" onClick={() => setMenuOpen(false)} />}

        <aside className={`app-sidebar${menuOpen ? " open" : ""}`}>
          <div className="sidebar-brand">
            <div className="brand-mark">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none">
                <path d="M12 2l2.5 6.5L21 11l-6.5 2.5L12 20l-2.5-6.5L3 11l6.5-2.5z" stroke="#fff" strokeWidth="1.8" strokeLinejoin="round" />
              </svg>
            </div>
            <div>
              <p className="brand-title">xolto</p>
              <p className="brand-subtitle">Used electronics copilot</p>
            </div>
          </div>

          <nav className="sidebar-nav">
            <div className="sidebar-ai-badge">
              <span className="sidebar-ai-dot" />
              AI is actively hunting
            </div>

            {NAV.map(({ href, label, description, Icon }) => {
              const active = pathname === href || pathname?.startsWith(`${href}/`);
              return (
                <Link key={href} href={href} className={`nav-item${active ? " active" : ""}`}>
                  <div className="nav-icon">
                    <Icon />
                  </div>
                  <div>
                    <p className="nav-label">{label}</p>
                    <p className="nav-meta">{description}</p>
                  </div>
                </Link>
              );
            })}
            {user?.is_admin && (() => {
              const { href, label, description, Icon } = ADMIN_NAV;
              const active = pathname === href || pathname?.startsWith(`${href}/`);
              return (
                <Link href={href} className={`nav-item${active ? " active" : ""}`}>
                  <div className="nav-icon"><Icon /></div>
                  <div>
                    <p className="nav-label">{label}</p>
                    <p className="nav-meta">{description}</p>
                  </div>
                </Link>
              );
            })()}
          </nav>

          <div className="sidebar-footer">
            <div className="sidebar-user-card">
              <div className="sidebar-avatar">{initialsForUser(user)}</div>
              <div className="sidebar-user-copy">
                <p className="sidebar-user-name">{user?.name || user?.email}</p>
                <p className="sidebar-user-tier">{user?.tier || "free"} plan</p>
              </div>
            </div>
            <button
              type="button"
              className="btn-ghost sidebar-signout"
              onClick={async () => {
                try {
                  await api.auth.logout();
                } catch {
                  // Best-effort logout.
                }
                window.location.replace("/login");
              }}
            >
              Sign out
            </button>
          </div>
        </aside>

        <div className="app-main">
          <header className="app-topbar">
            <div className="topbar-left">
              <button type="button" className="menu-trigger" onClick={() => setMenuOpen((open) => !open)} aria-label="Open navigation">
                <span />
                <span />
                <span />
              </button>
              <div>
                <p className="topbar-eyebrow">xolto</p>
                <h1 className="topbar-title">{currentNav.label}</h1>
              </div>
            </div>
            <div className="topbar-right">
              {activeMissionId > 0 && (
                <div className="topbar-chip">
                  <span className="chip-dot" />
                  Mission #{activeMissionId}
                </div>
              )}
              <div className="topbar-chip">
                <span className="chip-dot" />
                {shortlist.length} saved
              </div>
              <Link href="/settings" className="topbar-user-link">
                <span className="topbar-avatar">{initialsForUser(user)}</span>
                <span>{user?.name || "Account"}</span>
              </Link>
            </div>
          </header>

          <main className="page-shell">{children}</main>
        </div>
      </div>
    </DashboardProvider>
  );
}
