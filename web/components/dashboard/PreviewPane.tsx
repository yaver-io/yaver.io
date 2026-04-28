"use client";

import { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { agentClient, type MobileWorkerPreviewSession } from "@/lib/agent-client";
import pkg from "../../package.json";

// Surface the running web bundle version inside the dashboard so
// users can tell at a glance whether their browser is on the latest
// deploy or a stale Cloudflare cache. Replaces wondering "is this
// 1.1.51?" with a visible tag on the Recovery banner.
const __YAVER_WEB_VERSION__ = (pkg as { version?: string }).version ?? "?";

interface PreviewTarget {
  id: string;
  name: string;
}

type DeviceSkin = {
  id: string;
  label: string;
  width: number;
  height: number;
  radius: number;
  bezel: number;
  notch?: { width: number; height: number };
  punchHole?: { size: number; offsetTop: number };
  plain?: boolean;
};

const DEVICES: DeviceSkin[] = [
  { id: "iphone-15", label: "iPhone 15", width: 393, height: 852, radius: 55, bezel: 11, notch: { width: 120, height: 30 } },
  { id: "iphone-se", label: "iPhone SE", width: 375, height: 667, radius: 20, bezel: 8 },
  { id: "pixel-8", label: "Pixel 8", width: 412, height: 915, radius: 30, bezel: 9, punchHole: { size: 22, offsetTop: 16 } },
  { id: "pixel-8-pro", label: "Pixel 8 Pro", width: 448, height: 998, radius: 32, bezel: 9, punchHole: { size: 22, offsetTop: 16 } },
  { id: "tablet", label: "Tablet", width: 820, height: 1180, radius: 24, bezel: 14 },
  { id: "desktop", label: "Web", width: 0, height: 0, radius: 0, bezel: 0, plain: true },
];

const SKIN_STORAGE_KEY = "yaver_preview_skin";
const ORIENTATION_STORAGE_KEY = "yaver_preview_orientation";
type Orientation = "portrait" | "landscape";

type Project = {
  name: string;
  path: string;
  framework?: string;
  branch?: string;
  tags?: string[];
};

function frameworkIcon(fw?: string): string {
  const f = (fw || "").toLowerCase();
  if (f.includes("expo")) return "📱";
  if (f.includes("react-native") || f.includes("rn")) return "⚛";
  if (f.includes("flutter")) return "🦆";
  if (f.includes("next")) return "▲";
  if (f.includes("vite")) return "⚡";
  if (f === "react") return "⚛";
  return "💻";
}

function likelyFramework(project: Project): string {
  if (project.framework) return project.framework;
  const tags = (project.tags || []).map((t) => t.toLowerCase());
  if (tags.includes("expo")) return "expo";
  if (tags.includes("react-native")) return "react-native";
  if (tags.includes("flutter")) return "flutter";
  if (tags.includes("next") || tags.includes("nextjs")) return "nextjs";
  if (tags.includes("vite")) return "vite";
  return "vite";
}

// Returns true when the active dev server is mobile-by-intent — Expo,
// React Native, or any Metro instance. PreviewPane is the Hot Reload
// surface (the phone-shaped mockup); for these frameworks the iframe
// content is meaningless to the user regardless of whether Metro is
// in --dev-client or --web mode (web previews belong in Web Reload
// tab, where the user actually picked browser preview). Always show
// the instructional placeholder so the phone frame never paints blank
// white. Vite / Next.js / Flutter Web fall through to the iframe path
// because they have a meaningful HTML response to render.
function isMobileDevClient(status: { framework?: string; devMode?: string } | null | undefined): boolean {
  if (!status) return false;
  const fw = (status.framework || "").toLowerCase();
  return fw.includes("expo") || fw.includes("react-native") || fw === "metro";
}

function isWebPreviewFramework(framework?: string): boolean {
  const fw = (framework || "").toLowerCase();
  return (
    fw.includes("next") ||
    fw.includes("vite") ||
    fw === "react" ||
    fw.includes("expo") ||
    fw.includes("react-native")
  );
}

function previewPlatformForProject(project: Project): "web" | undefined {
  const fw = likelyFramework(project).toLowerCase();
  if (
    fw.includes("next") ||
    fw.includes("vite") ||
    fw === "react" ||
    fw.includes("expo") ||
    fw.includes("react-native")
  ) {
    return "web";
  }
  return undefined;
}

export default function PreviewPane({
  selectedPreviewTarget,
  onSelectPreviewTarget,
  mobileWorkers,
  preferredProjectPath,
  onReconnect,
  onRepairRelay,
  connectedDeviceNeedsAuth,
  onSwitchAgent,
  onTriggerReauth,
  primaryRunner,
}: {
  selectedPreviewTarget: PreviewTarget | null;
  onSelectPreviewTarget: (deviceId: string | null) => void;
  mobileWorkers: PreviewTarget[];
  preferredProjectPath?: string | null;
  onReconnect?: () => Promise<void>;
  onRepairRelay?: () => Promise<{ repaired: boolean; reason: string }>;
  /** True when the connected device's session token expired. Surfaces a
   *  re-auth CTA on top of the phone mockup so the user understands why
   *  the iframe is showing a stale frame. */
  connectedDeviceNeedsAuth?: boolean;
  /** Optional escape hatch from the re-auth CTA — open the Devices tab
   *  so the user can sign in to another runner (claude / codex / etc.)
   *  or pick a local LLM that doesn't need cloud auth. The selected
   *  agent saves the day until the cloud token comes back. */
  onSwitchAgent?: () => void;
  /** Direct re-auth: opens the browser-auth modal for the named runner
   *  without forcing the user to chase Devices → Sign in. Single click
   *  from the "Agent session expired" banner kicks off the flow. */
  onTriggerReauth?: (runner: string) => void;
  /** The device's primary coding agent — codex for the test box, claude
   *  on a fresh machine, etc. The "Sign in & reconnect" CTA uses this
   *  so the user re-auths into the runner they actually want, not a
   *  hardcoded default. Falls back to "claude" when unset. */
  primaryRunner?: string | null;
}) {
  const taskStreamStopRef = useRef<(() => void) | null>(null);
  const taskPollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const [devStatus, setDevStatus] = useState<{
    running: boolean;
    framework?: string;
    workDir?: string;
    port?: number;
    targetDeviceName?: string;
  } | null>(null);
  const [workerSession, setWorkerSession] = useState<MobileWorkerPreviewSession | null>(null);
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [iframeKey, setIframeKey] = useState(0);
  const [reloadNonce, setReloadNonce] = useState(0);
  const [skinId, setSkinId] = useState<string>("iphone-15");
  const [orientation, setOrientation] = useState<Orientation>("portrait");
  const [stageSize, setStageSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  const [shotPulse, setShotPulse] = useState(false);
  const [logLines, setLogLines] = useState<string[]>([]);
  const [startingPath, setStartingPath] = useState<string | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [previewError, setPreviewError] = useState<string | null>(null);
  const [recovering, setRecovering] = useState(false);
  const [recoveryLog, setRecoveryLog] = useState<string[]>([]);
  // Stop UX state — same shape as the mobile/web hot-reload tab. Drives
  // the "Stopping…" pill on the button + a 2.5s success/error banner so
  // the user has visible confirmation that /dev/stop verified=true.
  const [stopState, setStopState] = useState<"idle" | "stopping" | "stopped" | "error">("idle");
  const [stopMessage, setStopMessage] = useState("");
  const [stopBuildsCancelled, setStopBuildsCancelled] = useState(0);
  // Dev-server boot/recovery progress, mirrored from the same
  // /dev/events SSE stream the mobile app consumes. The heuristic
  // (keyword → percent) matches mobile/app/(tabs)/apps.tsx so the two
  // surfaces feel identical. `active` stays true while we're streaming
  // pre-ready events; flipped to false when we see `type==="ready"`
  // or the dev-server status poll confirms `running=true`.
  const [devProgress, setDevProgress] = useState<{ pct: number; stage: string; active: boolean }>({ pct: 0, stage: "", active: false });
  const [composer, setComposer] = useState("");
  const [sending, setSending] = useState(false);
  const [sendStatus, setSendStatus] = useState<string | null>(null);
  const [activeTaskStream, setActiveTaskStream] = useState<{
    id: string;
    title: string;
    status: "queued" | "running" | "completed" | "failed" | "stopped";
    lines: string[];
  } | null>(null);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const stageRef = useRef<HTMLDivElement>(null);

  const [userPickedSkin, setUserPickedSkin] = useState(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    const s = window.localStorage.getItem(SKIN_STORAGE_KEY);
    if (s && DEVICES.some((d) => d.id === s)) {
      setSkinId(s);
      setUserPickedSkin(true);
    }
    const o = window.localStorage.getItem(ORIENTATION_STORAGE_KEY);
    if (o === "portrait" || o === "landscape") setOrientation(o);
  }, []);

  useEffect(() => {
    if (userPickedSkin) return;
    const fw = (devStatus?.framework || "").toLowerCase();
    if (!fw) return;
    const isWeb = fw.includes("next") || fw.includes("vite") || fw === "react";
    const isMobile = fw.includes("expo") || fw.includes("react-native") || fw.includes("flutter");
    if (isWeb) setSkinId("desktop");
    else if (isMobile) setSkinId("iphone-15");
  }, [devStatus?.framework, userPickedSkin]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(SKIN_STORAGE_KEY, skinId);
  }, [skinId]);

  useEffect(() => {
    if (typeof window === "undefined") return;
    window.localStorage.setItem(ORIENTATION_STORAGE_KEY, orientation);
  }, [orientation]);

  const stopActiveTaskStream = useCallback(() => {
    if (taskStreamStopRef.current) {
      taskStreamStopRef.current();
      taskStreamStopRef.current = null;
    }
    if (taskPollRef.current) {
      clearInterval(taskPollRef.current);
      taskPollRef.current = null;
    }
  }, []);

  useEffect(() => {
    return () => stopActiveTaskStream();
  }, [stopActiveTaskStream]);

  // Poll dev server + worker-session status.
  useEffect(() => {
    let alive = true;
    const poll = async () => {
      try {
        const [status, session] = await Promise.all([
          agentClient.getDevServerStatus(),
          agentClient.getMobileWorkerPreviewSession(),
        ]);
        if (!alive) return;
        setDevStatus(status);
        setWorkerSession(session);
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => {
      alive = false;
      clearInterval(interval);
    };
  }, []);

  // Re-render the iframe when the agent transitions to "connected" so the
  // preview picks up the latest connection target and clears any stale
  // loading/error state from the previous transport attempt.
  useEffect(() => {
    const unsubscribe = agentClient.on("connectionState", (state) => {
      if (state === "connected") {
        setIframeKey((k) => k + 1);
        setReloadNonce((n) => n + 1);
        setPreviewError(null);
      }
    });
    return unsubscribe;
  }, []);

  // Fetch project list for the empty-state picker.
  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const rows = await agentClient.listProjects();
        if (alive) setProjects(rows as Project[]);
      } catch {
        if (alive) setProjects([]);
      }
    })();
    return () => {
      alive = false;
    };
  }, [devStatus?.running]);

  // SSE: /dev/events streams boot progress, log lines, reload/ready signals.
  // We subscribe unconditionally (not gated on devStatus.running) so the
  // overlay shows progress during initial startup too, not only once the
  // server is ready. The keyword heuristic below mirrors mobile
  // (apps.tsx) so web and mobile advance the bar at the same moments.
  //
  // Re-runs whenever agentClient transitions to "connected" so a
  // PreviewPane that mounted before the agent finished its relay
  // handshake (devEventsUrl was null) reconnects once baseUrl lands.
  // Without this we silently sat on a closed stream forever and the
  // CONSOLE pane stayed at "0 lines / waiting for output…".
  // Diagnostic state for the CONSOLE header so an empty pane is
  // never just "(waiting for output…)" — instead it tells the user
  // exactly what stage the SSE pipeline is in (handshake → open →
  // first event), how many events have arrived, and any error.
  const [agentReady, setAgentReady] = useState(() => agentClient.connectionState === "connected" && Boolean(agentClient.devEventsUrl));
  // Seed connState from agentClient's current state instead of "unknown" —
  // the page often mounts AFTER the connect event fired, so the
  // listener-only path was missing the initial transition and we
  // showed a misleading "agent: unknown" forever.
  const [connState, setConnState] = useState<string>(() => agentClient.connectionState || "unknown");
  const [sseState, setSseState] = useState<"idle" | "opening" | "open" | "closed" | "error">("idle");
  const [sseError, setSseError] = useState<string | null>(null);
  const [sseUrl, setSseUrl] = useState<string | null>(null);
  const [sseAttempts, setSseAttempts] = useState(0);
  const [totalEvents, setTotalEvents] = useState(0);
  const [lastEventAt, setLastEventAt] = useState<number | null>(null);
  // Live agent heartbeat — populated by the agent's dev-server
  // heartbeat loop (every 5s). The strip uses this to render
  // "agent live · uptime 2m 14s · pid 12345 ✓" with a pulsing dot
  // so the user can SEE the system is alive even when Metro is
  // quiet between bundle requests.
  const [lastBeat, setLastBeat] = useState<{
    at: number;
    pid: number;
    pidAlive: boolean;
    uptimeSec: number;
    port: number;
    framework: string;
    idleSec: number;
    beatNumber: number;
  } | null>(null);

  // Yaver Protocol v1: structured progress + snapshot from the agent.
  //
  //   topicProgress[topic] = { phase, pct, done, total, unit, currentFile, etaMs, src, updatedAt }
  //
  // Populated from "progress" + "phase" SSE events. Reset by "snapshot"
  // events (snapshot is source-of-truth — if we missed deltas, snapshot
  // restores the world). Consumer renders from this map; the strip's
  // "Hermes 67%" / "Web bundling 42% — Route.js" badges read here.
  type TopicProgress = {
    phase: string;
    pct: number;
    done: number;
    total: number;
    unit: string;
    currentFile: string;
    etaMs: number;
    src: "exact" | "heuristic" | "unknown";
    updatedAt: number;
  };
  const [topicProgress, setTopicProgress] = useState<Record<string, TopicProgress>>({});
  // Latest agent snapshot — full picture of every running stream.
  // Updated from "snapshot" events every 5s. UI uses this as source
  // of truth; deltas just make it feel snappier.
  const [latestSnapshot, setLatestSnapshot] = useState<{
    generatedAt: number;
    running: boolean;
    framework: string;
    port: number;
    webPort: number;
    workDir: string;
    uptimeSec: number;
    idleSec: number;
    phases: Record<string, string>;
    recentLogs: string[];
  } | null>(null);

  // Connection-health is decoupled from compile state. Time since the
  // last byte arrived on the SSE stream (any byte — heartbeat, log,
  // progress, snapshot). User sees one global "we're listening" dot;
  // per-topic compile state is rendered separately. This is the
  // "never feel disconnected" contract: agent guarantees a snapshot
  // every 5s WHEN A DEV SERVER IS RUNNING, so > 6s without a byte
  // during an active run means transport is sick. When no dev server
  // is running, the agent legitimately doesn't emit heartbeats —
  // silence is expected, channel is "idle", not "reconnecting".
  const [lastByteAt, setLastByteAt] = useState<number>(Date.now());
  const connectionHealth: "live" | "idle" | "syncing" | "reconnecting" | "lost" = (() => {
    const ms = Date.now() - lastByteAt;
    const noDev = !devStatus?.running;
    if (noDev) {
      // Agent isn't producing snapshots because nothing's running.
      // Until SSE EventSource actually closes (sseState becomes
      // "error" / "closed"), assume transport is fine.
      if (sseState === "error" || sseState === "closed") return "lost";
      if (ms < 5_000) return "live";
      return "idle";
    }
    if (ms < 6_000) return "live";
    if (ms < 15_000) return "syncing";
    if (ms < 60_000) return "reconnecting";
    return "lost";
  })();
  // Clear stale per-topic progress when dev server stops — otherwise
  // "Dev server / listening / working…" lingers from the previous
  // session forever.
  useEffect(() => {
    if (!devStatus?.running) {
      setTopicProgress({});
      setLatestSnapshot(null);
    }
  }, [devStatus?.running]);
  // Forces the "X seconds ago" labels to refresh once per second
  // even when no new event lands. Without this the relative-time
  // strings only update when a beat arrives, undermining the
  // liveness signal.
  const [, setRerenderTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setRerenderTick((n) => n + 1), 1000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    // Re-sync once on mount in case the connect event fired between
    // the useState initializer and effect registration.
    setConnState(agentClient.connectionState || "unknown");
    setAgentReady(agentClient.connectionState === "connected" && Boolean(agentClient.devEventsUrl));
    return agentClient.on("connectionState", (state) => {
      setConnState(state);
      setAgentReady(state === "connected" && Boolean(agentClient.devEventsUrl));
    });
  }, []);

  // One-shot repair guard — survives effect re-runs so a stale
  // relay password (server-side infra issue) doesn't put us in a
  // 200x/sec retry storm with the strip blinking through the same
  // error 200 times. After one failed repair we stop trying;
  // a real fix has to happen on the relay/Convex side.
  const repairAttemptedRef = useRef(false);
  useEffect(() => {
    const eventsUrl = agentClient.devEventsUrl;
    setSseUrl(eventsUrl);
    if (!eventsUrl) {
      setSseState("idle");
      return;
    }
    setSseState("opening");
    setSseError(null);
    setSseAttempts((n) => n + 1);

    // Native EventSource — Safari handles cross-origin SSE through
    // EventSource cleanly; fetch + ReadableStream stalls
    // indefinitely in the preflight phase on this browser. Auth
    // rides on the URL (token + __rp query params) since
    // EventSource doesn't support custom headers. The relay strips
    // __rp before forwarding to the agent so the password never
    // shows in agent-side request logs.
    const es = new EventSource(eventsUrl);

    const bumpFromMessage = (msg: string) => {
      const m = msg.toLowerCase();
      let pct = 0;
      if (m.includes("install")) pct = 0.1;
      else if (m.includes("prebuild")) pct = 0.2;
      else if (m.includes("pod install")) pct = 0.3;
      else if (m.includes("bundling") || m.includes("metro")) pct = 0.45;
      else if (m.includes("compile") || m.includes("hermes")) pct = 0.6;
      else if (m.includes("starting") || m.includes("listen")) pct = 0.75;
      else if (m.includes("waiting on")) pct = 0.85;
      else if (m.includes("ready") || m.includes("accepting")) pct = 0.95;
      if (pct > 0) {
        setDevProgress((prev) => ({ pct: Math.max(prev.pct, pct), stage: msg.slice(0, 120), active: true }));
      } else {
        setDevProgress((prev) => prev.active ? { ...prev, stage: msg.slice(0, 120) } : prev);
      }
    };

    es.onopen = () => {
      setSseState("open");
      setSseError(null);
    };
    es.onerror = () => {
      if (es.readyState !== EventSource.CLOSED) return; // transient — let auto-reconnect handle
      // First close, no events flowed yet → try ONE repair, then
      // give up so we don't spam the strip with #225 retries.
      if (!repairAttemptedRef.current && totalEvents === 0) {
        repairAttemptedRef.current = true;
        setSseState("error");
        setSseError("relay password mismatch — attempting one-shot repair…");
        es.close();
        (async () => {
          const r = await agentClient.repairRelayPassword();
          if (r.ok) {
            // Toggle agentReady so the SSE effect re-runs once with
            // the rotated password. If THIS attempt also fails, the
            // ref guard above keeps us at one error message instead
            // of looping.
            setAgentReady(false);
            setTimeout(
              () => setAgentReady(agentClient.connectionState === "connected" && Boolean(agentClient.devEventsUrl)),
              80,
            );
          } else {
            setSseError(`relay password mismatch — repair failed (${r.error || "unknown"}). Server-side infra issue; click Reconnect & Fix or contact ops.`);
          }
        })();
        return;
      }
      // Already tried repair, still failing → freeze the strip on a
      // clear error and stop the close-loop. EventSource is closed
      // permanently; nothing else we can do client-side.
      setSseState("error");
      setSseError(
        repairAttemptedRef.current
          ? "EventSource closed after repair — relay/Convex password are out of sync. Server-side fix needed."
          : "EventSource closed (browser/relay dropped the stream)",
      );
    };
    es.onmessage = (msg) => {
      try {
        const ev = JSON.parse(msg.data);
        setTotalEvents((n) => n + 1);
        setLastEventAt(Date.now());
        // Heartbeat events are agent-driven liveness pulses (every
        // 5s while a dev server is running). They're not log lines —
        // we update the live-status header from them instead of
        // appending to the CONSOLE log buffer (would flood it).
        // ANY message — even keepalives + heartbeats — bumps lastByteAt
        // so the connection-health indicator stays "live". This is the
        // "never feel disconnected" contract: agent guarantees an event
        // every 5s, so absence of bytes for >6s means the transport is
        // sick — independent of whether anything is actively compiling.
        setLastByteAt(Date.now());

        if (ev.type === "heartbeat") {
          setLastBeat({
            at: Date.now(),
            pid: typeof ev.pid === "number" ? ev.pid : 0,
            pidAlive: ev.pidAlive === true,
            uptimeSec: typeof ev.uptimeSec === "number" ? ev.uptimeSec : 0,
            port: typeof ev.port === "number" ? ev.port : 0,
            framework: typeof ev.framework === "string" ? ev.framework : "",
            idleSec: typeof ev.idleSec === "number" ? ev.idleSec : 0,
            beatNumber: typeof ev.beatNumber === "number" ? ev.beatNumber : 0,
          });
          return;
        }
        // Yaver Protocol v1 — phase / progress / snapshot.
        if (ev.type === "phase" && typeof ev.topic === "string" && typeof ev.phase === "string") {
          setTopicProgress((prev) => ({
            ...prev,
            [ev.topic]: {
              phase: ev.phase,
              pct: prev[ev.topic]?.pct ?? 0,
              done: prev[ev.topic]?.done ?? 0,
              total: prev[ev.topic]?.total ?? 0,
              unit: prev[ev.topic]?.unit ?? "",
              currentFile: "",
              etaMs: 0,
              src: prev[ev.topic]?.src ?? "unknown",
              updatedAt: Date.now(),
            },
          }));
          return;
        }
        if (ev.type === "progress" && typeof ev.topic === "string") {
          setTopicProgress((prev) => ({
            ...prev,
            [ev.topic]: {
              phase: typeof ev.phase === "string" ? ev.phase : (prev[ev.topic]?.phase ?? ""),
              pct: typeof ev.pct === "number" ? ev.pct : 0,
              done: typeof ev.done === "number" ? ev.done : 0,
              total: typeof ev.total === "number" ? ev.total : 0,
              unit: typeof ev.unit === "string" ? ev.unit : "",
              currentFile: typeof ev.currentFile === "string" ? ev.currentFile : "",
              etaMs: typeof ev.etaMs === "number" ? ev.etaMs : 0,
              src: ev.progressSrc === "exact" || ev.progressSrc === "heuristic"
                ? ev.progressSrc
                : "unknown",
              updatedAt: Date.now(),
            },
          }));
          return;
        }
        if (ev.type === "snapshot" && ev.snapshot && typeof ev.snapshot === "object") {
          const s = ev.snapshot;
          setLatestSnapshot({
            generatedAt: Date.now(),
            running: s.running === true,
            framework: typeof s.framework === "string" ? s.framework : "",
            port: typeof s.port === "number" ? s.port : 0,
            webPort: typeof s.webPort === "number" ? s.webPort : 0,
            workDir: typeof s.workDir === "string" ? s.workDir : "",
            uptimeSec: typeof s.uptimeSec === "number" ? s.uptimeSec : 0,
            idleSec: typeof s.idleSec === "number" ? s.idleSec : 0,
            phases: s.phases && typeof s.phases === "object" ? s.phases : {},
            recentLogs: Array.isArray(s.recentLogs) ? s.recentLogs.slice(-8) : [],
          });
          // Snapshot is source of truth — reconcile topicProgress
          // entries that no longer appear in the snapshot's phases.
          if (s.phases && typeof s.phases === "object") {
            setTopicProgress((prev) => {
              const next = { ...prev };
              for (const [topic, phase] of Object.entries(s.phases as Record<string, string>)) {
                next[topic] = {
                  phase: phase || "",
                  pct: prev[topic]?.pct ?? 0,
                  done: prev[topic]?.done ?? 0,
                  total: prev[topic]?.total ?? 0,
                  unit: prev[topic]?.unit ?? "",
                  currentFile: prev[topic]?.currentFile ?? "",
                  etaMs: prev[topic]?.etaMs ?? 0,
                  src: prev[topic]?.src ?? "unknown",
                  updatedAt: Date.now(),
                };
              }
              return next;
            });
          }
          // Embed progress field directly when the snapshot includes one
          if (s.progress && typeof s.progress === "object") {
            const p = s.progress;
            setTopicProgress((prev) => ({
              ...prev,
              [p.topic ?? "dev/start"]: {
                phase: typeof p.phase === "string" ? p.phase : (prev[p.topic ?? "dev/start"]?.phase ?? ""),
                pct: typeof p.pct === "number" ? p.pct : 0,
                done: typeof p.done === "number" ? p.done : 0,
                total: typeof p.total === "number" ? p.total : 0,
                unit: typeof p.unit === "string" ? p.unit : "",
                currentFile: typeof p.currentFile === "string" ? p.currentFile : "",
                etaMs: typeof p.etaMs === "number" ? p.etaMs : 0,
                src: p.progressSrc === "exact" || p.progressSrc === "heuristic" ? p.progressSrc : "unknown",
                updatedAt: Date.now(),
              },
            }));
          }
          if (s.webProgress && typeof s.webProgress === "object") {
            const p = s.webProgress;
            setTopicProgress((prev) => ({
              ...prev,
              [p.topic ?? "webview/build"]: {
                phase: typeof p.phase === "string" ? p.phase : (prev[p.topic ?? "webview/build"]?.phase ?? ""),
                pct: typeof p.pct === "number" ? p.pct : 0,
                done: typeof p.done === "number" ? p.done : 0,
                total: typeof p.total === "number" ? p.total : 0,
                unit: typeof p.unit === "string" ? p.unit : "",
                currentFile: typeof p.currentFile === "string" ? p.currentFile : "",
                etaMs: typeof p.etaMs === "number" ? p.etaMs : 0,
                src: p.progressSrc === "exact" || p.progressSrc === "heuristic" ? p.progressSrc : "unknown",
                updatedAt: Date.now(),
              },
            }));
          }
          return;
        }
        const text = (ev.logLine || ev.message || ev.text || "").toString();
        if (ev.type === "reload" || ev.type === "ready") {
          setIframeKey((k) => k + 1);
          setReloadNonce((n) => n + 1);
          if (ev.type === "ready") {
            setDevProgress({ pct: 1, stage: "ready", active: true });
            setTimeout(() => setDevProgress({ pct: 0, stage: "", active: false }), 1500);
          }
        } else if (ev.type === "log" || ev.type === "line") {
          if (text) {
            setLogLines((prev) => {
              const next = [...prev, text];
              return next.length > 200 ? next.slice(-200) : next;
            });
            bumpFromMessage(text);
          }
        } else if (ev.type === "error") {
          if (text) {
            setLogLines((prev) => [...prev.slice(-200), `[error] ${text}`]);
            setDevProgress((prev) => ({ ...prev, stage: `error: ${text.slice(0, 120)}`, active: true }));
          }
        } else if (ev.type === "stopped") {
          setDevProgress({ pct: 0, stage: "", active: false });
          setTopicProgress({});
          setLatestSnapshot(null);
        }
      } catch { /* ignore non-JSON keepalive comments etc. */ }
    };

    return () => es.close();
  }, [agentReady]);

  // When the dev server confirms "running" via the status poll, clear
  // the progress overlay (SSE may have missed the `ready` event if we
  // connected late — the status poll is the authoritative ground truth).
  useEffect(() => {
    if (devStatus?.running) {
      setDevProgress({ pct: 0, stage: "", active: false });
    }
  }, [devStatus?.running]);

  // Reset logs when dev server transitions stopped → running.
  useEffect(() => {
    if (devStatus?.running) {
      setStartError(null);
    } else {
      setLogLines([]);
    }
  }, [devStatus?.running]);

  useEffect(() => {
    const el = stageRef.current;
    if (!el) return;
    const measure = () => {
      const rect = el.getBoundingClientRect();
      setStageSize({ w: rect.width, h: rect.height });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const skin = useMemo(() => DEVICES.find((d) => d.id === skinId) ?? DEVICES[0], [skinId]);
  const previewUrl = agentClient.devPreviewUrl;
  const previewFrameUrl = useMemo(() => {
    if (!previewUrl) return null;
    try {
      const url = new URL(previewUrl);
      url.searchParams.set("__preview_reload", String(reloadNonce));
      return url.toString();
    } catch {
      const join = previewUrl.includes("?") ? "&" : "?";
      return `${previewUrl}${join}__preview_reload=${encodeURIComponent(String(reloadNonce))}`;
    }
  }, [previewUrl, reloadNonce]);

  const frame = useMemo(() => {
    if (skin.plain) return { width: 0, height: 0 };
    const w = orientation === "portrait" ? skin.width : skin.height;
    const h = orientation === "portrait" ? skin.height : skin.width;
    return {
      width: w + skin.bezel * 2,
      height: h + skin.bezel * 2,
      innerWidth: w,
      innerHeight: h,
    } as { width: number; height: number; innerWidth: number; innerHeight: number };
  }, [skin, orientation]);

  const scale = useMemo(() => {
    if (skin.plain || !frame.width || !frame.height || !stageSize.w || !stageSize.h) return 1;
    const margin = 32;
    const sx = (stageSize.w - margin) / frame.width;
    const sy = (stageSize.h - margin) / frame.height;
    return Math.min(sx, sy, 1);
  }, [skin.plain, frame.width, frame.height, stageSize.w, stageSize.h]);

  // Preflight: when the preview URL is set, fetch it once so auth/DNS/dev
  // server failures surface clearly instead of leaving the user staring at
  // a broken iframe.
  useEffect(() => {
    if (!previewFrameUrl || !devStatus?.running) {
      setPreviewError(null);
      return;
    }
    let alive = true;
    const controller = new AbortController();
    (async () => {
      try {
        const res = await fetch(previewFrameUrl, {
          method: "GET",
          signal: controller.signal,
          cache: "no-store",
          redirect: "manual",
        });
        if (!alive) return;
        if (res.status === 401 || res.status === 403) {
          const text = await res.text().catch(() => "");
          let msg = `HTTP ${res.status}`;
          try {
            const parsed = JSON.parse(text);
            if (parsed?.error) msg = parsed.error;
          } catch {
            if (text) msg = text.slice(0, 200);
          }
          setPreviewError(msg);
          return;
        }
        setPreviewError(null);
      } catch (e: any) {
        if (e?.name === "AbortError") return;
        if (!alive) return;
        // Network errors are often transient — don't over-report them.
        setPreviewError(null);
      }
    })();
    return () => {
      alive = false;
      controller.abort();
    };
  }, [previewFrameUrl, devStatus?.running, reloadNonce]);

  const appendRecovery = useCallback((line: string) => {
    setRecoveryLog((prev) => {
      const next = [...prev, line];
      return next.length > 80 ? next.slice(-80) : next;
    });
  }, []);

  const handleReconnect = useCallback(async () => {
    if (recovering) return;
    setRecovering(true);
    setRecoveryLog([]);
    setDevProgress({ pct: 0.05, stage: "starting recovery…", active: true });
    const savedWorkDir = devStatus?.workDir;
    const savedFramework = devStatus?.framework;
    const stage = (pct: number, label: string) => {
      setDevProgress((prev) => ({ pct: Math.max(prev.pct, pct), stage: label, active: true }));
    };
    try {
      stage(0.1, "checking agent reachability…");
      appendRecovery("→ checking agent reachability…");
      try {
        const info = await agentClient.getInfo();
        appendRecovery(`✓ agent ok (v${info?.version || "?"})`);
      } catch (e: any) {
        appendRecovery(`✗ agent not reachable: ${e?.message || e}`);
        if (onReconnect) {
          appendRecovery("→ reconnecting device…");
          try {
            await onReconnect();
            appendRecovery("✓ device reconnect done");
          } catch (err: any) {
            appendRecovery(`✗ device reconnect failed: ${err?.message || err}`);
          }
        } else {
          appendRecovery("  (no device reconnect handler — open Devices tab to reconnect manually)");
        }
      }

      // If the preview was 401-ing with "invalid relay password", a broken
      // userSettings.relayPassword row in Convex is the most likely cause
      // (fresh-install race, or a prod password rotation the user missed).
      // Ask Convex to re-sync with the platform default — this is a
      // single-row patch to the CURRENT platform password, never a new
      // secret, so it's idempotent and safe to call defensively.
      if (previewError && /invalid relay password/i.test(previewError) && onRepairRelay) {
        appendRecovery("→ repairing user relay password in Convex…");
        try {
          const r = await onRepairRelay();
          appendRecovery(r.repaired ? `✓ repaired: ${r.reason}` : `· ${r.reason}`);
          if (r.repaired) {
            appendRecovery("→ reconnecting device to pick up new password…");
            if (onReconnect) {
              try {
                await onReconnect();
                appendRecovery("✓ reconnected");
              } catch (err: any) {
                appendRecovery(`✗ reconnect after repair failed: ${err?.message || err}`);
              }
            }
          }
        } catch (e: any) {
          appendRecovery(`✗ repair failed: ${e?.message || e}`);
        }
      }

      if (savedWorkDir) {
        stage(0.25, "stopping dev server…");
        appendRecovery("→ stopping dev server…");
        try {
          await agentClient.stopDevServer();
          appendRecovery("✓ stopped");
        } catch (e: any) {
          appendRecovery(`warn: stop failed: ${e?.message || e}`);
        }

        // Fast-forward the project to origin/HEAD so a fix that landed
        // on github after the box's last pull actually shows up in the
        // iframe. Skipped silently when the directory isn't a git repo
        // or the working tree is dirty (we never blow away local edits).
        stage(0.4, "git pull --ff-only…");
        appendRecovery("→ pulling latest commit (git pull --ff-only)…");
        try {
          // startExec only returns {execId, pid} — it doesn't surface
          // stdout to the dashboard, so we don't pretend to read git's
          // output here. Success means the shell exited 0; that's
          // enough to advance the recovery flow. The exec is fire-and-
          // forget at the dashboard layer — Metro will pick up any new
          // files on its next file-watcher tick after restart.
          await agentClient.startExec({
            command:
              "if [ -d .git ]; then if git diff --quiet && git diff --cached --quiet; then git fetch --depth=50 && git pull --ff-only; else echo 'skip: working tree has uncommitted changes' >&2; fi; else echo 'skip: not a git repo' >&2; fi",
            workDir: savedWorkDir,
            timeout: 60,
          });
          appendRecovery("✓ git pulled (or skipped on dirty/non-repo)");
        } catch (e: any) {
          appendRecovery(`warn: git pull failed: ${e?.message || e}`);
        }

        // Kill stray Metro / Expo processes — the most common cause of
        // "expo:8082" / "expo:8083" port creep is a previous Metro that
        // wasn't reaped, and the new one renders the *old* bundle from
        // its in-memory cache. pkill -f is wide-net but limited to this
        // user's processes (no sudo).
        stage(0.55, "killing stray Metro / Expo…");
        appendRecovery("→ killing stray Metro / Expo processes…");
        try {
          await agentClient.startExec({
            command:
              "pkill -f 'expo start' 2>/dev/null; pkill -f 'metro' 2>/dev/null; pkill -f 'react-native start' 2>/dev/null; sleep 1; echo procs-killed",
            workDir: savedWorkDir,
            timeout: 20,
          });
          appendRecovery("✓ procs killed");
        } catch (e: any) {
          appendRecovery(`warn: pkill failed: ${e?.message || e}`);
        }

        stage(0.7, "clearing caches…");
        appendRecovery("→ clearing caches on remote (metro / .expo / node_modules/.cache)…");
        try {
          await agentClient.startExec({
            command:
              "rm -rf node_modules/.cache .expo/web/cache .metro-cache /tmp/metro-* /tmp/haste-map-* 2>/dev/null || true; echo cache-cleared",
            workDir: savedWorkDir,
            timeout: 30,
          });
          appendRecovery("✓ caches cleared");
        } catch (e: any) {
          appendRecovery(`warn: cache clear failed: ${e?.message || e}`);
        }

        // Don't auto-restart the previous dev server — that
        // forces a project the user may not want any more
        // ("auto-selects sfmg, I should select it"). After
        // cleaning we leave the picker visible so the user
        // chooses what to start next. To re-run the same
        // project they just click ▶ START on its row.
        appendRecovery(`  (cleaned — pick a project to start)`);
      } else {
        appendRecovery("  (no dev server was running — pick a project to start)");
      }

      stage(0.95, "refreshing preview…");
      appendRecovery("→ refreshing preview…");
      setIframeKey((k) => k + 1);
      setReloadNonce((n) => n + 1);
      setPreviewError(null);
      appendRecovery("✓ done");
      setDevProgress({ pct: 1, stage: "done", active: true });
      setTimeout(() => setDevProgress({ pct: 0, stage: "", active: false }), 1500);
    } catch (e: any) {
      appendRecovery(`✗ recovery failed: ${e?.message || e}`);
      setDevProgress((prev) => ({ ...prev, stage: `failed: ${e?.message || e}` }));
    }
    setRecovering(false);
  }, [recovering, devStatus, onReconnect, selectedPreviewTarget, appendRecovery, previewError, onRepairRelay]);

  const handleSendPrompt = useCallback(async () => {
    const prompt = composer.trim();
    if (!prompt || sending) return;
    setSending(true);
    setSendStatus(null);
    try {
      stopActiveTaskStream();
      const projectName = (() => {
        const wd = devStatus?.workDir;
        if (!wd) return undefined;
        const parts = wd.split("/").filter(Boolean);
        return parts[parts.length - 1];
      })();
      const task = await agentClient.createTask({
        title: prompt.slice(0, 80),
        description: prompt,
        projectName,
        workDir: devStatus?.workDir,
      });
      setActiveTaskStream({
        id: task.id,
        title: task.title,
        status: task.status,
        lines: [],
      });
      taskStreamStopRef.current = agentClient.streamTaskOutput(task.id, (line) => {
        const trimmed = String(line || "").trimEnd();
        if (!trimmed) return;
        setActiveTaskStream((prev) => {
          if (!prev || prev.id !== task.id) return prev;
          const next = [...prev.lines, trimmed];
          return {
            ...prev,
            status: "running",
            lines: next.length > 200 ? next.slice(-200) : next,
          };
        });
      });
      taskPollRef.current = setInterval(() => {
        void agentClient.getTask(task.id)
          .then((fresh) => {
            setActiveTaskStream((prev) => {
              if (!prev || prev.id !== task.id) return prev;
              const nextLines = fresh.output && fresh.output.length > 0
                ? fresh.output
                : prev.lines.length === 0 && fresh.resultText
                  ? [fresh.resultText]
                  : prev.lines;
              return {
                ...prev,
                status: fresh.status,
                lines: nextLines.length > 200 ? nextLines.slice(-200) : nextLines,
              };
            });
            if (fresh.status !== "queued" && fresh.status !== "running") {
              stopActiveTaskStream();
            }
          })
          .catch(() => {});
      }, 2000);
      setComposer("");
      setSendStatus(`✓ started “${task.title}”`);
    } catch (e: any) {
      setSendStatus(`✗ ${e?.message || e}`);
    }
    setSending(false);
  }, [composer, sending, devStatus?.workDir, stopActiveTaskStream]);

  const handleReload = useCallback(async () => {
    const framework = (devStatus?.framework || "").toLowerCase();
    setIframeKey((k) => k + 1);
    setReloadNonce((n) => n + 1);
    if (isWebPreviewFramework(framework)) {
      try {
        await agentClient.reloadDevServer({ mode: "dev" });
      } catch {
        // Browser preview already got a hard refresh above.
      }
      return;
    }
    await agentClient.reloadDevServer({ mode: "bundle" });
  }, [devStatus?.framework]);

  const handleStop = useCallback(async () => {
    setStopState("stopping");
    setStopMessage("");
    setStopBuildsCancelled(0);
    setLogLines((prev) => [...prev.slice(-200), `[ui] ▶ Stop & switch — sending POST /dev/stop…`]);
    try {
      const res: any = await agentClient.stopDevServer();
      setDevStatus(null);
      if (!res || res.ok === false) {
        const msg = res?.error || res?.message || "Stop failed.";
        setStopState("error");
        setStopMessage(msg);
        setLogLines((prev) => [...prev.slice(-200), `[ui] ✗ /dev/stop failed: ${msg}`]);
        setTimeout(() => setStopState("idle"), 5000);
        return;
      }
      if (res.verified === false) {
        setStopState("error");
        setStopMessage("Subprocess didn't confirm exit within 7s (agent issued SIGKILL).");
        setLogLines((prev) => [...prev.slice(-200), `[ui] ⚠ /dev/stop returned verified=false`]);
        setTimeout(() => setStopState("idle"), 5000);
        return;
      }
      setStopBuildsCancelled(res.buildsCancelled || 0);
      setStopState("stopped");
      setLogLines((prev) => [
        ...prev.slice(-200),
        `[ui] ✓ /dev/stop verified · ${res.buildsCancelled || 0} build(s) cancelled`,
      ]);
      setTimeout(() => {
        setStopState("idle");
        setStopMessage("");
        setStopBuildsCancelled(0);
      }, 2500);
    } catch (e: any) {
      setStopState("error");
      setStopMessage(e?.message ?? String(e));
      setLogLines((prev) => [...prev.slice(-200), `[ui] ✗ /dev/stop failed: ${e?.message ?? e}`]);
      setDevStatus(null);
      setTimeout(() => setStopState("idle"), 5000);
    }
  }, []);

  const handleRequestScreenshot = useCallback(async () => {
    const ok = await agentClient.sendMobileWorkerPreviewCommand("capture_screenshot", {
      reason: "preview-control-plane",
    });
    if (ok) {
      setShotPulse(true);
      setTimeout(() => setShotPulse(false), 1200);
    }
  }, []);

  const handleStartProject = useCallback(
    async (project: Project) => {
      setStartingPath(project.path);
      setStartError(null);
      setLogLines([
        `[ui] ▶ START ${project.name} (${likelyFramework(project)})`,
        `[ui] workDir: ${project.path}`,
        `[ui] sending POST /dev/start …`,
      ]);
      try {
        await agentClient.startDevServer({
          framework: likelyFramework(project),
          workDir: project.path,
          platform: previewPlatformForProject(project),
          targetDeviceId: selectedPreviewTarget?.id,
          targetDeviceName: selectedPreviewTarget?.name,
        });
        setLogLines((prev) => [...prev, `[ui] ✓ /dev/start accepted, waiting for "ready" event…`]);
        // status poll will pick up running=true shortly
      } catch (e: any) {
        const msg = e?.message || "Failed to start dev server";
        setStartError(msg);
        setLogLines((prev) => [...prev, `[ui] ✗ /dev/start failed: ${msg}`]);
      }
      setStartingPath(null);
    },
    [selectedPreviewTarget],
  );

  const mobileProjects = useMemo(() => {
    if (!projects) return [];
    return projects.filter((p) => {
      const fw = (p.framework || "").toLowerCase();
      const tags = (p.tags || []).map((t) => t.toLowerCase());
      return (
        fw.includes("expo") ||
        fw.includes("react-native") ||
        fw.includes("flutter") ||
        tags.includes("expo") ||
        tags.includes("react-native") ||
        tags.includes("flutter")
      );
    });
  }, [projects]);

  const webProjects = useMemo(() => {
    if (!projects) return [];
    return projects.filter((p) => {
      const fw = (p.framework || "").toLowerCase();
      const tags = (p.tags || []).map((t) => t.toLowerCase());
      return (
        fw.includes("next") ||
        fw.includes("vite") ||
        fw === "react" ||
        tags.includes("next") ||
        tags.includes("nextjs") ||
        tags.includes("vite") ||
        (tags.includes("react") && !tags.includes("react-native"))
      );
    });
  }, [projects]);

  const innerDim = skin.plain
    ? { width: "100%", height: "100%" }
    : { width: `${(frame as { innerWidth: number }).innerWidth}px`, height: `${(frame as { innerHeight: number }).innerHeight}px` };

  const innerContent = connectedDeviceNeedsAuth ? (
    <div className="w-full h-full flex flex-col items-center justify-center gap-3 bg-surface-950 p-6 text-center text-surface-300">
      <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-amber-400/80">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
        <path d="M7 11V7a5 5 0 0 1 10 0v4" />
      </svg>
      <p className="text-[12px] font-medium text-surface-100">Agent session expired on this machine</p>
      <p className="max-w-[320px] text-[11px] text-surface-400">
        Mobile App preview can&apos;t reach the agent until you sign back in on the host
        (run <code className="rounded bg-surface-800 px-1 py-px font-mono text-[10px]">yaver auth</code>),
        reconnect, or switch to a different coding agent that doesn&apos;t need
        re-auth (Ollama / Aider+Qwen, packaged with Yaver and ready to go).
      </p>
      <div className="mt-1 flex flex-wrap items-center justify-center gap-2">
        {/* Single-click re-auth: prefer triggering the browser flow
            directly when we know the issue is an expired session,
            because just calling /reconnect against a needsAuth device
            won't actually fix anything. Falls back to plain reconnect
            when no reauth handler is wired. */}
        {onTriggerReauth ? (
          <button
            onClick={() => onTriggerReauth(primaryRunner || "claude")}
            className="rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1 text-[11px] font-medium text-amber-200 hover:bg-amber-500/20"
            title={`Open the ${primaryRunner || "claude"} browser sign-in flow on the host. After you sign in the device reconnects automatically.`}
          >
            Sign in to {primaryRunner || "claude"} &amp; reconnect
          </button>
        ) : onReconnect ? (
          <button
            onClick={() => void onReconnect()}
            className="rounded border border-amber-500/40 bg-amber-500/10 px-3 py-1 text-[11px] text-amber-200 hover:bg-amber-500/20"
          >
            Try reconnect
          </button>
        ) : null}
        {onSwitchAgent && (
          <button
            onClick={() => onSwitchAgent()}
            className="rounded border border-emerald-500/40 bg-emerald-500/10 px-3 py-1 text-[11px] text-emerald-200 hover:bg-emerald-500/20"
            title="Pick a different runner. Cloud agents (Claude / Codex) trigger the browser sign-in flow; local agents (Ollama / Aider+Qwen) start immediately."
          >
            Switch agent
          </button>
        )}
      </div>
    </div>
  ) : devStatus?.running && previewFrameUrl && isMobileDevClient(devStatus) ? (
    // Metro in --dev-client mode returns a JSON manifest at /, not HTML.
    // Iframing it paints blank white and leaves the user staring at an
    // empty phone. Show a clear status + call-to-action instead so they
    // know Metro is alive and waiting for the phone to pick up the
    // bundle. SSE log tail + progress overlay still stream normally.
    <div className="w-full h-full flex flex-col items-center justify-center gap-4 bg-gradient-to-b from-surface-950 to-surface-900 p-6 text-center">
      <div className="text-4xl">📱</div>
      <div className="space-y-1">
        <p className="text-[13px] font-medium text-surface-100">Metro is ready</p>
        <p className="text-[11px] text-surface-400">
          <span className="font-mono">{devStatus?.framework || "expo"}</span>
          {devStatus?.port ? <span className="font-mono"> · :{devStatus.port}</span> : null}
        </p>
      </div>
      <p className="max-w-[280px] text-[11px] leading-5 text-surface-500">
        This is the Mobile App preview surface. Metro is waiting for the Yaver app
        on your phone to request a Hermes bundle. Open{" "}
        <span className="font-mono text-surface-300">
          {devStatus?.workDir?.split("/").slice(-1)[0] || "this project"}
        </span>{" "}
        in the Yaver mobile app to preview here.
      </p>
      <p className="max-w-[280px] text-[10px] leading-5 text-surface-600">
        Or switch this widget to <span className="text-surface-400">Web App</span> mode to
        render a browser preview via <span className="font-mono">expo --web</span>.
      </p>
      <a
        href={`https://yaver.io/docs/yaver-protocol#web-ui-rendering-paths`}
        target="_blank"
        rel="noreferrer"
        className="text-[9px] text-surface-700 hover:text-surface-500 underline-offset-2 hover:underline"
        title="Hermes-WASM browser execution is wired but experimental — runner JS pending upstream Hermes. The Web App tab's static bundle is the recommended browser-render path today."
      >
        experimental: try Hermes WASM in browser →
      </a>
    </div>
  ) : devStatus?.running && previewFrameUrl ? (
    <iframe
      key={iframeKey}
      ref={iframeRef}
      src={previewFrameUrl}
      className="w-full h-full border-none bg-white"
      sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
    />
  ) : devStatus?.running && !previewFrameUrl ? (
    // Dev server is up but we don't have a usable preview URL yet — typically
    // means the relay probe hasn't populated `activeRelayPassword` yet. Show
    // a clear waiting state with a manual retry, instead of blanking out.
    <div className="w-full h-full flex flex-col items-center justify-center bg-surface-950 text-surface-400 p-4 gap-3">
      <div className="h-6 w-6 animate-spin rounded-full border-2 border-surface-700 border-t-amber-400" />
      <div className="text-xs font-medium text-surface-300">Waiting for relay auth…</div>
      <div className="max-w-xs text-center text-[10px] text-surface-600">
        Dev server is up, but the relay password isn&apos;t loaded yet. This
        usually clears in a second.
      </div>
      <button
        onClick={() => void handleReconnect()}
        disabled={recovering}
        className="rounded border border-emerald-500/40 bg-emerald-500/10 px-3 py-1 text-[11px] text-emerald-300 hover:bg-emerald-500/20 disabled:opacity-50"
      >
        {recovering ? "Reconnecting…" : "Force reconnect"}
      </button>
    </div>
  ) : (
    <EmptyPhoneState
      projects={mobileProjects.length > 0 ? mobileProjects : webProjects}
      projectsAll={projects}
      preferredProjectPath={preferredProjectPath}
      onStart={handleStartProject}
      startingPath={startingPath}
      startError={startError}
    />
  );

  const projectLabel = devStatus?.workDir?.split("/").slice(-1)[0] || "this project";

  return (
    <div className="flex flex-col h-full">
      {/* Toolbar */}
      <div className="h-9 flex items-center px-3 gap-2 border-b border-surface-800 bg-surface-900/50 shrink-0">
        <span
          className={`text-[10px] ${
            devStatus?.running ? "text-emerald-400" : "text-surface-500"
          }`}
        >
          {devStatus?.running
            ? `${frameworkIcon(devStatus.framework)} ${devStatus.framework || "dev"}${devStatus.port ? ` :${devStatus.port}` : ""}`
            : "live preview"}
        </span>
        <span className="flex-1 text-[10px] text-surface-600 font-mono truncate">
          {devStatus?.running ? devStatus.workDir || previewFrameUrl : "no dev server running"}
        </span>
        {devStatus?.running ? (
          <span className="text-[10px] text-sky-300">
            {devStatus.targetDeviceName || selectedPreviewTarget?.name || "current"}
          </span>
        ) : null}
        {workerSession?.hasTarget ? (
          <span className={`text-[10px] ${workerSession.workerOnline ? "text-emerald-400" : "text-amber-400"}`}>
            {workerSession.workerOnline ? "worker online" : "worker offline"}
          </span>
        ) : null}
        {workerSession?.hasTarget && workerSession.workerOnline ? (
          <button
            onClick={handleRequestScreenshot}
            className={`text-xs ${shotPulse ? "text-emerald-400" : "text-surface-400 hover:text-surface-200"}`}
            title="Request screenshot from selected worker"
          >
            Shot
          </button>
        ) : null}
        <button
          onClick={() => void handleReconnect()}
          disabled={recovering}
          className={`text-[10px] rounded border px-2 py-0.5 ${
            recovering
              ? "border-amber-500/40 bg-amber-500/10 text-amber-300 cursor-wait"
              : "border-surface-700 text-surface-400 hover:border-emerald-500/40 hover:text-emerald-300"
          }`}
          title="Try to recover: ping agent, stop, clear caches, restart, refresh"
        >
          {recovering ? "Reconnecting…" : "Reconnect & Fix"}
        </button>
        {devStatus?.running ? (
          <>
            <button
              onClick={() => void handleReload()}
              className="rounded border border-surface-700 px-2 py-0.5 text-[10px] text-surface-300 hover:bg-surface-800 hover:text-surface-100"
              title={isWebPreviewFramework(devStatus?.framework) ? "Refresh preview" : "Reload mobile app"}
            >
              ↻ Reload
            </button>
            <button
              onClick={handleStop}
              disabled={stopState === "stopping"}
              className="rounded border border-red-500/40 bg-red-500/10 px-2 py-0.5 text-[10px] text-red-200 hover:bg-red-500/20 disabled:opacity-60 disabled:cursor-wait"
              title="Stop the dev server, cancel any in-flight Hermes build, clear stale incidents"
            >
              {stopState === "stopping" ? (
                <span className="inline-flex items-center gap-1">
                  <span className="h-2 w-2 animate-spin rounded-full border border-red-200/40 border-t-red-200" />
                  Stopping…
                </span>
              ) : (
                "■ Stop & switch"
              )}
            </button>
          </>
        ) : null}
      </div>

      {/* Post-stop banner — explicit confirmation that the agent verified
          the subprocess exited (or surfaced a problem). Self-dismisses
          on success. agent 1.99.93+ provides verified + buildsCancelled. */}
      {(stopState === "stopped" || stopState === "error") && (
        <div
          className={`flex items-start gap-2 border-b px-3 py-1.5 text-[10px] ${
            stopState === "stopped"
              ? "border-emerald-500/30 bg-emerald-500/5 text-emerald-200"
              : "border-red-500/30 bg-red-500/5 text-red-200"
          }`}
          role="status"
        >
          <span className="font-mono leading-none">{stopState === "stopped" ? "✓" : "⚠"}</span>
          <span className="flex-1">
            {stopState === "stopped"
              ? stopBuildsCancelled > 0
                ? `Dev server stopped — subprocess confirmed exit. Cancelled ${stopBuildsCancelled} in-flight build${stopBuildsCancelled === 1 ? "" : "s"}.`
                : "Dev server stopped — subprocess confirmed exit."
              : stopMessage || "Stop did not complete."}
          </span>
        </div>
      )}

      {previewError ? (
        <div className="flex items-start gap-2 border-b border-red-500/20 bg-red-500/5 px-3 py-1.5 text-[10px] text-red-300">
          <span className="font-mono">preview error:</span>
          <span className="flex-1 truncate">{previewError}</span>
          <button
            onClick={() => void handleReconnect()}
            disabled={recovering}
            className="shrink-0 rounded border border-red-500/40 bg-red-500/10 px-2 py-0.5 text-red-200 hover:bg-red-500/20 disabled:opacity-50"
          >
            {recovering ? "…" : "Recover"}
          </button>
        </div>
      ) : null}

      {/* Target picker (mobile workers) */}
      {mobileWorkers.length > 0 && (
        <div className="flex items-center gap-2 px-3 py-2 border-b border-surface-800 bg-surface-950/60 overflow-x-auto">
          <span className="text-[10px] uppercase tracking-widest text-surface-500 shrink-0">Target</span>
          <button
            onClick={() => onSelectPreviewTarget(null)}
            className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
              !selectedPreviewTarget
                ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                : "border-surface-800 text-surface-500"
            }`}
          >
            Current device
          </button>
          {mobileWorkers.map((device) => (
            <button
              key={device.id}
              onClick={() => onSelectPreviewTarget(device.id)}
              className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
                selectedPreviewTarget?.id === device.id
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                  : "border-surface-800 text-surface-500"
              }`}
            >
              {device.name}
            </button>
          ))}
        </div>
      )}

      {/* Skin + orientation picker */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-surface-800 bg-surface-950/60 overflow-x-auto">
        <span className="text-[10px] uppercase tracking-widest text-surface-500 shrink-0">Device</span>
        {DEVICES.map((d) => (
          <button
            key={d.id}
            onClick={() => {
              setSkinId(d.id);
              setUserPickedSkin(true);
            }}
            className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
              skinId === d.id
                ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                : "border-surface-800 text-surface-500 hover:text-surface-300"
            }`}
            title={d.plain ? "No chrome, full pane" : `${d.width}×${d.height}`}
          >
            {d.label}
          </button>
        ))}
        {!skin.plain ? (
          <>
            <span className="mx-1 text-surface-700">·</span>
            <button
              onClick={() => setOrientation("portrait")}
              className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
                orientation === "portrait"
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                  : "border-surface-800 text-surface-500 hover:text-surface-300"
              }`}
              title="Portrait"
            >
              &#x2B15;
            </button>
            <button
              onClick={() => setOrientation("landscape")}
              className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
                orientation === "landscape"
                  ? "border-sky-500/40 bg-sky-500/10 text-sky-300"
                  : "border-surface-800 text-surface-500 hover:text-surface-300"
              }`}
              title="Landscape"
            >
              &#x25AD;
            </button>
            <span className="ml-2 text-[10px] text-surface-600 font-mono">
              {orientation === "portrait" ? `${skin.width}×${skin.height}` : `${skin.height}×${skin.width}`}
              {scale < 1 ? ` · ${Math.round(scale * 100)}%` : ""}
            </span>
          </>
        ) : null}
      </div>

      <div className="flex-1 min-h-0 grid grid-cols-1 xl:grid-cols-[300px_minmax(0,1fr)_320px]">
        <div className="min-h-0 border-b border-surface-800 bg-surface-950/70 xl:border-b-0 xl:border-r">
          <div className="flex h-full min-h-0 flex-col">
            <div className="border-b border-surface-800 px-3 py-2">
              <div className="text-[10px] uppercase tracking-widest text-emerald-300">Vibing</div>
              <div className="mt-1 text-[11px] text-surface-500">
                {devStatus?.running
                  ? `Send changes directly to ${projectLabel}.`
                  : "Start a project, then send a task from here."}
              </div>
            </div>
            <div className="flex-1 min-h-0 overflow-auto px-3 py-2">
              {activeTaskStream ? (
                <div className="grid gap-2">
                  <div className="flex items-center justify-between text-[10px] uppercase tracking-widest text-sky-300">
                    <span>Task stream · {activeTaskStream.status}</span>
                    <span className="max-w-[52%] truncate text-right normal-case tracking-normal text-surface-500" title={activeTaskStream.title}>
                      {activeTaskStream.title}
                    </span>
                  </div>
                  <pre className="min-h-[180px] overflow-auto whitespace-pre-wrap break-all rounded border border-surface-800 bg-surface-950 px-3 py-2 font-mono text-[10px] leading-4 text-surface-300">
                    {activeTaskStream.lines.length === 0 ? (
                      <span className="text-surface-600">
                        {activeTaskStream.status === "queued" ? "(queued… waiting for runner output)" : "(waiting for output…)"}
                      </span>
                    ) : (
                      activeTaskStream.lines.slice(-120).join("\n")
                    )}
                  </pre>
                </div>
              ) : (
                <div className="rounded border border-dashed border-surface-800 bg-surface-950 px-3 py-4 text-[11px] leading-5 text-surface-600">
                  Send a vibing prompt here to stream the runner output beside the phone preview.
                </div>
              )}
            </div>
            <div className="border-t border-surface-800 bg-surface-900/60 p-2">
              <div className="flex items-end gap-2">
                <textarea
                  value={composer}
                  onChange={(e) => setComposer(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && !e.shiftKey) {
                      e.preventDefault();
                      void handleSendPrompt();
                    }
                  }}
                  placeholder={
                    devStatus?.running
                      ? `Vibe on ${projectLabel} — Enter to send`
                      : "Vibe: describe a change and press Enter"
                  }
                  rows={1}
                  className="max-h-24 flex-1 resize-none rounded border border-surface-800 bg-surface-950 px-2 py-1.5 text-[12px] text-surface-100 placeholder-surface-600 outline-none focus:border-surface-600"
                  style={{ minHeight: "32px" }}
                />
                <button
                  type="button"
                  onClick={() => void handleSendPrompt()}
                  disabled={!composer.trim() || sending}
                  className="shrink-0 rounded border border-emerald-500/40 bg-emerald-500/10 px-3 py-1.5 text-[11px] font-medium text-emerald-300 hover:bg-emerald-500/20 disabled:opacity-30"
                >
                  {sending ? "…" : "Send"}
                </button>
              </div>
              {sendStatus ? (
                <div
                  className={`mt-1 px-1 text-[10px] ${
                    sendStatus.startsWith("✓") ? "text-emerald-400" : "text-red-400"
                  }`}
                >
                  {sendStatus}
                </div>
              ) : null}
            </div>
          </div>
        </div>

        <div
          ref={stageRef}
          className="relative min-h-[420px] xl:min-h-0 flex items-center justify-center overflow-hidden border-b border-surface-800 bg-surface-950 xl:border-b-0"
        >
          {(recovering || recoveryLog.length > 0 || devProgress.active) ? (
            <div className="pointer-events-auto absolute top-3 right-3 z-10 w-72 max-w-[40%] rounded border border-amber-500/30 bg-surface-950/95 shadow-lg backdrop-blur">
              <div className="flex items-center justify-between px-2 py-1 text-[10px] uppercase tracking-widest text-amber-400 border-b border-amber-500/20">
                <span>
                  {recovering ? "Recovery · running" : recoveryLog.length > 0 ? "Recovery · last run" : "Dev server · starting"}
                  {/* Tag the running web bundle version so the user
                       can tell at a glance whether they're on the
                       latest deploy or stuck on a stale Cloudflare
                       cache. */}
                  <span className="ml-2 normal-case tracking-normal text-surface-500">
                    web v{__YAVER_WEB_VERSION__}
                  </span>
                </span>
                {!recovering && recoveryLog.length > 0 ? (
                  <button
                    onClick={() => setRecoveryLog([])}
                    className="text-surface-600 hover:text-surface-400"
                    title="Clear recovery log"
                  >
                    clear
                  </button>
                ) : null}
              </div>
              {devProgress.active ? (
                <div className="px-2 pt-2">
                  <div className="h-1 w-full overflow-hidden rounded bg-emerald-500/15">
                    <div
                      className="h-full rounded bg-emerald-400 transition-[width] duration-300 ease-out"
                      style={{ width: `${Math.max(devProgress.pct * 100, 5)}%` }}
                    />
                  </div>
                  {devProgress.stage ? (
                    <p className="mt-1 truncate font-mono text-[10px] text-emerald-200/80" title={devProgress.stage}>
                      {devProgress.stage}
                    </p>
                  ) : null}
                </div>
              ) : null}
              {(recovering || recoveryLog.length > 0) ? (
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-all px-2 py-1 font-mono text-[10px] leading-4 text-amber-200/80">
                  {recoveryLog.length === 0 ? (
                    <span className="text-surface-600">(starting…)</span>
                  ) : (
                    recoveryLog.join("\n")
                  )}
                </pre>
              ) : null}
            </div>
          ) : null}
          {skin.plain ? (
            <div style={innerDim}>{innerContent}</div>
          ) : (
            <div
              style={{
                width: frame.width,
                height: frame.height,
                transform: `scale(${scale})`,
                transformOrigin: "center center",
              }}
              className="relative"
            >
              <div
                style={{
                  width: frame.width,
                  height: frame.height,
                  borderRadius: skin.radius + skin.bezel,
                  background:
                    "linear-gradient(140deg, #1a1a1a 0%, #0d0d0d 50%, #1a1a1a 100%)",
                  boxShadow:
                    "inset 0 0 0 1px rgba(255,255,255,0.06), 0 30px 60px -20px rgba(0,0,0,0.7), 0 10px 30px -10px rgba(0,0,0,0.5)",
                  padding: skin.bezel,
                }}
              >
                <div
                  style={{
                    width: (frame as { innerWidth: number }).innerWidth,
                    height: (frame as { innerHeight: number }).innerHeight,
                    borderRadius: skin.radius,
                    overflow: "hidden",
                    position: "relative",
                    background: "#000",
                  }}
                >
                  {innerContent}
                  {skin.notch && orientation === "portrait" ? (
                    <div
                      style={{
                        position: "absolute",
                        top: 6,
                        left: "50%",
                        transform: "translateX(-50%)",
                        width: skin.notch.width,
                        height: skin.notch.height,
                        borderRadius: skin.notch.height,
                        background: "#000",
                        zIndex: 2,
                        pointerEvents: "none",
                      }}
                    />
                  ) : null}
                  {skin.punchHole && orientation === "portrait" ? (
                    <div
                      style={{
                        position: "absolute",
                        top: skin.punchHole.offsetTop,
                        left: "50%",
                        transform: "translateX(-50%)",
                        width: skin.punchHole.size,
                        height: skin.punchHole.size,
                        borderRadius: skin.punchHole.size,
                        background: "#000",
                        zIndex: 2,
                        pointerEvents: "none",
                      }}
                    />
                  ) : null}
                  {skin.notch && orientation === "portrait" ? (
                    <div
                      style={{
                        position: "absolute",
                        bottom: 8,
                        left: "50%",
                        transform: "translateX(-50%)",
                        width: 134,
                        height: 4,
                        borderRadius: 2,
                        background: "rgba(255,255,255,0.5)",
                        zIndex: 2,
                        pointerEvents: "none",
                        mixBlendMode: "difference",
                      }}
                    />
                  ) : null}
                </div>
              </div>
            </div>
          )}
        </div>

        <div className="min-h-0 border-surface-800 bg-surface-950/70 xl:border-l">
          <div className="flex h-full min-h-0 flex-col">
            <div className="flex items-center justify-between border-b border-surface-800 px-3 py-2">
              <div>
                <div className="text-[10px] uppercase tracking-widest text-surface-500">Console</div>
                <div className="mt-1 text-[11px] text-surface-600">
                  Metro / Expo / dev server output
                </div>
              </div>
              <span className="text-[10px] text-surface-600">{logLines.length} lines</span>
            </div>
            <ConsoleStatusHeader
              connState={connState}
              sseState={sseState}
              sseError={sseError}
              sseUrl={sseUrl}
              sseAttempts={sseAttempts}
              totalEvents={totalEvents}
              lastEventAt={lastEventAt}
              devStatus={devStatus}
              lastBeat={lastBeat}
              connectionHealth={connectionHealth}
              topicProgress={topicProgress}
              latestSnapshot={latestSnapshot}
            />
            <div className="flex-1 min-h-0 overflow-auto whitespace-pre-wrap break-all px-3 py-2 font-mono text-[10px] leading-4">
              {logLines.length === 0 ? (
                <span className="text-surface-600">{consoleEmptyHint(connState, sseState, sseError, devStatus)}</span>
              ) : (
                logLines.slice(-200).map((line, i) => (
                  <div key={i} className={consoleLineClass(line)}>{line}</div>
                ))
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function EmptyPhoneState({
  projects,
  projectsAll,
  preferredProjectPath,
  onStart,
  startingPath,
  startError,
}: {
  projects: Project[];
  projectsAll: Project[] | null;
  preferredProjectPath?: string | null;
  onStart: (p: Project) => void;
  startingPath: string | null;
  startError: string | null;
}) {
  const orderedProjects = useMemo(() => {
    if (!preferredProjectPath) return projects;
    const preferred = projects.find((project) => project.path === preferredProjectPath);
    if (!preferred) return projects;
    return [preferred, ...projects.filter((project) => project.path !== preferredProjectPath)];
  }, [preferredProjectPath, projects]);

  // Three distinct empty states. The previous implementation
  // collapsed all of them into one generic "No projects" message,
  // which was misleading when the agent was actually in bootstrap
  // mode (projects request 401'd) or still scanning. Each state
  // gets its own card so the user knows exactly what to do next.
  const isScanning = projectsAll === null;
  const hasNoProjects = !isScanning && projects.length === 0;

  return (
    <div className="w-full h-full flex flex-col gap-2 bg-surface-950 p-4 overflow-auto">
      <div className="rounded-lg border border-surface-800/70 bg-surface-900/40 p-4">
        <div className="flex items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-surface-800/60 text-base">
            📱
          </div>
          <div className="min-w-0">
            <div className="text-sm font-semibold text-surface-100">Mobile App preview</div>
            <div className="text-[11px] text-surface-500 mt-0.5">
              Pick a project below — Yaver starts Metro/Expo and renders it in the phone frame on the right with hot-reload + heartbeat.
            </div>
          </div>
        </div>
      </div>

      {startError ? (
        <div className="rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2 text-[11px] text-red-300">
          {startError}
        </div>
      ) : null}

      {isScanning ? (
        <div className="rounded-md border border-surface-800/70 bg-surface-900/40 px-3 py-3 text-center">
          <div className="inline-flex items-center gap-2 text-[11px] text-surface-400">
            <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-amber-400" />
            Scanning the agent's workspace for RN / Expo / Flutter / Vite projects…
          </div>
        </div>
      ) : hasNoProjects ? (
        <div className="rounded-md border border-amber-500/25 bg-amber-500/5 px-3 py-3 text-[11px] text-amber-200/90 leading-relaxed">
          <div className="font-semibold text-amber-200">No projects visible yet.</div>
          <div className="mt-1 text-amber-200/70">
            The agent didn't return any RN / Expo / Flutter / Next.js / Vite project. Common reasons:
          </div>
          <ul className="mt-1.5 list-disc pl-4 text-amber-200/80 space-y-0.5">
            <li>The device isn't paired yet (sidebar may show "needs auth"). Pair it first.</li>
            <li>The agent's <code className="rounded bg-surface-900 px-1 text-amber-100">workDir</code> doesn't include a recognised mobile/web project.</li>
            <li>Start one manually with <code className="rounded bg-surface-900 px-1 text-amber-100">yaver dev start</code> from a shell on the device.</li>
          </ul>
        </div>
      ) : (
        <>
          <div className="flex items-center justify-between text-[10px] uppercase tracking-wider text-surface-500 mt-1 mb-0.5">
            <span>Pick a project · {projects.length}</span>
            {preferredProjectPath ? <span className="text-emerald-400/70">★ default</span> : null}
          </div>
          <div className="flex flex-col gap-1.5">
            {orderedProjects.slice(0, 6).map((p) => (
              <button
                key={p.path}
                onClick={() => onStart(p)}
                disabled={startingPath === p.path}
                className={`flex items-center gap-2.5 rounded-md border px-2.5 py-2 text-left transition-all ${
                  startingPath === p.path
                    ? "cursor-wait border-amber-500/40 bg-amber-500/10 text-amber-200"
                    : p.path === preferredProjectPath
                      ? "border-emerald-500/35 bg-emerald-500/10 hover:border-emerald-400/50"
                      : "border-surface-800 bg-surface-900/60 hover:border-emerald-500/40 hover:bg-emerald-500/5"
                }`}
              >
                <span className="text-base shrink-0">{frameworkIcon(p.framework)}</span>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[12px] font-medium text-surface-200">{p.name}</div>
                  <div className="truncate text-[10px] text-surface-600 font-mono">{p.path}</div>
                </div>
                <span className="shrink-0 rounded border border-surface-700 bg-surface-950/60 px-1.5 py-0.5 text-[9px] uppercase tracking-wider text-surface-300">
                  {startingPath === p.path ? "starting…" : "▶ start"}
                </span>
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  );
}

// ConsoleStatusHeader renders the diagnostic strip between the
// "Console / Metro / Expo / dev server output" title and the log
// pre. It always shows what stage the SSE is in so an empty pane
// is never silent — the user immediately knows whether it's a
// connection problem, an auth problem, an empty replay buffer,
// or just Metro being idle.
function ConsoleStatusHeader({
  connState,
  sseState,
  sseError,
  sseUrl,
  sseAttempts,
  totalEvents,
  lastEventAt,
  devStatus,
  lastBeat,
  connectionHealth,
  topicProgress,
  latestSnapshot,
}: {
  connState: string;
  sseState: "idle" | "opening" | "open" | "closed" | "error";
  sseError: string | null;
  sseUrl: string | null;
  sseAttempts: number;
  totalEvents: number;
  lastEventAt: number | null;
  devStatus: { running: boolean; framework?: string; workDir?: string; port?: number; webPort?: number; targetDeviceName?: string } | null;
  lastBeat: {
    at: number;
    pid: number;
    pidAlive: boolean;
    uptimeSec: number;
    port: number;
    framework: string;
    idleSec: number;
    beatNumber: number;
  } | null;
  connectionHealth: "live" | "idle" | "syncing" | "reconnecting" | "lost";
  topicProgress: Record<string, {
    phase: string;
    pct: number;
    done: number;
    total: number;
    unit: string;
    currentFile: string;
    etaMs: number;
    src: "exact" | "heuristic" | "unknown";
    updatedAt: number;
  }>;
  latestSnapshot: {
    generatedAt: number;
    running: boolean;
    framework: string;
    port: number;
    webPort: number;
    workDir: string;
    uptimeSec: number;
    idleSec: number;
    phases: Record<string, string>;
    recentLogs: string[];
  } | null;
}) {
  const dot = (color: string) => (
    <span className="inline-block h-1.5 w-1.5 rounded-full" style={{ background: color }} aria-hidden />
  );
  const connDot = connState === "connected" ? dot("#34d399") : connState === "connecting" || connState === "reconnecting" ? dot("#fbbf24") : dot("#ef4444");
  const sseColor = sseState === "open" ? "#34d399" : sseState === "opening" ? "#fbbf24" : sseState === "closed" ? "#94a3b8" : sseState === "error" ? "#ef4444" : "#64748b";
  const sseDot = dot(sseColor);
  const lastEventLabel = (() => {
    if (!lastEventAt) return "no events yet";
    const ageMs = Date.now() - lastEventAt;
    if (ageMs < 1000) return "just now";
    if (ageMs < 60_000) return `${Math.floor(ageMs / 1000)}s ago`;
    if (ageMs < 3_600_000) return `${Math.floor(ageMs / 60_000)}m ago`;
    return `${Math.floor(ageMs / 3_600_000)}h ago`;
  })();

  // Stabilise the layout: every counter that changes per-second
  // (events, last X ago, beat number, uptime, idle) gets a fixed
  // min-width + tabular-nums so digits don't shift sibling tokens
  // when their width changes (`9s` → `10s`, `45s` → `50s`, etc).
  // Without this the whole CONSOLE strip jitters every render of
  // the 1 Hz rerenderTick — the user reported it as
  // "annoyingly up and down."
  const numStyle = { fontVariantNumeric: "tabular-nums" as const };
  const fixedWidth = (ch: number) => ({
    ...numStyle,
    minWidth: `${ch}ch`,
    display: "inline-block" as const,
    textAlign: "left" as const,
  });
  return (
    <div className="border-b border-surface-800/50 bg-surface-950/50 px-3 py-1.5 text-[10px] text-surface-500" style={numStyle}>
      <div className="flex items-center gap-3">
        <span className="flex items-center gap-1.5">
          {connDot}
          <span>agent: {connState}</span>
        </span>
        <span className="flex items-center gap-1.5">
          {sseDot}
          <span>sse: {sseState}{sseAttempts > 1 ? ` (#${sseAttempts})` : ""}</span>
        </span>
        <span className="text-surface-600">
          events: <span style={fixedWidth(5)}>{totalEvents}</span>
        </span>
        <span className="text-surface-600">
          last: <span style={fixedWidth(8)}>{lastEventLabel}</span>
        </span>
      </div>
      {(sseError || (devStatus && devStatus.running)) && (
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5">
          {devStatus?.running && (
            <span className="text-surface-600">
              dev: {devStatus.framework || "?"} :{devStatus.port ?? "?"}
              {devStatus.webPort ? ` (web :${devStatus.webPort})` : ""}
            </span>
          )}
          {sseError && (
            <span className="text-red-400/90">err: {sseError}</span>
          )}
        </div>
      )}
      {/* Heartbeat row — agent-driven liveness. Only visible while
          we have a recent beat (dev server running). Pulsing dot
          + "uptime / pid alive / idle" makes the system feel real
          even when Metro is silent between bundle requests. */}
      {lastBeat && (Date.now() - lastBeat.at < 30_000) && (
        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5">
          <span className="flex items-center gap-1.5">
            <span className="relative flex h-1.5 w-1.5">
              <span className="absolute inset-0 animate-ping rounded-full bg-emerald-400/60" />
              <span className="relative h-1.5 w-1.5 rounded-full bg-emerald-400" />
            </span>
            <span className="text-emerald-300">agent live</span>
          </span>
          <span className="text-surface-500">
            beat <span style={fixedWidth(5)}>#{lastBeat.beatNumber}</span>{" "}
            <span className="text-surface-700">
              (<span style={fixedWidth(4)}>{Math.max(0, Math.floor((Date.now() - lastBeat.at) / 1000))}s</span> ago)
            </span>
          </span>
          {lastBeat.uptimeSec > 0 && (
            <span className="text-surface-500">
              uptime: <span className="text-surface-300" style={fixedWidth(7)}>{formatHeartbeatUptime(lastBeat.uptimeSec)}</span>
            </span>
          )}
          {lastBeat.pid > 0 && (
            <span className="text-surface-500">
              pid <span style={fixedWidth(7)}>{lastBeat.pid}</span> <span className={lastBeat.pidAlive ? "text-emerald-400" : "text-red-400"}>{lastBeat.pidAlive ? "✓" : "✗"}</span>
            </span>
          )}
          {lastBeat.idleSec > 0 && (
            <span className="text-surface-700">
              idle <span style={fixedWidth(5)}>{lastBeat.idleSec}s</span>
            </span>
          )}
        </div>
      )}
      {/* Per-topic progress bars — Yaver Protocol v1. The agent
          parses Metro / Expo / hermesc stdout and emits real
          percentages with currentFile + ETA. We render one slim bar
          per active topic so the user sees "Web bundling 42% —
          Route.js · 18s left" instead of a fake wallclock spinner.

          Phase rendering rules — be honest:
          - "listening" + no real progress → calm "idle" line, NO bar
            (Metro is just waiting; no compile is happening; no
            bar should pretend otherwise).
          - "queued" / "preparing" / "*_bundling" + no exact pct →
            indeterminate slide bar (something IS happening, we
            just don't have a number yet).
          - any phase + exact pct → real progress bar with
            modules done/total + currentFile + ETA.
       */}
      {Object.entries(topicProgress)
        .filter(([, prog]) => prog.phase && prog.phase !== "ready" && prog.phase !== "stopped" && prog.phase !== "idle" && prog.phase !== "")
        .map(([topic, prog]) => {
          const label = topicLabel(topic);
          const phaseLabel = phaseLabelFor(prog.phase);
          const isExact = prog.src === "exact" && prog.total > 0;
          const pctDisplay = Math.max(0, Math.min(100, Math.round(prog.pct)));
          const etaSec = prog.etaMs > 0 ? Math.round(prog.etaMs / 1000) : 0;
          // "listening" without a real progress event is just idle —
          // no compile is happening. Show a calm message and skip
          // the bar entirely so the user doesn't see a fake spinner
          // for a process that's literally just sitting there.
          const isListeningIdle = prog.phase === "listening" && !isExact;
          if (isListeningIdle) {
            return (
              <div key={topic} className="mt-1 flex items-center gap-2 text-[10px]">
                <span className="inline-block h-1.5 w-1.5 rounded-full bg-emerald-400/70" />
                <span className="text-surface-300">{label}</span>
                <span className="text-surface-500">listening · idle</span>
                <span className="text-surface-700 italic">waiting for bundle request</span>
              </div>
            );
          }
          return (
            <div key={topic} className="mt-1 flex flex-col gap-0.5">
              <div className="flex items-center gap-2 text-[10px]">
                <span className="text-surface-300">{label}</span>
                <span className="text-surface-600">{phaseLabel}</span>
                {isExact ? (
                  <span className="font-mono text-emerald-300" style={fixedWidth(5)}>{pctDisplay}%</span>
                ) : (
                  <span className="text-surface-600 italic">working…</span>
                )}
                {prog.total > 0 && (
                  <span className="text-surface-700" style={fixedWidth(14)}>
                    {prog.done}/{prog.total} {prog.unit}
                  </span>
                )}
                {etaSec > 0 && etaSec < 600 && (
                  <span className="text-surface-700">~{etaSec}s left</span>
                )}
              </div>
              <div className="h-1 w-full overflow-hidden rounded-full bg-surface-800">
                {isExact ? (
                  <div
                    className="h-full bg-gradient-to-r from-sky-500 to-emerald-400 transition-[width] duration-300"
                    style={{ width: `${Math.max(2, pctDisplay)}%` }}
                  />
                ) : (
                  <div className="relative h-full w-full overflow-hidden">
                    <div className="absolute inset-y-0 left-0 h-full w-1/4 animate-[slide_1.6s_ease-in-out_infinite] bg-gradient-to-r from-transparent via-sky-400 to-transparent" />
                  </div>
                )}
              </div>
              {prog.currentFile && (
                <div className="truncate text-[9px] text-surface-700 font-mono" title={prog.currentFile}>
                  {prog.currentFile.split("/").slice(-3).join("/")}
                </div>
              )}
            </div>
          );
        })}
      {/* Connection-health chip — decoupled from compile state.
          ALWAYS visible so user sees "we're listening". Per the
          'never feel disconnected' contract: agent guarantees a
          snapshot every 5s, so > 6s without a byte = real transport
          issue. */}
      <div className="mt-1 flex items-center gap-2 text-[10px]">
        {(() => {
          const map = {
            live: { dot: "#34d399", label: "channel: live", animate: false, color: "text-emerald-300" },
            idle: { dot: "#64748b", label: "channel: idle (no dev server)", animate: false, color: "text-surface-500" },
            syncing: { dot: "#fbbf24", label: "channel: syncing…", animate: true, color: "text-amber-300" },
            reconnecting: { dot: "#fb923c", label: "channel: reconnecting…", animate: true, color: "text-orange-300" },
            lost: { dot: "#ef4444", label: "channel: lost — Reconnect & Fix", animate: false, color: "text-red-300" },
          } as const;
          const m = map[connectionHealth];
          return (
            <>
              <span className={`relative inline-flex h-1.5 w-1.5 items-center justify-center`}>
                {m.animate && (
                  <span className="absolute inset-0 animate-ping rounded-full opacity-50" style={{ background: m.dot }} />
                )}
                <span className="relative inline-block h-1.5 w-1.5 rounded-full" style={{ background: m.dot }} />
              </span>
              <span className={m.color}>{m.label}</span>
            </>
          );
        })()}
      </div>
      {sseUrl && (
        <div className="mt-0.5 truncate font-mono text-[9px] text-surface-700" title={sseUrl}>
          {sseUrl}
        </div>
      )}
    </div>
  );
}

