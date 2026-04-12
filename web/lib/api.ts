export const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8000";
const ACCESS_TOKEN_KEY = "marktbot_access_token";
const REFRESH_TOKEN_KEY = "marktbot_refresh_token";

export type User = {
  id: string;
  email: string;
  name: string;
  tier: string;
  is_admin?: boolean;
};

export type SearchSpec = {
  ID: number;
  UserID?: string;
  ProfileID?: number;
  Name: string;
  Query: string;
  MarketplaceID: string;
  CategoryID: number;
  MaxPrice: number;
  MinPrice: number;
  Condition: string[];
  OfferPercentage: number;
  AutoMessage: boolean;
  MessageTemplate: string;
  Attributes?: Record<string, string>;
  Enabled: boolean;
  CheckInterval?: string | number;
};

export type Listing = {
  ItemID: string;
  ProfileID?: number;
  Title: string;
  Price: number;
  PriceType?: string;
  Condition?: string;
  URL?: string;
  ImageURLs?: string[];
  MarketplaceID?: string;
  Score?: number;
  FairPrice?: number;
  OfferPrice?: number;
  Confidence?: number;
  Reason?: string;
  RiskFlags?: string[];
  Feedback?: "" | "approved" | "dismissed";
};

export type MatchFeedbackAction = "approve" | "dismiss" | "clear";

export type Mission = {
  ID?: number;
  UserID?: string;
  Name: string;
  TargetQuery?: string;
  CategoryID?: number;
  BudgetMax?: number;
  BudgetStretch?: number;
  PreferredCondition?: string[];
  RequiredFeatures?: string[];
  NiceToHave?: string[];
  SearchQueries?: string[];
  Status?: "active" | "paused" | "completed";
  Urgency?: "urgent" | "flexible" | "no-rush";
  AvoidFlags?: string[];
  TravelRadius?: number;
  Category?: "phone" | "laptop" | "camera" | "other" | string;
  Active?: boolean;
  MatchCount?: number;
  LastMatchAt?: string;
};

export type ShoppingProfile = Mission;

export type AssistantSession = {
  UserID: string;
  PendingIntent: string;
  PendingQuestion: string;
  DraftMission?: Mission | null;
  LastAssistantMsg: string;
  UpdatedAt?: string;
};

export type Recommendation = {
  Listing: Listing;
  Mission?: Mission;
  Label: string;
  Verdict: string;
  Concerns: string[];
  NextQuestions?: string[];
  SuggestedOffer?: number;
  Scored?: {
    Score: number;
    FairPrice: number;
    OfferPrice: number;
    Confidence?: number;
  };
};

export type ShortlistEntry = {
  ID: number;
  MissionID?: number;
  ItemID: string;
  Title: string;
  URL: string;
  RecommendationLabel: string;
  RecommendationScore: number;
  AskPrice: number;
  FairPrice: number;
  Verdict: string;
  Concerns: string[];
  SuggestedQuestions: string[];
  Status: string;
};

export type AssistantReply = {
  Message: string;
  Expecting: boolean;
  Intent?: string;
  Mission?: Mission | null;
  Recommendations?: Recommendation[];
};

type ErrorPayload = {
  error?: string;
  message?: string;
  detail?: string;
};

function canUseStorage() {
  return typeof window !== "undefined" && typeof window.localStorage !== "undefined";
}

export function getToken(): string {
  if (!canUseStorage()) return "";
  return window.localStorage.getItem(ACCESS_TOKEN_KEY) || "";
}

export function setToken(token: string) {
  if (!canUseStorage()) return;
  if (!token) {
    window.localStorage.removeItem(ACCESS_TOKEN_KEY);
    return;
  }
  window.localStorage.setItem(ACCESS_TOKEN_KEY, token);
}

export function clearToken() {
  if (!canUseStorage()) return;
  window.localStorage.removeItem(ACCESS_TOKEN_KEY);
  window.localStorage.removeItem(REFRESH_TOKEN_KEY);
}

function getRefreshToken(): string {
  if (!canUseStorage()) return "";
  return window.localStorage.getItem(REFRESH_TOKEN_KEY) || "";
}

function setRefreshToken(token: string) {
  if (!canUseStorage()) return;
  if (!token) {
    window.localStorage.removeItem(REFRESH_TOKEN_KEY);
    return;
  }
  window.localStorage.setItem(REFRESH_TOKEN_KEY, token);
}

