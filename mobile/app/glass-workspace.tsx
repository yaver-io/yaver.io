// Glass-workspace — i3-style tiling for the Yaver mobile app.
//
// Sibling to glass-terminal.tsx (the single-pane deep-focus terminal).
// Use this screen when you want to SEE multiple yaver outputs at once
// from XREAL One Pro AR glasses + Bluetooth keyboard — e.g. tmux on a
// remote dev box ALONGSIDE the live Next.js preview ALONGSIDE the most
// recent vibe clip ALONGSIDE an agent chat.
//
// The screen picks a default 3-pane layout from the agent's project
// kind (Mobile / Web / Backend / Generic) so the user lands in something
// useful without configuring anything. Long-press the title to swap
// devices; the layout re-loads against the new device's project kind.
//
// V1 panes:
//   Mobile project   →  shell · agent · clips   (1×3 column)
//   Web project      →  shell · web-preview · agent   (1×3 column)
//   Backend project  →  shell · logs · agent   (1×3 column)
//   Generic project  →  shell · agent   (2×1 side-by-side)
//
// Layout is intentionally column-based for portrait phones; AR glasses
// see the iPhone's portrait framebuffer too (iOS mirrors screen 0 to
// XREAL). Phase-2 will add a landscape grid for users on Beam Pro.
//
// **What this is NOT**: a duplicate of glass-terminal's PTY UX. The
// shell pane here is a stripped-down read-loop that shows live tmux
// output; type-into-tmux still happens in the full-screen terminal
// (glass-terminal). This screen is "see everything at once"; that one
// is "deep work in one place".

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Image,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { WebView } from "react-native-webview";
import { useRouter } from "expo-router";
import { AppBackButton } from "../src/components/AppBackButton";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useDevice, type Device } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import {
  fetchProjectKind,
  invalidateProjectKindCache,
  type ProjectKindResult,
} from "../src/lib/projectKind";
import { callMcpDirect } from "../src/lib/yaverMcpDirect";
import {
  runYaverAgent,
  type YaverAgentProgressEvent,
} from "../src/lib/yaverAgentRunner";
import type { YaverAgentToolContext } from "../src/lib/yaverAgentTools";
import {
  YaverWorkspace,
  type WorkspacePaneDef,
} from "../src/components/workspace/YaverWorkspace";

const PAL = {
  bg: "#000000",
  fg: "#e5e7eb",
  muted: "#9ca3af",
  border: "#1f2937",
  chip: "#111827",
  accent: "#a78bfa",
  tool: "#34d399",
  err: "#f87171",
};

