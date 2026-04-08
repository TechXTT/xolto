"use client";

import type { ReactNode } from "react";
import { createContext, useContext } from "react";

import type { Mission, ShortlistEntry, User } from "../lib/api";

type DashboardContextValue = {
  user: User | null;
  missions: Mission[];
  activeMissionId: number;
  setActiveMission: (missionID: number) => void;
  refreshMissions: () => Promise<void>;
  shortlist: ShortlistEntry[];
  shortlistIDs: Set<string>;
  refreshShortlist: () => Promise<void>;
  addToShortlist: (itemID: string) => Promise<void>;
  removeFromShortlist: (itemID: string) => Promise<void>;
  isShortlisted: (itemID: string) => boolean;
};

const DashboardContext = createContext<DashboardContextValue | null>(null);

export function DashboardProvider({
  children,
  value,
}: {
  children: ReactNode;
  value: DashboardContextValue;
}) {
  return <DashboardContext.Provider value={value}>{children}</DashboardContext.Provider>;
}

export function useDashboardContext() {
  const context = useContext(DashboardContext);
  if (!context) {
    throw new Error("useDashboardContext must be used inside DashboardProvider");
  }
  return context;
}
