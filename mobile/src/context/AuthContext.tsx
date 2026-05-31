import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useMemo,
  useState,
} from "react";
import { AppState, AppStateStatus } from "react-native";
import {
  User,
  getToken,
  getUser,
  saveToken,
  saveUser,
  clearToken,
  validateToken,
  validateTokenDetailed,
  refreshToken,
  getSurveyStatus,
  clearKeychainIfFreshInstall,
  getConvexSiteUrl,
} from "../lib/auth";
import {
  hydrateBackendConfigFromCache,
  refreshHostedBackendConfig,
} from "../lib/backendConfig";
import { appLog } from "../lib/logger";
import { clearCache } from "../lib/storage";

interface AuthState {
  user: User | null;
  token: string | null;
  isLoading: boolean;
  isAuthenticated: boolean;
  surveyCompleted: boolean;
  login: (token: string) => Promise<void>;
  logout: () => Promise<void>;
  markSurveyCompleted: () => void;
  refreshUser: () => Promise<void>;
  // Call when an authenticated request to the backend returns 401/403.
  // Routes through the single-flight token-refresh path: rotates the
  // bearer if the server hands back a new one, or signs the user out
  // (→ auth screen) if the session was genuinely revoked. Network errors
  // keep the cached session. Lets non-auth surfaces (device list, etc.)
  // recover instead of silently showing an empty / "Disconnected" state.
  notifyAuthFailure: () => Promise<void>;
}