export default function GlassWorkspaceScreen(): React.ReactElement {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { devices, primaryDeviceId } = useDevice();
  const [projectKind, setProjectKind] = useState<ProjectKindResult | null>(null);
  const [reloadNonce, setReloadNonce] = useState(0);

  // Re-classify whenever the active device changes. invalidate first so
  // the new device's kind isn't masked by the previous device's cache.
  useEffect(() => {
    let cancelled = false;
    const ac = new AbortController();
    invalidateProjectKindCache();
    fetchProjectKind({ signal: ac.signal })
      .then((res) => { if (!cancelled) setProjectKind(res); })
      .catch(() => { /* device switched again */ });
    return () => { cancelled = true; ac.abort(); };
  }, [primaryDeviceId]);

  const currentDevice = devices.find((d: Device) => d.id === primaryDeviceId);
  const deviceLabel = currentDevice ? (currentDevice.alias || currentDevice.name) : "no device";
  const kindLabel = projectKind?.kind ?? "loading";

  const panes = useMemo<WorkspacePaneDef[]>(
    () => panesForKind(projectKind, reloadNonce),
    [projectKind, reloadNonce],
  );

  const reloadAll = useCallback(() => setReloadNonce((n) => n + 1), []);

  return (
    <View style={[styles.root, { paddingTop: insets.top }]}>
      <View style={[styles.header, { borderBottomColor: PAL.border }]}>
        <AppBackButton variant="chevron" color={PAL.muted} onPress={() => router.back()} />
        <View style={{ flex: 1, alignItems: "center" }}>
          <Text style={[styles.headerTitle, { color: PAL.fg }]} numberOfLines={1}>
            workspace · {deviceLabel}
          </Text>
          <Text style={{
            color: PAL.muted,
            fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
            fontSize: 10,
            marginTop: 2,
          }}>
            kind: {kindLabel}
          </Text>
        </View>
        <Pressable
          hitSlop={8}
          onPress={reloadAll}
          style={[styles.modeChip, { backgroundColor: PAL.chip, borderColor: PAL.border }]}
        >
          <Text style={[styles.modeChipText, { color: PAL.accent }]}>⟳</Text>
        </Pressable>
        <Pressable
          hitSlop={8}
          onPress={() => router.replace("/glass-terminal")}
          style={[styles.modeChip, { backgroundColor: PAL.chip, borderColor: PAL.border, marginLeft: 6 }]}
        >
          <Text style={[styles.modeChipText, { color: PAL.accent }]}>→ terminal</Text>
        </Pressable>
      </View>
      <View style={{ flex: 1, paddingBottom: insets.bottom }}>
        <YaverWorkspace panes={panes} />
      </View>
    </View>
  );
}

// ── Pane composition per project kind ─────────────────────────────────────

function panesForKind(pk: ProjectKindResult | null, nonce: number): WorkspacePaneDef[] {
  const kind = pk?.kind ?? "generic";
  // Stable IDs so the keyboard layer's Cmd-N slot map stays consistent.
  const shellPane: WorkspacePaneDef = {
    id: "shell",
    title: "shell",
    kind: "terminal",
    render: ({ focused }) => <ShellPane focused={focused} nonce={nonce} />,
  };
  const agentPane: WorkspacePaneDef = {
    id: "agent",
    title: "agent",
    kind: "agent",
    render: ({ focused }) => <AgentChatPane focused={focused} nonce={nonce} />,
  };
  const clipsPane: WorkspacePaneDef = {
    id: "clips",
    title: "clips",
    kind: "clip",
    render: () => <ClipListPane projectName={projectNameFromWorkDir(pk)} nonce={nonce} />,
  };
  const webPreviewPane: WorkspacePaneDef = {
    id: "preview",
    title: "preview",
    kind: "webview",
    render: () => <WebPreviewPane nonce={nonce} />,
  };
  const logsPane: WorkspacePaneDef = {
    id: "logs",
    title: "logs",
    kind: "text",
    render: () => <LogsPane nonce={nonce} />,
  };

  switch (kind) {
    case "mobile":  return [shellPane, agentPane, clipsPane];
    case "web":     return [shellPane, webPreviewPane, agentPane];
    case "backend": return [shellPane, logsPane, agentPane];
    default:        return [shellPane, agentPane];
  }
}

function projectNameFromWorkDir(pk: ProjectKindResult | null): string {
  if (!pk?.workDir) return "default";
  const parts = pk.workDir.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? "default";
}

// ── ShellPane: read-only tmux/PTY stream from /ws/terminal ────────────────
// Type-into-tmux happens in glass-terminal; this pane just shows what's
// happening so the user can keep an eye on a long-running command while
// they work in another tile.

