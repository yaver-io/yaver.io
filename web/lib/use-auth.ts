"use client";

import { useEffect, useState, useCallback } from "react";
import { CONVEX_URL } from "@/lib/constants";

interface User {
  id: string;
  email: string;
  name?: string;
  provider?: string;
  avatarUrl?: string;
  surveyCompleted?: boolean;
  // Server-computed owner flag (ownerAllowlist). Gates owner-only hardware
  // cells; never carries the owner identity into the client bundle.
  isOwner?: boolean;
}

interface AuthState {
  user: User | null;
  token: string | null;
  isLoading: boolean;
  isAuthenticated: boolean;
  surveyCompleted: boolean;
  // True only when a stored token was rejected by the server (401/403) and
  // wiped. Lets the dashboard explain "your session expired" instead of
  // silently dumping the user back to a generic sign-in gate. NOT set on
  // network errors — those keep the token so we can retry offline.
  sessionExpired: boolean;
  logout: () => void;
}

function getStoredToken(): string | null {
  if (typeof window === "undefined") return null;

  // Check localStorage first (set by auth callback)
  const lsToken = localStorage.getItem("yaver_auth_token");
  if (lsToken) return lsToken;

  // Fall back to cookie
  const cookies = document.cookie.split(";");
  for (const cookie of cookies) {
    const [name, value] = cookie.trim().split("=");
    if (name === "yaver_session" || name === "yaver_auth_token") {
      return value || null;
    }
  }

  return null;
}

export function useAuth(): AuthState {
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [sessionExpired, setSessionExpired] = useState(false);

  const logout = useCallback(() => {
    localStorage.removeItem("yaver_auth_token");
    document.cookie = "yaver_auth_token=; path=/; max-age=0; secure; samesite=lax";
    document.cookie = "yaver_session=; path=/; max-age=0; secure; samesite=lax";
    setUser(null);
    setToken(null);
    window.location.href = "/";
  }, []);

  useEffect(() => {
    let cancelled = false;

    async function validate() {
      const storedToken = getStoredToken();
      if (!storedToken) {
        setIsLoading(false);
        return;
      }

      try {
        const res = await fetch(`${CONVEX_URL}/auth/validate`, {
          method: "GET",
          headers: { Authorization: `Bearer ${storedToken}` },
        });

        if (!res.ok) {
          // Token invalid -- clear it. 401/403 means the server actively
          // rejected the token (rotated / expired / revoked); flag it so
          // the dashboard can say "your session expired" rather than
          // logging the user out with no explanation.
          localStorage.removeItem("yaver_auth_token");
          if (!cancelled) {
            if (res.status === 401 || res.status === 403) setSessionExpired(true);
            setIsLoading(false);
          }
          return;
        }

        const data = await res.json();
        const raw = data.user ?? data;
        const mapped: User = {
          id: raw.userId ?? raw.id ?? "",
          email: raw.email ?? "",
          name: raw.fullName ?? raw.name ?? "",
          provider: raw.provider,
          avatarUrl: raw.avatarUrl,
          surveyCompleted: raw.surveyCompleted,
          isOwner: raw.isOwner === true,
        };
        if (!cancelled) {
          setUser(mapped);
          setToken(storedToken);
        }
      } catch {
        // Network error -- still set token so we can try offline
        if (!cancelled) {
          setToken(storedToken);
        }
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    }

    validate();
    return () => {
      cancelled = true;
    };
  }, []);

  return {
    user,
    token,
    isLoading,
    isAuthenticated: token !== null,
    surveyCompleted: user?.surveyCompleted ?? false,
    sessionExpired,
    logout,
  };
}