async function normalizeApiError(res: Response): Promise<string> {
  const fallback = `Request failed (${res.status})`;
  const contentType = res.headers.get("content-type") || "";

  if (contentType.includes("application/json")) {
    try {
      const payload = (await res.json()) as ErrorPayload;
      return payload.error || payload.message || payload.detail || fallback;
    } catch {
      return fallback;
    }
  }

  try {
    const text = (await res.text()).trim();
    if (!text) return fallback;
    try {
      const payload = JSON.parse(text) as ErrorPayload;
      return payload.error || payload.message || payload.detail || text;
    } catch {
      return text;
    }
  } catch {
    return fallback;
  }
}

async function rawFetch(path: string, options?: RequestInit): Promise<Response> {
  const headers = new Headers(options?.headers || {});
  if (!(options?.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (!headers.has("Authorization")) {
    const token = getToken();
    if (token) {
      headers.set("Authorization", `Bearer ${token}`);
    }
  }
  if (path === "/auth/refresh" && !headers.has("X-Refresh-Token")) {
    const refreshToken = getRefreshToken();
    if (refreshToken) {
      headers.set("X-Refresh-Token", refreshToken);
    }
  }

  return fetch(`${API_BASE}${path}`, {
    ...options,
    credentials: "include",
    headers,
  });
}

export async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  let res = await rawFetch(path, options);
  if (res.status === 401 && path !== "/auth/refresh" && path !== "/auth/login" && path !== "/auth/register") {
    const refreshRes = await rawFetch("/auth/refresh", { method: "POST" });
    if (refreshRes.ok) {
      try {
        const payload = (await refreshRes.clone().json()) as { access_token?: string; refresh_token?: string };
        if (payload.access_token) setToken(payload.access_token);
        if (payload.refresh_token) setRefreshToken(payload.refresh_token);
      } catch {
        // Ignore malformed refresh payloads and retry with cookies if present.
      }
      res = await rawFetch(path, options);
    }
    // Do NOT call clearToken() on failed refresh — server may be temporarily down.
    // Let the original 401 propagate so callers can decide what to do.
  }
  if (!res.ok) {
    throw new Error(await normalizeApiError(res));
  }
  return res.json();
}

export const api = {
  auth: {
    login: async (email: string, password: string) => {
      const response = await apiFetch<{ access_token: string; refresh_token?: string; user: User }>("/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password }),
      });
      setToken(response.access_token);
      if (response.refresh_token) setRefreshToken(response.refresh_token);
      return response;
    },
    register: async (email: string, password: string, name: string) => {
      const response = await apiFetch<{ access_token: string; refresh_token?: string; user: User }>("/auth/register", {
        method: "POST",
        body: JSON.stringify({ email, password, name }),
      });
      setToken(response.access_token);
      if (response.refresh_token) setRefreshToken(response.refresh_token);
      return response;
    },
    me: async () => apiFetch<User>("/users/me"),
    refresh: async () => {
      const response = await apiFetch<{ access_token: string; refresh_token?: string; user: User }>("/auth/refresh", { method: "POST" });
      setToken(response.access_token);
      if (response.refresh_token) setRefreshToken(response.refresh_token);
      return response;
    },
    logout: async () => {
      const response = await apiFetch<{ ok: boolean }>("/auth/logout", { method: "POST" });
      clearToken();
      return response;
    },
  },
  searches: {
    list: async () => apiFetch<{ searches: SearchSpec[] }>("/searches"),
    create: async (spec: Partial<SearchSpec>) =>
      apiFetch<SearchSpec>("/searches", { method: "POST", body: JSON.stringify(spec) }),
    update: async (id: number, spec: Partial<SearchSpec>) =>
      apiFetch<{ ok: boolean }>(`/searches/${id}`, { method: "PUT", body: JSON.stringify(spec) }),
    delete: async (id: number) =>
      apiFetch<{ ok: boolean }>(`/searches/${id}`, { method: "DELETE" }),
    run: async (id: number) =>
      apiFetch<{ ok: boolean; message: string }>(`/searches/${id}/run`, { method: "POST" }),
    runAll: async () =>
      apiFetch<{ ok: boolean; message: string }>("/searches/run", { method: "POST" }),
    generate: async (topic: string) =>
      apiFetch<{ searches: Array<Record<string, unknown>>; warning?: string }>("/searches/generate", {
        method: "POST",
        body: JSON.stringify({ topic }),
      }),
  },
  listings: {
    feed: async (missionID?: number) => {
      const query = missionID && missionID > 0 ? `?mission_id=${missionID}` : "";
      return apiFetch<{ listings: Listing[]; user_id: string }>(`/listings/feed${query}`);
    },
  },
  matches: {
    feedback: async (itemID: string, action: MatchFeedbackAction) =>
      apiFetch<{ ok: boolean; feedback: string }>("/matches/feedback", {
        method: "POST",
        body: JSON.stringify({ item_id: itemID, action }),
      }),
    analyze: async (url: string, missionID?: number) =>
      apiFetch<{
        listing: Listing;
        reasoning_source: string;
        search_advice: string;
        comparables?: Array<{ ItemID: string; Title: string; Price: number; Similarity: number; MatchReason: string }>;
        market_average: number;
      }>("/matches/analyze", {
        method: "POST",
        body: JSON.stringify({ url, mission_id: missionID && missionID > 0 ? missionID : 0 }),
      }),
  },
  missions: {
    list: async () => apiFetch<{ missions: Mission[] }>("/missions"),
    create: async (mission: Partial<Mission>) =>
      apiFetch<Mission>("/missions", { method: "POST", body: JSON.stringify(mission) }),
    get: async (id: number) => apiFetch<Mission>(`/missions/${id}`),
    update: async (id: number, mission: Partial<Mission>) =>
      apiFetch<Mission>(`/missions/${id}`, { method: "PUT", body: JSON.stringify(mission) }),
    updateStatus: async (id: number, status: "active" | "paused" | "completed") =>
      apiFetch<Mission>(`/missions/${id}/status`, { method: "PUT", body: JSON.stringify({ status }) }),
    delete: async (id: number) =>
      apiFetch<{ ok: boolean }>(`/missions/${id}`, { method: "DELETE" }),
    matches: async (id: number, params?: { limit?: number }) => {
      const limit = params?.limit ? `?limit=${params.limit}` : "";
      return apiFetch<{ mission: Mission; listings: Listing[] }>(`/missions/${id}/matches${limit}`);
    },
  },
  shortlist: {
    get: async () => apiFetch<{ shortlist: ShortlistEntry[] }>("/shortlist"),
    add: async (itemID: string) => apiFetch<ShortlistEntry>(`/shortlist/${itemID}`, { method: "POST" }),
    remove: async (itemID: string) => apiFetch<{ ok: boolean }>(`/shortlist/${itemID}`, { method: "DELETE" }),
    draftOffer: async (itemID: string) =>
      apiFetch<{ Content: string; ItemID: string }>(`/shortlist/${encodeURIComponent(itemID)}/draft`, { method: "POST" }),
  },
  assistant: {
    converse: async (message: string) =>
      apiFetch<AssistantReply>("/assistant/converse", {
        method: "POST",
        body: JSON.stringify({ message }),
      }),
    session: async () =>
      apiFetch<{ session: AssistantSession | null }>("/assistant/session"),
  },
  billing: {
    createCheckout: async (priceID: string) =>
      apiFetch<{ url: string; id: string }>("/billing/checkout", {
        method: "POST",
        body: JSON.stringify({ price_id: priceID }),
      }),
    portal: async () => apiFetch<{ url: string }>("/billing/portal"),
  },
  admin: {
    stats: async (days = 30) =>
      apiFetch<{ stats: AdminAIStats; days: number }>(`/admin/stats?days=${days}`),
    users: async () =>
      apiFetch<{ users: AdminUser[] }>("/admin/users"),
    usage: async (days = 7) =>
      apiFetch<{ entries: AdminUsageEntry[]; days: number }>(`/admin/usage?days=${days}`),
  },
};

export type AdminAIStats = {
  TotalCalls: number;
  TotalTokens: number;
  TotalPrompt: number;
  TotalCompletion: number;
  FailedCalls: number;
  EstimatedCostUSD: number;
};

export type AdminUser = {
  id: string;
  email: string;
  name: string;
  tier: string;
  is_admin: boolean;
  created_at: string;
  mission_count: number;
  search_count: number;
  ai_call_count: number;
  ai_tokens: number;
};

export type AdminUsageEntry = {
  ID: number;
  UserID: string;
  CallType: string;
  Model: string;
  PromptTokens: number;
  CompletionTokens: number;
  TotalTokens: number;
  LatencyMs: number;
  Success: boolean;
  ErrorMsg: string;
  CreatedAt: string;
};