// topicLabel — human-readable name for a Yaver Protocol topic.
function topicLabel(topic: string): string {
  switch (topic) {
    case "dev/start": return "Dev server";
    case "webview/build": return "Expo Web";
    case "hermes/compile": return "Hermes";
    case "bundle/push": return "Bundle push";
    default: return topic;
  }
}

// phaseLabelFor — human-readable label for a phase value.
function phaseLabelFor(phase: string): string {
  switch (phase) {
    case "queued": return "queued";
    case "preparing": return "preparing";
    case "installing_deps": return "installing deps";
    case "starting": return "starting";
    case "metro_bundling": return "metro bundling";
    case "web_bundling": return "web bundling";
    case "hermesc_compiling": return "hermes compiling";
    case "validating": return "validating";
    case "listening": return "listening";
    case "ready": return "ready";
    case "idle": return "idle";
    case "stopped": return "stopped";
    case "error": return "error";
    default: return phase;
  }
}

function formatHeartbeatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  if (m < 60) return `${m}m ${s}s`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  return `${h}h ${rm}m`;
}

// consoleEmptyHint replaces the bland "(waiting for output…)" with
// consoleLineClass maps Metro / Expo / agent log lines to a Tailwind
// color class so the CONSOLE pane reads at a glance instead of as a
// wall of muted-grey text. Heuristics — no shared schema with the
// log emitters, just text matches:
//   - "error" / "fail" / "✗"        → red
//   - "warn"                         → amber
//   - "ready" / "✓" / "started"     → green
//   - "[error]" prefix the SSE adds  → red
//   - "[super-host]" / "[hermesc]"  → indigo (Yaver-injected pipeline)
//   - everything else (Metro logs)   → surface-300 (default light)
function consoleLineClass(line: string): string {
  const lower = line.toLowerCase();
  if (line.startsWith("[error]") || /\b(error|failed|fatal|✗)\b/i.test(lower)) {
    return "text-red-300";
  }
  if (/\bwarn(ing)?\b/i.test(lower)) {
    return "text-amber-300";
  }
  if (/\b(ready|✓|listening on|accepting|reload|started)\b/i.test(lower)) {
    return "text-emerald-300";
  }
  if (line.startsWith("[super-host") || line.includes("hermesc") || line.startsWith("[dev:")) {
    return "text-indigo-300";
  }
  if (line.startsWith(":keep-alive")) {
    return "text-surface-700"; // SSE keepalives — barely visible
  }
  return "text-surface-300";
}

// a state-aware hint so the user knows whether to wait, click
// Reconnect, or check the dev server.
function consoleEmptyHint(
  connState: string,
  sseState: "idle" | "opening" | "open" | "closed" | "error",
  sseError: string | null,
  devStatus: { running: boolean; framework?: string } | null,
): string {
  if (connState !== "connected") {
    return `(agent ${connState} — events stream paused; reconnect when the device is back online)`;
  }
  if (sseState === "idle") {
    return "(waiting for agent to expose dev events endpoint…)";
  }
  if (sseState === "opening") {
    return "(opening event stream…)";
  }
  if (sseState === "error") {
    return `(stream error${sseError ? `: ${sseError}` : ""} — try Reconnect & Fix)`;
  }
  if (sseState === "closed") {
    return "(stream closed by server — usually means the dev server stopped; click Start again or pick a project)";
  }
  // sseState === "open"
  if (!devStatus?.running) {
    return "(stream open — start a dev server to populate this pane)";
  }
  return `(stream open, ${devStatus.framework || "dev server"} running but quiet — Metro only logs at boot or on bundle requests, so this is normal between actions)`;
}
