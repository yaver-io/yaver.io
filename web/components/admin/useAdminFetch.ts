// Tiny data hook for /admin/* endpoints. Mirrors the use-auth.ts
// fetch+useState pattern — the web client deliberately does NOT use
// convex/react hooks (no ConvexProvider in this app).
"use client";

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

function getStoredToken(): string | null {
  if (typeof window === "undefined") return null;
  const ls = localStorage.getItem("yaver_auth_token");
  if (ls) return ls;
  for (const cookie of document.cookie.split(";")) {
    const [name, value] = cookie.trim().split("=");
    if (name === "yaver_session" || name === "yaver_auth_token") return value || null;
  }
  return null;
}

export type AdminFetchState<T> = {
  data: T | null;
  error: string | null;
  loading: boolean;
  refresh: () => void;
};

export function useAdminFetch<T>(path: string, deps: ReadonlyArray<unknown> = []): AdminFetchState<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const refresh = useCallback(() => setTick((n) => n + 1), []);

  useEffect(() => {
    let cancelled = false;
    const token = getStoredToken();
    if (!token) {
      setError("Not signed in");
      setLoading(false);
      return;
    }
    setLoading(true);
    setError(null);
    fetch(`${CONVEX_URL}${path}`, { headers: { Authorization: `Bearer ${token}` } })
      .then(async (res) => {
        if (!res.ok) {
          const body = await res.text().catch(() => "");
          throw new Error(`${res.status} ${body || res.statusText}`);
        }
        return res.json();
      })
      .then((json) => {
        if (!cancelled) {
          setData(json as T);
          setLoading(false);
        }
      })
      .catch((err) => {
        if (!cancelled) {
          setError(String(err?.message || err));
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, tick, ...deps]);

  return { data, error, loading, refresh };
}

export async function adminPost<T>(path: string, body?: unknown): Promise<T> {
  const token = getStoredToken();
  if (!token) throw new Error("Not signed in");
  const res = await fetch(`${CONVEX_URL}${path}`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: body == null ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${res.status} ${text || res.statusText}`);
  }
  return (await res.json()) as T;
}
