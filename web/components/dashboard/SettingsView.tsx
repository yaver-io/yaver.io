"use client";

import { useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { useDevices } from "@/lib/use-devices";
import { PasskeysCard } from "./PasskeyEnrollPrompt";
import pkg from "../../package.json";

const WEB_VERSION = (pkg as { version?: string }).version ?? "unknown";

interface SettingsViewProps {
  user: {
    id: string;
    email: string;
    name?: string;
    provider?: string;
    avatarUrl?: string;
  } | null;
  onLogout: () => void;
}

function AuthProviderIcon({
  provider,
  className = "h-5 w-5",
}: {
  provider: string;
  className?: string;
}) {
  switch (provider) {
    case "apple":
      return (
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
          <path d="M16.87 12.62c.03 2.82 2.47 3.76 2.5 3.77-.02.07-.39 1.34-1.28 2.66-.77 1.15-1.58 2.3-2.84 2.33-1.24.03-1.64-.73-3.06-.73-1.43 0-1.87.7-3.03.75-1.21.05-2.13-1.21-2.91-2.35-1.6-2.31-2.82-6.53-1.18-9.39.81-1.42 2.26-2.31 3.83-2.34 1.19-.03 2.31.8 3.06.8.74 0 2.13-.99 3.59-.84.61.03 2.31.25 3.41 1.86-.09.05-2.04 1.19-2.02 3.48ZM14.5 4.29c.64-.78 1.08-1.88.96-2.96-.92.04-2.04.61-2.7 1.39-.59.68-1.1 1.79-.96 2.85 1.03.08 2.06-.52 2.7-1.28Z" />
        </svg>
      );
    case "gitlab":
      return (
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
          <path d="M12 22.4 16.4 8.9h-8.8L12 22.4Z" />
          <path d="M12 22.4 7.6 8.9H1.8L12 22.4Z" />
          <path d="M1.8 8.9.5 13a.9.9 0 0 0 .33 1.01L12 22.4 1.8 8.9Z" />
          <path d="M1.8 8.9h5.8L5.1 1.2a.45.45 0 0 0-.86 0L1.8 8.9Z" />
          <path d="M12 22.4 16.4 8.9h5.8L12 22.4Z" />
          <path d="M22.2 8.9 23.5 13a.9.9 0 0 1-.33 1.01L12 22.4 22.2 8.9Z" />
          <path d="M22.2 8.9h-5.8l2.5-7.7a.45.45 0 0 1 .86 0l2.5 7.7Z" />
        </svg>
      );
    case "github":
      return (
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
          <path d="M12 .75a11.25 11.25 0 0 0-3.56 21.92c.56.1.76-.24.76-.54v-2.07c-3.1.67-3.76-1.31-3.76-1.31-.5-1.29-1.24-1.63-1.24-1.63-1.02-.69.08-.67.08-.67 1.12.08 1.72 1.16 1.72 1.16 1 .17 1.96 1.42 1.96 1.42.89 1.52 2.33 1.08 2.9.82.09-.72.35-1.08.63-1.33-2.47-.28-5.07-1.23-5.07-5.5 0-1.22.43-2.22 1.15-3-.12-.28-.5-1.42.11-2.96 0 0 .93-.3 3.06 1.14a10.7 10.7 0 0 1 5.58 0c2.13-1.44 3.06-1.14 3.06-1.14.61 1.54.23 2.68.11 2.96.72.78 1.15 1.78 1.15 3 0 4.28-2.61 5.22-5.1 5.49.4.35.75 1.04.75 2.1v3.11c0 .3.2.65.77.54A11.25 11.25 0 0 0 12 .75Z" />
        </svg>
      );
    case "google":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true" className={className}>
          <path fill="#EA4335" d="M12 10.2v3.9h5.5c-.24 1.25-.96 2.3-2.02 3.01l3.27 2.54c1.91-1.76 3.01-4.36 3.01-7.45 0-.72-.06-1.4-.18-2.05H12Z" />
          <path fill="#34A853" d="M12 22c2.7 0 4.97-.9 6.63-2.44l-3.27-2.54c-.9.6-2.05.95-3.36.95-2.58 0-4.76-1.74-5.54-4.08H3.08v2.62A10 10 0 0 0 12 22Z" />
          <path fill="#4A90E2" d="M6.46 13.9A5.98 5.98 0 0 1 6.15 12c0-.66.11-1.3.31-1.9V7.48H3.08A10 10 0 0 0 2 12c0 1.61.38 3.13 1.08 4.52l3.38-2.62Z" />
          <path fill="#FBBC05" d="M12 6.03c1.47 0 2.79.5 3.83 1.49l2.87-2.87C16.96 2.99 14.7 2 12 2A10 10 0 0 0 3.08 7.48l3.38 2.62C7.24 7.76 9.42 6.03 12 6.03Z" />
        </svg>
      );
    case "microsoft":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true" className={className}>
          <path fill="#F25022" d="M2 2h9.5v9.5H2z" />
          <path fill="#7FBA00" d="M12.5 2H22v9.5h-9.5z" />
          <path fill="#00A4EF" d="M2 12.5h9.5V22H2z" />
          <path fill="#FFB900" d="M12.5 12.5H22V22h-9.5z" />
        </svg>
      );
    default:
      return (
        <span className={`inline-flex items-center justify-center rounded-full border border-surface-700 text-[10px] uppercase ${className}`}>
          {provider.slice(0, 1)}
        </span>
      );
  }
}

