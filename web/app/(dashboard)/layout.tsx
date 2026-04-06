"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import React, { useEffect, useState } from "react";

import { api, clearToken, User } from "../../lib/api";

const NAV = [
  { href: "/",          label: "Overview" },
  { href: "/feed",      label: "Feed" },
  { href: "/searches",  label: "Searches" },
  { href: "/shortlist", label: "Shortlist" },
  { href: "/assistant", label: "Assistant" },
  { href: "/settings",  label: "Settings" },
];

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const pathname = usePathname();

  useEffect(() => {
    api.auth
      .me()
      .then(setUser)
      .catch(() => {
        clearToken();
        window.location.href = "/login";
      })
      .finally(() => setLoading(false));
  }, []);

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gray-50">
        <span className="text-sm text-gray-400">Loading…</span>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen bg-gray-50">
      {/* Sidebar */}
      <aside className="w-56 shrink-0 bg-white border-r border-gray-200 flex flex-col">
        <div className="px-5 py-5 border-b border-gray-100">
          <p className="font-bold text-gray-900 text-base leading-tight">MarktBot</p>
          {user && (
            <p className="text-xs text-gray-400 mt-0.5 truncate">
              {user.name} ·{" "}
              <span className={`badge-${user.tier}`}>{user.tier}</span>
            </p>
          )}
        </div>
        <nav className="flex-1 px-3 py-4 space-y-0.5">
          {NAV.map(({ href, label }) => {
            const active = pathname === href || (href !== "/" && pathname?.startsWith(href));
            return (
              <Link
                key={href}
                href={href}
                className={`flex items-center px-3 py-2 rounded-md text-sm font-medium transition-colors ${
                  active
                    ? "bg-brand-50 text-brand-700"
                    : "text-gray-600 hover:bg-gray-100 hover:text-gray-900"
                }`}
              >
                {label}
              </Link>
            );
          })}
        </nav>
        <div className="px-4 py-4 border-t border-gray-100">
          <button
            type="button"
            className="btn-secondary w-full text-xs"
            onClick={async () => {
              try { await api.auth.logout(); } catch {}
              clearToken();
              window.location.href = "/login";
            }}
          >
            Sign out
          </button>
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 min-w-0 overflow-auto">
        <div className="max-w-5xl mx-auto px-6 py-8">
          {children}
        </div>
      </main>
    </div>
  );
}
