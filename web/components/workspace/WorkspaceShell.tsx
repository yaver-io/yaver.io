"use client";

// WorkspaceShell — i3-style tiling for the flat web dashboard.
//
// Lifted from /spatial's multi-pane logic but rendered as a plain CSS
// Grid instead of WebGL (Three.js) so it works in any browser, on any
// laptop, AND on AR glasses connected to an iPhone (mirrored display).
//
// V1 design:
//   • Project-kind aware default pane set (mobile / web / backend /
//     generic) — fetched once from the connected agent's
//     /project/kind endpoint.
//   • Fixed grid layouts (1×1, 2×1, 1×2, 2×2, 1×3). User picks via
//     the layout switcher in the header. Resize is intentionally
//     phase 2.
//   • i3-style keyboard binds via useWorkspaceKeyboard.
//   • Layout persisted in localStorage per-project so reload restores.
//
// What each kind shows by default:
//   mobile   →  Terminal · Web preview · Clips · Tests   (2×2)
//   web      →  Terminal · Web preview · Tests · Clips   (2×2)
//   backend  →  Terminal · Tests · Help                  (1×3 column)
//   generic  →  Terminal · Help                          (2×1)
//
// All panes render existing components so this file is mostly glue:
//   TerminalView                → existing xterm.js + /ws/terminal
//   WebPreviewFrame             → existing iframe + viewport picker
//   VibeClipsPanel              → list + MP4 player (lifted from VibePreviewView)
//   TestkitFailurePanel         → tail of testkit_last_failure
//   HelpPanel                   → static shortcut reference

import { useCallback, useEffect, useMemo, useState } from "react";
import dynamic from "next/dynamic";

import { agentClient } from "@/lib/agent-client";
import {
  useWorkspaceKeyboard,
  WORKSPACE_SHORTCUT_ROWS,
} from "./useWorkspaceKeyboard";

const TerminalView = dynamic(() => import("@/components/dashboard/TerminalView"), {
  ssr: false,
  loading: () => <div className="text-xs text-zinc-500 p-2">loading terminal…</div>,
});

type ProjectKind = "mobile" | "web" | "backend" | "generic";

interface PaneDef {
  id: string;
  title: string;
  /** 1-line status colour token (zinc / emerald / amber / red). */
  status?: string;
  render: () => React.ReactNode;
}

const LAYOUTS = {
  "1x1": "grid-cols-1 grid-rows-1",
  "2x1": "grid-cols-2 grid-rows-1",
  "1x2": "grid-cols-1 grid-rows-2",
  "2x2": "grid-cols-2 grid-rows-2",
  "1x3": "grid-cols-1 grid-rows-3",
  "3x1": "grid-cols-3 grid-rows-1",
} as const;
type LayoutId = keyof typeof LAYOUTS;

function defaultLayoutFor(n: number): LayoutId {
  if (n <= 1) return "1x1";
  if (n === 2) return "2x1";
  if (n === 3) return "1x3";
  return "2x2";
}

const LAYOUT_STORAGE_KEY = "yaver:workspace:layout:v1";