function StatusIcon({ primary }: { primary: boolean }) {
  if (primary) {
    return (
      <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className="h-4 w-4 text-emerald-300">
        <path d="m9.05 2.93-1.1 2.24a1 1 0 0 1-.75.55l-2.47.36c-.82.12-1.15 1.13-.56 1.7l1.79 1.75c.25.24.36.6.3.94l-.42 2.46c-.14.82.72 1.45 1.45 1.07L10 14.9l2.21 1.16c.73.38 1.59-.25 1.45-1.07l-.42-2.46a1 1 0 0 1 .3-.94l1.79-1.75c.59-.57.26-1.58-.56-1.7l-2.47-.36a1 1 0 0 1-.75-.55l-1.1-2.24c-.37-.76-1.46-.76-1.83 0Z" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className="h-4 w-4 text-surface-400">
      <path fillRule="evenodd" d="M16.7 5.3a1 1 0 0 1 0 1.4l-7.2 7.2a1 1 0 0 1-1.4 0l-4-4a1 1 0 1 1 1.4-1.4l3.3 3.29 6.5-6.5a1 1 0 0 1 1.4 0Z" clipRule="evenodd" />
    </svg>
  );
}

function DeviceSurfaceIcon({ platform }: { platform: string }) {
  const value = String(platform || "").trim().toLowerCase();
  const isMobile = value === "ios" || value === "android";
  if (isMobile) {
    return (
      <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" aria-hidden="true">
        <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
      </svg>
    );
  }
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M9 17.25v1.007a3 3 0 01-.879 2.122L7.5 21h9l-.621-.621A3 3 0 0115 18.257V17.25m6-12V15a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 15V5.25A2.25 2.25 0 015.25 3h13.5A2.25 2.25 0 0121 5.25z" />
    </svg>
  );
}

function platformLabel(platform: string): string {
  switch (String(platform || "").trim().toLowerCase()) {
    case "darwin":
    case "macos":
      return "macOS";
    case "linux":
      return "Linux";
    case "windows":
      return "Windows";
    case "android":
      return "Android";
    case "ios":
      return "iOS";
    default:
      return platform || "Unknown OS";
  }
}

