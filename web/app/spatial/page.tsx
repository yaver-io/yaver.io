"use client";

/**
 * /spatial — unified glass / HUD / VR React UI.
 *
 * Surfaces this serves:
 *   - Meta Quest 3 / 3S in Quest Browser (WebXR-ready, immersive-vr later)
 *   - Apple Vision Pro Safari (visionOS 26 — Liquid Glass via backdrop-filter)
 *   - Meta Ray-Ban Display "Web Apps" (600×600 viewport, monocular)
 *   - In-mobile WebView preview (RN host renders this inside a tab)
 *
 * NOT served: Mentra Live / Even G1·G2 / Vuzix Z100 — those use
 * vendor text primitives via the mentra-miniapp Bun server.
 *
 * Open with: https://yaver.io/spatial?agent=<https://host:18080>&token=<sdk>
 * The desktop app's "Open in headset" button generates this URL.
 *
 * Layout adapts by viewport class:
 *   - SMALL  (<= 800w):  HUD mode — session strip + 1 active pane + orb
 *   - MEDIUM (<= 1600w): 2 panes side by side
 *   - LARGE  (>= 1600w): 3-pane tmux-like grid + ambient strip
 */

import { useEffect, useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { readBridgeFromURL, useTasks, useVoiceBridge, type Task, type BridgeConfig } from "./useAgentBridge";
import { useSurface } from "./lib/surfaceDetect";
import { useSpatialShortcuts, SHORTCUT_HELP_ROWS } from "./lib/keyboardShortcuts";

// VR scene is a client-only WebGL bundle (Three.js + R3F + XR). Load
// it dynamically so the 2D /spatial route doesn't ship ~600KB of
// three.js to users who only want the flat view.
const VRScene = dynamic(() => import("./vr/VRScene").then((m) => m.VRScene), { ssr: false });
const EnterVRButton = dynamic(() => import("./vr/EnterVRButton").then((m) => m.EnterVRButton), { ssr: false });

type ViewportClass = "small" | "medium" | "large";

function useViewportClass(): ViewportClass {
  const [cls, setCls] = useState<ViewportClass>("large");
  useEffect(() => {
    const compute = () => {
      const w = window.innerWidth;
      setCls(w <= 800 ? "small" : w <= 1600 ? "medium" : "large");
    };
    compute();
    window.addEventListener("resize", compute);
    return () => window.removeEventListener("resize", compute);
  }, []);
  return cls;
}

export default function SpatialPage() {
  const cfg = useMemo(readBridgeFromURL, []);
  const viewport = useViewportClass();
  const surface = useSurface();
  const { tasks, error: tasksErr } = useTasks(cfg);
  const voice = useVoiceBridge(cfg);

  if (!cfg) {
    return <ConnectGuide />;
  }

  // Surface-specific tuning of the 2D layout. Quest Browser + Vision
  // Pro Safari default to 3 panes (large viewport assumed); Ray-Ban
  // Display always 1 pane (HUD constraint); mobile WebView preview
  // matches viewport. Desktop falls through to the viewport class.
  const paneCount =
    surface.surface === "quest" || surface.surface === "vision-pro" ? 3 :
    surface.surface === "ray-ban-display" ? 1 :
    viewport === "small" ? 1 : viewport === "medium" ? 2 : 3;
  const activeTasks = tasks
    .filter((t) => t.status === "running" || t.status === "review" || t.status === "queued")
    .slice(0, paneCount);
  // Pad with completed tasks if there are fewer active sessions than panes
  while (activeTasks.length < paneCount) {
    const next = tasks.find((t) => !activeTasks.includes(t));
    if (!next) break;
    activeTasks.push(next);
  }

  const isRayBan = surface.surface === "ray-ban-display";
  const isVisionPro = surface.surface === "vision-pro";
  const [helpOpen, setHelpOpen] = useState(false);
  const [focusedPane, setFocusedPane] = useState(0);

  // Bluetooth keyboard shortcuts for the "Yaver trio" (phone +
  // glasses + foldable BT keyboard) and desktop browsers alike.
  useSpatialShortcuts({
    onNextPane: () => setFocusedPane((i) => Math.min(activeTasks.length - 1, i + 1)),
    onPrevPane: () => setFocusedPane((i) => Math.max(0, i - 1)),
    onSelectPane: (i) => setFocusedPane(Math.max(0, Math.min(activeTasks.length - 1, i))),
    onToggleVoice: () => {
      if (voice.state.status === "idle" || voice.state.status === "error") void voice.start();
      else if (voice.state.status === "recording") void voice.stop();
      else voice.cancel();
    },
    onCancelVoice: () => {
      if (helpOpen) setHelpOpen(false);
      else if (voice.state.status !== "idle") voice.cancel();
    },
    onToggleHelp: () => setHelpOpen((v) => !v),
    onEnterVR: () => {
      if (typeof window !== "undefined") {
        window.dispatchEvent(new CustomEvent("yaver-enter-vr"));
      }
    },
    onScrollTop: () => {/* TerminalPane3D / TerminalPane will handle this in a follow-up */},
    onScrollBottom: () => {/* same */},
  });

  return (
    <div style={containerStyle}>
      {/* Surface badge — top-left. Hidden on Ray-Ban Display because
          the 600x600 viewport can't afford the chrome. */}
      {!isRayBan && <SurfaceBadge surface={surface} />}

      {/* Vision Pro one-time nudge: visionOS 26 Safari DOES support
          immersive-vr (per WebKit blog) but users don't know to look
          for the button. Show a centered card on first visit. */}
      {isVisionPro && <VisionProNudge webxrAvailable={surface.webxrAvailable} />}

      {/* WebGL VR layer — mounted always but only visible inside an
          immersive-vr XR session (the Canvas renders nothing visible
          on the 2D page since R3F's default behavior keeps the GL
          context invisible until enterVR fires). The Enter VR button
          appears top-right when the browser supports it. */}
      <VRScene cfg={cfg} tasks={tasks} voice={voice} />
      <EnterVRButton />

      {!isRayBan && <SessionStrip tasks={tasks} />}
      <div style={paneGridStyle(paneCount, isRayBan)}>
        {activeTasks.map((t) => (
          <TerminalPane key={t.id} task={t} cfg={cfg} />
        ))}
        {activeTasks.length === 0 && <EmptyState />}
      </div>
      <FloatingOrb
        status={voice.state.status}
        transcript={voice.state.transcript}
        errorMsg={voice.state.errorMsg}
        compact={isRayBan}
        onTap={() => {
          if (voice.state.status === "idle" || voice.state.status === "error") void voice.start();
          else if (voice.state.status === "recording") void voice.stop();
          else voice.cancel();
        }}
      />
      {tasksErr && <ErrorBanner msg={`tasks: ${tasksErr}`} />}

      {helpOpen && <ShortcutHelpOverlay onClose={() => setHelpOpen(false)} />}
    </div>
  );
}

function ShortcutHelpOverlay({ onClose }: { onClose: () => void }) {
  // Yaver-trio keyboard cheat sheet. Same source as the actual
  // bindings (SHORTCUT_HELP_ROWS) so the panel can't drift.
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 100003,
        background: "rgba(0,0,0,0.55)",
        backdropFilter: "blur(6px)",
        WebkitBackdropFilter: "blur(6px)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: "min(520px, 100%)",
          background: "rgba(8,12,20,0.92)",
          border: "1px solid rgba(255,255,255,0.12)",
          borderRadius: 12,
          padding: 24,
          color: "#e5e7eb",
          fontFamily: "ui-monospace, Menlo, monospace",
        }}
      >
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 14 }}>
          <div style={{ fontSize: 14, fontWeight: 700 }}>Keyboard shortcuts</div>
          <button
            onClick={onClose}
            style={{
              padding: "3px 8px", borderRadius: 4,
              background: "rgba(255,255,255,0.06)", border: "1px solid rgba(255,255,255,0.12)",
              color: "#9ca3af", fontSize: 11, cursor: "pointer",
            }}
          >
            close · Esc
          </button>
        </div>
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12 }}>
          <tbody>
            {SHORTCUT_HELP_ROWS.map((row) => (
              <tr key={row.keys}>
                <td style={{ padding: "5px 12px 5px 0", color: "#10b981", whiteSpace: "nowrap", fontWeight: 600 }}>
                  {row.keys}
                </td>
                <td style={{ padding: "5px 0", color: "#cbd5e1" }}>{row.what}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <div style={{ marginTop: 16, fontSize: 10, color: "#6b7280", lineHeight: 1.5 }}>
          Designed for the Yaver trio: phone + smart glasses (XReal Air, Mentra G2, Quest 3 Browser, Vision Pro Safari) + foldable Bluetooth keyboard. Pair the keyboard once; vibe code anywhere.
        </div>
      </div>
    </div>
  );
}

function VisionProNudge({ webxrAvailable }: { webxrAvailable: boolean }) {
  const [dismissed, setDismissed] = useState(false);
  if (dismissed) return null;
  return (
    <div style={{
      position: "fixed",
      top: 60,
      left: "50%",
      transform: "translateX(-50%)",
      zIndex: 100002,
      maxWidth: 480,
      padding: "12px 16px",
      background: "rgba(139, 92, 246, 0.18)",
      border: "1px solid rgba(139, 92, 246, 0.45)",
      borderRadius: 10,
      backdropFilter: "blur(20px)",
      WebkitBackdropFilter: "blur(20px)",
      color: "#e5e7eb",
      fontSize: 12,
      lineHeight: 1.5,
    }}>
      <div style={{ fontWeight: 600, marginBottom: 4 }}>
        Vision Pro detected · {webxrAvailable ? "Click \"Enter VR\" for immersive" : "Update to Safari 26.2 for immersive-vr"}
      </div>
      <div style={{ opacity: 0.85, fontSize: 11 }}>
        Tap-pinch the orb to speak. Add this URL to your Dock for one-tap launch.
      </div>
      <button
        onClick={() => setDismissed(true)}
        style={{
          marginTop: 8,
          padding: "4px 10px",
          background: "rgba(255,255,255,0.08)",
          border: "1px solid rgba(255,255,255,0.15)",
          borderRadius: 6,
          color: "#e5e7eb",
          fontSize: 11,
          cursor: "pointer",
        }}
      >
        Got it
      </button>
    </div>
  );
}

// ───────────────────────────── Components ─────────────────────────────

function SurfaceBadge({ surface }: { surface: ReturnType<typeof useSurface> }) {
  const [open, setOpen] = useState(false);
  const tones: Record<string, string> = {
    quest: "#1d4ed8",
    "vision-pro": "#a78bfa",
    "ray-ban-display": "#f97316",
    "mobile-webview": "#10b981",
    desktop: "#6b7280",
    unknown: "#374151",
  };
  return (
    <div style={{ position: "fixed", top: 12, left: 12, zIndex: 100002 }}>
      <button
        onClick={() => setOpen((o) => !o)}
        style={{
          padding: "5px 10px", borderRadius: 6,
          background: `${tones[surface.surface] ?? "#374151"}33`,
          border: `1px solid ${tones[surface.surface] ?? "#374151"}66`,
          color: "#e5e7eb", fontSize: 11, fontFamily: "ui-monospace, Menlo, monospace",
          cursor: "pointer",
        }}
        title="Click to override surface"
      >
        {surface.label}{surface.forced ? " (forced)" : ""} {surface.webxrAvailable ? "· WebXR ✓" : ""}
      </button>
      {open && (
        <div style={{ marginTop: 6, padding: 8, background: "rgba(0,0,0,0.85)", border: "1px solid rgba(255,255,255,0.1)", borderRadius: 6, fontSize: 10, minWidth: 220 }}>
          <div style={{ color: "#9ca3af", marginBottom: 6 }}>Force surface for testing:</div>
          {(["quest", "vision-pro", "ray-ban-display", "mobile-webview", "desktop"] as const).map((s) => (
            <a key={s} href={updateQuery(s)} style={{ display: "block", padding: "3px 6px", color: "#e5e7eb", textDecoration: "none", borderRadius: 3 }}>
              ?surface={s}
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

function updateQuery(s: string): string {
  if (typeof window === "undefined") return `?surface=${s}`;
  const u = new URL(window.location.href);
  u.searchParams.set("surface", s);
  return u.pathname + "?" + u.searchParams.toString();
}

function ConnectGuide() {
  return (
    <div style={{ ...containerStyle, alignItems: "center", justifyContent: "center" }}>
      <div style={{ ...cardStyle, maxWidth: 480, padding: 32 }}>
        <h1 style={{ fontSize: 20, margin: 0, marginBottom: 12 }}>Yaver Spatial</h1>
        <p style={{ fontSize: 13, lineHeight: 1.5, color: "#9ca3af", marginBottom: 16 }}>
          Open this page with a connection URL from your desktop:
        </p>
        <pre style={preStyle}>https://yaver.io/spatial?agent=&lt;url&gt;&amp;token=&lt;sdk&gt;</pre>
        <p style={{ fontSize: 11, color: "#6b7280", marginTop: 16 }}>
          Generate one via{" "}
          <code style={codeStyle}>yaver sdk token --scope feedback,voice</code>
          {" "}then paste the URL into Quest Browser, Vision Pro Safari, or any modern browser.
        </p>
      </div>
    </div>
  );
}

function SessionStrip({ tasks }: { tasks: Task[] }) {
  return (
    <div style={stripStyle}>
      {tasks.slice(0, 12).map((t) => (
        <div key={t.id} style={chipStyle}>
          <span style={{ ...dotStyle, background: dotColor(t.status) }} />
          <span style={{ fontSize: 11 }}>{shortTitle(t.title, 18)}</span>
        </div>
      ))}
      {tasks.length === 0 && <span style={{ color: "#6b7280", fontSize: 11 }}>no active sessions</span>}
    </div>
  );
}

function TerminalPane({ task, cfg }: { task: Task; cfg: BridgeConfig }) {
  const ref = useRef<HTMLDivElement>(null);
  const termRef = useRef<any>(null);
  const writtenLinesRef = useRef<number>(0);

  useEffect(() => {
    let term: any;
    let resizeObserver: ResizeObserver | null = null;
    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (!ref.current) return;
      term = new Terminal({
        fontFamily: "ui-monospace, 'JetBrains Mono', Menlo, monospace",
        fontSize: 12,
        theme: { background: "rgba(0,0,0,0.0)", foreground: "#e5e7eb" },
        allowTransparency: true,
        cursorBlink: false,
        disableStdin: true,
        convertEol: true,
        scrollback: 2000,
      });
      termRef.current = term;
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(ref.current);
      fit.fit();
      resizeObserver = new ResizeObserver(() => { try { fit.fit(); } catch {} });
      resizeObserver.observe(ref.current);
    })();
    return () => {
      try { term?.dispose(); } catch {}
      resizeObserver?.disconnect();
    };
  }, []);

  // Poll the task and write new output lines to the terminal.
  useEffect(() => {
    let cancelled = false;
    const tick = async () => {
      try {
        const res = await fetch(`${cfg.agentUrl}/tasks/${encodeURIComponent(task.id)}`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!res.ok) return;
        const t = (await res.json()) as Task;
        if (cancelled || !termRef.current) return;
        const lines = Array.isArray(t.output) ? t.output : [];
        for (let i = writtenLinesRef.current; i < lines.length; i++) {
          termRef.current.writeln(lines[i]);
        }
        writtenLinesRef.current = lines.length;
      } catch { /* swallow polling errors */ }
    };
    void tick();
    const i = window.setInterval(tick, 1500);
    return () => { cancelled = true; window.clearInterval(i); };
  }, [cfg, task.id]);

  return (
    <div style={{ ...paneStyle, position: "relative" }}>
      <div style={paneHeaderStyle}>
        <span style={{ ...dotStyle, background: dotColor(task.status), marginRight: 8 }} />
        <span style={{ fontSize: 11, fontWeight: 600 }} title={task.title}>{shortTitle(task.title, 38)}</span>
        <span style={{ fontSize: 10, color: "#6b7280", marginLeft: "auto" }}>
          {task.status}
          {task.outputTokens ? ` · ${formatTokens((task.inputTokens ?? 0) + task.outputTokens)} tok` : ""}
        </span>
      </div>
      <div ref={ref} style={{ flex: 1, minHeight: 0 }} />
    </div>
  );
}

function FloatingOrb({
  status, transcript, errorMsg, onTap, compact = false,
}: {
  status: string; transcript: string; errorMsg: string; onTap: () => void;
  /** Ray-Ban Display + small viewports get the compact variant — 48pt
   *  button + 1-line transcript so the 600x600 chrome doesn't choke. */
  compact?: boolean;
}) {
  const color = orbColor(status);
  const label = orbLabel(status);
  const size = compact ? 48 : 72;
  const borderWidth = compact ? 2 : 4;
  const fontSize = compact ? 18 : 24;
  const bottomOffset = compact ? 12 : 24;
  const labelMaxLines = compact ? 1 : 2;
  return (
    <div style={{ position: "fixed", bottom: bottomOffset, left: "50%", transform: "translateX(-50%)", zIndex: 100000, textAlign: "center" }}>
      <button
        onClick={onTap}
        style={{
          width: size, height: size, borderRadius: "50%",
          background: color, border: `${borderWidth}px solid ${color}55`,
          boxShadow: "0 8px 24px rgba(0,0,0,0.35)",
          color: "#fff", fontSize: fontSize, cursor: "pointer",
          transition: "transform 120ms ease",
        }}
        aria-label={label}
      >
        {status === "recording" ? "■" : "🎙"}
      </button>
      <div style={{
        marginTop: compact ? 4 : 8,
        fontSize: compact ? 10 : 11,
        color: errorMsg ? "#ef4444" : "#9ca3af",
        maxWidth: compact ? 240 : 280,
        textAlign: "center",
        display: "-webkit-box",
        WebkitLineClamp: labelMaxLines,
        WebkitBoxOrient: "vertical",
        overflow: "hidden",
      } as React.CSSProperties}>
        {errorMsg || (transcript ? `"${transcript}"` : label)}
      </div>
    </div>
  );
}

function EmptyState() {
  return (
    <div style={{ ...paneStyle, alignItems: "center", justifyContent: "center", color: "#6b7280", fontSize: 13 }}>
      No active sessions. Tap the orb and ask Yaver to start one.
    </div>
  );
}

function ErrorBanner({ msg }: { msg: string }) {
  return (
    <div style={{ position: "fixed", top: 12, left: 12, padding: "6px 10px", background: "#ef444422", border: "1px solid #ef444466", borderRadius: 6, fontSize: 11, color: "#fca5a5" }}>
      {msg}
    </div>
  );
}

// ───────────────────────────── Helpers ─────────────────────────────

function shortTitle(s: string, max: number): string {
  const t = (s ?? "").trim();
  if (t.length <= max) return t || "(untitled)";
  return t.slice(0, max - 1) + "…";
}

function dotColor(status: string): string {
  switch (status) {
    case "running": return "#10b981";
    case "queued": return "#94a3b8";
    case "review": return "#f59e0b";
    case "completed": return "#3b82f6";
    case "failed": return "#ef4444";
    case "stopped": return "#6b7280";
    default: return "#6b7280";
  }
}

function orbColor(status: string): string {
  switch (status) {
    case "idle": return "#10b981";
    case "recording": return "#ef4444";
    case "uploading":
    case "connecting": return "#3b82f6";
    case "thinking": return "#8b5cf6";
    case "speaking": return "#f59e0b";
    case "error": return "#6b7280";
    default: return "#10b981";
  }
}

function orbLabel(status: string): string {
  switch (status) {
    case "idle": return "Tap to speak";
    case "recording": return "Listening…";
    case "connecting": return "Connecting…";
    case "uploading": return "Sending…";
    case "thinking": return "Thinking…";
    case "speaking": return "Reading back…";
    case "error": return "Try again";
    default: return "Tap to speak";
  }
}

function formatTokens(n: number): string {
  if (n < 1000) return `${n}`;
  if (n < 1000000) return `${(n / 1000).toFixed(1)}k`;
  return `${(n / 1000000).toFixed(1)}m`;
}

// ───────────────────────────── Styles ─────────────────────────────

const containerStyle: React.CSSProperties = {
  position: "fixed",
  inset: 0,
  zIndex: 9999,
  display: "flex",
  flexDirection: "column",
  background: "rgba(8,12,20,0.6)",
  backdropFilter: "blur(8px) saturate(120%)",
  WebkitBackdropFilter: "blur(8px) saturate(120%)",
  color: "#e5e7eb",
};

const stripStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "row",
  gap: 8,
  overflowX: "auto",
  padding: "8px 12px",
  borderBottom: "1px solid rgba(255,255,255,0.08)",
  flexShrink: 0,
};

const chipStyle: React.CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 6,
  padding: "4px 10px",
  borderRadius: 999,
  background: "rgba(255,255,255,0.05)",
  border: "1px solid rgba(255,255,255,0.1)",
  whiteSpace: "nowrap",
};

const dotStyle: React.CSSProperties = {
  width: 8,
  height: 8,
  borderRadius: 4,
  display: "inline-block",
};

const paneGridStyle = (n: number, compact: boolean = false): React.CSSProperties => ({
  flex: 1,
  display: "grid",
  gridTemplateColumns: `repeat(${n}, 1fr)`,
  gap: compact ? 4 : 8,
  padding: compact ? 4 : 8,
  minHeight: 0,
});

const paneStyle: React.CSSProperties = {
  display: "flex",
  flexDirection: "column",
  background: "rgba(0,0,0,0.4)",
  border: "1px solid rgba(255,255,255,0.08)",
  borderRadius: 8,
  overflow: "hidden",
};

const paneHeaderStyle: React.CSSProperties = {
  display: "flex",
  alignItems: "center",
  padding: "6px 10px",
  borderBottom: "1px solid rgba(255,255,255,0.05)",
  background: "rgba(0,0,0,0.25)",
};

const cardStyle: React.CSSProperties = {
  background: "rgba(255,255,255,0.06)",
  border: "1px solid rgba(255,255,255,0.1)",
  borderRadius: 10,
};

const preStyle: React.CSSProperties = {
  background: "rgba(0,0,0,0.4)",
  padding: "8px 10px",
  borderRadius: 6,
  fontSize: 11,
  margin: 0,
  whiteSpace: "pre-wrap",
  wordBreak: "break-all",
};

const codeStyle: React.CSSProperties = {
  background: "rgba(0,0,0,0.4)",
  padding: "1px 5px",
  borderRadius: 3,
  fontSize: 11,
};