export default function WorkspaceShell(): React.ReactElement {
  const [kind, setKind] = useState<ProjectKind>("generic");
  const [workDir, setWorkDir] = useState<string>("");
  const [focusId, setFocusId] = useState<string>("");
  const [showHelp, setShowHelp] = useState(false);
  const [layoutOverride, setLayoutOverride] = useState<LayoutId | null>(null);

  // One-shot fetch of project kind. The agent serves /project/kind;
  // older agents return generic via the client's fallback.
  useEffect(() => {
    let alive = true;
    agentClient.getProjectKind().then((res) => {
      if (!alive) return;
      setKind(res.kind);
      setWorkDir(res.workDir);
    }).catch(() => { /* fall back to generic — already the default */ });
    return () => { alive = false; };
  }, []);

  // Restore the user's previously-chosen layout for this project.
  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      const raw = window.localStorage.getItem(LAYOUT_STORAGE_KEY);
      if (!raw) return;
      const parsed = JSON.parse(raw);
      if (parsed && typeof parsed[workDir] === "string" && parsed[workDir] in LAYOUTS) {
        setLayoutOverride(parsed[workDir] as LayoutId);
      }
    } catch { /* corrupt entry, ignore */ }
  }, [workDir]);

  const panes = useMemo<PaneDef[]>(() => panesForKind(kind), [kind]);
  const layoutId: LayoutId = layoutOverride ?? defaultLayoutFor(panes.length);

  // First pane defaults to focused so xterm gets keystrokes on land.
  useEffect(() => {
    if (!focusId && panes[0]) setFocusId(panes[0].id);
  }, [panes, focusId]);

  const persistLayout = useCallback((next: LayoutId) => {
    setLayoutOverride(next);
    if (typeof window === "undefined") return;
    try {
      const raw = window.localStorage.getItem(LAYOUT_STORAGE_KEY);
      const obj = raw ? JSON.parse(raw) : {};
      obj[workDir] = next;
      window.localStorage.setItem(LAYOUT_STORAGE_KEY, JSON.stringify(obj));
    } catch { /* full quota / private mode — fine, not fatal */ }
  }, [workDir]);

  useWorkspaceKeyboard({
    onSelectPane: (idx) => {
      const target = panes[idx];
      if (target) setFocusId(target.id);
    },
    onNextPane: () => cycle(panes, focusId, +1, setFocusId),
    onPrevPane: () => cycle(panes, focusId, -1, setFocusId),
    onUnfocusPane: () => setFocusId(""),
    onToggleHelp: () => setShowHelp((h) => !h),
    paneFocused: !!focusId,
  });

  return (
    <div className="h-screen w-screen bg-[#0b0d10] text-zinc-200 flex flex-col">
      {/* Header */}
      <div className="flex items-center gap-3 border-b border-zinc-800 px-4 py-2">
        <span className="text-sm font-semibold">yaver workspace</span>
        <span className="text-xs text-zinc-500">
          kind: <span className="text-zinc-300">{kind}</span>
          {workDir ? <> · <span className="text-zinc-400">{workDir.split("/").pop()}</span></> : null}
        </span>
        <div className="ml-auto flex items-center gap-2">
          <label className="text-xs text-zinc-500">layout</label>
          <select
            value={layoutId}
            onChange={(e) => persistLayout(e.target.value as LayoutId)}
            className="bg-zinc-900 border border-zinc-700 rounded px-2 py-0.5 text-xs"
          >
            {Object.keys(LAYOUTS).map((k) => (
              <option key={k} value={k}>{k}</option>
            ))}
          </select>
          <button
            onClick={() => setShowHelp((h) => !h)}
            className="text-xs px-2 py-0.5 border border-zinc-700 rounded hover:bg-zinc-800"
          >
            ?
          </button>
        </div>
      </div>

      {/* Grid */}
      <div className={`flex-1 grid gap-1 p-1 ${LAYOUTS[layoutId]}`}>
        {panes.map((p, i) => {
          const isFocused = p.id === focusId;
          return (
            <div
              key={p.id}
              className={`flex flex-col border rounded overflow-hidden bg-[#0b0d10] ${isFocused ? "border-violet-500" : "border-zinc-800"}`}
              onClick={() => setFocusId(p.id)}
            >
              <div className="flex items-center gap-2 border-b border-zinc-800 px-2 py-1">
                <span className={`text-[10px] font-mono ${isFocused ? "text-violet-400" : "text-zinc-500"}`}>
                  {i + 1}
                </span>
                <span className={`text-xs ${isFocused ? "text-zinc-100" : "text-zinc-400"}`}>
                  {p.title}
                </span>
                {p.status ? (
                  <span className="ml-auto text-[10px] text-zinc-500">{p.status}</span>
                ) : null}
              </div>
              <div className="flex-1 overflow-hidden">{p.render()}</div>
            </div>
          );
        })}
      </div>

      {/* Help overlay */}
      {showHelp ? (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/70"
          onClick={() => setShowHelp(false)}
        >
          <div
            className="bg-[#0b0d10] border border-zinc-700 rounded p-6 max-w-md"
            onClick={(e) => e.stopPropagation()}
          >
            <h2 className="text-sm font-semibold mb-3">keyboard shortcuts</h2>
            <table className="text-xs font-mono">
              <tbody>
                {WORKSPACE_SHORTCUT_ROWS.map((row) => (
                  <tr key={row.keys}>
                    <td className="pr-4 text-violet-400 align-top whitespace-nowrap">{row.keys}</td>
                    <td className="text-zinc-400">{row.what}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="text-xs text-zinc-500 mt-3">click anywhere to close</p>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function cycle(
  panes: PaneDef[],
  currentId: string,
  delta: number,
  set: (id: string) => void,
): void {
  if (panes.length === 0) return;
  const idx = Math.max(0, panes.findIndex((p) => p.id === currentId));
  const next = ((idx + delta) % panes.length + panes.length) % panes.length;
  set(panes[next].id);
}

// ── Pane composition per project kind ─────────────────────────────────────

function panesForKind(kind: ProjectKind): PaneDef[] {
  switch (kind) {
    case "mobile":  return [
      { id: "terminal", title: "shell · tmux", render: () => <TerminalPane /> },
      { id: "preview",  title: "preview",      render: () => <WebPreviewPane /> },
      { id: "clips",    title: "vibe clips",   render: () => <ClipsPane /> },
      { id: "tests",    title: "tests",        render: () => <TestsPane /> },
    ];
    case "web":     return [
      { id: "terminal", title: "shell · tmux", render: () => <TerminalPane /> },
      { id: "preview",  title: "live preview", render: () => <WebPreviewPane /> },
      { id: "tests",    title: "tests",        render: () => <TestsPane /> },
      { id: "clips",    title: "vibe clips",   render: () => <ClipsPane /> },
    ];
    case "backend": return [
      { id: "terminal", title: "shell · tmux", render: () => <TerminalPane /> },
      { id: "tests",    title: "tests",        render: () => <TestsPane /> },
      { id: "logs",     title: "logs",         render: () => <LogsPane /> },
    ];
    default:        return [
      { id: "terminal", title: "shell",        render: () => <TerminalPane /> },
      { id: "help",     title: "what is this", render: () => <HelpPane /> },
    ];
  }
}

// ── Per-pane content components ───────────────────────────────────────────

function TerminalPane(): React.ReactElement {
  return (
    <div className="h-full w-full">
      <TerminalView />
    </div>
  );
}

function WebPreviewPane(): React.ReactElement {
  const [url, setUrl] = useState<string | null>(null);
  const [status, setStatus] = useState<string>("checking");
  const [err, setErr] = useState<string | null>(null);

  const start = useCallback(async () => {
    setStatus("starting");
    setErr(null);
    try {
      const res = await agentClient.callOps("web-preview", { action: "start" });
      if (res.ok && res.initial) {
        const iframeUrl = (res.initial as any).iframeUrl ?? (res.initial as any).status?.bundleURL;
        if (iframeUrl) {
          setUrl(iframeUrl);
          setStatus("running");
          return;
        }
        setStatus("started, no url");
        return;
      }
      setErr(res.error ?? "start failed");
      setStatus("failed");
    } catch (e) {
      setErr(e instanceof Error ? e.message : "start failed");
      setStatus("failed");
    }
  }, []);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const st = await agentClient.callOps("web-preview", { action: "status" });
        if (!alive) return;
        if (st.ok && st.initial?.running && st.initial?.bundleURL) {
          setUrl(st.initial.bundleURL);
          setStatus("running");
          return;
        }
        // not running yet — auto-start once on mount
        void start();
      } catch (e) {
        if (alive) {
          setErr(e instanceof Error ? e.message : "status failed");
          setStatus("error");
        }
      }
    })();
    return () => { alive = false; };
  }, [start]);

  if (url) {
    return (
      <div className="h-full w-full flex flex-col">
        <div className="flex items-center gap-2 px-2 py-1 border-b border-zinc-800 text-[10px] text-zinc-500 font-mono">
          <span className="truncate flex-1">{url}</span>
          <button
            onClick={() => agentClient.callOps("web-preview", { action: "reload" })}
            className="px-1.5 py-0.5 border border-zinc-700 rounded hover:bg-zinc-800"
          >⟳ reload</button>
        </div>
        <iframe src={url} className="flex-1 w-full bg-white" />
      </div>
    );
  }

  return (
    <div className="h-full w-full flex flex-col items-center justify-center gap-2 text-xs text-zinc-500 p-4">
      <div>{status}</div>
      {err ? <div className="text-red-400 font-mono">{err}</div> : null}
      <button
        onClick={() => void start()}
        className="text-xs px-3 py-1 border border-zinc-700 rounded hover:bg-zinc-800"
      >
        start dev server
      </button>
    </div>
  );
}

interface ClipRecord {
  id: string;
  source?: string;
  durationSec?: number;
  status?: string;
  posterPath?: string;
}

function ClipsPane(): React.ReactElement {
  const [clips, setClips] = useState<ClipRecord[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        // The clips endpoint expects ?project=<name>.
        const info = await agentClient.getInfo().catch(() => null);
        const project = ((info as { workDir?: string } | null)?.workDir ?? "default").split("/").pop() ?? "default";
        const res = await agentClient.agentFetch(`/vibing/preview/clips?project=${encodeURIComponent(project)}`);
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const j = await res.json();
        if (alive) setClips(j.clips ?? []);
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : "fetch failed");
      }
    })();
    return () => { alive = false; };
  }, []);

  if (err) return <div className="p-3 text-xs text-red-400 font-mono">{err}</div>;
  if (clips.length === 0) return <div className="p-3 text-xs text-zinc-500">no clips yet</div>;

  if (selected) {
    return <ClipPlayerWithFix clipId={selected} onBack={() => setSelected(null)} />;
  }

  return (
    <div className="h-full w-full overflow-auto p-2 grid grid-cols-2 gap-2">
      {clips.map((c) => (
        <button
          key={c.id}
          onClick={() => setSelected(c.id)}
          className="text-left border border-zinc-800 rounded overflow-hidden hover:border-violet-500"
        >
          <img
            src={agentClient.agentAssetUrl(`/vibing/preview/clip/${encodeURIComponent(c.id)}/poster`)}
            alt={c.id}
            className="w-full aspect-video object-cover bg-zinc-900"
          />
          <div className="px-2 py-1 text-[10px] text-zinc-400 font-mono">
            {c.source} · {Math.round(c.durationSec ?? 0)}s · {c.status}
          </div>
        </button>
      ))}
    </div>
  );
}