function ShellPane(props: { focused: boolean; nonce: number }): React.ReactElement {
  const { focused, nonce } = props;
  const [lines, setLines] = useState<string[]>([]);
  const [connected, setConnected] = useState(false);
  const [input, setInput] = useState("");
  const wsRef = useRef<WebSocket | null>(null);
  const scrollRef = useRef<FlatList<string> | null>(null);
  const pending = useRef<string>("");
  const flush = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    let alive = true;
    let url: string;
    try { url = buildTerminalWsUrl(); }
    catch { setLines(["— no agent · pick a device —"]); return; }

    let ws: WebSocket;
    try {
      ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
    } catch { setLines(["— shell connect failed —"]); return; }

    wsRef.current = ws;
    ws.onopen = () => { if (alive) setConnected(true); };
    ws.onmessage = (e) => {
      if (!alive) return;
      const raw = typeof e.data === "string"
        ? e.data
        : new TextDecoder().decode(new Uint8Array(e.data as ArrayBuffer));
      pending.current += stripAnsi(raw);
      if (!flush.current) {
        flush.current = setTimeout(() => {
          flush.current = null;
          const chunk = pending.current;
          pending.current = "";
          if (!chunk) return;
          setLines((prev) => {
            const next = [...prev, ...chunk.split("\n").filter(Boolean)];
            return next.length > 400 ? next.slice(-300) : next;
          });
          requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: false }));
        }, 80);
      }
    };
    ws.onclose = () => { if (alive) { setConnected(false); } };
    return () => {
      alive = false;
      try { ws.close(); } catch { /* harmless */ }
      if (flush.current) clearTimeout(flush.current);
    };
  }, [nonce]);

  const send = useCallback(() => {
    const ws = wsRef.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(new TextEncoder().encode(input + "\n"));
    setInput("");
  }, [input]);

  return (
    <View style={{ flex: 1, backgroundColor: PAL.bg }}>
      <FlatList
        ref={scrollRef}
        data={lines}
        keyExtractor={(_, i) => String(i)}
        renderItem={({ item }) => (
          <Text style={{
            color: PAL.fg,
            fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
            fontSize: 11,
            lineHeight: 14,
            paddingHorizontal: 6,
          }} selectable>{item}</Text>
        )}
        contentContainerStyle={{ paddingVertical: 4 }}
      />
      {focused ? (
        <View style={[styles.paneInputRow, { borderTopColor: PAL.border }]}>
          <Text style={{ color: connected ? PAL.tool : PAL.muted, fontFamily: "Menlo", fontSize: 11, marginRight: 4 }}>
            {connected ? "$" : "○"}
          </Text>
          <TextInput
            value={input}
            onChangeText={setInput}
            onSubmitEditing={send}
            placeholder={connected ? "type & enter" : "connecting…"}
            placeholderTextColor={PAL.muted}
            autoCorrect={false}
            autoCapitalize="none"
            autoComplete="off"
            style={{
              flex: 1,
              color: PAL.fg,
              fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
              fontSize: 11,
              padding: 4,
            }}
            returnKeyType="send"
            blurOnSubmit={false}
          />
        </View>
      ) : null}
    </View>
  );
}

// ── AgentChatPane: runYaverAgent loop, abbreviated transcript ──────────────