const AuthContext = createContext<AuthState | undefined>(undefined);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [token, setToken] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [surveyCompleted, setSurveyCompleted] = useState(false);

  // Mirror of `token` for reading from async closures (retry loop's
  // fire-and-forget refresh). Flip to null on logout → any in-flight
  // `refreshToken().then()` that tries to push a rotated token back
  // bails out instead of re-authenticating a logged-out user.
  const currentTokenRef = useRef<string | null>(null);
  useEffect(() => {
    currentTokenRef.current = token;
  }, [token]);

  // Restore session on mount.
  //
  // Network-aware retry: a brief Convex outage or DNS hiccup at boot
  // must not log the user out. We retry validateToken with exponential
  // backoff (1s, 2s, 4s, 8s ≈ 15s budget) on network errors, and fall
  // back to the SecureStore-cached user so the UI proceeds even when
  // we're fully offline. Only a real 401/403 from the server clears
  // the token. Mirrors the Go agent's `ensureDaemonAlive` pattern.
  //
  // Cancellation: the retry loop can run for up to 15s. If the component
  // unmounts (only happens in dev reloads) or the user logs out via
  // another path mid-retry, the `cancelled` flag short-circuits every
  // await boundary so we don't end up calling setUser() on an unmounted
  // tree or racing with logout's state reset.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        await hydrateBackendConfigFromCache();
        await refreshHostedBackendConfig();
        if (cancelled) return;
        // Wipe stale Keychain tokens on fresh install (iOS Keychain survives uninstall)
        await clearKeychainIfFreshInstall();
        if (cancelled) return;
        const storedToken = await getToken();
        if (cancelled) return;
        if (!storedToken) return;

        // Retry validate on network errors; give up on explicit 401/403.
        const backoffMs = [0, 1_000, 2_000, 4_000, 8_000];
        let validated: User | null = null;
        let invalidated = false;
        for (let i = 0; i < backoffMs.length; i++) {
          if (backoffMs[i] > 0) {
            await new Promise((r) => setTimeout(r, backoffMs[i]));
          }
          if (cancelled) return;
          const result = await validateTokenDetailed(storedToken);
          if (cancelled) return;
          if (result.kind === "valid") {
            validated = result.user;
            break;
          }
          if (result.kind === "invalid") {
            invalidated = true;
            break;
          }
          // networkError — keep retrying until the backoff budget is exhausted.
        }

        if (validated) {
          appLog(
            "info",
            `[auth] restored ${validated.email || validated.id} via ${getConvexSiteUrl()}`,
          );
          setToken(storedToken);
          setUser(validated);
          await saveUser(validated);
          if (cancelled) return;
          // Refresh token to extend expiry; persist rotation if the server
          // handed us a new bearer (best-effort, non-blocking). Guarded
          // against completing after cancellation.
          refreshToken(storedToken).then(async (result) => {
            if (cancelled) return;
            // User logged out (or signed in as someone else) while the
            // fire-and-forget refresh was in flight — discard the
            // rotated token instead of reviving the old session.
            if (currentTokenRef.current !== storedToken) return;
            if (result.newToken) {
              await saveToken(result.newToken);
              if (cancelled) return;
              if (currentTokenRef.current !== storedToken) return;
              setToken(result.newToken);
            }
          }).catch(() => {});
          if (validated.surveyCompleted) {
            setSurveyCompleted(true);
          } else {
            try {
              const survey = await getSurveyStatus(storedToken);
              if (cancelled) return;
              setSurveyCompleted(survey.completed);
            } catch {
              if (!cancelled) setSurveyCompleted(false);
            }
          }
          return;
        }

        if (invalidated) {
          await clearToken();
          return;
        }

        // Network never recovered within the budget. Use the cached user
        // from SecureStore so the app loads authenticated; the background
        // AppState/resume listener will re-validate when connectivity is
        // back. This keeps airplane-mode + spotty-WiFi launches usable.
        const cachedUser = await getUser();
        if (cancelled) return;
        if (cachedUser) {
          setToken(storedToken);
          setUser(cachedUser);
          setSurveyCompleted(!!cachedUser.surveyCompleted);
        }
      } catch {
        // Silently fail; user stays unauthenticated.
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Refresh token when the app returns from background.
  //
  // Only a real 401/403 from the server triggers logout — network errors
  // keep the cached session so a transient Convex/WiFi hiccup between
  // background and foreground doesn't sign the user out. If the server
  // rotated our token we persist the new bearer immediately.
  // Shared "refresh-or-logout" recovery. Coalesced via refreshToken's
  // single-flight map so concurrent callers (resume + a 401 from the
  // device list) collapse to one round-trip. Rotates the bearer when the
  // server hands one back, signs out on a genuine revoke (401/403), and
  // keeps the cached session on network errors.
  const notifyAuthFailure = useCallback(async () => {
    const snapshotToken = currentTokenRef.current;
    if (!snapshotToken) return;
    try {
      const result = await refreshToken(snapshotToken);
      // User logged out / token replaced while the refresh was in flight.
      if (currentTokenRef.current !== snapshotToken) return;
      if (result.newToken) {
        await saveToken(result.newToken);
        if (currentTokenRef.current !== snapshotToken) return;
        setToken(result.newToken);
        return;
      }
      if (!result.ok && !result.networkError) {
        console.log("[auth] Token revoked by server — logging out");
        await clearToken();
        if (currentTokenRef.current !== snapshotToken) return;
        setToken(null);
        setUser(null);
        setSurveyCompleted(false);
      }
    } catch {
      // Recovery path must never throw.
    }
  }, []);

  const appStateRef = useRef(AppState.currentState);
  useEffect(() => {
    const handleAppState = (nextState: AppStateStatus) => {
      const prevState = appStateRef.current;
      appStateRef.current = nextState;
      if (nextState === "active" && prevState.match(/inactive|background/) && token) {
        void notifyAuthFailure();
      }
    };
    const sub = AppState.addEventListener("change", handleAppState);
    return () => sub.remove();
  }, [token, notifyAuthFailure]);

  const login = useCallback(async (newToken: string) => {
    await hydrateBackendConfigFromCache();
    // Don't AWAIT the remote config refresh — its fetch (to
    // yaver.io/api/mobile-config) currently 404s, and forcing it to
    // run in series before validateToken adds a 5-second AbortController
    // wait AND parks RN-Android's OkHttp pool with a stale socket that
    // breaks the next call. Fire it as background work so it can update
    // the cache for next launch without blocking sign-in.
    refreshHostedBackendConfig().catch(() => {});
    // Use the detailed variant so the caller can tell "server says
    // invalid" apart from "couldn't reach server" — a network blip
    // shouldn't look identical to a revoked token in the UI.
    const validation = await validateTokenDetailed(newToken);
    if (validation.kind === "networkError") {
      const detail = validation.detail ? ` — ${validation.detail}` : "";
      throw new Error(
        `Couldn't reach the auth server (${getConvexSiteUrl()})${detail}. Check your network and try again.`,
      );
    }
    if (validation.kind === "invalid") {
      throw new Error(
        `Auth server (${getConvexSiteUrl()}) rejected the token. Try signing in again.`,
      );
    }
    const validatedUser = validation.user;
    appLog(
      "info",
      `[auth] login resolved ${validatedUser.email || validatedUser.id} via ${getConvexSiteUrl()}`,
    );
    await saveToken(newToken);
    await saveUser(validatedUser);
    setToken(newToken);
    setUser(validatedUser);
    // Use surveyCompleted from user record if available
    if (validatedUser.surveyCompleted) {
      setSurveyCompleted(true);
    } else {
      try {
        const survey = await getSurveyStatus(newToken);
        setSurveyCompleted(survey.completed);
      } catch {
        setSurveyCompleted(false);
      }
    }
  }, []);

  const logout = useCallback(async () => {
    // Best-effort: invalidate all sessions server-side before clearing locally
    if (token) {
      fetch(`${getConvexSiteUrl()}/auth/logout`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      }).catch(() => {});
    }
    await clearToken();
    await clearCache(); // Clear cached tasks from previous session
    setToken(null);
    setUser(null);
    setSurveyCompleted(false);
  }, [token]);

  const markSurveyCompleted = useCallback(() => {
    setSurveyCompleted(true);
  }, []);

  const refreshUser = useCallback(async () => {
    if (!token) return;
    const validatedUser = await validateToken(token);
    if (validatedUser) {
      setUser(validatedUser);
      await saveUser(validatedUser);
    }
  }, [token]);

  const value = useMemo<AuthState>(
    () => ({
      user,
      token,
      isLoading,
      isAuthenticated: !!token && !!user,
      surveyCompleted,
      login,
      logout,
      markSurveyCompleted,
      refreshUser,
      notifyAuthFailure,
    }),
    [user, token, isLoading, surveyCompleted, login, logout, markSurveyCompleted, refreshUser, notifyAuthFailure]
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return ctx;
}
