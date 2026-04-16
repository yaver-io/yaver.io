"use client";

// /support — hosted landing for a Yaver remote-support session.
//
// Contract: the CLI prints a URL like
//   https://yaver.io/support?agent=<base>&code=<CODE>
// where <base> is either the host agent's relay URL
// (https://relay.yaver.io/d/<deviceId>) or, for LAN testing, a raw
// http://ip:18080. The guest opens it, we POST the code against
// <base>/support/redeem, and then speak to the host's agent with the
// returned bearer — exec, files, basic status.
//
// This is deliberately an unauthenticated page. The support code IS
// the credential. The bearer it redeems stays in memory only — we
// never localStorage it, so a tab close revokes the guest's access
// on the guest side. The host can also revoke unilaterally with
// `yaver support stop`.

import { useEffect, useRef, useState } from "react";

type SupportInfo = {
  active: boolean;
  host?: string;
  token?: string;
  code?: string;
  label?: string;
  expiresAt?: string;
  ttlSeconds?: number;
  allowed?: string[];
};

function normalizeAgentBase(raw: string): string {
  return raw.trim().replace(/\/+$/, "");
}

export default function SupportPage() {
  const [agentBase, setAgentBase] = useState("");
  const [codeInput, setCodeInput] = useState("");
  const [bearer, setBearer] = useState("");
  const [session, setSession] = useState<SupportInfo | null>(null);
  const [probe, setProbe] = useState<SupportInfo | null>(null);
  const [banner, setBanner] = useState<{ kind: "info" | "err"; text: string } | null>(null);
  const [cmd, setCmd] = useState("");
  const [cmdOut, setCmdOut] = useState("");
  const [cmdRunning, setCmdRunning] = useState(false);

  // Read query params on first render so a link from the CLI fires
  // everything in one shot.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const q = new URLSearchParams(window.location.search);
    const rawAgent = q.get("agent") || q.get("url") || q.get("relay") || "";
    const code = (q.get("code") || q.get("support") || q.get("c") || "").trim().toUpperCase();
    if (rawAgent) setAgentBase(normalizeAgentBase(rawAgent));
    if (code) setCodeInput(code);
  }, []);

  // Probe /support/info whenever the agent base changes so the user sees
  // "is a session open?" before they even type a code.
  useEffect(() => {
    if (!agentBase) {
      setProbe(null);
      return;
    }
    let abort = false;
    const run = async () => {
      try {
        const r = await fetch(`${agentBase}/support/info`);
        const info = (await r.json()) as SupportInfo;
        if (!abort) setProbe(info);
      } catch {
        if (!abort) setProbe(null);
      }
    };
    run();
    return () => {
      abort = true;
    };
  }, [agentBase]);

  // Auto-redeem when both pieces arrive via URL.
  useEffect(() => {
    if (agentBase && codeInput && !bearer && !session) {
      void redeemNow(agentBase, codeInput);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentBase, codeInput]);

  async function redeemNow(base: string, code: string) {
    setBanner(null);
    try {
      const r = await fetch(`${base}/support/redeem`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code }),
      });
      const info = (await r.json()) as SupportInfo & { error?: string };
      if (!r.ok) {
        throw new Error(info.error || `HTTP ${r.status}`);
      }
      if (!info.token) {
        throw new Error("no token in redeem response");
      }
      setBearer(info.token);
      setSession(info);
      setBanner({
        kind: "info",
        text: `Connected to ${info.host ?? "remote"}. The bearer is kept in memory only — close the tab to end your side of the session.`,
      });
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setBanner({ kind: "err", text: `Redeem failed: ${msg}` });
    }
  }

  const pollIdRef = useRef<number | null>(null);
  async function runCommand() {
    if (!bearer || !agentBase || !cmd.trim()) return;
    setCmdRunning(true);
    setCmdOut("(running…)\n");
    try {
      const r = await fetch(`${agentBase}/exec`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${bearer}`,
        },
        body: JSON.stringify({ command: cmd, timeout: 120 }),
      });
      const j = await r.json();
      if (!r.ok) {
        setCmdOut(`error: ${j.error || r.status}`);
        setCmdRunning(false);
        return;
      }
      const execId = j.execId as string;
      let seenOut = 0;
      let seenErr = 0;
      setCmdOut("");
      pollIdRef.current = window.setInterval(async () => {
        try {
          const rr = await fetch(`${agentBase}/exec/${execId}`, {
            headers: { Authorization: `Bearer ${bearer}` },
          });
          const jj = await rr.json();
          const sess = jj.exec || {};
          const so = (sess.stdout as string) || "";
          const se = (sess.stderr as string) || "";
          if (so.length > seenOut) {
            setCmdOut((prev) => prev + so.slice(seenOut));
            seenOut = so.length;
          }
          if (se.length > seenErr) {
            setCmdOut((prev) => prev + se.slice(seenErr));
            seenErr = se.length;
          }
          const status = sess.status as string;
          if (status === "completed" || status === "failed") {
            if (pollIdRef.current !== null) {
              window.clearInterval(pollIdRef.current);
              pollIdRef.current = null;
            }
            setCmdOut(
              (prev) =>
                prev + `\n[exit ${sess.exitCode ?? "?"}]`,
            );
            setCmdRunning(false);
          }
        } catch {
          /* transient — keep polling */
        }
      }, 300);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      setCmdOut(`error: ${msg}`);
      setCmdRunning(false);
    }
  }

  useEffect(
    () => () => {
      if (pollIdRef.current !== null) window.clearInterval(pollIdRef.current);
    },
    [],
  );

  return (
    <main className="mx-auto max-w-3xl px-4 py-12 text-surface-100">
      <h1 className="text-2xl font-semibold">Yaver Remote Support</h1>
      <p className="mt-2 text-sm text-surface-400">
        Paste an agent URL and a 6-character code from{" "}
        <code className="rounded bg-surface-900 px-1.5 py-0.5">yaver support start</code>. A
        successful redeem gives you scoped access — terminal, exec, file browse — until the host
        revokes the session or its TTL expires. Nothing is stored.
      </p>

      {banner && (
        <div
          role="alert"
          className={`mt-6 rounded border px-4 py-3 text-sm ${
            banner.kind === "err"
              ? "border-red-500/30 bg-red-950/40 text-red-200"
              : "border-emerald-500/30 bg-emerald-950/40 text-emerald-200"
          }`}
        >
          {banner.text}
        </div>
      )}

      <section className="mt-8">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-surface-400">
          Agent URL
        </h2>
        <input
          className="mt-2 w-full rounded border border-surface-700 bg-surface-900 px-3 py-2 font-mono text-sm"
          value={agentBase}
          onChange={(e) => setAgentBase(normalizeAgentBase(e.target.value))}
          placeholder="https://relay.yaver.io/d/abc123  or  http://10.0.0.5:18080"
        />
        {probe && (
          <p className="mt-2 text-xs text-surface-400">
            {probe.active
              ? `✓ Host ${probe.host ?? ""} is accepting support requests (expires ${probe.expiresAt}).`
              : "This agent does not currently have a support session open. Ask the host to run 'yaver support start'."}
          </p>
        )}
      </section>

      <section className="mt-6">
        <h2 className="text-xs font-semibold uppercase tracking-wider text-surface-400">Code</h2>
        <div className="mt-2 flex gap-2">
          <input
            className="flex-1 rounded border border-surface-700 bg-surface-900 px-3 py-2 font-mono text-lg uppercase tracking-widest"
            value={codeInput}
            onChange={(e) => setCodeInput(e.target.value.toUpperCase())}
            placeholder="ABCD23"
            maxLength={6}
            autoComplete="off"
          />
          <button
            type="button"
            disabled={!agentBase || codeInput.length !== 6}
            onClick={() => void redeemNow(agentBase, codeInput)}
            className="rounded bg-indigo-600 px-4 py-2 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40"
          >
            Redeem
          </button>
        </div>
      </section>

      {session && (
        <section className="mt-8 rounded border border-surface-700 bg-surface-950/40 p-4">
          <h2 className="text-xs font-semibold uppercase tracking-wider text-surface-400">
            Run a command
          </h2>
          <div className="mt-2 flex gap-2">
            <input
              className="flex-1 rounded border border-surface-700 bg-surface-900 px-3 py-2 font-mono text-sm"
              value={cmd}
              onChange={(e) => setCmd(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !cmdRunning) void runCommand();
              }}
              placeholder="uname -a"
              autoComplete="off"
            />
            <button
              type="button"
              disabled={!cmd.trim() || cmdRunning}
              onClick={() => void runCommand()}
              className="rounded bg-indigo-600 px-4 py-2 text-sm font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40"
            >
              {cmdRunning ? "Running…" : "Run"}
            </button>
          </div>
          <pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap rounded bg-black/40 p-3 font-mono text-xs">
            {cmdOut || "(no output yet)"}
          </pre>
          <p className="mt-2 text-xs text-surface-500">
            Allowed endpoints: {session.allowed?.join(", ") || "(unknown)"}
          </p>
        </section>
      )}

      <footer className="mt-12 text-xs text-surface-500">
        The Yaver agent is open source — see <code>/support</code> endpoints in{" "}
        <a
          href="https://github.com/kivanccakmak/yaver.io"
          className="underline"
        >
          the agent code
        </a>{" "}
        for exactly what this page can and cannot do.
      </footer>
    </main>
  );
}
