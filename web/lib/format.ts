import type { SearchSpec } from "./api";

const euroFormatter = new Intl.NumberFormat("nl-NL", {
  style: "currency",
  currency: "EUR",
  minimumFractionDigits: 0,
  maximumFractionDigits: 2,
});

export function formatEuroFromCents(cents: number, fallback = "—"): string {
  if (!Number.isFinite(cents) || cents <= 0) return fallback;
  return euroFormatter.format(cents / 100);
}

export function formatCompactEuroFromCents(cents: number, fallback = "—"): string {
  if (!Number.isFinite(cents) || cents <= 0) return fallback;
  return euroFormatter.format(Math.round(cents / 100));
}

export function normalizeCheckIntervalMinutes(input?: number | string): number {
  if (typeof input === "string") {
    const numeric = Number(input);
    if (Number.isFinite(numeric)) return normalizeCheckIntervalMinutes(numeric);
    return 5;
  }
  if (!Number.isFinite(input) || !input || input <= 0) return 5;
  if (input > 1_000_000) {
    return Math.max(1, Math.round(input / 1_000_000_000 / 60));
  }
  if (input > 60) {
    return Math.max(1, Math.round(input / 60));
  }
  return Math.max(1, Math.round(input));
}

export function intervalMinutesToDurationNs(minutes: number): number {
  return Math.max(1, Math.round(minutes)) * 60 * 1_000_000_000;
}

export function searchSignature(spec: Pick<SearchSpec, "MarketplaceID" | "Query" | "CategoryID" | "MaxPrice">): string {
  return [
    (spec.MarketplaceID || "").trim().toLowerCase(),
    (spec.Query || "").trim().toLowerCase(),
    spec.CategoryID || 0,
    spec.MaxPrice || 0,
  ].join("|");
}
