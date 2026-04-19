"use client";

// /pair — hosted landing for a Yaver pairing session.
//
// Contract: the agent prints a URL like
//   https://yaver.io/pair?sid=ABC123&mode=pair&host=mac-mini&exp=1714…
//                       &target=http://10.0.0.5:18080&code=ABC123
// where `sid` is the locator and `code` is the trust secret. The user
// either scanned the QR with the system camera (and landed here) or
// got the URL through chat. From this page we:
//
//   - probe the target's /auth/pair/session to confirm the session is
//     still open and read its hostname back to the user
//   - if the user is signed in to yaver.io: POST their token to the
//     target's /auth/pair/submit and report success
//   - if not signed in: bounce to /auth?return=/pair?... — the
//     existing auth page already plumbs ?return= through OAuth + TOTP,
//     so this gives us "scan-while-signed-out → sign in → finish
//     pairing" with zero new callback rewiring
//
// QR / pair-URL is purely additive — every existing pair flow
// (manual passkey, `yaver auth send`, mobile beacon adoption) keeps
// working without this page.

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { useAuth } from "@/lib/use-auth";

type PairSession = {
  ok: boolean;
  sessionId?: string;
  hostname?: string;
  expiresAt?: string;
  canDirectSubmit?: boolean;
  targetUrls?: string[];
  error?: string;
};

type PairURLPayload = {
  sid: string;
  mode: string;
  host?: string;
  target?: string;
  code?: string;
  exp?: number;
};

