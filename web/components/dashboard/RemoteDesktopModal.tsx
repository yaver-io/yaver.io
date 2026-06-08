"use client";

// RemoteDesktopModal — opens a device's live desktop (screen view + optional
// mouse/keyboard control) in the dashboard. Mirrors WebShellModal's connection
// gating: the agent must be connected (relay or direct) before the MJPEG stream
// and /rd/input endpoints are reachable.

import { useEffect, useState } from "react";
import RemoteDesktopView from "./RemoteDesktopView";
import type { Device } from "@/lib/use-devices";

export default function RemoteDesktopModal({
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

  const [maximized, setMaximized] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape" && !document.fullscreenElement) onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-stretch justify-center bg-black/70 backdrop-blur-sm p-0 sm:p-8"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className={`flex w-full flex-col overflow-hidden border border-slate-200 bg-white shadow-2xl dark:border-surface-700 dark:bg-[#0b0d10] ${
          maximized ? "max-w-none rounded-none sm:rounded-none" : "max-w-6xl rounded-none sm:rounded-xl"
        }`}
      >
        <div className="flex items-center justify-between border-b border-slate-200 bg-slate-50/95 px-4 py-2.5 dark:border-surface-800 dark:bg-surface-900/80">
          <div className="flex items-center gap-2 min-w-0">
            <span className={`inline-flex h-2 w-2 rounded-full ${state === "ready" ? "bg-emerald-400" : state === "needs-reauth" ? "bg-amber-400" : state === "connecting" ? "bg-cyan-400" : "bg-slate-400 dark:bg-surface-500"}`} />
            <span className="truncate text-[13px] font-semibold text-slate-900 dark:text-surface-100">
              Remote Desktop · {device.alias ? `@${device.alias}` : device.name}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <span className="hidden sm:inline rounded-full border border-slate-200 bg-white px-2 py-0.5 text-[10px] uppercase tracking-[0.14em] text-slate-500 dark:border-surface-700 dark:bg-surface-950/60 dark:text-surface-400">
              {state === "needs-reauth" ? "agent auth required" : state === "connecting" ? "connecting…" : "via relay · MJPEG"}
            </span>
            <button
              onClick={() => setMaximized((m) => !m)}
              className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-[11px] text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-300 dark:hover:border-surface-600 dark:hover:text-surface-100"
              title={maximized ? "Restore" : "Maximize"}
            >
              {maximized ? "❐ Restore" : "⛶ Maximize"}
            </button>
            <button
              onClick={onClose}
              className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-[11px] text-slate-600 hover:border-slate-300 hover:text-slate-900 dark:border-surface-700 dark:bg-surface-950 dark:text-surface-300 dark:hover:border-surface-600 dark:hover:text-surface-100"
              title="Close (Esc)"
            >
              Close
            </button>
          </div>
        </div>
        <div className={`overflow-hidden bg-[#0b0d10] ${maximized ? "h-[calc(100vh-3rem)]" : "h-[70vh]"}`}>
          {state === "ready" ? (
            <RemoteDesktopView />
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center text-slate-300">
              {state === "needs-reauth" ? (
                <>
                  <div className="rounded-full border border-amber-500/30 bg-amber-500/10 px-3 py-1 text-[10px] font-semibold uppercase tracking-[0.14em] text-amber-700 dark:text-amber-200">
                    Reauth required
                  </div>
                  <p className="max-w-md text-[13px] leading-5">
                    The agent on{" "}
                    <span className="font-mono text-amber-700 dark:text-amber-200">{device.alias ? `@${device.alias}` : device.name}</span>{" "}
                    is reachable but its Yaver session expired. Re-pair before opening the screen.
                  </p>
                  {onOpenRescue ? (
                    <button
                      onClick={() => { onClose(); onOpenRescue(); }}
                      className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-[11px] font-semibold text-amber-700 dark:text-amber-200 hover:bg-amber-500/15"
                    >
                      Open Rescue
                    </button>
                  ) : null}
                </>
              ) : state === "connecting" ? (
                <p className="text-[13px]">
                  Connecting to{" "}
                  <span className="font-mono text-cyan-700 dark:text-cyan-300">{device.alias ? `@${device.alias}` : device.name}</span>{" "}
                  before opening the screen…
                </p>
              ) : (
                <>
                  <p className="text-[13px]">
                    Remote Desktop needs an active agent connection to{" "}
                    <span className="font-mono text-emerald-700 dark:text-emerald-300">{device.alias ? `@${device.alias}` : device.name}</span>.
                  </p>
                  <button
                    onClick={onConnect}
                    className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-4 py-2 text-[12px] font-semibold text-emerald-700 dark:text-emerald-200 hover:bg-emerald-500/15"
                  >
                    Connect &amp; open desktop
                  </button>
                </>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
