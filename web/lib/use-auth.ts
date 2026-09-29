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

export function yaverAuthTokenCookie(token: string, maxAgeSeconds = 60 * 60 * 24 * 30): string {
  const secure = (() => {
    if (typeof window === "undefined") return true;
    const host = window.location.hostname;
    if (window.location.protocol === "http:" && (host === "localhost" || host === "127.0.0.1" || host === "::1")) {
      return false;
    }
    return true;
  })();
  return `yaver_auth_token=${token}; path=/; max-age=${maxAgeSeconds};${secure ? " secure;" : ""} samesite=lax`;
}

function syncAuthTokenCookie(token: string) {
  if (typeof document === "undefined") return;
  document.cookie = yaverAuthTokenCookie(token);
}

function clearAuthTokenCookies() {
  if (typeof document === "undefined") return;
  document.cookie = "yaver_auth_token=; path=/; max-age=0; secure; samesite=lax";
  document.cookie = "yaver_session=; path=/; max-age=0; secure; samesite=lax";
}

export function useAuth(): AuthState {
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [sessionExpired, setSessionExpired] = useState(false);

  const logout = useCallback(() => {
    localStorage.removeItem("yaver_auth_token");
    clearAuthTokenCookies();
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
          // Only a DEFINITIVE auth rejection (401/403) may destroy the session.
          // A 429 (rate limit from a burst of validations) or a 5xx is a server
          // condition, not a verdict on the credential — clearing the token
          // there logged the user straight out (2026-09-29: every open of the
          // Vibing tab bounced to /auth while /auth/validate returned non-OK).
          // Keep the token, degrade, and let the next real request decide.
          if (res.status === 401 || res.status === 403) {
            localStorage.removeItem("yaver_auth_token");
            clearAuthTokenCookies();
            if (!cancelled) {
              setSessionExpired(true);
              setIsLoading(false);
            }
            return;
          }
          if (!cancelled) {
            syncAuthTokenCookie(storedToken);
            setToken(storedToken);
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
          syncAuthTokenCookie(storedToken);
          setUser(mapped);
          setToken(storedToken);
        }

        // Netflix-on-AppleTV contract: a dashboard opened at least once a year
        // must never re-prompt for OAuth. `/auth/validate` alone never extends
        // the 1-year session, so a token that's simply been sitting hard-expires
        // and forces a fresh sign-in. Fire an extend-only refresh so every visit
        // resets the clock. Extend-only (NO X-Yaver-Rotate-Token): a browser tab
        // can be closed mid-flight and lose the response — rotating would strand
        // it on a dead token and log the user out of a live session. Mirrors
        // mobile's deliberate no-rotate decision (mobile/src/lib/auth.ts,
        // root-caused 2026-07-15). Fire-and-forget: failure is a silent no-op,
        // the existing token stays valid.
        void fetch(`${CONVEX_URL}/auth/refresh`, {
          method: "POST",
          headers: { Authorization: `Bearer ${storedToken}` },
        })
          .then(async (r) => {
            if (!r.ok) return;
            const body = await r.json().catch(() => null);
            // Defensive only: honour a rotated token if the server ever returns
            // one. We don't opt in, so this stays undefined in practice.
            if (body?.token && typeof body.token === "string" && !cancelled) {
              localStorage.setItem("yaver_auth_token", body.token);
              syncAuthTokenCookie(body.token);
              setToken(body.token);
            }
          })
          .catch(() => {});
      } catch {
        // Network error -- still set token so we can try offline
        if (!cancelled) {
          syncAuthTokenCookie(storedToken);
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
