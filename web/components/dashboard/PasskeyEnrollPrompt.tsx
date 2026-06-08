"use client";

// Passkey management card — lives inside Settings.
//
// Renders only when the browser supports WebAuthn. Always visible to
// signed-in users (no dismiss / TTL): users opt in by clicking "Add
// passkey," and can remove existing passkeys per-row. This used to be
// a dismissible banner pinned to the dashboard top; it's now a
// regular Settings card so it never overlays other tabs.
//
// Backed by /auth/passkey/{list,register/start,register/finish,remove}
// — all signed in as the current user via Authorization: Bearer.

import { useEffect, useState } from "react";
import { startRegistration, browserSupportsWebAuthn } from "@simplewebauthn/browser";
import { CONVEX_URL } from "@/lib/constants";

type PasskeyRow = {
  _id: string;
  credentialId: string;
  deviceLabel: string | null;
  backedUp: boolean | null;
  createdAt: number;
  lastUsedAt: number | null;
};

function getAuthToken(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem("yaver_auth_token");
  } catch {
    return null;
  }
}

function formatRelative(ts: number | null): string {
  if (!ts) return "never";
  const diff = Date.now() - ts;
  if (diff < 60_000) return "just now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d ago`;
  return new Date(ts).toLocaleDateString();
}

export function PasskeysCard() {
  const [supported, setSupported] = useState(false);
  const [passkeys, setPasskeys] = useState<PasskeyRow[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [removingId, setRemovingId] = useState<string | null>(null);

  useEffect(() => {
    setSupported(browserSupportsWebAuthn());
  }, []);

  const refresh = async () => {
    const token = getAuthToken();
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/auth/passkey/list`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) {
        setPasskeys([]);
        return;
      }
      const data = await res.json();
      setPasskeys(Array.isArray(data?.passkeys) ? data.passkeys : []);
    } catch {
      setPasskeys([]);
    }
  };

  useEffect(() => {
    if (!supported) return;
    void refresh();
  }, [supported]);

  const enrol = async () => {
    setError(null);
    setBusy(true);
    const token = getAuthToken();
    if (!token) {
      setError("Sign in first.");
      setBusy(false);
      return;
    }
    try {
      const startRes = await fetch(`${CONVEX_URL}/auth/passkey/register/start`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: "{}",
      });
      if (!startRes.ok) {
        setError((await startRes.text()) || "Could not start passkey registration.");
        setBusy(false);
        return;
      }
      const { options } = await startRes.json();

      let attResp;
      try {
        attResp = await startRegistration({ optionsJSON: options });
      } catch (err: any) {
        if (err?.name === "NotAllowedError" || err?.name === "AbortError") {
          // User cancelled the OS prompt — silent.
          setBusy(false);
          return;
        }
        setError(err?.message || "Passkey registration cancelled.");
        setBusy(false);
        return;
      }

      const finishRes = await fetch(`${CONVEX_URL}/auth/passkey/register/finish`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ response: attResp }),
      });
      if (!finishRes.ok) {
        setError((await finishRes.text()) || "Could not save passkey.");
        setBusy(false);
        return;
      }
      setBusy(false);
      await refresh();
    } catch {
      setError("Network error. Please try again.");
      setBusy(false);
    }
  };

  const remove = async (credentialId: string) => {
    setError(null);
    setRemovingId(credentialId);
    const token = getAuthToken();
    if (!token) {
      setRemovingId(null);
      return;
    }
    try {
      const res = await fetch(`${CONVEX_URL}/auth/passkey/remove`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ credentialId }),
      });
      if (!res.ok) {
        setError((await res.text()) || "Could not remove passkey.");
      } else {
        await refresh();
      }
    } catch {
      setError("Network error. Please try again.");
    } finally {
      setRemovingId(null);
    }
  };

  if (!supported) return null;

  return (
    <div className="card mb-6">
      <h3 className="mb-3 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-surface-400">
        <span aria-hidden>🔑</span> Passkeys
      </h3>
      <p className="mb-4 text-xs text-surface-500">
        One-tap sign-in via Touch ID, Face ID, or Windows Hello. iCloud
        Keychain / Google Password Manager sync them across your devices.
        Your existing OAuth / email sign-in keeps working as a fallback.
      </p>

      {passkeys === null ? (
        <p className="text-xs text-surface-500">Loading…</p>
      ) : passkeys.length === 0 ? (
        <p className="mb-4 text-xs text-surface-500">No passkeys yet on this account.</p>
      ) : (
        <div className="mb-4 space-y-2">
          {passkeys.map((pk) => (
            <div
              key={pk._id}
              className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2"
            >
              <div className="min-w-0">
                <div className="truncate text-sm text-surface-200">
                  {pk.deviceLabel || "Unnamed passkey"}
                </div>
                <div className="text-[11px] text-surface-500">
                  Added {formatRelative(pk.createdAt)} · last used {formatRelative(pk.lastUsedAt)}
                  {pk.backedUp ? " · synced" : " · device-bound"}
                </div>
              </div>
              <button
                onClick={() => void remove(pk.credentialId)}
                disabled={removingId === pk.credentialId}
                className="ml-3 shrink-0 rounded-full border border-surface-700 px-2 py-1 text-[10px] uppercase tracking-[0.16em] text-surface-300 transition-colors hover:border-red-500/40 hover:text-red-700 dark:hover:text-red-300 disabled:opacity-30"
              >
                {removingId === pk.credentialId ? "…" : "Remove"}
              </button>
            </div>
          ))}
        </div>
      )}

      {error && <p className="mb-3 text-xs text-amber-700 dark:text-amber-300">{error}</p>}

      <button
        type="button"
        onClick={enrol}
        disabled={busy}
        className="w-full rounded-lg border border-cyan-500/30 bg-cyan-500/5 px-4 py-3 text-sm font-medium text-cyan-700 dark:text-cyan-200 transition-colors hover:bg-cyan-500/10 disabled:opacity-50"
      >
        {busy ? "Saving…" : "Add passkey"}
      </button>
    </div>
  );
}
