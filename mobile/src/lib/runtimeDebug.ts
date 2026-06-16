// Runtime JS-error capture for the Yaver mobile app itself (not for
// guest apps loaded via Hermes push — those go through the Feedback
// SDK in sdk/feedback/react-native).
//
// Why this exists: when the app crashes inside JS (uncaught
// exception, unhandled promise rejection, ErrorBoundary catch), we
// want a captured stack trace + recent log tail in two places:
//
//   1) appLog ring buffer (visible in the in-app Connection Logs
//      modal) so the user can see what blew up without leaving the
//      device, and
//
//   2) the connected agent's BlackBox stream (when reachable) so a
//      developer at the beach can pull logs from the agent CLI /
//      web dashboard without needing the user to email a screenshot.
//
// This is the runtime side of the debug-build agent change: when
// the agent compiles a bundle with `debug=true`, hermesc emits
// source-position info AND a sourcemap sidecar; the captured stack
// here can then be symbolicated against the sidecar so the user
// sees `screens/Game.tsx:42` instead of an obfuscated hex offset.
//
// Side-effect import. No exports — installs handlers at module
// evaluation time. Pull in BEFORE anything that might throw, i.e.
// near the top of mobile/app/_layout.tsx.

import { appLog, getLogEntries, hydratePersistedLogs } from "./logger";

// React Native exposes `ErrorUtils` on the global object. The
// declared type lives in react-native's untyped vendor layer so we
// reach it through globalThis to avoid a cascade of type imports.
type GlobalErrorHandler = (error: Error, isFatal?: boolean) => void;
interface RNErrorUtils {
  getGlobalHandler?: () => GlobalErrorHandler | undefined;
  setGlobalHandler: (handler: GlobalErrorHandler) => void;
}

function getErrorUtils(): RNErrorUtils | undefined {
  // RN injects this on globalThis at startup. Hermes / JSC both have
  // it; in any environment without it, the install is a silent no-op.
  return (globalThis as any).ErrorUtils;
}

// Track whether we've already installed so hot-reload / Fast Refresh
// doesn't stack handlers (they'd each fire and spam logs).
let installed = false;

export function installRuntimeDebugHandlers(): void {
  if (installed) return;
  installed = true;

  // Restore the previous session's connection logs so a failure that
  // preceded a restart/crash is still visible in the in-app panel.
  // Fire-and-forget — never block handler install on disk I/O.
  hydratePersistedLogs().catch(() => {});

  const utils = getErrorUtils();
  if (!utils?.setGlobalHandler) {
    appLog("warn", "[runtime-debug] ErrorUtils unavailable — JS error capture disabled");
    return;
  }
  const previous = utils.getGlobalHandler?.();

  utils.setGlobalHandler((error, isFatal) => {
    const tag = isFatal ? "FATAL" : "error";
    const message = error?.message || String(error);
    const stack = (error?.stack || "")
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
      .slice(0, 12)
      .join("\n  ");
    appLog("error", `[js-${tag}] ${message}\n  ${stack}`);

    // Best-effort forward to a connected agent's BlackBox stream.
    // Lazy-required so this module can load before the QUIC client
    // singleton is ready, and so a missing client never blocks the
    // ErrorUtils install. If the post fails, we've already saved the
    // entry to the in-app log ring buffer above.
    forwardErrorToAgent({ message, stack, isFatal: !!isFatal }).catch(() => {});

    // Preserve RN's default behavior so red-box / app-crash flows
    // still fire after we've captured. Without this passthrough an
    // isFatal=true crash would silently corrupt the runtime instead
    // of showing the standard error screen.
    if (previous) {
      try {
        previous(error, isFatal);
      } catch {
        // swallow — we already captured the original.
      }
    }
  });

  // Unhandled promise rejections. Hermes routes them through
  // process.on('unhandledRejection') equivalent; in RN they surface
  // via a require-cycle hack on the global Promise constructor. We
  // attach via the standard JS event when present.
  if (typeof (globalThis as any).addEventListener === "function") {
    (globalThis as any).addEventListener("unhandledrejection", (event: any) => {
      const reason = event?.reason;
      const message = reason?.message || String(reason);
      const stack = reason?.stack || "";
      appLog("error", `[js-promise] unhandled rejection: ${message}\n  ${stack}`);
      forwardErrorToAgent({ message: `unhandled rejection: ${message}`, stack, isFatal: false }).catch(() => {});
    });
  }

  appLog("info", "[runtime-debug] JS error handlers installed");
}

// Lazy-fire the agent push so we don't hard-import quic.ts (which
// imports DeviceContext, which imports tweetnacl, which imports …)
// before the runtime is ready. The dynamic import means cold starts
// don't pay this cost, and an unloadable client doesn't block local
// logging.
async function forwardErrorToAgent(payload: {
  message: string;
  stack: string;
  isFatal: boolean;
}): Promise<void> {
  let quicClient: any;
  try {
    quicClient = (await import("./quic")).quicClient;
  } catch {
    return;
  }
  const activeDeviceId: string | undefined = quicClient?.deviceId || undefined;
  if (!activeDeviceId || typeof quicClient.pushBlackBoxEvents !== "function") return;
  try {
    await quicClient.pushBlackBoxEvents(activeDeviceId, [
      {
        type: "error",
        level: payload.isFatal ? "error" : "warn",
        message: `mobile-app: ${payload.message}`,
        timestamp: Date.now(),
        metadata: {
          source: "yaver-mobile-runtime-debug",
          stackHead: payload.stack.split("\n").slice(0, 8).join("\n"),
          recentLogs: getLogEntries().slice(-12).map((e) => `[${e.level}] ${e.message}`).join(" | "),
        },
      },
    ]);
  } catch {
    // swallow — best-effort
  }
}