function AgentChatPane(props: { focused: boolean; nonce: number }): React.ReactElement {
  const { focused, nonce } = props;
  const { devices, primaryDeviceId, selectDevice } = useDevice();
  const [transcript, setTranscript] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [input, setInput] = useState("");
  const ac = useRef<AbortController | null>(null);

  useEffect(() => {
    // On reload, clear transcript so the pane is a fresh canvas.
    setTranscript([]);
  }, [nonce]);

  const submit = useCallback(async () => {
    const prompt = input.trim();
    if (!prompt || busy) return;
    setInput("");
    setTranscript((t) => [...t, `▶ ${prompt}`]);
    setBusy(true);
    ac.current = new AbortController();
    try {
      const ctx: YaverAgentToolContext = {
        devices: () => devices,
        primaryDeviceId: () => primaryDeviceId,
        secondaryDeviceId: () => null,
        selectDevice: async (id) => {
          const d = devices.find((x) => x.id === id);
          if (d) await selectDevice(d);
        },
      };
      const res = await runYaverAgent({
        prompt,
        ctx,
        signal: ac.current.signal,
        onProgress: (ev: YaverAgentProgressEvent) => {
          if (ev.kind === "model_text" && ev.text.trim()) {
            setTranscript((t) => [...t, ev.text]);
          } else if (ev.kind === "tool_call") {
            setTranscript((t) => [...t, `⏺ ${ev.call.name}`]);
          }
        },
      });
      setTranscript((t) => [...t, `— ${res.steps} step(s) —`]);
    } catch (e: unknown) {
      setTranscript((t) => [...t, e instanceof Error ? `❌ ${e.message}` : "❌ failed"]);
    } finally {
      setBusy(false);
      ac.current = null;
    }
  }, [input, busy, devices, primaryDeviceId, selectDevice]);

  return (
    <View style={{ flex: 1, backgroundColor: PAL.bg }}>
      <FlatList
        data={transcript}
        keyExtractor={(_, i) => String(i)}
        renderItem={({ item }) => (
          <Text style={{
            color: item.startsWith("❌") ? PAL.err : item.startsWith("⏺") ? PAL.tool : PAL.fg,
            fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
            fontSize: 11,
            lineHeight: 14,
            paddingHorizontal: 6,
          }} selectable>{item}</Text>
        )}
        contentContainerStyle={{ paddingVertical: 4 }}
      />
      {focused ? (
        <View style={[styles.paneInputRow, { borderTopColor: PAL.border }]}>
          <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: 11, marginRight: 4 }}>▶</Text>
          <TextInput
            value={input}
            onChangeText={setInput}
            onSubmitEditing={submit}
            editable={!busy}
            placeholder={busy ? "running…" : "ask the agent"}
            placeholderTextColor={PAL.muted}
            autoCorrect={false}
            autoCapitalize="none"
            autoComplete="off"
            style={{
              flex: 1,
              color: PAL.fg,
              fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
              fontSize: 11,
              padding: 4,
            }}
            returnKeyType="send"
            blurOnSubmit={false}
          />
          {busy ? <ActivityIndicator size="small" color={PAL.accent} /> : null}
        </View>
      ) : null}
    </View>
  );
}

// ── WebPreviewPane: ops_web_preview start + WebView ───────────────────────

function WebPreviewPane(props: { nonce: number }): React.ReactElement {
  const { nonce } = props;
  const [iframeUrl, setIframeUrl] = useState<string | null>(null);
  const [status, setStatus] = useState<string>("idle");

  useEffect(() => {
    let alive = true;
    (async () => {
      setStatus("starting");
      // First check status — if a dev server is already running we
      // can skip the start call.
      type OpsEnv<T> = { ok?: boolean; code?: string; error?: string; initial?: T };
      type StatusInitial = { running?: boolean; bundleURL?: string };
      type StartInitial = { iframeUrl?: string; status?: { bundleURL?: string } };
      const st = await callMcpDirect<OpsEnv<StatusInitial>>(
        "ops",
        { verb: "web-preview", machine: "local", payload: { action: "status" } },
      );
      if (!alive) return;
      const stInner = st.result?.initial;
      if (st.ok && st.result?.ok && stInner?.running && stInner?.bundleURL) {
        setIframeUrl(stInner.bundleURL);
        setStatus("running");
        return;
      }
      const r = await callMcpDirect<OpsEnv<StartInitial>>(
        "ops",
        { verb: "web-preview", machine: "local", payload: { action: "start" } },
      );
      if (!alive) return;
      if (r.ok && r.result?.ok) {
        const rInner = r.result.initial;
        const url = rInner?.iframeUrl ?? rInner?.status?.bundleURL ?? null;
        if (url) {
          setIframeUrl(url);
          setStatus("running");
        } else {
          setStatus("no iframe url");
        }
      } else {
        setStatus(r.result?.error ?? r.error ?? "failed to start");
      }
    })();
    return () => { alive = false; };
  }, [nonce]);

  if (iframeUrl) {
    return (
      <WebView
        source={{ uri: iframeUrl }}
        style={{ flex: 1, backgroundColor: PAL.bg }}
        originWhitelist={["*"]}
        javaScriptEnabled
        domStorageEnabled
        startInLoadingState
        renderLoading={() => (
          <View style={{ flex: 1, alignItems: "center", justifyContent: "center" }}>
            <ActivityIndicator color={PAL.accent} />
          </View>
        )}
      />
    );
  }
  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 12 }}>
      <Text style={{
        color: PAL.muted,
        fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
        fontSize: 11,
        textAlign: "center",
      }}>
        {status}
        {"\n"}
        {status === "starting" ? "(launching Next/Vite/Nuxt…)" : "tap ⟳ to retry"}
      </Text>
    </View>
  );
}

