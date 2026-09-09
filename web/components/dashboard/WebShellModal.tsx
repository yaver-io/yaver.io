"use client";

// Cloud-console-style "open shell from console" modal.
// Hosts the existing TerminalView (xterm.js + agent /ws/terminal PTY
// over relay) so the device's shell opens directly in the dashboard
// without needing a local SSH or terminal app.
//
// States:
//   - needs-reauth: device is online but the agent's Convex session
//     expired. Convex will reject the WebSocket session-token issue,
//     so we route the user to Rescue → Reset Auth or `yaver auth` on
//     the box instead of presenting a dead terminal.
//   - not-connected: agentClient is pointed elsewhere (or nowhere).
//     We show a "Connect & open shell" CTA.
//   - connecting: a connect attempt is in flight. BOUNDED — see below.
//   - failed: the connect attempt is over and it did not work. This
//     state exists because without it the modal hung on "Connecting to
//     @linux-2…" forever: page.tsx sets connectedDevice *before* it
//     probes and never clears it on failure, so isCurrentDeviceSelected
//     stays true while agentClient sits in "error" with a backoff
//     reconnect pending. The user saw a spinner-shaped lie for a box
//     that had already returned "Could not reach agent (direct, tunnel,
//     or relay)". We now read agentClient's own connectionState (plus a
//     hard timeout, so a wedged attempt can't stall us either) and show
//     the real reason with a retry.
//   - ready: mount TerminalView. WebSocket is created/torn down with
//     the modal lifecycle (mount on open, dispose on close).
//
// RUNNER LAUNCH GATE (claude / codex) — see lib/runnerLaunchGate.ts for the
// decision table and its tests. Short version of why it was rewritten on
// 2026-07-27: clicking Codex on a remote box parked this modal on
// "CHECKING RUNNER AUTH · 12s…" with the PTY closed, because the gate called
// `POST /agent/runners/test`, which for codex spawns a REAL `codex exec`
// generation. Measured on the live box: 5.3 s and 6,212 tokens of the user's
// paid quota per click, under a 20 s client budget, plus relay RTT — to
// rediscover what `/runner-auth/status` answered in 0.20 s for free
// (`authVerified: true`), and what the device heartbeat row was already
// carrying before the click happened.
//
// The contract now: a device row that proves the runner signed in opens the
// PTY with zero network; a row that proves it signed out routes straight to
// RunnerAuthModal; anything unknown gets ONE cheap /runner-auth/status check
// bounded at RUNNER_VERIFY_BUDGET_MS, narrated, and failing OPEN with a named
// banner. Once the terminal is mounted it is never yanked back — a late
// contradicting answer raises the sign-in modal OVER the live session instead.

"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import TerminalView from "./TerminalView";
import { agentClient, type ConnectAttemptDiagnostic } from "@/lib/agent-client";
import { getLastFailure, subscribeLastFailure } from "@/lib/probe-backoff";
import { deriveBrowserReach, type BrowserReach } from "@/lib/device-lifecycle";
import {
  RUNNER_VERIFY_BUDGET_MS,
  decideRunnerLaunchGate,
  decisionOpensTerminal,
  findRunnerRow,
  isGatedRunner,
  probeFromStatusRow,
  type RunnerLaunchDecision,
  type RunnerProbeOutcome,
} from "@/lib/runnerLaunchGate";
import type { Device } from "@/lib/use-devices";

// A connect attempt that hasn't resolved by now is not going to. attemptConnect
// budgets 8s per relay candidate, 8s per tunnel candidate and 5s direct, then
// page.tsx may run one auto-reauth + a second full pass. 90s covers the whole
// worst case with headroom; past that we stop claiming progress.
const CONNECT_STALL_MS = 90_000;
function useAgentConnectionState(): string {
  const [state, setState] = useState<string>(() => agentClient.connectionState);
  useEffect(() => {
    const unsubscribe = agentClient.on("connectionState", (s) => setState(s));
    setState(agentClient.connectionState);
    return unsubscribe;
  }, []);
  return state;
}

/** Same reachability signal the device card badge and CTAs use. */
function useBrowserReach(device: Device): BrowserReach {
  const [failure, setFailure] = useState(() => getLastFailure(device.id));
  useEffect(() => {
    setFailure(getLastFailure(device.id));
    return subscribeLastFailure(() => setFailure(getLastFailure(device.id)));
  }, [device.id]);
  return deriveBrowserReach(device, failure);
}

