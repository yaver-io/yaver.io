"use client";

import { useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

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

  useEffect(() => {
    if (!token) return;
    refreshIdentities(token);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  const startLink = async (provider: "apple" | "github" | "google" | "microsoft") => {
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
      <div className="card mb-6">
        <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
          Sign-In Methods
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Link Apple, GitHub, Google, or Microsoft to this same Yaver account. Future sign-ins with any linked provider open the same machines and devices.
        </p>
        <div className="mb-4 space-y-2">
          {identities.length === 0 ? (
            <p className="text-xs text-surface-500">No linked providers loaded yet.</p>
          ) : (
            identities.map((identity) => {
              const canUnlink = identities.length > 1;
              return (
                <div key={`${identity.provider}:${identity.email || "none"}`} className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2">
                  <div>
                    <p className="text-sm text-surface-200">{identity.provider}</p>
                    <p className="text-xs text-surface-500">{identity.email || "No email reported by provider"}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    {identity.isPrimary ? (
                      <span className="rounded-full border border-emerald-500/30 px-2 py-1 text-[10px] uppercase tracking-[0.16em] text-emerald-300">Primary</span>
                    ) : (
                      <span className="rounded-full border border-surface-700 px-2 py-1 text-[10px] uppercase tracking-[0.16em] text-surface-400">Linked</span>
                    )}
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
        <div className="grid gap-2 sm:grid-cols-4">
          {(["apple", "github", "google", "microsoft"] as const).map((provider) => {
            const alreadyLinked = identities.some((identity) => identity.provider === provider);
            return (
              <button
                key={provider}
                onClick={() => startLink(provider)}
                disabled={linkingProvider !== null || alreadyLinked}
                className="rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50 disabled:opacity-40"
              >
                {alreadyLinked ? `${provider} linked` : linkingProvider === provider ? "Connecting..." : `Connect ${provider}`}
              </button>
            );
          })}
        </div>
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

      {/* Legal */}
      <div className="card mb-6">
        <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
          Legal
        </h3>
        <div className="space-y-2">
          <a
            href="https://yaver.io/privacy"
            target="_blank"
            rel="noopener noreferrer"
            className="block text-sm text-surface-400 transition-colors hover:text-surface-50"
          >
            Privacy Policy
          </a>
          <a
            href="https://yaver.io/terms"
            target="_blank"
            rel="noopener noreferrer"
            className="block text-sm text-surface-400 transition-colors hover:text-surface-50"
          >
            Terms of Service
          </a>
        </div>
      </div>

      {/* Sign out */}
      <button
        onClick={onLogout}
        className="mb-6 w-full rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50"
      >
        Sign Out
      </button>

      {/* Delete Account */}
      <div className="card mb-6 border-red-500/20">
        <h3 className="mb-2 text-sm font-medium uppercase tracking-wider text-red-400/80">
          Danger Zone
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
