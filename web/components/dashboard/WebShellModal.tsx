"use client";

// Hetzner / GCP / AWS-style "open shell from console" modal.
// Hosts the existing TerminalView (xterm.js + agent /ws/terminal PTY
// over relay) so the device's shell opens directly in the dashboard
// without needing a local SSH or terminal app.
//
// Three states:
//   - needs-reauth: device is online but the agent's Convex session
//     expired. Convex will reject the WebSocket session-token issue,
//     so we route the user to Rescue → Reset Auth or `yaver auth` on
//     the box instead of presenting a dead terminal.
//   - not-connected: agentClient is pointed elsewhere (or nowhere).
//     We show a "Connect & open shell" CTA.
//   - ready: mount TerminalView. WebSocket is created/torn down with
//     the modal lifecycle (mount on open, dispose on close).

import TerminalView from "./TerminalView";
import type { Device } from "@/lib/use-devices";

export default function WebShellModal({
  device,
  isCurrentDeviceSelected,
  isCurrentDeviceConnected,
  onClose,
  onConnect,
  onOpenRescue,
}: {
  device: Device;
  isCurrentDeviceSelected: boolean;
  isCurrentDeviceConnected: boolean;
  onClose: () => void;
  onConnect: () => void;
  onOpenRescue?: () => void;
}) {
  const reauthRequired = Boolean(device.needsAuth) && !device.isGuest;
  const state: "needs-reauth" | "not-connected" | "connecting" | "ready" = reauthRequired
    ? "needs-reauth"
    : isCurrentDeviceConnected
      ? "ready"
      : isCurrentDeviceSelected
        ? "connecting"
        : "not-connected";

  return (
    <div
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-black/70 backdrop-blur-sm p-0 sm:p-8"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="flex w-full max-w-5xl flex-col overflow-hidden rounded-none border border-slate-200 bg-white shadow-2xl dark:border-surface-700 dark:bg-[#0b0d10] sm:rounded-xl"
      >
        <div className="flex items-center justify-between border-b border-slate-200 bg-slate-50/95 px-4 py-2.5 dark:border-surface-800 dark:bg-surface-900/80">
          <div className="flex items-center gap-2 min-w-0">
            <span className={`inline-flex h-2 w-2 rounded-full ${state === "ready" ? "bg-emerald-400" : state === "needs-reauth" ? "bg-amber-400" : state === "connecting" ? "bg-cyan-400" : "bg-slate-400 dark:bg-surface-500"}`} />
            <span className="truncate text-[13px] font-semibold text-slate-900 dark:text-surface-100">
              Shell · {device.alias ? `@${device.alias}` : device.name}
            </span>
            <span className="hidden sm:inline truncate text-[11px] text-slate-500 dark:text-surface-500">
              {device.host}:{device.port}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden sm:inline rounded-full border border-slate-200 bg-white px-2 py-0.5 text-[10px] uppercase tracking-[0.14em] text-slate-500 dark:border-surface-700 dark:bg-surface-950/60 dark:text-surface-400">
              {state === "needs-reauth" ? "agent auth required" : state === "connecting" ? "connecting…" : "via relay · PTY"}
            </span>
            <button
              onClick={onClose}
              className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-[11px] text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-300 dark:hover:border-surface-600 dark:hover:text-surface-100"
              title="Close (Esc)"
            >
              Close
            </button>
          </div>
        </div>
        <div className={`flex-1 overflow-hidden ${state === "ready" ? "bg-[#0b0d10]" : "bg-slate-50/70 dark:bg-transparent p-2"}`}>
          {state === "ready" ? (
            <TerminalView />
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
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center text-slate-700 dark:text-surface-300">
              <p className="text-[13px]">
                Browser shell needs an active agent connection to{" "}
                <span className="font-mono text-emerald-700 dark:text-emerald-300">
                  {device.alias ? `@${device.alias}` : device.name}
                </span>
                .
              </p>
              <button
                onClick={onConnect}
                className="rounded-md border border-emerald-300 bg-emerald-50 px-4 py-2 text-[12px] font-semibold text-emerald-700 hover:bg-emerald-100 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200 dark:hover:bg-emerald-500/15"
              >
                Connect &amp; open shell
              </button>
              <p className="text-[11px] text-slate-500 dark:text-surface-500">
                Once connected the PTY opens through the relay — works even when
                direct LAN is unreachable.
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
