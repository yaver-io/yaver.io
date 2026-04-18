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
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

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
    fetch(`${CONVEX_URL}/auth/providers`, {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then(async (res) => (res.ok ? res.json() : null))
      .then((data) => setIdentities(data?.identities || []))
      .catch(() => undefined);
  }, [token]);

  const startLink = async (provider: "apple" | "google" | "microsoft") => {
    if (!token) return;
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
          Link Apple, Google, or Microsoft to this same Yaver account. Future sign-ins with any linked provider open the same machines and devices.
        </p>
        <div className="mb-4 space-y-2">
          {identities.length === 0 ? (
            <p className="text-xs text-surface-500">No linked providers loaded yet.</p>
          ) : (
            identities.map((identity) => (
              <div key={`${identity.provider}:${identity.email || "none"}`} className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2">
                <div>
                  <p className="text-sm text-surface-200">{identity.provider}</p>
                  <p className="text-xs text-surface-500">{identity.email || "No email reported by provider"}</p>
                </div>
                {identity.isPrimary ? (
                  <span className="rounded-full border border-emerald-500/30 px-2 py-1 text-[10px] uppercase tracking-[0.16em] text-emerald-300">Primary</span>
                ) : (
                  <span className="rounded-full border border-surface-700 px-2 py-1 text-[10px] uppercase tracking-[0.16em] text-surface-400">Linked</span>
                )}
              </div>
            ))
          )}
        </div>
        <div className="grid gap-2 sm:grid-cols-3">
          {(["apple", "google", "microsoft"] as const).map((provider) => (
            <button
              key={provider}
              onClick={() => startLink(provider)}
              disabled={linkingProvider !== null}
              className="rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50 disabled:opacity-50"
            >
              {linkingProvider === provider ? "Connecting..." : `Connect ${provider}`}
            </button>
          ))}
        </div>
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