export default function SettingsView({ user, onLogout }: SettingsViewProps) {
  const [identities, setIdentities] = useState<Array<{ provider: string; email: string | null; isPrimary: boolean }>>([]);
  const [linkingProvider, setLinkingProvider] = useState<string | null>(null);
  const [linkError, setLinkError] = useState<string | null>(null);
  const [unlinkingProvider, setUnlinkingProvider] = useState<string | null>(null);
  const [unlinkError, setUnlinkError] = useState<string | null>(null);
  const [mergeStarting, setMergeStarting] = useState(false);
  const [mergeIntent, setMergeIntent] = useState<{ token: string; approvalUrl: string; expiresAt: number } | null>(null);
  const [mergeError, setMergeError] = useState<string | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const refreshIdentities = async (authToken: string) => {
    try {
      const res = await fetch(`${CONVEX_URL}/auth/providers`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });
      if (!res.ok) return;
      const data = await res.json();
      setIdentities(data?.identities || []);
    } catch {
      // ignore
    }
  };

  const [unlinkSuccess, setUnlinkSuccess] = useState<string | null>(null);
  const [mergeUrlCopied, setMergeUrlCopied] = useState(false);
  const [mergeCountdown, setMergeCountdown] = useState<string>("");

  const unlinkProvider = async (provider: string) => {
    if (!token) return;
    const confirmed = window.confirm(
      `Remove ${provider} from this Yaver account? You won't be able to sign in with ${provider} afterwards.`,
    );
    if (!confirmed) return;
    setUnlinkingProvider(provider);
    setUnlinkError(null);
    setUnlinkSuccess(null);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/oauth-link/${encodeURIComponent(provider)}`, {
        method: "DELETE",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: "{}",
      });
      if (res.status === 412) {
        const code = window.prompt("2FA is enabled on this account. Enter your current 6-digit code:") ?? "";
        if (!code.trim()) {
          setUnlinkingProvider(null);
          return;
        }
        const retry = await fetch(`${CONVEX_URL}/auth/oauth-link/${encodeURIComponent(provider)}`, {
          method: "DELETE",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ totpCode: code.trim() }),
        });
        if (!retry.ok) {
          const text = await retry.text();
          setUnlinkError(text || "Failed to unlink");
        } else {
          setUnlinkSuccess(`${provider} unlinked.`);
          await refreshIdentities(token);
        }
      } else if (!res.ok) {
        const text = await res.text();
        setUnlinkError(text || "Failed to unlink");
      } else {
        setUnlinkSuccess(`${provider} unlinked.`);
        await refreshIdentities(token);
      }
    } catch {
      setUnlinkError("Network error — try again.");
    } finally {
      setUnlinkingProvider(null);
    }
  };

  const copyMergeUrl = async () => {
    if (!mergeIntent) return;
    try {
      await navigator.clipboard.writeText(mergeIntent.approvalUrl);
      setMergeUrlCopied(true);
      window.setTimeout(() => setMergeUrlCopied(false), 2000);
    } catch {
      setMergeUrlCopied(false);
    }
  };

  // Live countdown under the merge approval URL so the user sees the
  // 30-minute window tick down rather than a static timestamp.
  useEffect(() => {
    if (!mergeIntent) {
      setMergeCountdown("");
      return;
    }
    const tick = () => {
      const remainingMs = mergeIntent.expiresAt - Date.now();
      if (remainingMs <= 0) {
        setMergeCountdown("expired");
        setMergeIntent(null);
        return;
      }
      const mins = Math.floor(remainingMs / 60_000);
      const secs = Math.floor((remainingMs % 60_000) / 1000);
      setMergeCountdown(`${mins}m ${String(secs).padStart(2, "0")}s`);
    };
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [mergeIntent]);

  const startMerge = async () => {
    if (!token) return;
    setMergeStarting(true);
    setMergeError(null);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/account/merge/start`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ client: "web" }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.mergeToken) {
        setMergeError(data?.error || "Failed to start merge");
        return;
      }
      setMergeIntent({
        token: data.mergeToken,
        approvalUrl: `${window.location.origin}/account/merge?token=${encodeURIComponent(data.mergeToken)}`,
        expiresAt: data.expiresAt,
      });
    } catch {
      setMergeError("Network error — try again.");
    } finally {
      setMergeStarting(false);
    }
  };

  const cancelMerge = async () => {
    if (!token || !mergeIntent) return;
    try {
      await fetch(`${CONVEX_URL}/auth/account/merge/cancel`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ mergeToken: mergeIntent.token }),
      });
    } catch {
      // best-effort
    }
    setMergeIntent(null);
  };

  const handleDeleteAccount = async () => {
    setDeleteLoading(true);
    setDeleteError(null);

    try {
      const convexSiteUrl = CONVEX_URL;

      const token =
        localStorage.getItem("yaver_auth_token") ||
        document.cookie
          .split(";")
          .find((c) => c.trim().startsWith("yaver_session="))
          ?.split("=")[1];

      if (!token) {
        setDeleteError("Not authenticated. Please sign in again.");
        setDeleteLoading(false);
        return;
      }

      const res = await fetch(`${convexSiteUrl}/auth/delete-account`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
        },
      });

      if (!res.ok) {
        const text = await res.text();
        setDeleteError(text || "Failed to delete account.");
        setDeleteLoading(false);
        return;
      }

      // Clear auth and redirect
      localStorage.removeItem("yaver_auth_token");
      document.cookie = "yaver_auth_token=; path=/; max-age=0; secure; samesite=lax";
      document.cookie = "yaver_session=; path=/; max-age=0; secure; samesite=lax";
      window.location.href = "/";
    } catch {
      setDeleteError("Network error. Please try again.");
      setDeleteLoading(false);
    }
  };

  const token =
    typeof window !== "undefined"
      ? localStorage.getItem("yaver_auth_token") ||
        document.cookie
          .split(";")
          .find((c) => c.trim().startsWith("yaver_auth_token="))
          ?.split("=")[1] ||
        null
      : null;
  const { devices } = useDevices(token);
  const ownedDevices = devices.filter((device) => !device.isGuest);

  useEffect(() => {
    if (!token) return;
    refreshIdentities(token);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  const startLink = async (provider: "apple" | "github" | "google" | "microsoft" | "gitlab") => {
    if (!token) return;
    setLinkError(null);
    setLinkingProvider(provider);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/oauth-link/start`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          provider,
          client: "web",
          returnTo: "/dashboard",
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.token) {
        throw new Error(data?.error || "Failed to start link");
      }
      window.location.href = `/api/auth/oauth/${provider}?client=web&intent=link&linkToken=${encodeURIComponent(data.token)}&return=${encodeURIComponent("/dashboard")}`;
    } catch (error) {
      console.error(error);
      const message = error instanceof Error ? error.message : `Failed to start ${provider} link`;
      setLinkError(message);
      setLinkingProvider(null);
    }
  };

  const isEmailUser = user?.provider === "email" || user?.provider === "password";

  return (
    <>
      <PasskeysCard />

      <div className="card mb-6">
        <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
          Sign-In Methods
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Link Apple, GitHub, GitLab, Google, or Microsoft to this same Yaver account. Future sign-ins with any linked provider open the same machines and devices.
        </p>
        <div className="mb-4 space-y-2">
          {identities.length === 0 ? (
            <p className="text-xs text-surface-500">No linked providers loaded yet.</p>
          ) : (
            identities.map((identity) => {
              const canUnlink = identities.length > 1;
              return (
                <div key={`${identity.provider}:${identity.email || "none"}`} className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2">
                  <div className="flex items-center gap-3">
                    <span className={`flex h-9 w-9 items-center justify-center rounded-full border border-surface-800 bg-surface-950 ${
                      identity.provider === "gitlab" ? "text-orange-300" : "text-surface-200"
                    }`}>
                      <AuthProviderIcon provider={identity.provider} className="h-4 w-4" />
                    </span>
                    <div>
                      <div className="flex items-center gap-2">
                        <p className="text-sm text-surface-200">{identity.provider}</p>
                        <span
                          className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-[0.16em] ${
                            identity.isPrimary
                              ? "border-emerald-500/30 text-emerald-300"
                              : "border-surface-700 text-surface-400"
                          }`}
                          title={identity.isPrimary ? "Primary sign-in method" : "Linked sign-in method"}
                        >
                          <StatusIcon primary={identity.isPrimary} />
                          {identity.isPrimary ? "Primary" : "Linked"}
                        </span>
                      </div>
                      <p className="text-xs text-surface-500">{identity.email || "No email reported by provider"}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => unlinkProvider(identity.provider)}
                      disabled={!canUnlink || unlinkingProvider === identity.provider}
                      title={canUnlink ? `Remove ${identity.provider} from this account` : "Cannot unlink — this is your only sign-in method"}
                      className="rounded-full border border-surface-700 px-2 py-1 text-[10px] uppercase tracking-[0.16em] text-surface-300 transition-colors hover:border-red-500/40 hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-30"
                    >
                      {unlinkingProvider === identity.provider ? "…" : "Unlink"}
                    </button>
                  </div>
                </div>
              );
            })
          )}
        </div>
        {unlinkSuccess && (
          <p className="mb-3 rounded-md border border-emerald-500/30 bg-emerald-500/5 px-3 py-2 text-xs text-emerald-300">
            {unlinkSuccess}
          </p>
        )}
        {linkError && <p className="mb-3 text-xs text-red-400">{linkError}</p>}
        {unlinkError && <p className="mb-3 text-xs text-red-400">{unlinkError}</p>}
        {(() => {
          const unlinked = (["apple", "github", "gitlab", "google", "microsoft"] as const).filter(
            (provider) => !identities.some((identity) => identity.provider === provider),
          );
          if (unlinked.length === 0) return null;
          return (
            <div className="grid gap-2 sm:grid-cols-4">
              {unlinked.map((provider) => (
                <button
                  key={provider}
                  onClick={() => startLink(provider)}
                  disabled={linkingProvider !== null}
                  className="rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50 disabled:opacity-40"
                >
                  {linkingProvider === provider ? "Connecting..." : `Connect ${provider}`}
                </button>
              ))}
            </div>
          );
        })()}
      </div>

      {/* Merge another account into this one */}
      <div className="card mb-6">
        <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
          Merge Another Account
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Accidentally created two Yaver accounts? Merge them into one. Start
          here, then open the approval URL on any browser where the OTHER
          account is signed in and confirm. The OTHER account&apos;s devices,
          sessions, linked providers, and settings move onto this one. The
          other account is deleted afterwards.
        </p>
        {!mergeIntent ? (
          <>
            {mergeError && <p className="mb-3 text-xs text-red-400">{mergeError}</p>}
            <button
              onClick={startMerge}
              disabled={mergeStarting}
              className="w-full rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50 disabled:opacity-50"
            >
              {mergeStarting ? "Starting…" : "Start merge"}
            </button>
          </>
        ) : (
          <div className="space-y-3">
            <p className="text-xs text-surface-400">
              Open this link on a browser where the OTHER Yaver account is
              signed in. The page will confirm that <span className="text-surface-200">{user?.email}</span> is the account receiving the data, then ask for confirmation.
            </p>
            <div className="rounded-lg border border-surface-800 bg-surface-900/60 p-3">
              <p className="break-all font-mono text-xs text-surface-200">{mergeIntent.approvalUrl}</p>
              <button
                onClick={copyMergeUrl}
                className="mt-3 rounded-md border border-surface-700 px-3 py-1.5 text-[11px] uppercase tracking-[0.16em] text-surface-300 transition-colors hover:bg-surface-800/60 hover:text-surface-50 focus:outline-none focus:ring-2 focus:ring-surface-600"
              >
                {mergeUrlCopied ? "Copied" : "Copy URL"}
              </button>
            </div>
            <div className="flex items-center justify-between">
              <p className="text-[10px] uppercase tracking-[0.16em] text-surface-500">
                Expires in <span className="font-mono text-surface-300">{mergeCountdown || "—"}</span>
              </p>
              <button
                onClick={cancelMerge}
                className="rounded-full border border-surface-700 px-3 py-1 text-[10px] uppercase tracking-[0.16em] text-surface-300 hover:border-red-500/40 hover:text-red-300 focus:outline-none focus:ring-2 focus:ring-red-500/40"
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>

      {/* About */}
      <div className="card mb-6">
        <h3 className="mb-3 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-surface-400">
          <span aria-hidden>ℹ️</span> About
        </h3>
        <div className="flex items-center justify-between text-sm">
          <span className="flex items-center gap-2 text-surface-400">
            <span aria-hidden>🌐</span> yaver.io web
          </span>
          <span className="font-mono text-surface-200">v{WEB_VERSION}</span>
        </div>
        <div className="mt-4 border-t border-surface-800 pt-4">
          <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">
            Your Boxes
          </div>
          {ownedDevices.length === 0 ? (
            <p className="text-sm text-surface-500">No boxes connected yet.</p>
          ) : (
            <div className="space-y-2">
              {ownedDevices.map((device) => (
                <div key={device.id} className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2">
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="flex h-8 w-8 items-center justify-center rounded-full border border-surface-800 bg-surface-950 text-surface-300">
                      <DeviceSurfaceIcon platform={device.platform} />
                    </span>
                    <div className="min-w-0">
                      <div className="truncate text-sm text-surface-200">{device.name || device.hostName || device.id}</div>
                      <div className="text-xs text-surface-500">
                        {platformLabel(device.platform)} · {device.agentVersion || "no version info"}
                      </div>
                    </div>
                  </div>
                  <span className={`ml-3 h-2.5 w-2.5 shrink-0 rounded-full ${device.online ? "bg-emerald-400" : "bg-surface-700"}`} title={device.online ? "Online" : "Offline"} />
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Legal */}
      <div className="card mb-6">
        <h3 className="mb-3 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-surface-400">
          <span aria-hidden>📜</span> Legal
        </h3>
        <div className="space-y-2">
          <a
            href="https://yaver.io/privacy"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 text-sm text-surface-400 transition-colors hover:text-surface-50"
          >
            <span aria-hidden>🔒</span> Privacy Policy
          </a>
          <a
            href="https://yaver.io/terms"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 text-sm text-surface-400 transition-colors hover:text-surface-50"
          >
            <span aria-hidden>📄</span> Terms of Service
          </a>
        </div>
      </div>

      {/* Sign out */}
      <button
        onClick={onLogout}
        className="mb-6 flex w-full items-center justify-center gap-2 rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50"
      >
        <span aria-hidden>🚪</span> Sign Out
      </button>

      {/* Dogfood — develop Yaver from a remote machine */}
      <div className="card mb-6 border-sky-500/20" data-testid="dogfood-section">
        <h3 className="mb-2 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-sky-400/80">
          <span aria-hidden>🐶</span> Dogfood
        </h3>
        <p className="mb-3 text-xs text-surface-500">
          Develop Yaver itself from any paired machine. Each box you connect that has{" "}
          <span className="font-mono text-surface-300">~/Workspace/yaver.io</span> checked out becomes
          a remote dev surface — you can run the agent's Go tests, ship CLI / web / docs commits,
          and even reload Yaver from inside Yaver via the Feedback SDK.
        </p>
        <ol className="mb-4 list-decimal space-y-1 pl-5 text-xs text-surface-400">
          <li>
            On the remote machine:{" "}
            <code className="font-mono text-surface-300">git clone https://github.com/kivanccakmak/yaver.io.git ~/Workspace/yaver.io</code>
          </li>
          <li>
            Open the Webview tab → pick <span className="font-mono text-surface-300">yaver.io</span> →
            the Web App preview will auto-build a static bundle and render the dashboard inside the
            iframe (Yaver-in-Yaver).
          </li>
          <li>
            Push commits with{" "}
            <code className="font-mono text-surface-300">yaver vibing</code> from the dashboard, or
            tail tests via the agent's <code className="font-mono text-surface-300">go test</code>{" "}
            integration.
          </li>
        </ol>
        <a
          href="https://yaver.io/docs/yaver-protocol#dogfooding-yaver-from-yaver"
          target="_blank"
          rel="noreferrer"
          className="inline-block rounded-md border border-sky-500/30 px-3 py-1.5 text-xs text-sky-300 transition-colors hover:bg-sky-500/10"
          data-testid="dogfood-docs-link"
        >
          Dogfooding docs →
        </a>
      </div>

      {/* Delete Account */}
      <div className="card mb-6 border-red-500/20">
        <h3 className="mb-2 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-red-400/80">
          <span aria-hidden>⚠️</span> Danger Zone
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Permanently delete your account and all associated data. This action cannot be undone.
        </p>
        <p className="mb-3 text-xs text-surface-500">
          Type <span className="font-mono text-surface-300">delete my account</span> to confirm:
        </p>
        <input
          type="text"
          value={deleteConfirm}
          onChange={(e) => setDeleteConfirm(e.target.value)}
          placeholder="delete my account"
          disabled={deleteLoading}
          className="mb-3 w-full rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 text-sm text-surface-200 placeholder-surface-600 outline-none transition-colors focus:border-red-500/50 disabled:opacity-50"
        />
        {deleteError && (
          <p className="mb-3 text-sm text-red-400">{deleteError}</p>
        )}
        <button
          onClick={handleDeleteAccount}
          disabled={deleteConfirm !== "delete my account" || deleteLoading}
          className="w-full rounded-lg border border-red-500/30 px-4 py-3 text-sm font-medium text-red-400 transition-colors hover:bg-red-500/10 disabled:opacity-30 disabled:hover:bg-transparent"
        >
          {deleteLoading ? "Deleting..." : "Delete My Account"}
        </button>
      </div>
    </>
  );
}
