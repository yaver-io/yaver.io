"use client";

import { useState, useEffect, useRef, useMemo, useCallback } from "react";
import { agentClient, type MobileWorkerPreviewSession } from "@/lib/agent-client";

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
  const [agentReady, setAgentReady] = useState(() => Boolean(agentClient.devEventsUrl));
  useEffect(() => {
    return agentClient.on("connectionState", (state) => {
      setAgentReady(state === "connected" && Boolean(agentClient.devEventsUrl));
    });
  }, []);
  useEffect(() => {
    const eventsUrl = agentClient.devEventsUrl;
    if (!eventsUrl) return;
    const controller = new AbortController();
    (async () => {
      try {
        const res = await fetch(eventsUrl, {
          headers: agentClient.getAuthHeaders(),
          signal: controller.signal,
        });
        const reader = res.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder();
        let buffer = "";
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
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop() || "";
          for (const line of lines) {
            if (!line.startsWith("data: ")) continue;
            try {
              const ev = JSON.parse(line.slice(6));
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
              }
            } catch {}
          }
        }
      } catch {}
    })();
    return () => controller.abort();
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

        if (savedFramework) {
          stage(0.85, `restarting dev server (${savedFramework})…`);
          appendRecovery(`→ restarting dev server (${savedFramework})…`);
          try {
            await agentClient.startDevServer({
              framework: savedFramework,
              workDir: savedWorkDir,
              targetDeviceId: selectedPreviewTarget?.id,
              targetDeviceName: selectedPreviewTarget?.name,
            });
            appendRecovery("✓ dev server restarted");
          } catch (e: any) {
            appendRecovery(`✗ restart failed: ${e?.message || e}`);
          }
        }
      } else {
        appendRecovery("  (no dev server was running — skipping restart)");
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
        await agentClient.reloadDevServer();
      } catch {
        // Browser preview already got a hard refresh above.
      }
      return;
    }
    await agentClient.reloadDevServer();
  }, [devStatus?.framework]);

  const handleStop = useCallback(async () => {
    await agentClient.stopDevServer();
    setDevStatus(null);
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
      setLogLines([]);
      try {
        await agentClient.startDevServer({
          framework: likelyFramework(project),
          workDir: project.path,
          platform: previewPlatformForProject(project),
          targetDeviceId: selectedPreviewTarget?.id,
          targetDeviceName: selectedPreviewTarget?.name,
        });
        // status poll will pick up running=true shortly
      } catch (e: any) {
        setStartError(e?.message || "Failed to start dev server");
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
        Hot reload can&apos;t reach the agent until you sign back in on the host
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
        This tab is the mobile Hot Reload surface. Metro is waiting for the Yaver app
        on your phone to request a Hermes bundle. Open{" "}
        <span className="font-mono text-surface-300">
          {devStatus?.workDir?.split("/").slice(-1)[0] || "this project"}
        </span>{" "}
        in the Yaver mobile app to preview here.
      </p>
      <p className="max-w-[280px] text-[10px] leading-5 text-surface-600">
        Or switch to the <span className="text-surface-400">Web Reload</span> tab to
        render a browser preview via <span className="font-mono">expo --web</span>.
      </p>
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
              title={isWebPreviewFramework(devStatus?.framework) ? "Refresh preview" : "Hot reload"}
            >
              ↻ Reload
            </button>
            <button
              onClick={handleStop}
              className="rounded border border-red-500/40 bg-red-500/10 px-2 py-0.5 text-[10px] text-red-200 hover:bg-red-500/20"
              title="Stop the dev server and return to the project picker"
            >
              ■ Stop & switch
            </button>
          </>
        ) : null}
      </div>

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
            <pre className="flex-1 min-h-0 overflow-auto whitespace-pre-wrap break-all px-3 py-2 font-mono text-[10px] leading-4 text-surface-400">
              {logLines.length === 0 ? (
                <span className="text-surface-600">(waiting for output…)</span>
              ) : (
                logLines.slice(-200).join("\n")
              )}
            </pre>
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

  return (
    <div className="w-full h-full flex flex-col gap-3 bg-surface-950 text-surface-400 p-4 overflow-auto">
      <div className="text-center mt-2">
        <div className="text-3xl opacity-30">📱</div>
        <div className="mt-1 text-xs font-medium text-surface-300">Hot reload</div>
        <div className="text-[10px] text-surface-600">
          Start a dev server to preview it live in this phone frame.
        </div>
      </div>
      {startError ? (
        <div className="rounded border border-red-500/30 bg-red-500/5 px-2 py-1 text-[10px] text-red-300">
          {startError}
        </div>
      ) : null}
      {projectsAll === null ? (
        <div className="text-center text-[10px] text-surface-600">Scanning projects…</div>
      ) : projects.length === 0 ? (
        <div className="text-center text-[10px] text-surface-600 px-4">
          No RN / Expo / Flutter / Next.js / Vite projects detected on this machine.
          <br />
          Start one manually from a shell with <code className="rounded bg-surface-900 px-1">yaver dev start</code>.
        </div>
      ) : (
        <div className="flex flex-col gap-1.5">
          {orderedProjects.slice(0, 6).map((p) => (
            <button
              key={p.path}
              onClick={() => onStart(p)}
              disabled={startingPath === p.path}
              className={`flex items-center gap-2 rounded border px-2 py-1.5 text-left transition-colors ${
                startingPath === p.path
                  ? "cursor-wait border-amber-500/30 bg-amber-500/5 text-amber-200"
                  : p.path === preferredProjectPath
                    ? "border-emerald-500/30 bg-emerald-500/5"
                    : "border-surface-800 bg-surface-900/60 hover:border-emerald-500/30 hover:bg-emerald-500/5"
              }`}
            >
              <span className="text-sm">{frameworkIcon(p.framework)}</span>
              <div className="min-w-0 flex-1">
                <div className="truncate text-[11px] font-medium text-surface-200">{p.name}</div>
                <div className="truncate text-[9px] text-surface-600 font-mono">{p.path}</div>
              </div>
              <span className="shrink-0 text-[9px] uppercase tracking-wider text-surface-500">
                {startingPath === p.path ? "starting…" : "start"}
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