// normalizeAgentBase strips trailing slashes and (optionally) defaults
// scheme so a user pasting `10.0.0.5:18080` still gets a usable URL.
function normalizeAgentBase(raw: string): string {
  let url = raw.trim();
  if (!url) return "";
  if (!/^https?:\/\//i.test(url)) url = "http://" + url;
  return url.replace(/\/+$/, "");
}

// readPairPayload parses the current URL the user landed at. We only
// recognise a pair payload when both sid+mode (or sid alone) are
// present so a stray /pair?foo=bar doesn't trigger half-states.
function readPairPayload(): PairURLPayload | null {
  if (typeof window === "undefined") return null;
  const q = new URLSearchParams(window.location.search);
  const sid = (q.get("sid") || q.get("code") || "").trim();
  if (!sid) return null;
  const expRaw = q.get("exp");
  const expNum = expRaw ? Number(expRaw) : NaN;
  return {
    sid,
    mode: (q.get("mode") || "pair").toLowerCase(),
    host: q.get("host") || undefined,
    target: q.get("target") || undefined,
    code: q.get("code") || undefined,
    exp: Number.isFinite(expNum) ? expNum : undefined,
  };
}

export default function PairPage() {
  const auth = useAuth();
  const [payload, setPayload] = useState<PairURLPayload | null>(null);
  const [agentBase, setAgentBase] = useState("");
  const [probe, setProbe] = useState<PairSession | null>(null);
  const [probeBusy, setProbeBusy] = useState(false);
  const [submitBusy, setSubmitBusy] = useState(false);
  const [banner, setBanner] = useState<{ kind: "info" | "ok" | "err"; text: string } | null>(null);

  // Read payload + initial target from query on mount.
  useEffect(() => {
    const p = readPairPayload();
    setPayload(p);
    if (p?.target) setAgentBase(normalizeAgentBase(p.target));
  }, []);

  // Probe /auth/pair/session whenever the agent base changes. Older
  // agents (pre-Slice-A) may 404 on /session; falling back to /info
  // keeps the page useful without a forced agent upgrade.
  useEffect(() => {
    if (!agentBase || !payload?.sid) {
      setProbe(null);
      return;
    }
    let abort = false;
    const run = async () => {
      setProbeBusy(true);
      try {
        const url = `${agentBase}/auth/pair/session?sid=${encodeURIComponent(payload.sid)}`;
        const r = await fetch(url, { method: "GET", cache: "no-store" });
        if (r.ok) {
          const data = (await r.json()) as PairSession;
          if (!abort) setProbe({ ...data, ok: true });
        } else if (r.status === 404) {
          // Try the older /info shape — pre-Slice-A agent.
          const r2 = await fetch(`${agentBase}/auth/pair/info`, { cache: "no-store" });
          if (r2.ok) {
            const old = (await r2.json()) as { host?: string; expiresAt?: string };
            if (!abort) setProbe({ ok: true, hostname: old.host, expiresAt: old.expiresAt, canDirectSubmit: true });
          } else if (!abort) {
            setProbe({ ok: false, error: "No active pairing session at that URL" });
          }
        } else if (!abort) {
          setProbe({ ok: false, error: `Probe failed (HTTP ${r.status})` });
        }
      } catch (e) {
        if (!abort) {
          const msg = e instanceof Error ? e.message : String(e);
          setProbe({ ok: false, error: msg });
        }
      } finally {
        if (!abort) setProbeBusy(false);
      }
    };
    void run();
    return () => {
      abort = true;
    };
  }, [agentBase, payload?.sid]);

  const expiryText = useMemo(() => {
    const at = probe?.expiresAt ?? (payload?.exp ? new Date(payload.exp * 1000).toISOString() : "");
    if (!at) return "";
    try {
      const d = new Date(at);
      const ms = d.getTime() - Date.now();
      if (Number.isNaN(ms)) return "";
      if (ms <= 0) return " (expired — start a new pair window on the target)";
      const minutes = Math.round(ms / 60000);
      return ` (expires in ${minutes} minute${minutes === 1 ? "" : "s"})`;
    } catch {
      return "";
    }
  }, [probe?.expiresAt, payload?.exp]);

  // signinHref preserves the current URL so the existing /auth page's
  // ?return= plumbing carries the user through OAuth + TOTP and back
  // to /pair with the same sid/code.
  const signinHref = useMemo(() => {
    if (typeof window === "undefined") return "/auth";
    const here = window.location.pathname + window.location.search;
    return `/auth?return=${encodeURIComponent(here)}`;
  }, []);

  async function doSubmit() {
    if (!auth.token) {
      setBanner({ kind: "err", text: "Not signed in. Tap 'Sign in to continue' to finish pairing." });
      return;
    }
    if (!payload?.sid) {
      setBanner({ kind: "err", text: "Pair URL is missing the sid parameter." });
      return;
    }
    if (!agentBase) {
      setBanner({ kind: "err", text: "No reachable agent URL — paste one below." });
      return;
    }
    // The submit secret is `code` (or, for Slice A, sid==code as a
    // fallback). The trust anchor is the short-lived agent-side
    // pairing window, not the URL.
    const submitCode = (payload.code || payload.sid).toUpperCase();
    setSubmitBusy(true);
    setBanner({ kind: "info", text: "Submitting your token…" });
    try {
      const res = await fetch(
        `${agentBase}/auth/pair/submit?code=${encodeURIComponent(submitCode)}`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            token: auth.token,
            convexSiteUrl: CONVEX_URL,
            userId: auth.user?.id ?? "",
          }),
        },
      );
      if (!res.ok) {
        let msg = `HTTP ${res.status}`;
        try {
          const data = await res.json();
          if (data?.error) msg = data.error;
        } catch {}
        setBanner({ kind: "err", text: `Pairing failed: ${msg}` });
        return;
      }
      const data = (await res.json()) as { host?: string };
      setBanner({
        kind: "ok",
        text: `Paired with ${data.host ?? probe?.hostname ?? "the agent"}. You can close this tab.`,
      });
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setBanner({ kind: "err", text: `Network error reaching the agent: ${msg}` });
    } finally {
      setSubmitBusy(false);
    }
  }

  // Friendly empty-state if the user landed here without a payload —
  // makes it easy to test the page directly.
  if (!payload) {
    return (
      <main className="mx-auto max-w-2xl px-4 py-12 text-surface-100">
        <h1 className="text-2xl font-semibold">Yaver Pairing</h1>
        <p className="mt-3 text-sm text-surface-400">
          This page expects a pair URL like
          <br />
          <code className="rounded bg-surface-900 px-1.5 py-0.5">
            https://yaver.io/pair?sid=ABC123&amp;target=http://10.0.0.5:18080&amp;code=ABC123
          </code>
          <br />
          generated by <code className="rounded bg-surface-900 px-1.5 py-0.5">yaver auth pair</code> or
          shown on first boot of a Yaver-imaged Pi.
        </p>
        <p className="mt-4 text-sm text-surface-400">
          Don&apos;t have a code yet? Open the{" "}
          <Link href="/dashboard" className="underline">
            dashboard
          </Link>{" "}
          to start a pairing session, or scan the QR shown on the headless machine&apos;s console.
        </p>
      </main>
    );
  }

  return (
    <main className="mx-auto max-w-2xl px-4 py-12 text-surface-100">
      <h1 className="text-2xl font-semibold">Pair this device</h1>
      <p className="mt-2 text-sm text-surface-400">
        {payload.host ? (
          <>
            Yaver agent <code className="rounded bg-surface-900 px-1.5 py-0.5">{payload.host}</code> is
            waiting for a token{expiryText}.
          </>
        ) : (
          <>This URL points at a Yaver pair session{expiryText}.</>
        )}
      </p>

      {auth.isLoading ? (
        <p className="mt-6 text-sm text-surface-400">Checking your sign-in…</p>
      ) : !auth.isAuthenticated ? (
        <section className="mt-8 rounded border border-surface-700 bg-surface-950/40 p-4">
          <h2 className="text-sm font-semibold">Sign in to continue</h2>
          <p className="mt-2 text-sm text-surface-400">
            Pairing forwards your Yaver session to the target machine. We&apos;ll bring you back here
            when sign-in completes — including any 2FA challenge if your account has one.
          </p>
          <Link
            href={signinHref}
            className="mt-4 inline-block rounded bg-indigo-600 px-4 py-2 text-sm font-semibold text-white"
          >
            Sign in to continue
          </Link>
          <p className="mt-3 text-xs text-surface-500">
            Prefer the mobile app? Open the Yaver app and paste this URL into More → Pair a device.
          </p>
        </section>
      ) : (
        <>
          <section className="mt-8 rounded border border-surface-700 bg-surface-950/40 p-4">
            <h2 className="text-xs font-semibold uppercase tracking-wider text-surface-400">
              Target agent URL
            </h2>
            <input
              className="mt-2 w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 font-mono text-sm"
              value={agentBase}
              onChange={(e) => setAgentBase(normalizeAgentBase(e.target.value))}
              placeholder="http://10.0.0.5:18080  or  https://relay.yaver.io/d/abc123"
            />
            {probeBusy && <p className="mt-2 text-xs text-surface-500">Probing…</p>}
            {probe && probe.ok && (
              <p className="mt-2 text-xs text-emerald-300">
                ✓ Session is open on {probe.hostname || "the agent"}.
              </p>
            )}
            {probe && !probe.ok && (
              <p className="mt-2 text-xs text-amber-300">
                {probe.error}. If the URL is correct, the pair window may have expired — restart{" "}
                <code className="rounded bg-surface-900 px-1 py-0.5">yaver auth pair</code> on the
                target.
              </p>
            )}
            {agentBase && /^http:\/\//i.test(agentBase) && typeof window !== "undefined" && window.location.protocol === "https:" && (
              <p className="mt-2 text-xs text-surface-500">
                This URL is plain HTTP. Some browsers block HTTPS → HTTP requests. If the submit
                fails, paste the agent&apos;s relay URL (https://...) instead, or run the pair
                from the mobile app.
              </p>
            )}
          </section>

          {banner && (
            <div
              role="alert"
              className={`mt-6 rounded border px-4 py-3 text-sm ${
                banner.kind === "err"
                  ? "border-red-500/30 bg-red-950/40 text-red-200"
                  : banner.kind === "ok"
                  ? "border-emerald-500/30 bg-emerald-950/40 text-emerald-200"
                  : "border-indigo-500/30 bg-indigo-950/40 text-indigo-200"
              }`}
            >
              {banner.text}
            </div>
          )}

          <button
            type="button"
            disabled={submitBusy || !agentBase || !payload.sid}
            onClick={() => void doSubmit()}
            className="mt-6 w-full rounded bg-indigo-600 px-4 py-2 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40"
          >
            {submitBusy ? "Pairing…" : `Pair as ${auth.user?.email || "this account"}`}
          </button>

          <p className="mt-3 text-xs text-surface-500">
            Your token is sent directly to the target agent — never stored on yaver.io. To revoke
            later, run <code className="rounded bg-surface-900 px-1 py-0.5">yaver auth pair revoke</code>{" "}
            on that machine.
          </p>
        </>
      )}

      <footer className="mt-12 text-xs text-surface-500">
        Pairing details:{" "}
        <code className="rounded bg-surface-900 px-1 py-0.5">sid={payload.sid}</code>
        {payload.mode && (
          <>
            {" "}
            · <code className="rounded bg-surface-900 px-1 py-0.5">mode={payload.mode}</code>
          </>
        )}
        . The QR + URL flow is optional — every existing pair path
        (manual passkey, mobile beacon adoption,{" "}
        <code className="rounded bg-surface-900 px-1 py-0.5">yaver auth send</code>) still works.
      </footer>
    </main>
  );
}
