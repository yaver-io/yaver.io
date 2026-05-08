"use client";

// Post-login passkey enrollment banner.
//
// Shown only when:
//  1. The browser supports WebAuthn.
//  2. The user is signed in (auth token present).
//  3. The user has zero passkeys registered (queried from
//     /auth/passkey/list).
//  4. They haven't dismissed the prompt within the last 14 days
//     (localStorage key `yaver_passkey_prompt_dismissed`).
//
// Dismissal is silent — the user can always come back via the Settings
// view (future). The button calls /auth/passkey/register/{start,finish}
// and writes a new credential to their existing users row. It does NOT
// touch their existing OAuth/email login — passkeys are purely additive.

import { useEffect, useState } from "react";
import { startRegistration, browserSupportsWebAuthn } from "@simplewebauthn/browser";
import { CONVEX_URL } from "@/lib/constants";

const DISMISS_KEY = "yaver_passkey_prompt_dismissed";
const DISMISS_TTL_MS = 14 * 24 * 60 * 60 * 1000;

function getAuthToken(): string | null {
  if (typeof window === "undefined") return null;
  try {
    return window.localStorage.getItem("yaver_auth_token");
  } catch {
    return null;
  }
}

function recentlyDismissed(): boolean {
  if (typeof window === "undefined") return true;
  try {
    const raw = window.localStorage.getItem(DISMISS_KEY);
    if (!raw) return false;
    const ts = parseInt(raw, 10);
    if (Number.isNaN(ts)) return false;
    return Date.now() - ts < DISMISS_TTL_MS;
  } catch {
    return false;
  }
}

export function PasskeyEnrollPrompt() {
  const [visible, setVisible] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  useEffect(() => {
    let cancelled = false;
    if (!browserSupportsWebAuthn()) return;
    if (recentlyDismissed()) return;
    const token = getAuthToken();
    if (!token) return;
    (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/auth/passkey/list`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (!res.ok) return;
        const data = await res.json();
        const count = Array.isArray(data?.passkeys) ? data.passkeys.length : 0;
        if (!cancelled && count === 0) setVisible(true);
      } catch {
        // Silently skip — banner is optional. The user can still sign
        // in normally via OAuth/email next time.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const dismiss = () => {
    try {
      window.localStorage.setItem(DISMISS_KEY, String(Date.now()));
    } catch {}
    setVisible(false);
  };

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
        const text = await startRes.text();
        setError(text || "Could not start passkey registration.");
        setBusy(false);
        return;
      }
      const { options } = await startRes.json();

      let attResp;
      try {
        attResp = await startRegistration({ optionsJSON: options });
      } catch (err: any) {
        if (err?.name === "NotAllowedError" || err?.name === "AbortError") {
          // User cancelled the OS prompt. Don't treat as an error;
          // just hide the banner so they aren't asked again this
          // session.
          dismiss();
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
        const text = await finishRes.text();
        setError(text || "Could not save passkey.");
        setBusy(false);
        return;
      }
      setDone(true);
      setBusy(false);
      // Auto-collapse after 4s — banner has done its job.
      setTimeout(() => setVisible(false), 4000);
    } catch {
      setError("Network error. Please try again.");
      setBusy(false);
    }
  };

  if (!visible) return null;

  return (
    <div className="mx-3 mt-3 flex flex-wrap items-center gap-3 rounded-2xl border border-cyan-400/30 bg-cyan-400/5 px-4 py-3 text-[13px] text-cyan-100 md:mx-4">
      <svg className="h-5 w-5 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3" y="11" width="18" height="11" rx="2" />
        <path d="M7 11V7a5 5 0 0 1 10 0v4" />
        <circle cx="12" cy="16" r="1.5" />
      </svg>
      {done ? (
        <span className="flex-1 text-emerald-200">
          Passkey saved. Next time you can sign in with Touch ID, Face ID, or Windows Hello.
        </span>
      ) : (
        <>
          <span className="flex-1 leading-snug">
            Add a passkey on this device for one-tap sign-in next time. iCloud Keychain / Google Password Manager will sync it across your devices automatically. Your existing sign-in keeps working as a fallback.
          </span>
          <button
            type="button"
            onClick={enrol}
            disabled={busy}
            className="rounded-xl border border-cyan-400/50 bg-cyan-400/10 px-3 py-1.5 text-[12px] font-semibold text-cyan-50 hover:bg-cyan-400/20 disabled:opacity-50"
          >
            {busy ? "Saving..." : "Add passkey"}
          </button>
          <button
            type="button"
            onClick={dismiss}
            className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-1.5 text-[12px] text-surface-300 hover:border-surface-500"
          >
            Not now
          </button>
        </>
      )}
      {error ? <span className="basis-full text-[12px] text-amber-200">{error}</span> : null}
    </div>
  );
}