function ClipPlayerWithFix(props: { clipId: string; onBack: () => void }): React.ReactElement {
  const { clipId, onBack } = props;
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<string | null>(null);

  const fileFix = useCallback(async (autoFix: boolean) => {
    setBusy(true);
    setResult(null);
    try {
      const r = await agentClient.agentFetch(`/vibing/preview/clip/${encodeURIComponent(clipId)}/fix`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ comment: comment || "Fix the bug shown in this clip.", autoFix }),
      });
      const j = await r.json();
      if (!r.ok) {
        setResult(`error: ${j?.error ?? `HTTP ${r.status}`}`);
      } else {
        setResult(j.hint ?? `filed: ${j.feedbackId}${j.taskId ? ` task ${j.taskId}` : ""}`);
      }
    } catch (e) {
      setResult(e instanceof Error ? e.message : "fix failed");
    } finally {
      setBusy(false);
    }
  }, [clipId, comment]);

  return (
    <div className="h-full w-full flex flex-col bg-black">
      <div className="flex items-center gap-2 px-2 py-1 border-b border-zinc-800 text-[10px] text-zinc-500 font-mono">
        <span className="flex-1 truncate">{clipId}</span>
        <button
          onClick={onBack}
          className="px-1.5 py-0.5 border border-zinc-700 rounded hover:bg-zinc-800"
        >back</button>
      </div>
      <video
        src={agentClient.agentAssetUrl(`/vibing/preview/clip/${encodeURIComponent(clipId)}`)}
        controls autoPlay
        className="flex-1 w-full bg-black"
      />
      <div className="border-t border-zinc-800 p-2 flex gap-2">
        <input
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          placeholder={'what is the bug? e.g. "login button no longer responds on tap"'}
          disabled={busy}
          className="flex-1 bg-zinc-900 border border-zinc-700 rounded px-2 py-1 text-xs"
        />
        <button
          onClick={() => void fileFix(true)}
          disabled={busy}
          className="px-3 py-1 text-xs bg-violet-600 hover:bg-violet-500 rounded disabled:opacity-50"
        >
          {busy ? "filing…" : "fix this"}
        </button>
        <button
          onClick={() => void fileFix(false)}
          disabled={busy}
          className="px-2 py-1 text-xs border border-zinc-700 rounded hover:bg-zinc-800 disabled:opacity-50"
          title="file as feedback without spawning a fix task"
        >
          file only
        </button>
      </div>
      {result ? (
        <div className="px-2 pb-2 text-[10px] font-mono text-zinc-400">{result}</div>
      ) : null}
    </div>
  );
}