function describeDiagnostic(d: ConnectAttemptDiagnostic): string {
  const where = d.path === "relay" ? `relay${d.relayId ? ` (${d.relayId})` : ""}` : d.path;
  const why = d.authExpired
    ? "agent session expired"
    : d.error || (d.status ? `HTTP ${d.status}` : "failed");
  return `${where}: ${why}`;
}

export default function WebShellModal({
  device,
  launch,
  tmuxSession,
  tmuxTaskId,
  isCurrentDeviceSelected,
  isCurrentDeviceConnected,
  onClose,
  onConnect,
  onOpenRescue,
  onRunnerNeedsAuth,
  onTmuxClosed,
}: {
  device: Device;
  launch?: "claude" | "codex" | "opencode";
  tmuxSession?: string;
  tmuxTaskId?: string;
  isCurrentDeviceSelected: boolean;
  isCurrentDeviceConnected: boolean;
  onClose: () => void;
  onConnect: () => void;
  onOpenRescue?: () => void;
  onRunnerNeedsAuth?: (runner: "claude" | "codex") => void;
  onTmuxClosed?: () => void;
}) {
  const [maximized, setMaximized] = useState(false);
  const [gateCheck, setGateCheck] = useState<{
    key: string;
    startedAt: number;
    probe: RunnerProbeOutcome | null;
  } | null>(null);
  // Keys the user (or a converged decision) has already opened a terminal for.
  // Latched so a late answer cannot unmount a session someone is typing in.
  const [openedKey, setOpenedKey] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const connState = useAgentConnectionState();
  const reach = useBrowserReach(device);
  const hasRelay = agentClient.configuredRelayServers.length > 0;

  // Bounded "connecting": if the attempt neither succeeds nor flips
  // agentClient to "error" within CONNECT_STALL_MS, we call it failed
  // ourselves rather than spinning forever.
  const [stalled, setStalled] = useState(false);
  const attemptActive = isCurrentDeviceSelected && !isCurrentDeviceConnected && connState !== "error";
  useEffect(() => {
    if (!attemptActive) {
      setStalled(false);
      return;
    }
    setStalled(false);
    const t = setTimeout(() => setStalled(true), CONNECT_STALL_MS);
    return () => clearTimeout(t);
  }, [attemptActive, device.id]);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  const reauthRequired = Boolean(device.needsAuth);
  const state: "needs-reauth" | "not-connected" | "connecting" | "failed" | "ready" = reauthRequired
    ? "needs-reauth"
    : isCurrentDeviceConnected
      ? "ready"
      : !isCurrentDeviceSelected
        ? "not-connected"
        : connState === "error" || stalled
          ? "failed"
          : "connecting";

  const failureDiagnostics = state === "failed" ? agentClient.lastConnectDiagnostics.filter((d) => !d.ok) : [];
  const failureHeadline = stalled && connState !== "error"
    ? "The connect attempt didn't finish."
    : failureDiagnostics.some((d) => d.authExpired)
      ? "The agent answered, but its Yaver session is expired."
      : reach.label
        ? reach.label
        : "Could not reach the agent (direct, tunnel, or relay).";
  const title = tmuxSession
    ? `Yaver session · ${tmuxSession}`
    : launch === "claude"
    ? "Claude"
    : launch === "codex"
      ? "Codex"
      : launch === "opencode"
        ? "OpenCode"
        : "Shell";
  const authSensitiveLaunch = isGatedRunner(launch);
  const gateKey = state === "ready" && authSensitiveLaunch ? `${device.id}:${launch}` : null;

  // The cheap check. `/runner-auth/status` reads each runner's own login state
  // (for codex: a cached `codex login status`; measured 0.20s on the live box)
  // and spends no tokens. It runs even on the fast path — but only as
  // BACKGROUND corroboration, because decideRunnerLaunchGate already answered
  // from the device row and never waits on this.
  useEffect(() => {
    if (!gateKey || !launch) {
      setGateCheck(null);
      return;
    }
    let cancelled = false;
    setGateCheck({ key: gateKey, startedAt: Date.now(), probe: null });
    (async () => {
      let outcome: RunnerProbeOutcome;
      try {
        const rows = await agentClient.runnerAuthStatus();
        outcome = probeFromStatusRow(findRunnerRow(rows, launch), launch);
      } catch (err) {
        outcome = { state: "error", reason: err instanceof Error ? err.message : String(err) };
      }
      if (cancelled) return;
      setGateCheck((cur) => (cur?.key === gateKey ? { ...cur, probe: outcome } : cur));
    })();
    return () => {
      cancelled = true;
    };
  }, [gateKey, launch]);

  const gateDecision: RunnerLaunchDecision = useMemo(() => {
    const live = gateCheck?.key === gateKey ? gateCheck : null;
    return decideRunnerLaunchGate({
      runner: launch,
      deviceRunners: device.runners,
      probe: live?.probe ?? null,
      elapsedMs: live ? now - live.startedAt : 0,
    });
  }, [device.runners, gateCheck, gateKey, launch, now]);

  // Latch: the instant any decision opens a terminal, that key stays open. A
  // slow answer arriving afterwards must never unmount a live PTY.
  const terminalOpen = !authSensitiveLaunch || openedKey === gateKey || decisionOpensTerminal(gateDecision);
  useEffect(() => {
    if (!gateKey) {
      setOpenedKey(null);
      return;
    }
    if (decisionOpensTerminal(gateDecision) && openedKey !== gateKey) setOpenedKey(gateKey);
  }, [gateDecision, gateKey, openedKey]);

  // Route into the existing runner sign-in flow (RunnerAuthModal → browser
  // OAuth, Claude's code/token submission, the Deliver callback lane). Fires
  // once per key, and also when a BACKGROUND check contradicts a fast-path
  // open — in that case the modal simply appears over the live terminal.
  const signInRaisedFor = useRef<string | null>(null);
  const backgroundNeedsAuth =
    gateCheck?.key === gateKey && gateCheck.probe?.state === "needs-auth";
  useEffect(() => {
    if (!gateKey || !launch || !isGatedRunner(launch)) return;
    if (gateDecision.kind !== "sign-in" && !backgroundNeedsAuth) return;
    if (signInRaisedFor.current === gateKey) return;
    signInRaisedFor.current = gateKey;
    onRunnerNeedsAuth?.(launch);
  }, [backgroundNeedsAuth, gateDecision, gateKey, launch, onRunnerNeedsAuth]);
  useEffect(() => {
    if (signInRaisedFor.current && signInRaisedFor.current !== gateKey) signInRaisedFor.current = null;
  }, [gateKey]);

  const terminalLaunch = authSensitiveLaunch ? (terminalOpen ? launch : undefined) : launch;
  const degradedBanner =
    terminalOpen && gateDecision.kind === "open-degraded" ? gateDecision.banner : null;
  const openRunnerPtyAnyway = () => {
    if (gateKey) setOpenedKey(gateKey);
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-black/70 backdrop-blur-sm p-0 sm:p-8"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className={`flex w-full flex-col overflow-hidden border border-slate-200 bg-white shadow-2xl dark:border-surface-700 dark:bg-[#0b0d10] ${
          maximized ? "max-w-none rounded-none h-screen sm:rounded-none" : "max-w-5xl rounded-none sm:rounded-xl"
        }`}
      >
        <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-slate-200 bg-slate-50/95 px-3 py-2.5 dark:border-surface-800 dark:bg-surface-900/80 sm:px-4">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <span className={`inline-flex h-2 w-2 shrink-0 rounded-full ${state === "ready" ? "bg-emerald-400" : state === "needs-reauth" ? "bg-amber-400" : state === "failed" ? "bg-rose-400" : state === "connecting" ? "bg-cyan-400" : "bg-slate-400 dark:bg-surface-500"}`} />
            <span className="min-w-0 truncate text-[13px] font-semibold text-slate-900 dark:text-surface-100">
              {title} · {device.alias ? `@${device.alias}` : device.name}
            </span>
            <span className="hidden max-w-[12rem] shrink truncate text-[11px] text-slate-500 dark:text-surface-500 2xl:inline">
              {device.host}:{device.port}
            </span>
          </div>
          <div className="flex min-w-[5.75rem] shrink-0 items-center justify-end gap-1.5 sm:gap-2">
            <span className="hidden max-w-[7rem] truncate rounded-full border border-slate-200 bg-white px-2 py-0.5 text-[10px] uppercase tracking-[0.08em] text-slate-500 dark:border-surface-700 dark:bg-surface-950/60 dark:text-surface-400 xl:inline">
              {state === "needs-reauth" ? "auth required" : state === "failed" ? "unreachable" : state === "connecting" ? "connecting" : "PTY"}
            </span>
            <button
              onClick={() => setMaximized((m) => !m)}
              className="grid h-7 w-7 place-items-center rounded-md border border-slate-200 bg-white text-[11px] text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-300 dark:hover:border-surface-600 dark:hover:text-surface-100"
              title={maximized ? "Restore" : "Maximize"}
            >
              {maximized ? "❐" : "⛶"}
            </button>
            <button
              onClick={onClose}
              className="h-7 rounded-md border border-slate-200 bg-white px-2 text-[11px] text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-300 dark:hover:border-surface-600 dark:hover:text-surface-100"
              title="Close (Esc)"
            >
              Close
            </button>
          </div>
        </div>
        <div className={`flex-1 overflow-hidden ${state === "ready" ? "bg-[#0b0d10]" : "bg-slate-50/70 dark:bg-transparent p-2"}`}>
          {state === "ready" ? (
            authSensitiveLaunch && !terminalOpen ? (
              <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center text-slate-700 dark:text-surface-300">
                <div
                  className={
                    gateDecision.kind === "verify"
                      ? "rounded-full border border-sky-300 bg-sky-50 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-sky-700 dark:border-sky-500/30 dark:bg-sky-500/10 dark:text-sky-200"
                      : "rounded-full border border-indigo-300 bg-indigo-50 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-indigo-700 dark:border-indigo-500/30 dark:bg-indigo-500/10 dark:text-indigo-200"
                  }
                >
                  {gateDecision.kind === "verify" ? "Confirming login" : `Sign in to ${title}`}
                </div>
                <p className="max-w-md text-[13px] leading-5">
                  {gateDecision.kind === "verify"
                    ? `${gateDecision.detail} · ${gateDecision.elapsedSec}s — the terminal opens either way in ${gateDecision.remainingSec}s.`
                    : gateDecision.kind === "sign-in"
                      ? gateDecision.reason
                      : `${title} is not ready on this machine.`}
                </p>
                {gateDecision.kind === "verify" ? (
                  <div className="w-full max-w-md rounded-md border border-sky-300 bg-sky-50 p-3 text-left text-[12px] leading-5 text-sky-800 dark:border-sky-500/30 dark:bg-sky-500/10 dark:text-sky-100">
                    <div className="font-semibold">Reading the cached runner login</div>
                    <div>
                      <code className="rounded bg-white/70 px-1 py-0.5 font-mono dark:bg-surface-950/70">/runner-auth/status</code>{" "}
                      asks the {title} CLI about its own sign-in. It spends none of your {title} quota — unlike the
                      probe this replaced, which billed a real generation on every click.
                    </div>
                    <div className="mt-1 opacity-80">
                      We only check because the last heartbeat from this box did not carry a confirmed {title} login.
                      If it does not answer in time the terminal opens anyway and {title} shows its own prompt.
                    </div>
                  </div>
                ) : (
                  <div className="w-full max-w-md rounded-md border border-indigo-300 bg-indigo-50 p-3 text-left text-[12px] leading-5 text-indigo-800 dark:border-indigo-500/30 dark:bg-indigo-500/10 dark:text-indigo-100">
                    Sign-in runs on {device.alias ? `@${device.alias}` : device.name} itself — the browser OAuth opens
                    from here and the callback is delivered back to that box.
                    {launch === "claude" ? " You can also paste a code or token if the browser round-trip is blocked." : ""}
                  </div>
                )}
                {gateDecision.kind === "sign-in" ? (
                  <div className="flex flex-wrap items-center justify-center gap-2">
                    <button
                      onClick={() => { if (isGatedRunner(launch)) onRunnerNeedsAuth?.(launch); }}
                      className="rounded-md border border-indigo-300 bg-indigo-50 px-4 py-2 text-[12px] font-semibold text-indigo-700 hover:bg-indigo-100 dark:border-indigo-500/40 dark:bg-indigo-500/10 dark:text-indigo-200 dark:hover:bg-indigo-500/15"
                    >
                      Sign in to {title}
                    </button>
                    <button
                      onClick={openRunnerPtyAnyway}
                      className="rounded-md border border-slate-300 bg-white px-4 py-2 text-[12px] font-semibold text-slate-700 hover:bg-slate-50 dark:border-surface-600 dark:bg-surface-900 dark:text-surface-200 dark:hover:bg-surface-800"
                    >
                      Open {title} PTY anyway
                    </button>
                  </div>
                ) : (
                  <button
                    onClick={openRunnerPtyAnyway}
                    className="rounded-md border border-slate-300 bg-white px-4 py-2 text-[12px] font-semibold text-slate-700 hover:bg-slate-50 dark:border-surface-600 dark:bg-surface-900 dark:text-surface-200 dark:hover:bg-surface-800"
                  >
                    Skip the check — open {title} now
                  </button>
                )}
              </div>
            ) : (
              <div className="flex h-full flex-col">
                {/* Failing open is only honest if it SAYS what it could not
                    confirm. A silent degraded terminal is the same defect as
                    the spinner it replaced, one step later. */}
                {degradedBanner ? (
                  <div className="flex shrink-0 items-start gap-2 border-b border-amber-500/30 bg-amber-500/10 px-3 py-2 text-left text-[11px] leading-4 text-amber-800 dark:text-amber-100">
                    <span aria-hidden>⚠️</span>
                    <span className="min-w-0 flex-1">{degradedBanner}</span>
                    {isGatedRunner(launch) ? (
                      <button
                        onClick={() => onRunnerNeedsAuth?.(launch)}
                        className="shrink-0 rounded border border-amber-500/50 px-2 py-0.5 font-semibold hover:bg-amber-500/20"
                      >
                        Sign in
                      </button>
                    ) : null}
                  </div>
                ) : null}
                <div className="min-h-0 flex-1">
                  <TerminalView
                    launch={terminalLaunch}
                    tmuxSession={tmuxSession}
                    tmuxTaskId={tmuxTaskId}
                    sshProfile={device.sshProfile}
                    onRunnerNeedsAuth={onRunnerNeedsAuth}
                    onCloseTerminal={onClose}
                    onTmuxClosed={onTmuxClosed}
                  />
                </div>
              </div>
            )
          ) : state === "needs-reauth" ? (
            <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center text-slate-700 dark:text-surface-300">
              <div className="rounded-full border border-amber-300 bg-amber-50 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
                Reauth required
              </div>
              <p className="max-w-md text-[13px] leading-5">
                The agent on{" "}
                <span className="font-mono text-amber-700 dark:text-amber-200">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>{" "}
                is reachable but its Yaver session expired. Convex won&apos;t
                authenticate the PTY WebSocket until the agent re-auths.
              </p>
              <div className="flex w-full max-w-md flex-col gap-2 text-left text-[12px] text-slate-700 dark:text-surface-300">
                <div className="rounded-md border border-slate-200 bg-white p-3 dark:border-surface-700 dark:bg-surface-900/60">
                  <p className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 dark:text-surface-400">
                    From this dashboard
                  </p>
                  <p className="mt-1">
                    Open <span className="text-amber-700 dark:text-amber-200">Rescue → Reset Auth</span>{" "}
                    on the device card. The agent restarts in bootstrap mode
                    and you re-pair from the mobile app or by running{" "}
                    <code className="rounded bg-slate-100 px-1 py-0.5 text-amber-700 dark:bg-surface-950 dark:text-amber-200">yaver auth</code>{" "}
                    on the box.
                  </p>
                  {onOpenRescue ? (
                    <button
                      onClick={() => { onClose(); onOpenRescue(); }}
                      className="mt-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-1.5 text-[11px] font-semibold text-amber-700 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:bg-amber-500/15"
                    >
                      Open Rescue
                    </button>
                  ) : null}
                </div>
                <div className="rounded-md border border-slate-200 bg-white p-3 dark:border-surface-700 dark:bg-surface-900/60">
                  <p className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 dark:text-surface-400">
                    From the device terminal
                  </p>
                  <p className="mt-1">
                    Run{" "}
                    <code className="rounded bg-slate-100 px-1 py-0.5 text-emerald-700 dark:bg-surface-950 dark:text-emerald-200">yaver auth</code>{" "}
                    on the box (browser sign-in opens automatically). Once it
                    finishes, click Connect &amp; open shell here.
                  </p>
                </div>
                <div className="rounded-md border border-slate-200 bg-white p-3 dark:border-surface-700 dark:bg-surface-900/60">
                  <p className="text-[11px] font-semibold uppercase tracking-[0.14em] text-slate-500 dark:text-surface-400">
                    From the mobile app
                  </p>
                  <p className="mt-1">
                    Open the device in the Yaver app and tap{" "}
                    <span className="text-sky-700 dark:text-sky-200">Reauth this device</span> in
                    the attention banner — pairing finishes over the relay
                    even if you&apos;re off the device&apos;s LAN.
                  </p>
                </div>
              </div>
            </div>
          ) : state === "connecting" ? (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center text-slate-700 dark:text-surface-300">
              <p className="text-[13px]">
                Connecting to{" "}
                <span className="font-mono text-cyan-700 dark:text-cyan-300">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>{" "}
                before opening the PTY.
              </p>
              <p className="text-[11px] text-slate-500 dark:text-surface-500">
                Remote machines such as relay-only boxes need the dashboard connection to finish first.
              </p>
            </div>
          ) : state === "failed" ? (
            <div className="flex h-full flex-col items-center justify-center gap-4 px-6 text-center text-slate-700 dark:text-surface-300">
              <div className="rounded-full border border-rose-300 bg-rose-50 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-200">
                Can&apos;t open shell
              </div>
              <p className="max-w-md text-[13px] leading-5">
                {failureHeadline}{" "}
                <span className="font-mono text-rose-700 dark:text-rose-200">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>{" "}
                heartbeats to Convex, but this browser has no working path to it,
                so there is no PTY to attach to.
              </p>
              {failureDiagnostics.length ? (
                <ul className="w-full max-w-md space-y-1 rounded-md border border-slate-200 bg-white p-3 text-left font-mono text-[11px] text-slate-600 dark:border-surface-700 dark:bg-surface-900/60 dark:text-surface-400">
                  {failureDiagnostics.map((d, i) => (
                    <li key={`${d.path}-${i}`} className="truncate">· {describeDiagnostic(d)}</li>
                  ))}
                </ul>
              ) : null}
              <p className="max-w-md text-[11px] leading-4 text-slate-500 dark:text-surface-500">
                {failureDiagnostics.some((d) => d.authExpired) || reach.reason === "unauthorized"
                  ? "The box refused our token — run `yaver auth` on it (or use Rescue → Reset Auth), then retry."
                  : hasRelay
                    ? "Its QUIC tunnel to the relay is most likely down. Restart the agent with `yaver serve` on the box, then retry."
                    : "No relay server is configured for this dashboard, so a box that isn't reachable on your LAN can't be reached at all from the browser."}
              </p>
              <div className="flex flex-wrap items-center justify-center gap-2">
                <button
                  onClick={onConnect}
                  className="rounded-md border border-slate-300 bg-white px-4 py-2 text-[12px] font-semibold text-slate-700 hover:bg-slate-50 dark:border-surface-600 dark:bg-surface-900 dark:text-surface-200 dark:hover:bg-surface-800"
                >
                  Retry
                </button>
                {onOpenRescue ? (
                  <button
                    onClick={() => { onClose(); onOpenRescue(); }}
                    className="rounded-md border border-amber-300 bg-amber-50 px-4 py-2 text-[12px] font-semibold text-amber-700 hover:bg-amber-100 dark:border-amber-500/40 dark:bg-amber-500/10 dark:text-amber-200 dark:hover:bg-amber-500/15"
                  >
                    Open Rescue
                  </button>
                ) : null}
              </div>
            </div>
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center text-slate-700 dark:text-surface-300">
              <p className="text-[13px]">
                Browser shell needs an active agent connection to{" "}
                <span className="font-mono text-emerald-700 dark:text-emerald-300">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>
                .
              </p>
              {/* Don't promise a connection we already know is failing. The
                  device card downgrades its own badge off this same record
                  ("Ready to Connect (Unauthorized)"); the shell CTA used to
                  ignore it and invite a click that could only end in a hang. */}
              {reach.unreachable ? (
                <p className="max-w-md rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-[11px] leading-4 text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
                  Last browser probe of this box failed: <span className="font-semibold">{reach.label}</span>.{" "}
                  {reach.detail} Connecting will probably fail too.
                </p>
              ) : null}
              <button
                onClick={onConnect}
                className={reach.unreachable
                  ? "rounded-md border border-slate-300 bg-white px-4 py-2 text-[12px] font-semibold text-slate-700 hover:bg-slate-50 dark:border-surface-600 dark:bg-surface-900 dark:text-surface-200 dark:hover:bg-surface-800"
                  : "rounded-md border border-emerald-300 bg-emerald-50 px-4 py-2 text-[12px] font-semibold text-emerald-700 hover:bg-emerald-100 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200 dark:hover:bg-emerald-500/15"}
              >
                {reach.unreachable ? "Try connecting anyway" : "Connect & open shell"}
              </button>
              <p className="text-[11px] text-slate-500 dark:text-surface-500">
                {hasRelay
                  ? "Once connected the PTY opens through the relay — works even when direct LAN is unreachable."
                  : "No relay server is configured, so this needs the box to be reachable directly from your browser."}
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
