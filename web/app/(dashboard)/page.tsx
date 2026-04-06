"use client";

import { useEffect, useState } from "react";

import { api } from "../../lib/api";

export default function DashboardPage() {
  const [summary, setSummary] = useState({ searches: 0, shortlist: 0 });

  useEffect(() => {
    Promise.all([api.searches.list(), api.shortlist.get()])
      .then(([searches, shortlist]) => {
        setSummary({ searches: searches.searches.length, shortlist: shortlist.shortlist.length });
      })
      .catch(() => undefined);
  }, []);

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Overview</h1>
      <div className="grid grid-cols-2 gap-4 max-w-sm">
        <div className="card p-4">
          <p className="text-xs text-gray-500 uppercase tracking-wide font-medium">Searches</p>
          <p className="text-3xl font-bold text-gray-900 mt-1">{summary.searches}</p>
        </div>
        <div className="card p-4">
          <p className="text-xs text-gray-500 uppercase tracking-wide font-medium">Shortlist</p>
          <p className="text-3xl font-bold text-gray-900 mt-1">{summary.shortlist}</p>
        </div>
      </div>
      <p className="text-sm text-gray-400 mt-6">
        New deals appear live in{" "}
        <a href="/feed" className="underline">Feed</a>.
      </p>
    </div>
  );
}