function TestsPane(): React.ReactElement {
  const [body, setBody] = useState<string>("loading…");

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const r = await agentClient.agentFetch("/mcp", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            jsonrpc: "2.0", id: 1, method: "tools/call",
            params: { name: "testkit_last_failure", arguments: {} },
          }),
        });
        const j = await r.json();
        const text = j?.result?.content?.[0]?.text ?? "(no test history yet)";
        if (alive) setBody(text);
      } catch (e) {
        if (alive) setBody(e instanceof Error ? e.message : "fetch failed");
      }
    })();
    return () => { alive = false; };
  }, []);

  return (
    <pre className="h-full w-full overflow-auto p-2 text-[11px] font-mono text-zinc-300 whitespace-pre-wrap">
      {body}
    </pre>
  );
}

function LogsPane(): React.ReactElement {
  const [lines, setLines] = useState<string[]>([]);
  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const r = await agentClient.agentFetch("/mcp", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            jsonrpc: "2.0", id: 1, method: "tools/call",
            params: { name: "journalctl", arguments: { lines: 80 } },
          }),
        });
        const j = await r.json();
        const text: string = j?.result?.content?.[0]?.text ?? "";
        if (alive) setLines(text.split("\n").slice(-80));
      } catch (e) {
        if (alive) setLines([e instanceof Error ? e.message : "fetch failed"]);
      }
    })();
    return () => { alive = false; };
  }, []);
  return (
    <pre className="h-full w-full overflow-auto p-2 text-[10px] font-mono text-zinc-300 whitespace-pre">
      {lines.join("\n")}
    </pre>
  );
}

function HelpPane(): React.ReactElement {
  return (
    <div className="p-3 text-xs text-zinc-400 leading-relaxed">
      <p className="mb-2">
        This is the yaver workspace — a tiling i3-style view for AR-glasses
        + Bluetooth keyboard developers.
      </p>
      <p className="mb-2">
        Open a project (any directory with a package.json / go.mod /
        Cargo.toml / pubspec.yaml) on the connected agent. The workspace
        will swap to the matching default pane set the next time you load.
      </p>
      <p className="mb-2 text-zinc-500">
        Press <code className="text-violet-400">?</code> for shortcuts.
      </p>
    </div>
  );
}
