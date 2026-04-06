export const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

export type User = {
  id: string;
  email: string;
  name: string;
  tier: string;
};

export type SearchSpec = {
  ID: number;
  UserID?: string;
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
  Title: string;
  Price: number;
  PriceType?: string;
  Condition?: string;
  URL?: string;
  ImageURLs?: string[];
  MarketplaceID?: string;
};

export type ShortlistEntry = {
  ID: number;
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
};

export function getToken(): string {
  return "";
}

export function setToken(_token: string) {
}

export function clearToken() {
}

async function rawFetch(path: string, options?: RequestInit): Promise<Response> {
  return fetch(`${API_BASE}${path}`, {
    ...options,
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(options?.headers || {})
    }
  });
}

export async function apiFetch<T>(path: string, options?: RequestInit): Promise<T> {
  let res = await rawFetch(path, options);
  if (res.status === 401 && path !== "/auth/refresh" && path !== "/auth/login" && path !== "/auth/register") {
    const refreshRes = await rawFetch("/auth/refresh", { method: "POST" });
    if (refreshRes.ok) {
      res = await rawFetch(path, options);
    }
  }
  if (!res.ok) {
    throw new Error(await res.text());
  }
  return res.json();
}

export const api = {
  auth: {
    login: async (email: string, password: string) =>
      apiFetch<{ access_token: string; user: User }>("/auth/login", {
        method: "POST",
        body: JSON.stringify({ email, password })
      }),
    register: async (email: string, password: string, name: string) =>
      apiFetch<{ access_token: string; user: User }>("/auth/register", {
        method: "POST",
        body: JSON.stringify({ email, password, name })
      }),
    me: async () => apiFetch<User>("/users/me"),
    refresh: async () => apiFetch<{ access_token: string; user: User }>("/auth/refresh", { method: "POST" }),
    logout: async () => apiFetch<{ ok: boolean }>("/auth/logout", { method: "POST" })
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
    generate: async (topic: string) =>
      apiFetch<{ searches: Array<Record<string, unknown>>; warning?: string }>("/searches/generate", {
        method: "POST",
        body: JSON.stringify({ topic })
      })
  },
  listings: {
    feed: async () => apiFetch<{ listings: Listing[]; user_id: string }>("/listings/feed")
  },
  shortlist: {
    get: async () => apiFetch<{ shortlist: ShortlistEntry[] }>("/shortlist"),
    add: async (itemID: string) => apiFetch<ShortlistEntry>(`/shortlist/${itemID}`, { method: "POST" }),
    remove: async (itemID: string) => apiFetch<{ ok: boolean }>(`/shortlist/${itemID}`, { method: "DELETE" })
  },
  assistant: {
    converse: async (message: string) =>
      apiFetch<AssistantReply>("/assistant/converse", {
        method: "POST",
        body: JSON.stringify({ message })
      })
  },
  billing: {
    createCheckout: async (priceID: string) =>
      apiFetch<{ url: string; id: string }>("/billing/checkout", {
        method: "POST",
        body: JSON.stringify({ price_id: priceID })
      })
  }
};
