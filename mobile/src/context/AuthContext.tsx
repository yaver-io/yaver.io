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
  const appStateRef = useRef(AppState.currentState);
  useEffect(() => {
    const handleAppState = (nextState: AppStateStatus) => {
      const prevState = appStateRef.current;
      appStateRef.current = nextState;
      if (nextState === "active" && prevState.match(/inactive|background/) && token) {
        const snapshotToken = token;
        refreshToken(snapshotToken).then(async (result) => {
          // User logged out (or was replaced) between the resume trigger
          // and the refresh landing — drop the outcome silently.
          if (currentTokenRef.current !== snapshotToken) return;
          if (result.newToken) {
            await saveToken(result.newToken);
            if (currentTokenRef.current !== snapshotToken) return;
            setToken(result.newToken);
          }
          if (!result.ok && !result.networkError) {
            console.log("[auth] Token revoked by server — logging out");
            await clearToken();
            if (currentTokenRef.current !== snapshotToken) return;
            setToken(null);
            setUser(null);
            setSurveyCompleted(false);
          }
        }).catch(() => {});
      }
    };
    const sub = AppState.addEventListener("change", handleAppState);
    return () => sub.remove();
  }, [token]);

  const login = useCallback(async (newToken: string) => {
    const validatedUser = await validateToken(newToken);
    if (!validatedUser) {
      throw new Error("Invalid token");
    }
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
    }),
    [user, token, isLoading, surveyCompleted, login, logout, markSurveyCompleted, refreshUser]
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