// ── ClipListPane: list recent vibe-preview clips, tap poster to play ──────

interface ClipRecord {
  id: string;
  source?: string;
  durationSec?: number;
  status?: string;
  posterPath?: string;
  startedAt?: string;
}

function ClipListPane(props: { projectName: string; nonce: number }): React.ReactElement {
  const { projectName, nonce } = props;
  const [clips, setClips] = useState<ClipRecord[]>([]);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const base = quicClient.baseUrl;
    if (!base) { setErr("no agent"); return; }
    const url = `${base}/vibing/preview/clips?project=${encodeURIComponent(projectName)}`;
    fetch(url, { headers: quicClient.getAuthHeaders() })
      .then((r) => r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`)))
      .then((j: { clips?: ClipRecord[] }) => { if (alive) setClips(j.clips ?? []); })
      .catch((e: Error) => { if (alive) setErr(e.message); });
    return () => { alive = false; };
  }, [projectName, nonce]);

  if (err) {
    return (
      <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 12 }}>
        <Text style={{ color: PAL.err, fontFamily: "Menlo", fontSize: 11 }}>{err}</Text>
      </View>
    );
  }
  if (clips.length === 0) {
    return (
      <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 12 }}>
        <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 11, textAlign: "center" }}>
          no clips yet for {projectName}
          {"\n"}record one from the device modal
        </Text>
      </View>
    );
  }
  const base = quicClient.baseUrl;
  return (
    <FlatList
      data={clips}
      keyExtractor={(c) => c.id}
      renderItem={({ item }) => <ClipRow clip={item} base={base ?? ""} />}
    />
  );
}

// One row in the clip list. The 🔧 button POSTs /vibing/preview/clip/<id>/fix
// with a baked default comment so glass-users can spawn a fix task in one
// tap. The web /workspace surface has a richer comment-input variant of
// the same flow — see ClipPlayerWithFix in WorkspaceShell.tsx.
function ClipRow(props: { clip: ClipRecord; base: string }): React.ReactElement {
  const { clip, base } = props;
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const fileFix = useCallback(async () => {
    if (!base) { setMsg("no agent"); return; }
    setBusy(true);
    setMsg(null);
    try {
      const r = await fetch(`${base}/vibing/preview/clip/${encodeURIComponent(clip.id)}/fix`, {
        method: "POST",
        headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ comment: "Fix the bug visible in this vibe clip.", autoFix: true }),
      });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) setMsg(`err: ${j?.error ?? r.status}`);
      else setMsg(j.hint ?? `filed ${j.feedbackId}`);
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "fix failed");
    } finally {
      setBusy(false);
    }
  }, [clip.id, base]);

  return (
    <View style={{ padding: 6, borderBottomColor: PAL.border, borderBottomWidth: StyleSheet.hairlineWidth }}>
      <View style={{ flexDirection: "row" }}>
        {clip.posterPath ? (
          <Image
            source={{
              uri: `${base}/vibing/preview/clip/${encodeURIComponent(clip.id)}/poster`,
              headers: quicClient.getAuthHeaders() as Record<string, string>,
            }}
            style={{ width: 56, height: 32, borderRadius: 4, marginRight: 8, backgroundColor: PAL.chip }}
          />
        ) : (
          <View style={{ width: 56, height: 32, borderRadius: 4, marginRight: 8, backgroundColor: PAL.chip }} />
        )}
        <View style={{ flex: 1 }}>
          <Text style={{ color: PAL.fg, fontFamily: "Menlo", fontSize: 10 }} numberOfLines={1}>
            {clip.id}
          </Text>
          <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 9 }}>
            {clip.source} · {Math.round(clip.durationSec ?? 0)}s · {clip.status}
          </Text>
        </View>
        <Pressable
          onPress={fileFix}
          disabled={busy}
          style={{ paddingHorizontal: 8, paddingVertical: 4, borderRadius: 4, backgroundColor: busy ? PAL.chip : "#3b0764" }}
        >
          <Text style={{ color: busy ? PAL.muted : PAL.accent, fontFamily: "Menlo", fontSize: 10 }}>
            {busy ? "…" : "🔧 fix"}
          </Text>
        </Pressable>
      </View>
      {msg ? (
        <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 9, marginTop: 2 }} numberOfLines={2}>
          {msg}
        </Text>
      ) : null}
    </View>
  );
}

// ── LogsPane: tail last N lines of cloud_logs / journalctl for the agent ──

function LogsPane(props: { nonce: number }): React.ReactElement {
  const { nonce } = props;
  const [logs, setLogs] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);

  const fetchOnce = useCallback(async () => {
    setBusy(true);
    const res = await callMcpDirect<{ lines?: string[] } | string>("journalctl", { lines: 40 });
    if (res.ok) {
      // journalctl handler returns either { lines } or a stringified body
      if (typeof res.result === "string") {
        setLogs(res.result.split("\n").slice(-40));
      } else if (res.result && Array.isArray(res.result.lines)) {
        setLogs(res.result.lines);
      } else {
        setLogs(["(no log output)"]);
      }
    } else {
      setLogs([`error: ${res.error ?? "unknown"}`]);
    }
    setBusy(false);
  }, []);

  useEffect(() => { void fetchOnce(); }, [nonce, fetchOnce]);

  return (
    <View style={{ flex: 1, backgroundColor: PAL.bg }}>
      <FlatList
        data={logs}
        keyExtractor={(_, i) => String(i)}
        renderItem={({ item }) => (
          <Text style={{
            color: item.toLowerCase().includes("error") ? PAL.err : PAL.fg,
            fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
            fontSize: 10,
            lineHeight: 13,
            paddingHorizontal: 6,
          }} selectable>{item}</Text>
        )}
        contentContainerStyle={{ paddingVertical: 4 }}
      />
      {busy ? (
        <View style={{ position: "absolute", top: 4, right: 4 }}>
          <ActivityIndicator size="small" color={PAL.accent} />
        </View>
      ) : null}
    </View>
  );
}

// ── Helpers ───────────────────────────────────────────────────────────────

function buildTerminalWsUrl(): string {
  const base = quicClient.baseUrl;
  if (!base) throw new Error("no device selected");
  const wsBase = base.replace(/^http/, "ws");
  const h = quicClient.getAuthHeaders();
  const token = encodeURIComponent((h.Authorization || "").replace("Bearer ", ""));
  return `${wsBase}/ws/terminal?token=${token}`;
}

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07/g;
function stripAnsi(s: string): string {
  return s.replace(ANSI, "").replace(/\r(?!\n)/g, "");
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: PAL.bg },
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  headerBtn: { fontSize: 26, fontWeight: "300", paddingHorizontal: 4 },
  headerTitle: {
    fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
    fontSize: 13,
    fontWeight: "600",
  },
  modeChip: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 999,
    borderWidth: 1,
  },
  modeChipText: {
    fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
    fontSize: 11,
    fontWeight: "600",
  },
  paneInputRow: {
    flexDirection: "row",
    alignItems: "center",
    padding: 4,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
});
