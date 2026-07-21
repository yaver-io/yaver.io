"use client";

import type { ReactNode } from "react";
import { memo, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import { capStreamText } from "@/lib/streamBuffer";
import { agentClient, type AgentGraphRun, type ConnectionState, type GitCommitRow, type GitProviderStatusRow, type GitRemoteRepo, type GitStatusRow, type MachineInfo, type Runner, type Task } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import { useAuth } from "@/lib/use-auth";
import { detectAskBreadth, detectAskIntent } from "@/lib/ask-intent";
import {
  activationBlockReason,
  activateTaskPlacement,
  createTaskDispatchIntent,
  expensiveCloudPlacementMessage,
  getTaskPlacementStatus,
  listTaskDispatchIntents,
  listRecentTaskPlacements,
  markTaskPlacementStatus,
  placementCreditLabel,
  placementLaneLabel,
  previewTaskPlacement,
  recordTaskPlacement,
  pendingPlacementTaskId,
  shouldConfirmExpensiveCloudPlacement,
  shouldDeferTaskForCloudWorkspace,
  rebindTaskPlacement,
  updateTaskDispatchIntent,
  upsertProjectProfile,
  type TaskPlacementDecision,
  type TaskPlacementKind,
  type TaskPlacementRequest,
  type TaskPlacementResourceClass,
} from "@/lib/task-placement";
import {
  listPendingCloudDispatches,
  mergePendingCloudPlacementStatus,
  mergePendingCloudDispatchIntents,
  pendingCloudDispatchNeedsUserAction,
  pendingCloudTaskPlaceholder,
  removePendingCloudDispatch,
  saveCloudWorkspaceRequiredDispatch,
  savePendingCloudDispatch,
  updatePendingCloudDispatch,
  type PendingCloudDispatch,
} from "@/lib/pending-cloud-dispatch";
import { CloudWorkspaceRequiredError } from "@/lib/cloud-workspace-required";
import PreviewPane from "./PreviewPane";
import { preferredDefaultModelForRunner, preferredDefaultRunnerForDevice, usePrimaryRunnerByDevice } from "./DevicesView";

// ANSI escape stripper. Mirrors desktop/agent/result_cleanup.go::stripANSI
// so anything reaching the chat surface — historic task.Output rows that
// the older agent persisted with raw codes, MCP-piped output, or any
// future runner whose live stream isn't yet pre-cleaned — renders as
// plain text instead of literal `[91m` blobs. Cheap regex; called once
// per turn render, not per token.
const ANSI_ESC_RE = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07/g;
const BARE_CSI_RE = /\[\d+(?:;\d+)*m/g;
export function stripAnsi(s: string): string {
  if (!s) return s;
  return s.replace(ANSI_ESC_RE, "").replace(BARE_CSI_RE, "");
}

function inferTaskPlacementKind(text: string): TaskPlacementKind {
  const lower = String(text || "").toLowerCase();
  if (/\b(deploy|publish|release|ship)\b/.test(lower)) return "deploy";
  if (/\b(build|apk|ipa|xcode|gradle|eas|archive)\b/.test(lower)) return "build";
  if (/\b(test|spec|lint|typecheck|ci)\b/.test(lower)) return "test";
  if (/\b(read|explain|review|summarize|inspect)\b/.test(lower)) return "source";
  return "vibe";
}

function projectSlugForPlacement(pathOrName?: string | null): string | undefined {
  const leaf = String(pathOrName || "")
    .split(/[\\/]/)
    .filter(Boolean)
    .pop()
    ?.trim();
  return leaf ? leaf.slice(0, 80) : undefined;
}

function resourceClassFromProjectHints(args: {
  kind: TaskPlacementKind;
  framework?: string;
  appCount?: number;
  hasNativeMobile?: boolean;
  hasDocker?: boolean;
}): TaskPlacementResourceClass {
  if (args.kind === "deploy" || args.kind === "build") return "build";
  if (args.hasNativeMobile || args.hasDocker || /react-native|expo|xcode|gradle/i.test(args.framework || "")) {
    return "heavy";
  }
  if ((args.appCount ?? 0) >= 2) return "heavy";
  return args.kind === "source" || args.kind === "vibe" ? "relay-source" : "standard";
}

// react-markdown component overrides shared by every assistant-side
// surface (final bubble + live streaming bubble). Keeps the visual
// vocabulary aligned with web/components/ChatWidget.tsx so the support
// chat and the vibing surface don't diverge.
//
// `**$ <cmd>**` is the marker readStreamJSON emits for claude bash
// tool_use events and that opencodeStreamFilter rewrites for opencode
// shell calls; the `strong` override pulls those out as a slightly
// styled pill so the eye can scan a long agent run for tool calls
// without parsing prose.
export const ASSISTANT_MARKDOWN_COMPONENTS = {
  a: ({ href, children }: { href?: string; children?: ReactNode }) => (
    <a href={href} target="_blank" rel="noopener noreferrer" className="underline text-[#818cf8] hover:text-[#a5b4fc]">
      {children}
    </a>
  ),
  p: ({ children }: { children?: ReactNode }) => <p className="mb-1 last:mb-0">{children}</p>,
  strong: ({ children }: { children?: ReactNode }) => {
    // Detect the shell-pill marker (`$ <cmd>`) and apply a distinct
    // visual treatment so it doesn't blend with prose-emphasized bold.
    const text = Array.isArray(children) && typeof children[0] === "string" ? children[0] : (typeof children === "string" ? children : "");
    if (text.startsWith("$ ")) {
      return (
        <code className="rounded bg-surface-900/70 px-1.5 py-0.5 font-mono text-[12px] text-cyan-700 dark:text-cyan-200">
          {children}
        </code>
      );
    }
    return <strong className="font-semibold text-surface-100">{children}</strong>;
  },
  ul: ({ children }: { children?: ReactNode }) => <ul className="list-disc pl-4 mb-1">{children}</ul>,
  ol: ({ children }: { children?: ReactNode }) => <ol className="list-decimal pl-4 mb-1">{children}</ol>,
  li: ({ children }: { children?: ReactNode }) => <li className="mb-0.5">{children}</li>,
  code: ({ children }: { children?: ReactNode }) => <code className="bg-surface-900/50 px-1 rounded text-[12px] font-mono">{children}</code>,
  pre: ({ children }: { children?: ReactNode }) => <pre className="my-1 overflow-x-auto rounded bg-surface-900/50 p-2 text-[12px] font-mono leading-5">{children}</pre>,
};

// Memoized chat bubble. Without this, every streaming token rebuilt
// `conversationTurns` (filter() returns a fresh array; turns themselves
// keep stable identity but the .map() at the render site recreates the
// element nodes), causing every prior bubble to commit on every token.
// The <pre> + whitespace-pre-wrap are cheap individually but at 60+
// bubbles × 100 tokens/sec they stall the layout pass and degrade
// streaming smoothness. Compare on (role, content) — turns are
// append-only, so once a turn is finalized its content never changes
// and React skips it.
type ChatTurn = { role: string; content: string; timestamp?: number | string };
// Live agent output. Memoized on the raw text so it re-strips + re-parses ONLY
// when the text actually changes — not on every unrelated re-render of the
// parent (which re-renders constantly while a task streams). Pairs with the
// capStreamText() bound at the write site: without the cap this parse is over
// the whole session transcript, which is what freezes the tab.
export const AssistantMarkdown = memo(function AssistantMarkdown({ text }: { text: string }) {
  const cleaned = useMemo(() => stripAnsi(text), [text]);
  return <ReactMarkdown components={ASSISTANT_MARKDOWN_COMPONENTS}>{cleaned}</ReactMarkdown>;
});

const ChatBubble = memo(function ChatBubble({ turn }: { turn: ChatTurn }) {
  // User input stays as a verbatim block — preserves whitespace,
  // never re-parses markdown the user didn't intend (a literal
  // backslash or asterisk in a code-question shouldn't bold).
  // Assistant output renders through react-markdown with shared
  // component overrides (mirrors mobile's react-native-markdown-
  // display flow) so claude / codex / opencode all show consistent
  // bold, code, lists, and the `$ <cmd>` shell-pill marker that the
  // agent emits for tool calls. ANSI is stripped defensively for
  // historic rows persisted before the live-stream filter shipped.
  const isUser = turn.role === "user";
  const content = isUser ? turn.content : stripAnsi(turn.content);
  return (
    <div
      className={`max-w-[88%] rounded-2xl border px-4 py-3 ${
        isUser
          ? "ml-auto border-indigo-500/30 bg-indigo-500/10 text-surface-100"
          : "border-surface-800 bg-surface-900/70 text-surface-200"
      }`}
    >
      <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
        {isUser ? "You" : "Agent"}
      </div>
      {isUser ? (
        <pre className="whitespace-pre-wrap font-mono text-[13px] leading-6">{content}</pre>
      ) : (
        <div className="prose-invert text-[13px] leading-6 break-words [&_pre]:whitespace-pre-wrap">
          <ReactMarkdown components={ASSISTANT_MARKDOWN_COMPONENTS}>{content}</ReactMarkdown>
        </div>
      )}
    </div>
  );
}, (prev, next) =>
  prev.turn.role === next.turn.role &&
  prev.turn.content === next.turn.content,
);

type SectionKey = "projects" | "runner" | "actions" | "repo" | "secrets" | "providers" | "sessions";

type SectionState = { visible: boolean; open: boolean };

const SECTION_ORDER: SectionKey[] = ["projects", "runner", "actions", "repo", "secrets", "providers", "sessions"];

const SECTION_LABELS: Record<SectionKey, string> = {
  projects: "Projects",
  runner: "Coding Agent",
  actions: "Actions",
  repo: "Repo",
  secrets: "Project Secrets",
  providers: "Git Providers",
  sessions: "Sessions",
};

const SECTION_DEFAULTS: Record<SectionKey, SectionState> = {
  projects: { visible: true, open: true },
  runner: { visible: true, open: true },
  actions: { visible: true, open: true },
  repo: { visible: true, open: false },
  secrets: { visible: false, open: false },
  providers: { visible: false, open: false },
  sessions: { visible: true, open: true },
};

const SECTION_STORAGE_KEY = "yaver_vibe_sections_v1";

function loadSectionState(): Record<SectionKey, SectionState> {
  if (typeof window === "undefined") return { ...SECTION_DEFAULTS };
  try {
    const raw = window.localStorage.getItem(SECTION_STORAGE_KEY);
    if (!raw) return { ...SECTION_DEFAULTS };
    const parsed = JSON.parse(raw) as Partial<Record<SectionKey, SectionState>>;
    const next = { ...SECTION_DEFAULTS };
    for (const key of SECTION_ORDER) {
      if (parsed[key]) next[key] = { ...next[key], ...parsed[key] };
    }
    return next;
  } catch {
    return { ...SECTION_DEFAULTS };
  }
}

type Project = {
  name: string;
  path: string;
  branch?: string;
  framework?: string;
  tags?: string[];
};

type PreviewTarget = {
  id: string;
  name: string;
  deviceClass?: string;
  edgeProfile?: {
    supportsLocalInference: boolean;
    maxModelClass: "none" | "tiny" | "small" | "medium";
  };
};

type DeployPreviewSummary = {
  warnings?: string[];
  lastDeploy?: {
    target?: string;
    status?: string;
    startedAt?: string;
    finishedAt?: string;
  };
};

type DeployActionKind = "testflight" | "play-internal" | "eas";

// analyticsAgentSwitch is a thin best-effort logger for runtime
// agent-switch events. We don't have a central analytics pipeline on
// the web side yet, so for now this writes a structured console line
// the user can grep + a navigator.sendBeacon to /activity if the agent
// is reachable. Both are wrapped in try/catch by the caller — analytics
// must NEVER block a user-driven action.
function analyticsAgentSwitch(
  event: "agent_switch_requested" | "agent_switch_completed" | "agent_switch_failed",
  data: Record<string, unknown>,
) {
  const payload = { event, source: "web", ...data, ts: Date.now() };
  try {
    // eslint-disable-next-line no-console
    console.log("[yaver-analytics]", JSON.stringify(payload));
  } catch { /* ignore */ }
  // Beacon path intentionally elided until a /activity endpoint exists
  // on the agent. Console line is the only sink for now; that's enough
  // to verify the flow during dev. Wire up sendBeacon when we add a
  // real ingest endpoint server-side.
}

function previewPlatformForFramework(framework?: string): "web" | undefined {
  const fw = (framework || "").toLowerCase();
  if (
    fw.includes("expo") ||
    fw.includes("react-native") ||
    fw.includes("next") ||
    fw.includes("vite") ||
    fw === "react"
  ) {
    return "web";
  }
  return undefined;
}

export default function VibeCodingView({
  devices,
  connectedDevice,
  connState,
  onSelectDevice,
  mobileWorkers,
  selectedPreviewTarget,
  onSelectPreviewTarget,
}: {
  devices: Device[];
  connectedDevice: Device | null;
  connState: ConnectionState;
  onSelectDevice: (device: Device) => Promise<void> | void;
  mobileWorkers: PreviewTarget[];
  selectedPreviewTarget: PreviewTarget | null;
  onSelectPreviewTarget: (deviceId: string | null) => void;
}) {
  const { token, user } = useAuth();
  const { primaryRunnerByDevice, primaryModelByDevice } = usePrimaryRunnerByDevice(token);
  const [projects, setProjects] = useState<Project[]>([]);
  const [runners, setRunners] = useState<Runner[]>([]);
  const [selectedProjectPath, setSelectedProjectPath] = useState("");
  const [selectedRunner, setSelectedRunner] = useState("");
  const [selectedModel, setSelectedModel] = useState("");
  // OpenCode-specific: which agent (build / plan / custom) drives the
  // task. Maps to `--agent <mode>` on `opencode run`. Empty = the
  // user's defaultAgent in opencode.json. Other runners ignore it.
  const [selectedMode, setSelectedMode] = useState("");
  const [taskList, setTaskList] = useState<Task[]>([]);
  const [activeTaskId, setActiveTaskId] = useState("");
  const placementStatusSyncRef = useRef<Set<string>>(new Set());
  const pendingDispatchRef = useRef<Set<string>>(new Set());
  // Deep ask graph (investigate → answer → verify) — set when a broad
  // architectural question auto-escalates from a single agent to a graph.
  // While active, the main panel shows graph progress instead of a task.
  const [activeGraphRunId, setActiveGraphRunId] = useState<string | null>(null);
  const [graphRun, setGraphRun] = useState<AgentGraphRun | null>(null);
  const [graphNodeOutput, setGraphNodeOutput] = useState("");
  // Cost mode for multi-step graph runs: 0 = single-model (default, your plan),
  // 2 = duo (Claude Code + GLM), 3 = trio (Claude Code + Codex + GLM). Spreads
  // independent slices across the lanes — coherence stays on the flat
  // subscription plans, parallel overflow spills to the cheap GLM apikey lane.
  const [hybridDegree, setHybridDegree] = useState<number>(0);
  const [composer, setComposer] = useState("");
  const [draftTitle, setDraftTitle] = useState("");
  // Video summary toggle — when on, the agent records a short MP4 demo
  // after the task completes (vibe-preview pipeline). Surfaces as a
  // "▶ Watch demo" button on the task row when ready.
  const [videoSummaryEnabled, setVideoSummaryEnabled] = useState(false);
  // Currently-playing clip id; opens a fullscreen <video> overlay.
  const [activeClipId, setActiveClipId] = useState<string | null>(null);
  // OpenCode agents discovered via /runner/opencode/config — used to
  // populate the agent dropdown below. Includes built-ins (build,
  // plan) plus any custom agents the user has defined under
  // `agent.<name>` in their opencode.json. Re-fetched whenever the
  // connected device changes so a phone driving a Mac mini sees that
  // mac mini's local agents.
  const [opencodeAgents, setOpencodeAgents] = useState<Array<{ name: string; model?: string; isBuiltin?: boolean }>>([]);

  useEffect(() => {
    // `connected` is declared further down; recompute it locally so this
    // effect can run before its top-level binding without TS hoisting
    // issues. Same expression as the const at line 216.
    const isConnected = connState === "connected" && !!connectedDevice;
    if (selectedRunner !== "opencode" || !isConnected) {
      setOpencodeAgents([]);
      return;
    }
    let cancelled = false;
    void (async () => {
      try {
        const cfg = await agentClient.openCodeConfig(connectedDevice?.id);
        if (cancelled) return;
        setOpencodeAgents((cfg?.agents || []) as any);
      } catch {
        // Silently fall back to hardcoded build/plan; the dropdown
        // below tolerates an empty agents list and shows the stock
        // entries inline.
      }
    })();
    return () => { cancelled = true; };
  }, [selectedRunner, connState, connectedDevice?.id]);
  const [busy, setBusy] = useState("");
  const [refreshNonce, setRefreshNonce] = useState(0);
  const [deployTargets, setDeployTargets] = useState<Array<{ id: string; name: string }>>([]);
  const [deployPreviewSummary, setDeployPreviewSummary] = useState<DeployPreviewSummary | null>(null);
  const [gitStatus, setGitStatus] = useState<GitStatusRow | null>(null);
  const [gitCommits, setGitCommits] = useState<GitCommitRow[]>([]);
  const [gitCommitMessage, setGitCommitMessage] = useState("");
  const [providers, setProviders] = useState<GitProviderStatusRow[]>([]);
  const [machines, setMachines] = useState<MachineInfo[]>([]);
  const [repoBrowserHost, setRepoBrowserHost] = useState<string | null>(null);
  const [providerRepos, setProviderRepos] = useState<GitRemoteRepo[]>([]);
  const [providerSearch, setProviderSearch] = useState("");
  const [providerToken, setProviderToken] = useState("");
  const [manualProvider, setManualProvider] = useState<"github" | "gitlab" | null>(null);
  const [projectSecrets, setProjectSecrets] = useState<Record<string, string>>({});
  const [savingSecretKey, setSavingSecretKey] = useState<string | null>(null);
  const [devStatus, setDevStatus] = useState<{
    running: boolean;
    framework?: string;
    workDir?: string;
    targetDeviceName?: string;
  } | null>(null);
  const [streamedOutput, setStreamedOutput] = useState("");
  const [sections, setSections] = useState<Record<SectionKey, SectionState>>(() => loadSectionState());
  const [customizeOpen, setCustomizeOpen] = useState(false);
  const [runnerAuthBusy, setRunnerAuthBusy] = useState(false);
  const [runnerAuthError, setRunnerAuthError] = useState<string | null>(null);
  const [runnerAuthSessionId, setRunnerAuthSessionId] = useState<string | null>(null);
  const [runnerAuthStatus, setRunnerAuthStatus] = useState<{
    runner: "claude" | "codex";
    status: "starting" | "awaiting_browser" | "completed" | "failed" | "cancelled";
    openUrl?: string;
    code?: string;
    detail?: string;
    error?: string;
  } | null>(null);

  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(SECTION_STORAGE_KEY, JSON.stringify(sections));
    } catch {}
  }, [sections]);

  const toggleSectionOpen = (key: SectionKey) =>
    setSections((prev) => ({ ...prev, [key]: { ...prev[key], open: !prev[key].open } }));

  const toggleSectionVisible = (key: SectionKey) =>
    setSections((prev) => ({ ...prev, [key]: { ...prev[key], visible: !prev[key].visible } }));

  const resetSections = () => setSections({ ...SECTION_DEFAULTS });

  const connected = connState === "connected" && !!connectedDevice;
  const visibleDevices = useMemo(() => devices, [devices]);
  const selectedProject = useMemo(
    () => projects.find((project) => project.path === selectedProjectPath) || null,
    [projects, selectedProjectPath],
  );
  const connectedMachine = useMemo(() => {
    if (!connectedDevice) return null;
    const normalize = (value?: string | null) =>
      String(value || "").trim().toLowerCase().replace(/\.local$/i, "");
    return (
      machines.find((machine) => machine.deviceId === connectedDevice.id) ||
      machines.find((machine) => normalize(machine.name) === normalize(connectedDevice.name)) ||
      null
    );
  }, [connectedDevice, machines]);
  const activeTask = useMemo(
    () => taskList.find((task) => task.id === activeTaskId) || taskList[0] || null,
    [taskList, activeTaskId],
  );

  useEffect(() => {
    const pending = listPendingCloudDispatches();
    if (pending.length === 0) return;
    setTaskList((prev) => {
      const known = new Set(prev.map((task) => task.id));
      const restored = pending
        .filter((row) => !known.has(row.localTaskId))
        .map(pendingCloudTaskPlaceholder);
      return restored.length ? [...restored, ...prev] : prev;
    });
  }, []);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    const run = async () => {
      const pending = listPendingCloudDispatches();
      if (pending.length === 0) return;
      let placements: TaskPlacementDecision[] = [];
      let pendingRows = pending;
      try {
        placements = await listRecentTaskPlacements(token, { limit: 50 });
      } catch {
        placements = [];
      }
      try {
        pendingRows = mergePendingCloudDispatchIntents(await listTaskDispatchIntents(token, { limit: 80 }));
        const placeholders = pendingRows.map(pendingCloudTaskPlaceholder);
        setTaskList((prev) => [
          ...placeholders,
          ...prev.filter((task) => !placeholders.some((pendingTask) => pendingTask.id === task.id)),
        ]);
      } catch {
        pendingRows = pending;
      }
      for (const row of pendingRows) {
        if (cancelled || pendingDispatchRef.current.has(row.localTaskId)) continue;
        let currentRow = row;
        if (currentRow.placementId) {
          try {
            currentRow = mergePendingCloudPlacementStatus(
              currentRow,
              await getTaskPlacementStatus(token, { placementId: currentRow.placementId }),
            );
            updatePendingCloudDispatch(currentRow.localTaskId, currentRow);
            setTaskList((prev) => prev.map((task) =>
              task.id === currentRow.localTaskId ? pendingCloudTaskPlaceholder(currentRow) : task,
            ));
          } catch {
            /* placement status is advisory; dispatch intents remain authoritative */
          }
        }
        if (pendingCloudDispatchNeedsUserAction(currentRow)) continue;
        const placement = currentRow.placementId
          ? placements.find((candidate) => candidate.id === currentRow.placementId)
          : undefined;
        const targetDeviceId = placement?.targetDeviceId || currentRow.targetDeviceId || undefined;
        if (placement?.targetDeviceId && placement.targetDeviceId !== currentRow.targetDeviceId) {
          updatePendingCloudDispatch(currentRow.localTaskId, {
            targetDeviceId: placement.targetDeviceId,
            placementLane: placement.lane,
            placementReason: placement.reason,
            placementCreditLabel: placementCreditLabel(placement) ?? undefined,
          });
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "queued",
            targetDeviceId: placement.targetDeviceId,
          }).catch(() => null);
        }
        if (!targetDeviceId) continue;
        if (connectedDevice?.id !== targetDeviceId || connState !== "connected") {
          const target = devices.find((device) => device.id === targetDeviceId && device.online && !device.needsAuth);
          if (target) void onSelectDevice(target);
          continue;
        }
        pendingDispatchRef.current.add(currentRow.localTaskId);
        try {
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "dispatching",
            targetDeviceId,
            clearBlockedAction: currentRow.clearedBlockedAction === true,
          }).catch(() => null);
          const task = await agentClient.createTask({ ...currentRow.params, allowLocalFallback: true });
          if (placement?.id || currentRow.placementId) {
            await rebindTaskPlacement(token, placement?.id ?? currentRow.placementId!, task.id, "running").catch(() => null);
          }
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "dispatched",
            taskId: task.id,
            targetDeviceId,
          }).catch(() => null);
          removePendingCloudDispatch(currentRow.localTaskId);
          setTaskList((prev) => [
            {
              ...task,
              placementId: placement?.id ?? currentRow.placementId,
              placementLane: placement?.lane ?? currentRow.placementLane,
              placementReason: placement?.reason ?? currentRow.placementReason,
              placementCreditLabel: placementCreditLabel(placement) ?? currentRow.placementCreditLabel,
            },
            ...prev.filter((item) => item.id !== currentRow.localTaskId && item.id !== task.id),
          ]);
          setActiveTaskId(task.id);
        } catch (err) {
          const message = err instanceof Error ? err.message : String(err);
          void updateTaskDispatchIntent(token, {
            intentId: currentRow.dispatchIntentId,
            localTaskId: currentRow.localTaskId,
            status: "failed",
            lastError: message,
            bumpAttempt: true,
          }).catch(() => null);
          updatePendingCloudDispatch(currentRow.localTaskId, {
            attempts: currentRow.attempts + 1,
            lastError: message,
          });
        } finally {
          pendingDispatchRef.current.delete(currentRow.localTaskId);
        }
      }
    };
    void run();
    const id = setInterval(() => void run(), 5000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [connState, connectedDevice?.id, devices, onSelectDevice, token]);
  const selectedRunnerRow = useMemo(
    () => runners.find((runner) => runner.id === selectedRunner) || null,
    [runners, selectedRunner],
  );
  const availableModels = selectedRunnerRow?.models || [];
  const conversationTurns = useMemo(
    () => (activeTask?.turns || []).filter((turn) => String(turn.content || "").trim()),
    [activeTask?.turns],
  );
  // Cap the live-output buffer the same way mobile does (8000 lines per
  // task). Codex / opencode tool runs spew bash stdout uncompressed; a
  // multi-hour vibing session against a remote agent can accumulate
  // 50k+ lines, each ~80–120 chars. Even on desktop browsers the
  // resulting multi-MB string + every prior chat bubble re-rendering
  // on stream tick is enough to thrash the layout pass. Keep the tail;
  // the agent retains the full transcript on disk.
  const liveOutput = useMemo(() => {
    const stream = streamedOutput.trim();
    if (stream) return stream;
    const lines = activeTask?.output;
    if (!lines || lines.length === 0) return "";
    const bounded = lines.length > 8000 ? lines.slice(-8000) : lines;
    return bounded.join("\n").trim();
  }, [streamedOutput, activeTask?.output]);
  const lastAssistantTurn = useMemo(() => {
    for (let i = conversationTurns.length - 1; i >= 0; i -= 1) {
      if (conversationTurns[i].role === "assistant") return conversationTurns[i].content.trim();
    }
    return "";
  }, [conversationTurns]);
  const showLiveOutput = !!liveOutput && liveOutput !== lastAssistantTurn;

  useEffect(() => {
    if (!connected) {
      setProjects([]);
      setRunners([]);
      const pending = listPendingCloudDispatches().map(pendingCloudTaskPlaceholder);
      setTaskList(pending);
      setActiveTaskId((current) => current || pending[0]?.id || "");
      setDeployTargets([]);
      return;
    }

    let cancelled = false;
    const refresh = async () => {
      try {
        const [projectRows, runnerRows, tasks, preview, currentDevStatus, git, commits, gitProviders, machineInventory] = await Promise.all([
          agentClient.listProjects().catch(() => []),
          agentClient.getRunners().catch(() => []),
          agentClient.listTasks(12).catch(() => []),
          selectedProjectPath ? agentClient.deployPreview(selectedProjectPath).catch(() => null) : Promise.resolve(null),
          agentClient.getDevServerStatus().catch(() => null),
          selectedProjectPath ? agentClient.gitStatus(selectedProjectPath).catch(() => null) : Promise.resolve(null),
          selectedProjectPath ? agentClient.gitLog(selectedProjectPath, 8).catch(() => []) : Promise.resolve([]),
          agentClient.gitProviderStatus().catch(() => []),
          agentClient.consoleMachines().catch(() => ({ machines: [] })),
        ]);
        if (cancelled) return;
        setProjects(projectRows);
        setRunners((runnerRows || []).filter((runner) => runner.installed));
        setTaskList((prev) => {
          const priorById = new Map(prev.map((task) => [task.id, task]));
          const pending = listPendingCloudDispatches().map(pendingCloudTaskPlaceholder);
          const merged = (tasks || []).map((task) => {
            const prior = priorById.get(task.id);
            if (!prior) return task;
            return {
              ...task,
              placementId: task.placementId || prior.placementId,
              placementLane: task.placementLane || prior.placementLane,
              placementReason: task.placementReason || prior.placementReason,
              placementCreditLabel: task.placementCreditLabel || prior.placementCreditLabel,
            };
          });
          return [...pending, ...merged.filter((task) => !pending.some((row) => row.id === task.id))];
        });
        setDevStatus(currentDevStatus);
        setGitStatus(git ?? null);
        setGitCommits(commits || []);
        setProviders(gitProviders || []);
        setMachines(machineInventory?.machines || []);
        const targets = Array.isArray(preview?.targets) ? preview.targets : [];
        setDeployTargets(targets.map((target: any) => ({ id: String(target.id), name: String(target.name) })));
        setDeployPreviewSummary(
          preview
            ? {
                warnings: Array.isArray(preview.warnings) ? preview.warnings : [],
                lastDeploy: preview.lastDeploy,
              }
            : null,
        );

        if (!selectedProjectPath && projectRows.length > 0) {
          setSelectedProjectPath(projectRows[0].path);
        }
        const installed = (runnerRows || []).filter((runner) => runner.installed);
        const ready = installed.filter((runner) => runner.ready);
        const explicitRunner = connectedDevice ? primaryRunnerByDevice[connectedDevice.id] : "";
        if (explicitRunner && installed.some((runner) => runner.id === explicitRunner) && selectedRunner !== explicitRunner) {
          setSelectedRunner(explicitRunner);
        } else if (!selectedRunner) {
          const seededRunner = connectedDevice
            ? preferredDefaultRunnerForDevice(
                connectedDevice,
                user?.email,
                ready.map((runner) => runner.id).concat(installed.map((runner) => runner.id)),
              )
            : null;
          const preferred =
            ready.find((runner) => runner.id === seededRunner) ||
            installed.find((runner) => runner.id === seededRunner) ||
            ready.find((runner) => runner.active) ||
            ready.find((runner) => runner.id === "claude") ||
            ready.find((runner) => runner.id === "opencode") ||
            ready.find((runner) => runner.id === "codex") ||
            installed.find((runner) => runner.active) ||
            installed.find((runner) => runner.id === "claude") ||
            installed.find((runner) => runner.id === "opencode") ||
            installed.find((runner) => runner.id === "codex") ||
            installed[0];
          if (preferred) setSelectedRunner(preferred.id);
        }
        if (!activeTaskId && tasks.length > 0) {
          setActiveTaskId(tasks[0].id);
        }
      } catch (error) {
        if (!cancelled) setBusy(error instanceof Error ? error.message : String(error));
      }
    };

    void refresh();
    const interval = setInterval(refresh, 4000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [activeTaskId, connected, connectedDevice, primaryRunnerByDevice, refreshNonce, selectedProjectPath, selectedRunner, user?.email]);

  useEffect(() => {
    if (!selectedRunnerRow || availableModels.length === 0) {
      setSelectedModel("");
      return;
    }
    const explicitModel = connectedDevice ? primaryModelByDevice[connectedDevice.id] : "";
    if (explicitModel && availableModels.some((model) => model.id === explicitModel) && selectedModel !== explicitModel) {
      setSelectedModel(explicitModel);
      return;
    }
    if (selectedModel && availableModels.some((model) => model.id === selectedModel)) {
      return;
    }
    const seededModel = connectedDevice
      ? preferredDefaultModelForRunner(selectedRunnerRow.id, connectedDevice, user?.email)
      : null;
    const preferred =
      (explicitModel && availableModels.find((model) => model.id === explicitModel)) ||
      (seededModel && availableModels.find((model) => model.id === seededModel)) ||
      availableModels.find((model) => model.isDefault) ||
      availableModels[0];
    setSelectedModel(preferred?.id || "");
  }, [availableModels, connectedDevice, primaryModelByDevice, selectedModel, selectedRunnerRow, user?.email]);

  useEffect(() => {
    if (!runnerAuthSessionId) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const session = await agentClient.getRunnerBrowserAuthStatus(runnerAuthSessionId);
        if (cancelled) return;
        setRunnerAuthStatus({
          runner: session.runner,
          status: session.status,
          openUrl: session.openUrl,
          code: session.code,
          detail: session.detail,
          error: session.error,
        });
        if (session.status === "completed") {
          setRunnerAuthBusy(false);
          setRunnerAuthError(null);
          setBusy(`${session.runner === "claude" ? "Claude Code" : "Codex"} is ready on this machine.`);
          setRefreshNonce((value) => value + 1);
          setTimeout(() => {
            setRunnerAuthSessionId(null);
            setRunnerAuthStatus(null);
          }, 1500);
        } else if (session.status === "failed" || session.status === "cancelled") {
          setRunnerAuthBusy(false);
          setRunnerAuthError(session.error || session.detail || "Sign-in did not complete.");
        }
      } catch (error) {
        if (!cancelled) {
          setRunnerAuthBusy(false);
          setRunnerAuthError(error instanceof Error ? error.message : String(error));
        }
      }
    };
    void poll();
    const interval = setInterval(() => void poll(), 1500);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [runnerAuthSessionId]);

  useEffect(() => {
    if (!activeTask) {
      setStreamedOutput("");
      return;
    }
    setStreamedOutput(capStreamText(activeTask.output.join("\n")));
    const stop = agentClient.streamTaskOutput(activeTask.id, (chunk) => {
      // Cap HERE, at the write. The useMemo below only bounded the fallback
      // branch, so while streaming — the one case that matters — the buffer
      // grew without limit and every chunk re-stripped + re-parsed the whole
      // transcript.
      setStreamedOutput((prev) => capStreamText(prev + extractOutputText(chunk)));
    });
    return stop;
  }, [activeTask?.id]);

  useEffect(() => {
    if (!token || !activeTask?.placementId) return;
    const nextStatus =
      activeTask.status === "completed"
        ? "completed"
        : activeTask.status === "failed" || activeTask.status === "stopped"
          ? "failed"
          : activeTask.status === "queued"
            ? "queued"
            : "running";
    const key = `${activeTask.placementId}:${nextStatus}`;
    if (placementStatusSyncRef.current.has(key)) return;
    placementStatusSyncRef.current.add(key);
    void markTaskPlacementStatus(token, activeTask.placementId, nextStatus).catch(() => {
      placementStatusSyncRef.current.delete(key);
    });
  }, [token, activeTask?.placementId, activeTask?.status]);

  // Poll the active deep-ask graph until it reaches a terminal status.
  useEffect(() => {
    if (!activeGraphRunId) {
      setGraphRun(null);
      return;
    }
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      const run = await agentClient.getAgentGraph(activeGraphRunId).catch(() => null);
      if (cancelled) return;
      if (run) setGraphRun(run);
      const terminal = run && ["completed", "failed", "stopped"].includes(run.status);
      if (!terminal) timer = setTimeout(() => void poll(), 1500);
    };
    void poll();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [activeGraphRunId]);

  // Stream the currently-running graph node's live output so the user sees
  // the agent working, not just a status pill.
  const runningGraphNode = useMemo(
    () => graphRun?.nodes.find((n) => n.status === "running" && n.taskId) ?? null,
    [graphRun],
  );
  useEffect(() => {
    if (!runningGraphNode?.taskId) {
      setGraphNodeOutput("");
      return;
    }
    setGraphNodeOutput("");
    const stop = agentClient.streamTaskOutput(runningGraphNode.taskId, (chunk) => {
      setGraphNodeOutput((prev) => capStreamText(prev + extractOutputText(chunk)));
    });
    return stop;
  }, [runningGraphNode?.taskId]);

  useEffect(() => {
    if (!selectedProject) {
      setProjectSecrets({});
      return;
    }
    let cancelled = false;
    const loadSecrets = async () => {
      const next: Record<string, string> = {};
      for (const secret of PROJECT_SECRET_TEMPLATES) {
        try {
          const entry = await agentClient.vaultGet(projectVaultEntryName(selectedProject.path, secret.id));
          next[secret.id] = entry.value;
        } catch {
          next[secret.id] = "";
        }
      }
      if (!cancelled) setProjectSecrets(next);
    };
    void loadSecrets();
    return () => {
      cancelled = true;
    };
  }, [selectedProject?.path]);

  async function rememberProjectProfile(kind: TaskPlacementKind): Promise<Partial<TaskPlacementRequest>> {
    const projectSlug = projectSlugForPlacement(selectedProject?.path || selectedProject?.name);
    if (!token || !projectSlug || !selectedProject) return { projectSlug };
    const stack = selectedProject.framework || devStatus?.framework || undefined;
    const hasNativeMobile = /react-native|expo|ios|android|mobile|hermes/i.test(stack || selectedProject.name || "");
    const hasDocker = /docker/i.test(stack || "");
    const appCount = Math.min(100, Math.max(1, projects.length || 1));
    const resourceClass = resourceClassFromProjectHints({
      kind,
      framework: stack,
      appCount,
      hasNativeMobile,
      hasDocker,
    });
    void upsertProjectProfile(token, {
      projectSlug,
      sourceDeviceId: connectedDevice?.id,
      stack,
      appCount,
      hasNativeMobile,
      hasDocker,
      resourceClass,
      confidence: 0.65,
    }).catch(() => {});
    return { projectSlug, appCount, hasNativeMobile, hasDocker };
  }

  async function startChatTask() {
    if (!selectedProject || !composer.trim()) {
      setBusy("Pick a project and enter a prompt.");
      return;
    }
    const promptText = composer.trim();

    // Deep ask escalation: a broad / architectural QUESTION ("how does auth
    // work end to end?") runs a multi-agent graph (investigate → answer →
    // verify) instead of a single agent. Narrow questions stay single-agent
    // ask mode (askMode below); build instructions stay normal tasks.
    if (detectAskIntent(promptText) && detectAskBreadth(promptText)) {
      setBusy("Deep ask — investigate → answer → verify…");
      const res = await agentClient.createAgentGraph({
        name: "ask",
        workDir: selectedProject.path,
        prompt: promptText,
        runner: selectedRunner || undefined,
        model: selectedModel || undefined,
        template: "ask",
        hybridDegree: hybridDegree || undefined,
      });
      if (!res.ok || !res.run) {
        setBusy(res.error || "Failed to start deep ask.");
        return;
      }
      setComposer("");
      setDraftTitle("");
      setActiveTaskId("");
      setActiveGraphRunId(res.run.id);
      setGraphRun(res.run);
      setBusy(`Deep ask started (${res.run.nodes?.length ?? 3} steps).`);
      setRefreshNonce((value) => value + 1);
      return;
    }

    setBusy("Starting coding task…");
    // Leaving any prior deep-ask graph view when starting a normal task.
    setActiveGraphRunId(null);
    const title = draftTitle.trim() || summarizeTitle(composer, selectedProject.name);
    let placementPreview: TaskPlacementDecision | null = null;
    const placementKind = inferTaskPlacementKind(promptText);
    const profileHints = await rememberProjectProfile(placementKind);
    const placementRequest = {
      kind: placementKind,
      sourceSurface: "web-vibe-coding",
      requestedRunner: selectedRunner || undefined,
      targetDeviceId: connectedDevice?.id,
      ...profileHints,
      hasNativeMobile:
        profileHints.hasNativeMobile ||
        deployTargets.some((target) => /ios|android|expo|apk|ipa|mobile/i.test(`${target.id} ${target.name}`)),
      hasDocker: profileHints.hasDocker || (devStatus?.framework ? /docker/i.test(devStatus.framework) : undefined),
    };
    if (token) {
      placementPreview = await previewTaskPlacement(token, placementRequest).catch(() => null);
    }
    if (shouldConfirmExpensiveCloudPlacement(placementPreview)) {
      const ok = window.confirm(expensiveCloudPlacementMessage(placementPreview));
      if (!ok) {
        setBusy("Heavy Cloud Workspace task cancelled.");
        return;
      }
    }
    const cloudTargetIsCurrent =
      !!placementPreview?.lane?.startsWith("cloud_") &&
      !!placementPreview.targetDeviceId &&
      placementPreview.targetDeviceId === connectedDevice?.id;
    if (token && placementPreview?.lane?.startsWith("cloud_") && (!cloudTargetIsCurrent || shouldDeferTaskForCloudWorkspace(placementPreview))) {
      const pendingId = pendingPlacementTaskId();
      const recorded = await recordTaskPlacement(token, {
        ...placementRequest,
        taskId: pendingId,
      }).catch(() => null);
      const now = Date.now();
      const pending: PendingCloudDispatch = {
        localTaskId: pendingId,
        placementId: recorded?.id,
        placementLane: recorded?.lane ?? placementPreview?.lane ?? undefined,
        placementReason: recorded?.reason ?? placementPreview?.reason ?? undefined,
        placementCreditLabel: placementCreditLabel(recorded ?? placementPreview) ?? undefined,
        targetDeviceId: recorded?.targetDeviceId ?? placementPreview?.targetDeviceId ?? null,
        params: {
          title,
          description: buildVibeTaskPrompt({
            project: selectedProject,
            prompt: composer.trim(),
            gitStatus,
            deployTargets,
            machine: connectedMachine,
          }),
          userPrompt: composer.trim(),
          runner: selectedRunner || undefined,
          model: selectedModel || undefined,
          mode: selectedRunner === "opencode" && selectedMode ? selectedMode : undefined,
          projectName: selectedProject.name,
          workDir: selectedProject.path,
          videoEnabled: videoSummaryEnabled,
          askMode: detectAskIntent(composer.trim()),
        },
        createdAt: now,
        updatedAt: now,
        attempts: 0,
      };
      savePendingCloudDispatch(pending);
      createTaskDispatchIntent(token, {
        localTaskId: pendingId,
        placementId: recorded?.id,
        sourceSurface: placementRequest.sourceSurface,
        lane: recorded?.lane ?? placementPreview?.lane ?? undefined,
        targetDeviceId: recorded?.targetDeviceId ?? placementPreview?.targetDeviceId ?? null,
        cloudMachineId: recorded?.cloudMachineId ?? placementPreview?.cloudMachineId ?? null,
        requestedRunner: placementRequest.requestedRunner,
        projectSlug: placementRequest.projectSlug,
        reason: recorded?.reason ?? placementPreview?.reason ?? undefined,
      }).then((intent) => {
        updatePendingCloudDispatch(pendingId, {
          dispatchIntentId: intent.id,
          dispatchStatus: intent.status,
          dispatchExpiresAt: intent.expiresAt,
        });
      }).catch(() => null);
      if (recorded?.id) {
        void activateTaskPlacement(token, { placementId: recorded.id }).then((activation) => {
          const blockedReason = activationBlockReason(activation);
          if (!blockedReason) return;
          updatePendingCloudDispatch(pendingId, {
            dispatchStatus: "blocked",
            blockedAction: activation.action,
            blockedReason,
          });
          void updateTaskDispatchIntent(token, {
            localTaskId: pendingId,
            status: "blocked",
            blockedAction: activation.action,
            reason: blockedReason,
          }).catch(() => null);
        }).catch(() => {});
      }
      const pendingTask = pendingCloudTaskPlaceholder(pending);
      setComposer("");
      setDraftTitle("");
      setTaskList((prev) => [pendingTask, ...prev.filter((row) => row.id !== pendingTask.id)]);
      setActiveTaskId(pendingTask.id);
      setBusy("Cloud Workspace is waking. Task is queued locally; it was not sent to the wrong machine.");
      setRefreshNonce((value) => value + 1);
      return;
    }
    if (selectedRunnerRow && selectedRunnerRow.ready === false) {
      setBusy(selectedRunnerRow.error || selectedRunnerRow.warning || `${selectedRunnerRow.name} is installed but not ready on this machine.`);
      return;
    }
    const taskParams = {
      title,
      description: buildVibeTaskPrompt({
        project: selectedProject,
        prompt: composer.trim(),
        gitStatus,
        deployTargets,
        machine: connectedMachine,
      }),
      userPrompt: composer.trim(),
      runner: selectedRunner || undefined,
      model: selectedModel || undefined,
      mode: selectedRunner === "opencode" && selectedMode ? selectedMode : undefined,
      projectName: selectedProject.name,
      workDir: selectedProject.path,
      videoEnabled: videoSummaryEnabled,
      // Console auto-detect: a natural-language question ("how do I test
      // STT/TTS?") routes to ask mode — deep grounded analysis, explain-first
      // — instead of a work run. High-precision; imperative build prompts are
      // left as normal tasks. See lib/ask-intent.ts.
      askMode: detectAskIntent(composer.trim()),
    };
    let task: Task;
    try {
      task = await agentClient.createTask(taskParams);
    } catch (err) {
      if (!(err instanceof CloudWorkspaceRequiredError)) throw err;
      const pending = saveCloudWorkspaceRequiredDispatch({
        err,
        params: taskParams,
        token,
        sourceSurface: placementRequest.sourceSurface,
        requestedRunner: placementRequest.requestedRunner,
        projectSlug: placementRequest.projectSlug,
      });
      const pendingTask = pendingCloudTaskPlaceholder(pending);
      setComposer("");
      setDraftTitle("");
      setTaskList((prev) => [pendingTask, ...prev.filter((row) => row.id !== pendingTask.id)]);
      setActiveTaskId(pendingTask.id);
      setBusy("Cloud Workspace is waking. Task is queued locally; it was not sent to the wrong machine.");
      setRefreshNonce((value) => value + 1);
      return;
    }
    let nextTask: Task = {
      ...task,
      placementLane: placementPreview?.lane ?? undefined,
      placementReason: placementPreview?.reason ?? undefined,
      placementCreditLabel: placementCreditLabel(placementPreview) ?? undefined,
    };
    if (token) {
      const recorded = await recordTaskPlacement(token, {
        ...placementRequest,
        taskId: task.id,
      }).catch(() => null);
      if (recorded) {
        nextTask = {
          ...nextTask,
          placementId: recorded.id,
          placementLane: recorded.lane,
          placementReason: recorded.reason ?? undefined,
          placementCreditLabel: placementCreditLabel(recorded) ?? undefined,
        };
        if (recorded.id && recorded.lane.startsWith("cloud_")) {
          void activateTaskPlacement(token, { placementId: recorded.id }).catch(() => {});
        }
      }
    }
    setComposer("");
    setDraftTitle("");
    setTaskList((prev) => [nextTask, ...prev.filter((row) => row.id !== nextTask.id)]);
    setActiveTaskId(nextTask.id);
    setBusy(`Started ${nextTask.title}.`);
    setRefreshNonce((value) => value + 1);
  }

  async function continueChatTask() {
    if (!activeTask || !composer.trim()) return;
    if (selectedRunnerRow && selectedRunnerRow.ready === false) {
      setBusy(selectedRunnerRow.error || selectedRunnerRow.warning || `${selectedRunnerRow.name} is installed but not ready on this machine.`);
      return;
    }
    const promptText = buildVibeContinuationPrompt({
      project: selectedProject,
      prompt: composer.trim(),
      gitStatus,
      deployTargets,
      machine: connectedMachine,
    });

    // Runtime agent switch: if the user picked a different runner in the
    // composer than the parent task's runner, fork a child task with
    // the new runner instead of continuing the parent. Keeps the parent
    // session immutable (Claude/Codex/OpenCode don't share session
    // formats) while carrying a bounded recent-context handoff to the
    // new runner. See task_fork.go and
    // CODING_AGENT_CHANGE_FROM_MOBILE_APP_CHAT.md.
    const parentRunner = (activeTask.runnerId || "").trim();
    const desiredRunner = (selectedRunner || "").trim();
    const switching = !!desiredRunner && !!parentRunner && desiredRunner !== parentRunner;

    if (switching) {
      // Confirm before forking — this creates a new child task and
      // changes which task subsequent sends target. The recent-context
      // copy avoids forwarding the full transcript by default.
      const niceName = desiredRunner.charAt(0).toUpperCase() + desiredRunner.slice(1);
      const confirmed = typeof window !== "undefined"
        ? window.confirm(
            `Switching to ${niceName} will start a new child chat. ` +
            `Yaver will include the most recent part of this conversation as context ` +
            `so the new agent can pick up where you left off.\n\n` +
            `For speed and token safety, Yaver sends roughly the last ~1200 words plus ` +
            `the latest task summary, not the entire chat history.`,
          )
        : true;
      if (!confirmed) {
        setBusy("");
        return;
      }
      try {
        analyticsAgentSwitch("agent_switch_requested", { from: parentRunner, to: desiredRunner });
      } catch { /* analytics is best-effort */ }
      setBusy(`Switching ${activeTask.title} to ${niceName}…`);
      try {
        const result = await agentClient.forkTask(activeTask.id, {
          runner: desiredRunner,
          model: selectedModel || undefined,
          mode: selectedMode || undefined,
          input: promptText,
        });
        setComposer("");
        setActiveTaskId(result.taskId);
        setBusy(`Forked to ${niceName} — ${result.contextWordsUsed} words of context carried.`);
        setRefreshNonce((value) => value + 1);
        try {
          analyticsAgentSwitch("agent_switch_completed", {
            from: parentRunner,
            to: desiredRunner,
            contextWords: result.contextWordsUsed,
          });
        } catch { /* analytics is best-effort */ }
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        setBusy(`Switch to ${niceName} failed: ${msg}`);
        try {
          analyticsAgentSwitch("agent_switch_failed", { from: parentRunner, to: desiredRunner, error: msg });
        } catch { /* analytics is best-effort */ }
      }
      return;
    }

    setBusy(`Continuing ${activeTask.title}…`);
    await agentClient.continueTask(activeTask.id, promptText);
    setComposer("");
    setBusy(`Continued ${activeTask.title}.`);
  }

  async function startPreview() {
    if (!selectedProject) return;
    await agentClient.startDevServer({
      framework: selectedProject.framework || "",
      workDir: selectedProject.path,
      platform: previewPlatformForFramework(selectedProject.framework),
      targetDeviceId: selectedPreviewTarget?.id,
      targetDeviceName: selectedPreviewTarget?.name,
      targetDeviceClass: selectedPreviewTarget?.deviceClass,
    });
    setBusy(`Started preview for ${selectedProject.name}.`);
    setRefreshNonce((value) => value + 1);
  }

  async function deploy(targetId?: string) {
    if (!selectedProject) return;
    setBusy(`Deploying ${selectedProject.name}…`);
    const result = await agentClient.deployRun(selectedProject.path);
    const deployName = result?.target || targetId || "deploy";
    setBusy(`Triggered ${deployName}.`);
    setRefreshNonce((value) => value + 1);
  }

  async function launchDeployTask(kind: DeployActionKind) {
    if (!selectedProject) {
      setBusy("Pick a project first.");
      return;
    }
    const plan = buildDeployTaskPlan({
      kind,
      project: selectedProject,
      machine: connectedMachine,
      deployTargets,
      gitStatus,
    });
    setBusy(plan.startingLabel);
    const task = await agentClient.createTask({
      title: plan.title,
      description: plan.prompt,
      userPrompt: plan.userPrompt,
      runner: selectedRunner || undefined,
      model: selectedModel || undefined,
      projectName: selectedProject.name,
      workDir: selectedProject.path,
      videoEnabled: videoSummaryEnabled,
    });
    setActiveGraphRunId(null);
    setActiveTaskId(task.id);
    setBusy(plan.startedLabel);
    setRefreshNonce((value) => value + 1);
  }

  function prepareDeployPrompt(kind: DeployActionKind) {
    if (!selectedProject) return;
    const plan = buildDeployTaskPlan({
      kind,
      project: selectedProject,
      machine: connectedMachine,
      deployTargets,
      gitStatus,
    });
    setDraftTitle(plan.title);
    setComposer(plan.userPrompt);
    setBusy(plan.preparedLabel);
  }

  async function autoDetectProviders() {
    setBusy("Detecting git providers from the machine…");
    const detected = await agentClient.gitProviderDetect();
    setProviders(await agentClient.gitProviderStatus());
    setBusy(detected.length > 0 ? `Detected ${detected.map((item) => item.provider).join(", ")}.` : "No git providers detected.");
  }

  async function saveProviderToken() {
    if (!manualProvider || !providerToken.trim()) return;
    setBusy(`Saving ${manualProvider} token to machine vault…`);
    const result = await agentClient.gitProviderSetup({ provider: manualProvider, token: providerToken.trim() });
    if (!result.ok) {
      setBusy(result.error || "Could not save provider token.");
      return;
    }
    setProviderToken("");
    setManualProvider(null);
    setProviders(await agentClient.gitProviderStatus());
    setBusy(`Connected ${result.provider || manualProvider} as ${result.username || "user"}.`);
  }

  async function toggleProviderRepos(host: string) {
    if (repoBrowserHost === host) {
      setRepoBrowserHost(null);
      setProviderRepos([]);
      return;
    }
    setRepoBrowserHost(host);
    setBusy(`Loading repos from ${host}…`);
    const repos = await agentClient.gitProviderRepos(host);
    setProviderRepos(repos);
    setBusy(`Loaded ${repos.length} repos from ${host}.`);
  }

  async function cloneRemoteRepo(repo: GitRemoteRepo) {
    const url = repo.sshUrl || repo.cloneUrl;
    if (!url) return;
    setBusy(`Cloning ${repo.fullName}…`);
    const result = await agentClient.cloneRepo(url);
    if (!result?.ok) {
      setBusy(result?.error || `Could not clone ${repo.fullName}.`);
      return;
    }
    const nextProjects = await agentClient.listProjects().catch(() => []);
    if (nextProjects.length > 0) {
      setProjects(nextProjects);
      if (result.path && nextProjects.some((project: Project) => project.path === result.path)) {
        setSelectedProjectPath(result.path);
      }
    }
    setBusy(`Cloned ${repo.fullName}${result.path ? ` → ${result.path}` : ""}.`);
  }

  async function removeProvider(host: string) {
    await agentClient.gitProviderRemove(host);
    setProviders(await agentClient.gitProviderStatus());
    if (repoBrowserHost === host) {
      setRepoBrowserHost(null);
      setProviderRepos([]);
    }
    setBusy(`Removed ${host}.`);
  }

  async function runGitAction(action: "pull" | "push" | "stash" | "stash-pop" | "commit" | "revert-head") {
    if (!selectedProject) return;
    try {
      if (action === "commit") {
        if (!gitCommitMessage.trim()) {
          setBusy("Enter a commit message first.");
          return;
        }
        setBusy(`Committing ${selectedProject.name}…`);
        const result = await agentClient.gitCommit(selectedProject.path, gitCommitMessage.trim());
        setGitCommitMessage("");
        setBusy(result.hash ? `Committed ${result.hash.trim()}.` : result.message || "Committed changes.");
      } else if (action === "pull") {
        setBusy(`Pulling ${selectedProject.name}…`);
        const result = await agentClient.gitPull(selectedProject.path);
        setBusy(result.message || "Pulled latest changes.");
      } else if (action === "push") {
        setBusy(`Pushing ${selectedProject.name}…`);
        const result = await agentClient.gitPush(selectedProject.path);
        setBusy(result.message || "Pushed branch.");
      } else if (action === "stash") {
        setBusy(`Stashing changes in ${selectedProject.name}…`);
        const result = await agentClient.gitStash(selectedProject.path);
        setBusy(result.message || "Stashed changes.");
      } else if (action === "stash-pop") {
        setBusy(`Restoring stash in ${selectedProject.name}…`);
        const result = await agentClient.gitStashPop(selectedProject.path);
        setBusy(result.message || "Restored stash.");
      } else if (action === "revert-head") {
        const head = gitCommits[0]?.hash;
        if (!head) {
          setBusy("No commit available to revert.");
          return;
        }
        setBusy(`Reverting ${gitCommits[0]?.shortHash || "HEAD"}…`);
        const result = await agentClient.gitRevert(selectedProject.path, head);
        setBusy(result.message || `Reverted ${gitCommits[0]?.shortHash || "HEAD"}.`);
      }
    } catch (error) {
      setBusy(error instanceof Error ? error.message : String(error));
    } finally {
      setRefreshNonce((value) => value + 1);
    }
  }

  async function launchWorkflowShortcut(mode: "sync" | "resolve" | "ship") {
    if (!selectedProject) return;
    const nextPrompt =
      mode === "sync"
        ? "Sync this project with origin. Prefer rebase over merge, resolve any conflicts carefully, keep my local work intact, run a quick sanity check, then summarize the final branch state."
        : mode === "resolve"
          ? "Inspect the current git state, resolve any merge or rebase conflicts completely, finish the interrupted git operation cleanly, run a sanity check, commit if needed, and tell me exactly what changed."
          : "Finalize this project for shipping: verify the tree, commit any intended changes with a clear message, push the current branch, deploy through the detected path, and report the resulting URL or machine-hosted address.";
    setComposer(nextPrompt);
    setDraftTitle(
      mode === "sync"
        ? `${selectedProject.name} — sync branch`
        : mode === "resolve"
          ? `${selectedProject.name} — resolve conflicts`
          : `${selectedProject.name} — push and deploy`,
    );
    setBusy(
      mode === "sync"
        ? "Prepared a sync/rebase workflow prompt."
        : mode === "resolve"
          ? "Prepared a conflict-resolution workflow prompt."
          : "Prepared a commit/push/deploy workflow prompt.",
    );
  }

  async function saveProjectSecret(secretId: string, value: string) {
    if (!selectedProject) return;
    setSavingSecretKey(secretId);
    try {
      const meta = PROJECT_SECRET_TEMPLATES.find((item) => item.id === secretId);
      if (!meta) return;
      await agentClient.vaultSet({
        name: projectVaultEntryName(selectedProject.path, secretId),
        category: "api-key",
        value,
        notes: `project=${selectedProject.path}; key=${meta.label}; source=web-vibe`,
      });
      setBusy(`Saved ${meta.label} for ${selectedProject.name} to machine vault.`);
    } finally {
      setSavingSecretKey(null);
    }
  }

  async function startSelectedRunnerSignIn() {
    if (!selectedRunnerRow) return;
    if (selectedRunnerRow.id !== "claude" && selectedRunnerRow.id !== "codex") {
      setRunnerAuthError(`${selectedRunnerRow.name} does not expose browser sign-in here.`);
      return;
    }
    setRunnerAuthBusy(true);
    setRunnerAuthError(null);
    setRunnerAuthStatus({
      runner: selectedRunnerRow.id,
      status: "starting",
    });
    try {
      const session = await agentClient.startRunnerBrowserAuth(selectedRunnerRow.id);
      setRunnerAuthSessionId(session.id);
      setRunnerAuthStatus({
        runner: session.runner,
        status: session.status,
        openUrl: session.openUrl,
        code: session.code,
        detail: session.detail,
        error: session.error,
      });
      if (session.openUrl && typeof window !== "undefined") {
        window.open(session.openUrl, "_blank", "noopener,noreferrer");
      }
    } catch (error) {
      setRunnerAuthBusy(false);
      setRunnerAuthError(error instanceof Error ? error.message : String(error));
    }
  }

  const filteredProviderRepos = useMemo(() => {
    const needle = providerSearch.trim().toLowerCase();
    if (!needle) return providerRepos;
    return providerRepos.filter((repo) =>
      repo.name.toLowerCase().includes(needle) ||
      repo.fullName.toLowerCase().includes(needle) ||
      String(repo.description || "").toLowerCase().includes(needle),
    );
  }, [providerRepos, providerSearch]);

  const machineSummary = useMemo(() => {
    if (!connectedMachine) return null;
    const caps = connectedMachine.capabilities;
    return {
      platform: connectedMachine.platform || connectedMachine.os || "machine",
      supportsIos: !!caps?.supportsIos,
      supportsAndroid: !!caps?.supportsAndroid,
      supportsTestFlight: !!caps?.supportsTestFlight,
      supportsPlayStore: !!caps?.supportsPlayStore,
      supportsDocker: !!caps?.supportsDocker,
      runnerNames: (caps?.runners || [])
        .filter((runner) => runner.ready)
        .map((runner) => runner.name),
      currentWorkDir: connectedMachine.currentWorkDir || "",
    };
  }, [connectedMachine]);

  // Live, structured deploy-readiness from the new
  // /deploy/capabilities endpoint. Overrides the snapshot-based
  // booleans below so the buttons reflect the agent's actual
  // tools+secrets+path probe instead of the cached MachineInfo —
  // a stale snapshot is the most common reason a "ready" button
  // fails halfway through xcodebuild.
  const [liveDeployCaps, setLiveDeployCaps] = useState<Record<
    string,
    { canDeploy: boolean; reason?: string }
  >>({});
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const report = await agentClient.deployCapabilities({});
        if (cancelled) return;
        const map: Record<string, { canDeploy: boolean; reason?: string }> = {};
        report.targets.forEach((t) => {
          map[t.target] = { canDeploy: t.canDeploy, reason: t.reason };
        });
        setLiveDeployCaps(map);
      } catch {
        // older agent without /deploy/capabilities — fall through
        // to the snapshot-based readiness we already had.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [connectedMachine?.deviceId, refreshNonce]);

  const deployQuickActions = useMemo(() => {
    const base = buildDeployQuickActions(selectedProject, connectedMachine, deployTargets);
    // Map the UI's deploy-action kinds onto the agent's catalogue
    // target names. EAS isn't in the agent catalogue today — leave
    // its enabled flag to the framework-based fallback.
    const kindToTarget: Record<DeployActionKind, string | null> = {
      testflight: "testflight",
      "play-internal": "playstore",
      eas: null,
    };
    return base.map((action) => {
      const target = kindToTarget[action.kind];
      if (!target) return action;
      const live = liveDeployCaps[target];
      if (!live) return action;
      return {
        ...action,
        enabled: live.canDeploy,
        readiness: live.canDeploy
          ? `Live probe: ${target} is deploy-ready on this host.`
          : `Live probe blocked: ${live.reason ?? "host can't ship to " + target + " right now."}`,
      };
    });
  }, [selectedProject, connectedMachine, deployTargets, liveDeployCaps]);

  // Resolve the clip URL once when the user hits "▶ Watch demo".
  // We can't put the auth token in <video src> directly (browser
  // ignores headers), so we fetch the MP4 as a blob and render the
  // object URL. Same blob-shim the standalone VibePreviewView uses.
  const [activeClipBlobUrl, setActiveClipBlobUrl] = useState<string | null>(null);
  useEffect(() => {
    if (!activeClipId) {
      if (activeClipBlobUrl) URL.revokeObjectURL(activeClipBlobUrl);
      setActiveClipBlobUrl(null);
      return;
    }
    const req = agentClient.vibeClipRequest(activeClipId);
    if (!req) return;
    let cancelled = false;
    let url: string | null = null;
    void (async () => {
      try {
        const res = await fetch(req.url, { headers: req.headers });
        if (!res.ok) return;
        const blob = await res.blob();
        url = URL.createObjectURL(blob);
        if (!cancelled) setActiveClipBlobUrl(url);
      } catch { /* ignore */ }
    })();
    return () => {
      cancelled = true;
      if (url) URL.revokeObjectURL(url);
    };
  }, [activeClipId]);

  return (
    <div className="flex h-full min-h-0 overflow-hidden bg-surface-950 text-surface-100">
      {/* Video summary overlay — fullscreen player triggered from the
          task header chip. Closes on ✕ or video end. */}
      {activeClipId && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/95"
          onClick={() => setActiveClipId(null)}
        >
          <button
            onClick={() => setActiveClipId(null)}
            className="absolute right-6 top-6 rounded-full bg-white/10 px-4 py-2 text-white hover:bg-white/20"
          >
            ✕ Close
          </button>
          {activeClipBlobUrl ? (
            <video
              src={activeClipBlobUrl}
              controls
              autoPlay
              className="max-w-full max-h-full"
              onClick={(e) => e.stopPropagation()}
              onEnded={() => setActiveClipId(null)}
            />
          ) : (
            <span className="text-zinc-400 text-sm">Loading clip…</span>
          )}
        </div>
      )}
      <div className="w-[46vw] min-w-[380px] max-w-[760px] border-r border-surface-800">
        <PreviewPane
          selectedPreviewTarget={selectedPreviewTarget}
          onSelectPreviewTarget={onSelectPreviewTarget}
          mobileWorkers={mobileWorkers}
        />
      </div>

      <div className="flex min-w-0 flex-1 flex-col">
        <div className="border-b border-surface-800 bg-surface-900/70 px-4 py-4">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-500">Vibing</span>
            <StatusPill>{connectedDevice?.name || "no machine"}</StatusPill>
            {selectedProject ? <StatusPill>{selectedProject.name}</StatusPill> : null}
            {selectedRunner ? <StatusPill>{selectedRunner}</StatusPill> : null}
            {selectedModel ? <StatusPill>{selectedModel}</StatusPill> : null}
            {devStatus?.running ? <StatusPill>preview live</StatusPill> : null}
            <div className="relative ml-auto">
              <button
                onClick={() => setCustomizeOpen((v) => !v)}
                className="rounded-full border border-surface-700 bg-surface-950 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-300 hover:border-surface-600"
              >
                Sections
              </button>
              {customizeOpen ? (
                <div className="absolute right-0 top-full z-20 mt-2 w-56 rounded-xl border border-surface-700 bg-surface-900 p-2 shadow-xl">
                  <div className="mb-1 px-1 text-[10px] uppercase tracking-[0.16em] text-surface-500">Show sections</div>
                  {SECTION_ORDER.map((key) => (
                    <label
                      key={key}
                      className="flex cursor-pointer items-center justify-between gap-2 rounded-lg px-2 py-1.5 text-[11px] text-surface-200 hover:bg-surface-950"
                    >
                      <span>{SECTION_LABELS[key]}</span>
                      <input
                        type="checkbox"
                        checked={sections[key].visible}
                        onChange={() => toggleSectionVisible(key)}
                        className="accent-indigo-500"
                      />
                    </label>
                  ))}
                  <button
                    onClick={() => {
                      resetSections();
                      setCustomizeOpen(false);
                    }}
                    className="mt-1 w-full rounded-lg border border-surface-700 px-2 py-1 text-[10px] text-surface-400 hover:border-surface-600"
                  >
                    Reset to defaults
                  </button>
                </div>
              ) : null}
            </div>
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            {visibleDevices.map((device) => (
              <button
                key={device.id}
                onClick={() => void onSelectDevice(device)}
                className={`rounded-full border px-3 py-2 text-xs font-semibold ${
                  connectedDevice?.id === device.id
                    ? "border-emerald-500/40 bg-emerald-500/10 text-emerald-800 dark:text-emerald-100"
                    : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                }`}
              >
                {device.name}
              </button>
            ))}
          </div>
        </div>

        <div className="grid min-h-0 flex-1 xl:grid-cols-[320px,minmax(0,1fr)]">
          <aside className="flex min-h-0 flex-col gap-2 overflow-auto border-r border-surface-800 bg-surface-900/40 p-3">
            <section className="rounded-xl border border-surface-800 bg-surface-950/40 px-3 py-3">
              <div className="text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-400">Remote Machine</div>
              <div className="mt-2 text-sm font-semibold text-surface-100">{connectedMachine?.name || connectedDevice?.name || "No machine"}</div>
              <div className="mt-1 text-[11px] text-surface-500">
                {machineSummary?.platform || "Connect to a machine to see toolchain readiness."}
              </div>
              <div className="mt-3 flex flex-wrap gap-1.5">
                <MiniPill>{machineSummary?.supportsIos ? "iOS toolchain" : "no iOS toolchain"}</MiniPill>
                <MiniPill>{machineSummary?.supportsAndroid ? "Android toolchain" : "no Android toolchain"}</MiniPill>
                <MiniPill>{machineSummary?.supportsTestFlight ? "TestFlight ready" : "TestFlight gated"}</MiniPill>
                <MiniPill>{machineSummary?.supportsPlayStore ? "Play deploy ready" : "Play deploy gated"}</MiniPill>
              </div>
              {machineSummary?.runnerNames?.length ? (
                <div className="mt-3 text-[11px] text-surface-500">
                  Ready runners: {machineSummary.runnerNames.join(", ")}
                </div>
              ) : null}
              {machineSummary?.currentWorkDir ? (
                <div className="mt-2 truncate text-[10px] font-mono text-surface-600">
                  cwd: {machineSummary.currentWorkDir}
                </div>
              ) : null}
            </section>

            <FoldableSection
              sectionKey="projects"
              label={SECTION_LABELS.projects}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="flex min-h-0 flex-col gap-2 overflow-auto">
                {projects.map((project) => (
                  <button
                    key={project.path}
                    onClick={() => setSelectedProjectPath(project.path)}
                    className={`rounded-2xl border p-3 text-left ${
                      selectedProjectPath === project.path
                        ? "border-indigo-500/40 bg-indigo-500/10"
                        : "border-surface-800 bg-surface-950/70 hover:border-surface-700"
                    }`}
                  >
                    <div className="text-sm font-semibold text-surface-100">{project.name}</div>
                    <div className="mt-1 truncate text-[11px] text-surface-500">{project.path}</div>
                    <div className="mt-2 flex flex-wrap gap-1.5">
                      {project.branch ? <MiniPill>{project.branch}</MiniPill> : null}
                      {project.framework ? <MiniPill>{project.framework}</MiniPill> : null}
                    </div>
                  </button>
                ))}
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="runner"
              label={SECTION_LABELS.runner}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <label className="block text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                Agent
              </label>
              <select
                value={selectedRunner}
                onChange={(event) => setSelectedRunner(event.target.value)}
                className="mt-2 w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
              >
                {runners.map((runner) => (
                  <option key={runner.id} value={runner.id}>
                    {runner.name}{runner.ready === false ? " (sign-in needed)" : ""}
                  </option>
                ))}
              </select>
              <div className="mt-3 flex flex-wrap gap-2">
                {runners.map((runner) => (
                  <button
                    key={runner.id}
                    onClick={() => setSelectedRunner(runner.id)}
                    className={`rounded-full border px-3 py-2 text-xs font-semibold transition-colors ${
                      selectedRunner === runner.id
                        ? "border-brand/40 bg-brand-soft text-brand-softFg"
                        : runner.ready === false
                          ? "border-warning/30 bg-warning-soft/40 text-warning-softFg hover:border-warning/50"
                          : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                    }`}
                    title={runner.error || runner.warning || runner.name}
                  >
                    {runner.name}
                    {runner.ready === false ? " (blocked)" : ""}
                  </button>
                ))}
              </div>
              {availableModels.length > 0 ? (
                <>
                  <label className="mt-4 block text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                    Model
                  </label>
                  <select
                    value={selectedModel}
                    onChange={(event) => setSelectedModel(event.target.value)}
                    className="mt-2 w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                  >
                    {availableModels.map((model) => (
                      <option key={model.id} value={model.id}>
                        {model.name}
                      </option>
                    ))}
                  </select>
                </>
              ) : null}
              {/* OpenCode-only: build vs plan agent picker. Maps to
                  `--agent <mode>` on `opencode run`. The user's
                  opencode.json defines what each agent does (different
                  models, system prompts, allowed tools); Yaver just
                  forwards the choice. Empty value = "default agent",
                  which means whatever defaultAgent is set in their
                  opencode.json. Custom agents the user defined show
                  up here automatically because the agent's
                  /runner/opencode/config endpoint surfaces them. */}
              {selectedRunner === "opencode" ? (
                <>
                  <label className="mt-4 block text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">
                    OpenCode Agent
                  </label>
                  <div className="mt-2 flex flex-wrap gap-2">
                    {/* Default + every agent from opencode.json. The
                        agents[] array surfaces both stock (build, plan)
                        and custom (review, research, etc.) entries.
                        Falls back to hardcoded build/plan when the
                        config isn't reachable so the picker is never
                        empty. */}
                    {([{ name: "", isBuiltin: true } as { name: string; model?: string; isBuiltin?: boolean }]
                      .concat(
                        opencodeAgents.length > 0
                          ? opencodeAgents
                          : [
                              { name: "build", isBuiltin: true },
                              { name: "plan", isBuiltin: true },
                            ],
                      )
                    ).map((agent) => {
                      const id = agent.name;
                      const label = id === "" ? "Default" : id.charAt(0).toUpperCase() + id.slice(1);
                      const tooltip = id === ""
                        ? "Use defaultAgent from opencode.json"
                        : agent.model
                          ? `Run with --agent ${id} (${agent.model})`
                          : `Run with --agent ${id}`;
                      return (
                        <button
                          key={id || "default"}
                          onClick={() => setSelectedMode(id)}
                          className={`rounded-full border px-3 py-1.5 text-xs font-semibold transition-colors ${
                            selectedMode === id
                              ? "border-brand/40 bg-brand-soft text-brand-softFg"
                              : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                          } ${!agent.isBuiltin && id !== "" ? "italic" : ""}`}
                          title={tooltip}
                        >
                          {label}
                          {agent.model ? <span className="ml-1.5 text-[10px] text-surface-500">({agent.model.split("/").pop()})</span> : null}
                        </button>
                      );
                    })}
                  </div>
                  <p className="mt-1 text-[10px] text-surface-500">
                    Build for code edits · Plan for architecture / dry runs · italic = custom agents from your <span className="font-mono">opencode.json</span>.
                  </p>
                </>
              ) : null}
              {selectedRunnerRow?.ready === false ? (
                <div className="mt-3 rounded-2xl border border-amber-500/20 bg-amber-500/10 p-3 text-[11px] leading-5 text-amber-800 dark:text-amber-100">
                  {selectedRunnerRow.error || selectedRunnerRow.warning || `${selectedRunnerRow.name} is installed but not ready on this machine.`}
                </div>
              ) : null}
              {selectedRunnerRow && (selectedRunnerRow.id === "claude" || selectedRunnerRow.id === "codex") && selectedRunnerRow.ready === false ? (
                <div className="mt-3 rounded-2xl border border-sky-500/20 bg-sky-500/10 p-3 text-[11px] text-sky-800 dark:text-sky-100">
                  <div className="font-semibold">
                    {selectedRunnerRow.id === "claude" ? "Claude Code" : "OpenAI Codex"} sign-in is available from here.
                  </div>
                  <div className="mt-1 leading-5 text-sky-800 dark:text-sky-100/80">
                    Start the browser auth flow on the host, finish it in your browser, then this runner will become selectable for vibe coding.
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2">
                    <button
                      onClick={() => void startSelectedRunnerSignIn()}
                      disabled={runnerAuthBusy}
                      className="rounded-xl border border-sky-400/30 bg-sky-400/10 px-3 py-2 text-xs font-semibold text-sky-800 dark:text-sky-100 hover:bg-sky-400/15 disabled:opacity-40"
                    >
                      {runnerAuthBusy ? "Opening sign-in…" : `Sign in to ${selectedRunnerRow.name}`}
                    </button>
                    {runnerAuthStatus?.openUrl ? (
                      <a
                        href={runnerAuthStatus.openUrl}
                        target="_blank"
                        rel="noreferrer"
                        className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                      >
                        Open auth page
                      </a>
                    ) : null}
                  </div>
                  {runnerAuthStatus ? (
                    <div className="mt-3 rounded-xl border border-surface-800 bg-surface-950/60 p-3 text-[10px] text-surface-300">
                      <div className="font-semibold uppercase tracking-[0.14em] text-surface-500">Sign-in status</div>
                      <div className="mt-2">{runnerAuthStatus.status.replaceAll("_", " ")}</div>
                      {runnerAuthStatus.code ? <div className="mt-1 font-mono text-surface-200">Code: {runnerAuthStatus.code}</div> : null}
                      {runnerAuthStatus.detail ? <div className="mt-1 text-surface-400">{runnerAuthStatus.detail}</div> : null}
                    </div>
                  ) : null}
                  {runnerAuthError ? (
                    <div className="mt-2 text-[10px] text-rose-700 dark:text-rose-200">{runnerAuthError}</div>
                  ) : null}
                </div>
              ) : null}
              {availableModels.length > 0 ? (
                <div className="mt-3 flex flex-wrap gap-2">
                  {availableModels.map((model) => (
                    <button
                      key={model.id}
                      onClick={() => setSelectedModel(model.id)}
                      className={`rounded-full border px-3 py-1.5 text-[11px] font-semibold transition-colors ${
                        selectedModel === model.id
                          ? "border-brand/40 bg-brand-soft text-brand-softFg"
                          : "border-surface-700 bg-surface-950 text-surface-400 hover:border-surface-600"
                      }`}
                      title={model.description || model.name}
                    >
                      {model.name}
                    </button>
                  ))}
                </div>
              ) : null}
            </FoldableSection>

            <FoldableSection
              sectionKey="actions"
              label={SECTION_LABELS.actions}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="flex flex-wrap gap-2">
                <button
                  onClick={() => void startPreview()}
                  disabled={!selectedProject}
                  className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                >
                  Start Preview
                </button>
                <button
                  onClick={() => void agentClient.reloadDevServer({ mode: (devStatus?.framework || "").match(/^(expo|react-native)$/i) ? "bundle" : "dev" })}
                  disabled={!devStatus?.running}
                  className="rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                >
                  Refresh Preview
                </button>
                {deployTargets.map((target) => (
                  <button
                    key={target.id}
                    onClick={() => void deploy(target.id)}
                    disabled={!selectedProject}
                    className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs font-semibold text-emerald-800 dark:text-emerald-100 hover:bg-emerald-500/15 disabled:opacity-40"
                  >
                    Deploy {target.name}
                  </button>
                ))}
              </div>
              <div className="mt-3 grid gap-2">
                {deployQuickActions.map((action) => (
                  <div key={action.kind} className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <div className="text-sm font-semibold text-surface-100">{action.label}</div>
                        <div className="mt-1 text-[11px] leading-5 text-surface-500">{action.description}</div>
                      </div>
                      <span
                        className={`rounded-full px-2 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] ${
                          action.enabled
                            ? "border border-emerald-500/30 bg-emerald-500/10 text-emerald-800 dark:text-emerald-100"
                            : "border border-amber-500/20 bg-amber-500/10 text-amber-800 dark:text-amber-100"
                        }`}
                      >
                        {action.enabled ? "ready" : "check host"}
                      </span>
                    </div>
                    <div className="mt-3 text-[11px] text-surface-500">{action.readiness}</div>
                    <div className="mt-3 flex flex-wrap gap-2">
                      <button
                        onClick={() => void launchDeployTask(action.kind)}
                        disabled={!selectedProject || !action.enabled}
                        title={!action.enabled ? action.readiness : undefined}
                        className="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs font-semibold text-emerald-800 dark:text-emerald-100 hover:bg-emerald-500/15 disabled:opacity-40 disabled:cursor-not-allowed"
                      >
                        Run with agent
                      </button>
                      <button
                        onClick={() => prepareDeployPrompt(action.kind)}
                        disabled={!selectedProject}
                        className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                      >
                        Draft prompt
                      </button>
                    </div>
                  </div>
                ))}
              </div>
              {deployPreviewSummary?.warnings && deployPreviewSummary.warnings.length > 0 ? (
                <div className="mt-3 rounded-2xl border border-amber-500/20 bg-amber-500/10 p-3 text-[11px] leading-5 text-amber-800 dark:text-amber-100">
                  {deployPreviewSummary.warnings.slice(0, 3).map((warning) => (
                    <div key={warning}>{warning}</div>
                  ))}
                </div>
              ) : null}
              {deployPreviewSummary?.lastDeploy ? (
                <div className="mt-3 text-[11px] text-surface-500">
                  Last deploy: {deployPreviewSummary.lastDeploy.target || "target"} · {deployPreviewSummary.lastDeploy.status || "unknown"}
                </div>
              ) : null}
            </FoldableSection>

            <FoldableSection
              sectionKey="repo"
              label={SECTION_LABELS.repo}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3 text-xs text-surface-300">
                <div className="flex flex-wrap gap-2">
                  <MiniPill>{gitStatus?.branch || selectedProject?.branch || "no branch"}</MiniPill>
                  <MiniPill>{gitStatus?.ahead || 0} ahead</MiniPill>
                  <MiniPill>{gitStatus?.behind || 0} behind</MiniPill>
                  <MiniPill>{gitStatus?.clean ? "clean" : "dirty"}</MiniPill>
                </div>
                <div className="mt-3 space-y-1 text-surface-400">
                  <div>staged: {gitStatus?.staged?.length || 0}</div>
                  <div>modified: {gitStatus?.modified?.length || 0}</div>
                  <div>untracked: {gitStatus?.untracked?.length || 0}</div>
                </div>
                <div className="mt-4 flex flex-wrap gap-2">
                  <button
                    onClick={() => void runGitAction("pull")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Pull
                  </button>
                  <button
                    onClick={() => void runGitAction("push")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Push
                  </button>
                  <button
                    onClick={() => void runGitAction("stash")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Stash
                  </button>
                  <button
                    onClick={() => void runGitAction("stash-pop")}
                    disabled={!selectedProject}
                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600 disabled:opacity-40"
                  >
                    Pop Stash
                  </button>
                  <button
                    onClick={() => void runGitAction("revert-head")}
                    disabled={!selectedProject || gitCommits.length === 0}
                    className="rounded-lg border border-red-500/30 px-2.5 py-1.5 text-[11px] text-red-700 dark:text-red-300 hover:bg-red-500/10 disabled:opacity-40"
                  >
                    Revert Last
                  </button>
                </div>
                <div className="mt-3 flex gap-2">
                  <input
                    value={gitCommitMessage}
                    onChange={(event) => setGitCommitMessage(event.target.value)}
                    placeholder="commit message"
                    className="flex-1 rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                  />
                  <button
                    onClick={() => void runGitAction("commit")}
                    disabled={!selectedProject || !gitCommitMessage.trim()}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Commit All
                  </button>
                </div>
                <div className="mt-3 flex flex-wrap gap-2">
                  <button
                    onClick={() => void launchWorkflowShortcut("sync")}
                    disabled={!selectedProject}
                    className="rounded-full border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[11px] font-semibold text-amber-800 dark:text-amber-100 hover:bg-amber-500/15 disabled:opacity-40"
                  >
                    Sync/Rebase Prompt
                  </button>
                  <button
                    onClick={() => void launchWorkflowShortcut("resolve")}
                    disabled={!selectedProject}
                    className="rounded-full border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-[11px] font-semibold text-amber-800 dark:text-amber-100 hover:bg-amber-500/15 disabled:opacity-40"
                  >
                    Resolve Conflicts Prompt
                  </button>
                  <button
                    onClick={() => void launchWorkflowShortcut("ship")}
                    disabled={!selectedProject}
                    className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-[11px] font-semibold text-emerald-800 dark:text-emerald-100 hover:bg-emerald-500/15 disabled:opacity-40"
                  >
                    Commit/Push/Deploy Prompt
                  </button>
                </div>
                {gitCommits.length > 0 ? (
                  <div className="mt-4 border-t border-surface-800 pt-3">
                    <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-500">Recent commits</div>
                    <div className="space-y-2">
                      {gitCommits.slice(0, 5).map((commit) => (
                        <div key={commit.hash} className="rounded-xl border border-surface-800 bg-surface-900/60 p-2">
                          <div className="text-[11px] font-semibold text-surface-200">{commit.message}</div>
                          <div className="mt-1 text-[10px] text-surface-500">
                            {commit.shortHash} · {commit.author} · {new Date(commit.date).toLocaleDateString()}
                          </div>
                        </div>
                      ))}
                    </div>
                  </div>
                ) : null}
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="secrets"
              label={SECTION_LABELS.secrets}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                <div className="mb-3 text-[11px] leading-5 text-surface-500">
                  Stored in the selected machine vault, namespaced by project path. Nothing here goes to Convex.
                </div>
                <div className="space-y-3">
                  {PROJECT_SECRET_TEMPLATES.map((secret) => (
                    <div key={secret.id}>
                      <label className="mb-1 block text-[11px] font-semibold uppercase tracking-[0.14em] text-surface-500">{secret.label}</label>
                      <div className="mb-1 text-[10px] text-surface-600">{secret.hint}</div>
                      <div className="flex gap-2">
                        <input
                          type={secret.multiline ? "text" : "password"}
                          value={projectSecrets[secret.id] || ""}
                          onChange={(event) => setProjectSecrets((prev) => ({ ...prev, [secret.id]: event.target.value }))}
                          placeholder={secret.placeholder}
                          className="flex-1 rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                        />
                        <button
                          onClick={() => void saveProjectSecret(secret.id, projectSecrets[secret.id] || "")}
                          disabled={!selectedProject || !projectSecrets[secret.id] || savingSecretKey === secret.id}
                          className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                        >
                          {savingSecretKey === secret.id ? "Saving…" : "Save"}
                        </button>
                      </div>
                    </div>
                  ))}
                </div>
                {selectedProject ? (
                  <div className="mt-3 rounded-xl border border-surface-800 bg-surface-900/60 p-2 text-[10px] text-surface-500">
                    Namespace: <span className="font-mono text-surface-400">{projectVaultEntryName(selectedProject.path, "…")}</span>
                  </div>
                ) : null}
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="providers"
              label={SECTION_LABELS.providers}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="rounded-2xl border border-surface-800 bg-surface-950/70 p-3">
                <div className="flex flex-wrap gap-2">
                  <button
                    onClick={() => void autoDetectProviders()}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                  >
                    Detect
                  </button>
                  <button
                    onClick={() => setManualProvider("github")}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                  >
                    GitHub Token
                  </button>
                  <button
                    onClick={() => setManualProvider("gitlab")}
                    className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600"
                  >
                    GitLab Token
                  </button>
                </div>
                {manualProvider ? (
                  <div className="mt-3 flex gap-2">
                    <input
                      type="password"
                      value={providerToken}
                      onChange={(event) => setProviderToken(event.target.value)}
                      placeholder={`${manualProvider} token`}
                      className="flex-1 rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                    />
                    <button
                      onClick={() => void saveProviderToken()}
                      disabled={!providerToken.trim()}
                      className="rounded-xl border border-surface-700 bg-surface-900 px-3 py-2 text-xs font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                    >
                      Save
                    </button>
                  </div>
                ) : null}
                <div className="mt-3 space-y-2">
                  {providers.length === 0 ? (
                    <div className="text-xs text-surface-500">No git providers connected on this machine.</div>
                  ) : providers.map((provider) => (
                    <div key={provider.host} className="rounded-xl border border-surface-800 bg-surface-900/60 p-3">
                      <div className="flex items-center justify-between gap-2">
                        <div>
                          <div className="text-sm font-semibold text-surface-100">{provider.username}</div>
                          <div className="text-[11px] text-surface-500">{provider.provider} · {provider.host}{provider.hasSsh ? " · SSH" : ""}</div>
                        </div>
                        <div className="flex gap-2">
                          <button
                            onClick={() => void toggleProviderRepos(provider.host)}
                            className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600"
                          >
                            {repoBrowserHost === provider.host ? "Hide" : "Repos"}
                          </button>
                          <button
                            onClick={() => void removeProvider(provider.host)}
                            className="rounded-lg border border-red-500/30 px-2.5 py-1.5 text-[11px] text-red-700 dark:text-red-300 hover:bg-red-500/10"
                          >
                            Remove
                          </button>
                        </div>
                      </div>
                      {repoBrowserHost === provider.host ? (
                        <div className="mt-3">
                          <input
                            value={providerSearch}
                            onChange={(event) => setProviderSearch(event.target.value)}
                            placeholder="search repos"
                            className="w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
                          />
                          <div className="mt-2 max-h-64 space-y-2 overflow-auto">
                            {filteredProviderRepos.map((repo) => (
                              <div key={repo.fullName} className="rounded-xl border border-surface-800 bg-surface-950/70 p-3">
                                <div className="flex items-start justify-between gap-3">
                                  <div className="min-w-0">
                                    <div className="truncate text-sm font-semibold text-surface-100">{repo.fullName}</div>
                                    <div className="mt-1 flex flex-wrap gap-1.5">
                                      {repo.private ? <MiniPill>private</MiniPill> : null}
                                      {repo.language ? <MiniPill>{repo.language}</MiniPill> : null}
                                    </div>
                                    {repo.description ? <div className="mt-1 text-[11px] text-surface-500">{repo.description}</div> : null}
                                  </div>
                                  <button
                                    onClick={() => void cloneRemoteRepo(repo)}
                                    className="rounded-lg border border-surface-700 px-2.5 py-1.5 text-[11px] text-surface-300 hover:border-surface-600"
                                  >
                                    Clone
                                  </button>
                                </div>
                              </div>
                            ))}
                          </div>
                        </div>
                      ) : null}
                    </div>
                  ))}
                </div>
              </div>
            </FoldableSection>

            <FoldableSection
              sectionKey="sessions"
              label={SECTION_LABELS.sessions}
              sections={sections}
              onToggle={toggleSectionOpen}
            >
              <div className="flex min-h-0 flex-col gap-2 overflow-auto">
                {taskList.map((task) => (
                  <button
                    key={task.id}
                    onClick={() => {
                      setActiveGraphRunId(null);
                      setActiveTaskId(task.id);
                    }}
                    className={`rounded-2xl border p-3 text-left ${
                      activeTask?.id === task.id
                        ? "border-amber-500/40 bg-amber-500/10"
                        : "border-surface-800 bg-surface-950/70 hover:border-surface-700"
                    }`}
                  >
                    <div className="text-sm font-semibold text-surface-100">{task.title}</div>
                    <div className="mt-1 text-[11px] text-surface-500">
                      {task.status} · {new Date(task.updatedAt).toLocaleTimeString()}
                    </div>
                  </button>
                ))}
              </div>
            </FoldableSection>
          </aside>

          <div className="flex min-h-0 flex-col bg-[#08111a]">
            <div className="border-b border-surface-800 px-4 py-3">
              <div className="flex items-center gap-2">
                <div className="text-sm font-semibold text-surface-100 flex-1">
                  {activeGraphRunId
                    ? "Deep ask · investigate → answer → verify"
                    : activeTask?.title || "New coding session"}
                  {!activeGraphRunId && activeTask && (activeTask.tmuxSession || activeTask.tmuxSessionId) ? (
                    <div
                      className="mt-0.5 font-mono text-[10px] font-normal text-surface-400 select-all"
                      title="Attach on the box with: tmux attach -t <session>"
                    >
                      tmux: {activeTask.tmuxSession || activeTask.tmuxSessionId}
                    </div>
                  ) : null}
                </div>
                {!activeGraphRunId ? (
                  <div
                    className="flex items-center gap-0.5 rounded-md border border-surface-700 bg-surface-950 p-0.5"
                    title="Cost mode for multi-step (deep ask / graph) runs. Single = your subscription plan only. Duo = Claude Code + GLM. Trio = Claude Code + Codex + GLM. Coherence-critical work stays on the flat subscription plans; parallel overflow spills to the cheap GLM apikey lane."
                  >
                    {([[0, "Single"], [2, "Duo"], [3, "Trio"]] as [number, string][]).map(([deg, label]) => (
                      <button
                        key={deg}
                        type="button"
                        onClick={() => setHybridDegree(deg)}
                        className={`rounded px-1.5 py-0.5 text-[10px] font-semibold transition-colors ${hybridDegree === deg ? "bg-sky-500/20 text-sky-600 dark:text-sky-300" : "text-surface-400 hover:text-surface-200"}`}
                      >
                        {label}
                      </button>
                    ))}
                  </div>
                ) : null}
                {activeGraphRunId ? (
                  <button
                    onClick={() => {
                      void agentClient.stopAgentGraph(activeGraphRunId);
                      setActiveGraphRunId(null);
                    }}
                    className="rounded-md border border-surface-700 bg-surface-950 px-2 py-0.5 text-[11px] font-semibold text-surface-300 hover:border-surface-500"
                  >
                    ✕ Close
                  </button>
                ) : null}
                {/* Video summary chip — same shape as the mobile chip,
                    just rendered as a button next to the title. Opens
                    a fullscreen <video> when clicked. */}
                {!activeGraphRunId && activeTask?.videoStatus === "ready" && activeTask?.videoClipId ? (
                  <button
                    onClick={() => setActiveClipId(activeTask.videoClipId!)}
                    className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-2 py-0.5 text-[11px] font-semibold text-emerald-700 dark:text-emerald-300 hover:bg-emerald-500/20"
                  >
                    ▶ Watch demo
                  </button>
                ) : activeTask?.videoStatus === "recording" || activeTask?.videoStatus === "queued" ? (
                  <span className="rounded-md border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[11px] font-semibold text-amber-700 dark:text-amber-300">
                    🎬 {activeTask.videoStatus}…
                  </span>
                ) : null}
                {!activeGraphRunId && placementLaneLabel(activeTask?.placementLane) ? (
                  <span
                    className="max-w-[190px] truncate rounded-md border border-surface-700 bg-surface-950 px-2 py-0.5 text-[11px] font-semibold text-surface-300"
                    title={activeTask?.placementReason || activeTask?.placementCreditLabel || placementLaneLabel(activeTask?.placementLane) || undefined}
                  >
                    {[
                      placementLaneLabel(activeTask?.placementLane),
                      activeTask?.placementCreditLabel,
                    ].filter(Boolean).join(" · ")}
                  </span>
                ) : null}
              </div>
              <div className="text-xs text-surface-500">
                {activeGraphRunId
                  ? `${graphRun?.status ?? "queued"} · grounded answer with file:line cites · ${selectedProject?.path || ""}`
                  : activeTask
                    ? `${activeTask.status} · ${selectedProject?.path || ""}`
                    : "Project-scoped remote coding through the connected machine"}
              </div>
            </div>

            <div className="min-h-0 flex-1 overflow-auto px-4 py-4">
              {activeGraphRunId ? (
                <DeepAskGraphPanel run={graphRun} liveOutput={graphNodeOutput} />
              ) : conversationTurns.length > 0 || showLiveOutput ? (
                <div className="space-y-4">
                  {conversationTurns.map((turn, index) => (
                    <ChatBubble
                      key={`${turn.role}:${turn.timestamp}:${index}`}
                      turn={turn}
                    />
                  ))}
                  {showLiveOutput ? (
                    <div className="max-w-[92%] rounded-2xl border border-amber-500/20 bg-[#14110a] px-4 py-3 text-surface-200">
                      <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-amber-700 dark:text-amber-300">
                        {activeTask?.status === "running" ? "Live output" : "Agent output"}
                      </div>
                      <div className="text-[13px] leading-6 break-words [&_pre]:whitespace-pre-wrap">
                        <AssistantMarkdown text={liveOutput} />
                      </div>
                    </div>
                  ) : null}
                </div>
              ) : (
                <div className="flex h-full min-h-[240px] items-center justify-center text-sm text-surface-500">
                  Start a task on the left, or continue the selected session here.
                </div>
              )}
            </div>

            <div className="border-t border-surface-800 bg-surface-900/70 p-4">
              <input
                value={draftTitle}
                onChange={(event) => setDraftTitle(event.target.value)}
                placeholder="optional title"
                className="mb-3 w-full rounded-xl border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100 outline-none focus:border-surface-500"
              />
              <textarea
                value={composer}
                onChange={(event) => setComposer(event.target.value)}
                placeholder="Fix the mobile login screen, keep the RN preview running, and tell me the local URL or tunnel once it is reachable."
                className="min-h-28 w-full rounded-2xl border border-surface-700 bg-surface-950 px-4 py-3 text-sm text-surface-100 outline-none focus:border-indigo-500"
              />
              <label className="mt-3 flex cursor-pointer items-center gap-2 text-xs text-surface-400 select-none">
                <input
                  type="checkbox"
                  className="h-3.5 w-3.5 rounded border-surface-600 bg-surface-950 text-indigo-500 focus:ring-1 focus:ring-indigo-500"
                  checked={videoSummaryEnabled}
                  onChange={(e) => setVideoSummaryEnabled(e.target.checked)}
                />
                🎬 Record demo video when this task finishes
              </label>
              <div className="mt-3 flex items-center justify-between gap-3">
                <div className="text-xs text-surface-500">
                  Task is scoped to {selectedProject?.name || "the selected project"} and runs on {connectedMachine?.name || connectedDevice?.name || "the connected machine"}.
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => void agentClient.reloadDevServer({ mode: (devStatus?.framework || "").match(/^(expo|react-native)$/i) ? "bundle" : "dev" })}
                    disabled={!devStatus?.running}
                    className="rounded-xl border border-surface-700 bg-surface-950 px-4 py-2 text-sm font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Refresh Preview
                  </button>
                  <button
                    onClick={() => void continueChatTask()}
                    disabled={!activeTask || !composer.trim() || selectedRunnerRow?.ready === false}
                    className="rounded-xl border border-surface-700 bg-surface-950 px-4 py-2 text-sm font-semibold text-surface-200 hover:border-surface-600 disabled:opacity-40"
                  >
                    Continue
                  </button>
                  <button
                    onClick={() => void startChatTask()}
                    disabled={!selectedProject || !composer.trim() || selectedRunnerRow?.ready === false}
                    className="rounded-xl bg-indigo-500 px-4 py-2 text-sm font-semibold text-white hover:bg-indigo-400 disabled:opacity-40"
                  >
                    Start Task
                  </button>
                </div>
              </div>
              {busy ? <div className="mt-3 text-xs text-surface-400">{busy}</div> : null}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

const PROJECT_SECRET_TEMPLATES = [
  { id: "cloudflare_api_token", label: "Cloudflare Token", placeholder: "Cloudflare API token", hint: "Used for Workers, DNS, and deploy flows.", multiline: false },
  { id: "cloudflare_account_id", label: "Cloudflare Account ID", placeholder: "Cloudflare account ID", hint: "Project-level Cloudflare account target.", multiline: false },
  { id: "convex_deploy_key", label: "Convex Deploy Key", placeholder: "Convex deploy key", hint: "For `npx convex deploy` and backend promotion.", multiline: false },
  { id: "convex_url", label: "Convex URL", placeholder: "https://...convex.cloud", hint: "Project runtime endpoint if you keep several Convex apps.", multiline: false },
  { id: "vercel_token", label: "Vercel Token", placeholder: "Vercel token", hint: "For Vercel CLI deploys tied to this repo.", multiline: false },
  { id: "supabase_access_token", label: "Supabase Token", placeholder: "Supabase access token", hint: "For project-linked Supabase deploy or db flows.", multiline: false },
  { id: "expo_token", label: "Expo Token", placeholder: "Expo access token", hint: "For EAS / Expo publish from this mobile project.", multiline: false },
  { id: "sentry_auth_token", label: "Sentry Token", placeholder: "Sentry auth token", hint: "Upload source maps or releases for this project only.", multiline: false },
] as const;

function projectVaultEntryName(projectPath: string, secretId: string): string {
  const normalized = projectPath.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  const suffix = hashProjectPath(projectPath);
  return `project:${normalized}:${suffix}:${secretId}`;
}

function hashProjectPath(input: string): string {
  let hash = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

function summarizeTitle(prompt: string, projectName: string): string {
  const clean = prompt.replace(/\s+/g, " ").trim();
  const base = clean.slice(0, 64);
  return base ? `${projectName} — ${base}` : `${projectName} task`;
}

function buildVibeTaskPrompt({
  project,
  prompt,
  gitStatus,
  deployTargets,
  machine,
}: {
  project: Project;
  prompt: string;
  gitStatus: GitStatusRow | null;
  deployTargets: Array<{ id: string; name: string }>;
  machine: MachineInfo | null;
}): string {
  const machineLines = describeMachineForPrompt(machine);
  const lines = [
    `You are working for a solo developer in project ${project.name}.`,
    `Project path: ${project.path}`,
    project.framework ? `Framework: ${project.framework}` : "",
    gitStatus?.branch ? `Current branch: ${gitStatus.branch}` : "",
    gitStatus ? `Git state: ${gitStatus.clean ? "clean" : "dirty"}, ahead=${gitStatus.ahead || 0}, behind=${gitStatus.behind || 0}, staged=${gitStatus.staged?.length || 0}, modified=${gitStatus.modified?.length || 0}, untracked=${gitStatus.untracked?.length || 0}` : "",
    deployTargets.length > 0 ? `Known deploy targets: ${deployTargets.map((target) => target.name).join(", ")}` : "Known deploy targets: none detected yet",
    ...machineLines,
    "",
    "Execution contract:",
    "1. Stay inside the selected project unless the task explicitly requires adjacent workspace files.",
    "2. Sync with git before making risky changes. If the branch is behind or diverged, fetch and rebase intelligently instead of creating an unnecessary merge commit.",
    "3. If conflicts happen, resolve them carefully, explain the resolution, and continue instead of stopping to ask.",
    "4. After implementing, run the relevant checks, keep the working tree coherent, then commit the work with a clear message.",
    "5. Push the branch unless the prompt explicitly says not to.",
    "6. If there is a detected deploy path and the change is meant to ship, deploy it and report the result.",
    "7. If you start or reload a dev/mobile preview, surface the reachable preview/LAN/device URL clearly.",
    "8. Do not stop at analysis; carry the work through to a real state change when feasible.",
  "",
  "User request:",
  prompt,
  ].filter(Boolean);
  return lines.join("\n");
}

function buildVibeContinuationPrompt({
  project,
  prompt,
  gitStatus,
  deployTargets,
  machine,
}: {
  project: Project | null;
  prompt: string;
  gitStatus: GitStatusRow | null;
  deployTargets: Array<{ id: string; name: string }>;
  machine: MachineInfo | null;
}): string {
  const lines = [
    project ? `Continue in project ${project.name} (${project.path}).` : "",
    gitStatus?.branch ? `Current branch: ${gitStatus.branch}` : "",
    deployTargets.length > 0 ? `Deploy targets still available: ${deployTargets.map((target) => target.name).join(", ")}` : "",
    ...describeMachineForPrompt(machine),
    "Keep the solo-developer flow: sync/rebase if needed, resolve conflicts, finish the git operation cleanly, commit, push, and deploy when the request implies shipping.",
    "",
    prompt,
  ].filter(Boolean);
  return lines.join("\n");
}

function describeMachineForPrompt(machine: MachineInfo | null): string[] {
  if (!machine) return [];
  const caps = machine.capabilities;
  const readyRunners = (caps?.runners || []).filter((runner) => runner.ready).map((runner) => runner.name);
  return [
    `Execution machine: ${machine.name} (${machine.platform || machine.os || "unknown platform"})`,
    caps
      ? `Machine capabilities: iOS=${caps.supportsIos ? "yes" : "no"}, Android=${caps.supportsAndroid ? "yes" : "no"}, TestFlight=${caps.supportsTestFlight ? "yes" : "no"}, PlayStore=${caps.supportsPlayStore ? "yes" : "no"}, Docker=${caps.supportsDocker ? "yes" : "no"}`
      : "",
    readyRunners.length > 0 ? `Ready coding runners on this machine: ${readyRunners.join(", ")}` : "",
    machine.currentWorkDir ? `Machine current work dir: ${machine.currentWorkDir}` : "",
    "Before doing platform-specific release work, verify the host actually has the required signing, CLI auth, team configuration, and build tooling. If something is missing, fix it when feasible or report the exact blocker clearly.",
  ].filter(Boolean);
}

function buildDeployQuickActions(
  project: Project | null,
  machine: MachineInfo | null,
  deployTargets: Array<{ id: string; name: string }>,
): Array<{ kind: DeployActionKind; label: string; description: string; readiness: string; enabled: boolean }> {
  const caps = machine?.capabilities;
  const framework = (project?.framework || "").toLowerCase();
  const projectLooksMobile = framework.includes("expo") || framework.includes("react-native") || framework.includes("flutter") || !project?.framework;
  const hasEasTarget = deployTargets.some((target) => /eas|expo/i.test(target.name) || /eas|expo/i.test(target.id));

  return [
    {
      kind: "testflight",
      label: "TestFlight",
      description: "Build, sign, upload, and verify the iOS lane on a machine that can actually handle Apple tooling.",
      readiness: caps?.supportsTestFlight
        ? "Connected machine advertises TestFlight-capable iOS tooling."
        : "Needs a macOS host with Xcode, Apple credentials, signing assets, and team configuration.",
      enabled: !!caps?.supportsTestFlight && projectLooksMobile,
    },
    {
      kind: "play-internal",
      label: "Google Play Internal",
      description: "Ship an Android build to the internal testing track and confirm the upload state.",
      readiness: caps?.supportsPlayStore
        ? "Connected machine advertises Android/Play deployment capability."
        : "Needs Android build tooling, signing config, and Play Console credentials on the host.",
      enabled: !!caps?.supportsPlayStore && projectLooksMobile,
    },
    {
      kind: "eas",
      label: "EAS Deploy",
      description: "Use Expo/EAS from the current machine for updates or builds, depending on what the project supports.",
      readiness: hasEasTarget
        ? "Project already exposes an EAS/Expo deploy target."
        : "Agent will inspect `eas.json`, Expo auth, channels, and build profiles before choosing update vs build.",
      enabled: (framework.includes("expo") || framework.includes("react-native")) && projectLooksMobile,
    },
  ];
}

function buildDeployTaskPlan({
  kind,
  project,
  machine,
  deployTargets,
  gitStatus,
}: {
  kind: DeployActionKind;
  project: Project;
  machine: MachineInfo | null;
  deployTargets: Array<{ id: string; name: string }>;
  gitStatus: GitStatusRow | null;
}) {
  const targetLabel =
    kind === "testflight" ? "TestFlight" : kind === "play-internal" ? "Google Play Internal" : "EAS";
  const hostLine = machine
    ? `Run this on ${machine.name} (${machine.platform || machine.os || "unknown platform"}).`
    : "Run this on the currently connected machine.";
  const capabilityLine = machine?.capabilities
    ? `Host capabilities: iOS=${machine.capabilities.supportsIos ? "yes" : "no"}, Android=${machine.capabilities.supportsAndroid ? "yes" : "no"}, TestFlight=${machine.capabilities.supportsTestFlight ? "yes" : "no"}, PlayStore=${machine.capabilities.supportsPlayStore ? "yes" : "no"}.`
    : "";
  const deployLine = deployTargets.length > 0
    ? `Detected deploy targets: ${deployTargets.map((target) => target.name).join(", ")}.`
    : "No deploy target was auto-detected yet, so inspect the repo and toolchain before you decide the release path.";
  const branchLine = gitStatus?.branch ? `Current branch: ${gitStatus.branch}.` : "";
  const dirtyLine = gitStatus
    ? `Git state: ${gitStatus.clean ? "clean" : "dirty"}, ahead=${gitStatus.ahead || 0}, behind=${gitStatus.behind || 0}.`
    : "";
  const platformRequest =
    kind === "testflight"
      ? "Prepare and ship this project to TestFlight. Verify Xcode, Apple team selection, signing certificates/profiles, bundle identifiers, and any App Store Connect credentials before building. If the current host cannot do it, explain exactly why."
      : kind === "play-internal"
        ? "Prepare and ship this project to Google Play internal testing. Verify Gradle/Android tooling, keystore/signing config, Play Console service credentials, package id, and release track configuration before building. If the current host cannot do it, explain exactly why."
        : "Prepare and ship this project through Expo/EAS. Decide whether this should be an EAS Update, EAS Build, or EAS Submit flow after inspecting the repo. Verify `eas.json`, channels/profiles, Expo auth, Apple/Google credentials, and any required project secrets before doing the release.";

  const prompt = [
    `You are shipping project ${project.name} at ${project.path}.`,
    project.framework ? `Framework: ${project.framework}.` : "",
    hostLine,
    capabilityLine,
    branchLine,
    dirtyLine,
    deployLine,
    "",
    platformRequest,
    "Execution contract:",
    "1. Inspect the repository and the host first. Do not guess whether the machine can sign or submit builds.",
    "2. If the branch is behind/diverged, fetch and rebase cleanly before release work unless that would be unsafe.",
    "3. Check all release prerequisites on the host: CLI availability, auth state, signing/team settings, env vars, and project config.",
    "4. If a prerequisite is missing but fixable from this machine, fix it and continue.",
    "5. If a prerequisite cannot be fixed here, stop before a broken release and report the exact missing requirement.",
    "6. If release succeeds, include the artifact/build id/submission state and the next place the user should check.",
    "7. Trigger preview reload only when it helps validate the shipped change locally first.",
  ].filter(Boolean).join("\n");

  return {
    title: `${project.name} — deploy ${targetLabel}`,
    userPrompt: `Ship this project to ${targetLabel}. Check whether the connected machine is actually capable first, then handle the missing setup or complete the release.`,
    prompt,
    startingLabel: `Starting ${targetLabel} workflow…`,
    startedLabel: `Started ${targetLabel} workflow.`,
    preparedLabel: `Prepared ${targetLabel} deploy prompt.`,
  };
}

function extractOutputText(chunk: string): string {
  try {
    const parsed = JSON.parse(chunk);
    if (parsed?.text) return String(parsed.text);
  } catch {}
  return chunk;
}

function FoldableSection({
  sectionKey,
  label,
  sections,
  onToggle,
  children,
}: {
  sectionKey: SectionKey;
  label: string;
  sections: Record<SectionKey, SectionState>;
  onToggle: (key: SectionKey) => void;
  children: ReactNode;
}) {
  const state = sections[sectionKey];
  if (!state.visible) return null;
  return (
    <section className="rounded-xl border border-surface-800 bg-surface-950/40">
      <button
        onClick={() => onToggle(sectionKey)}
        className="flex w-full items-center justify-between gap-2 px-3 py-2 text-[11px] font-semibold uppercase tracking-[0.18em] text-surface-400 hover:text-surface-200"
      >
        <span>{label}</span>
        <span className="text-surface-500">{state.open ? "−" : "+"}</span>
      </button>
      {state.open ? <div className="border-t border-surface-800 px-3 py-3">{children}</div> : null}
    </section>
  );
}

function StatusPill({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-950 px-2.5 py-1 text-[10px] font-semibold uppercase tracking-[0.16em] text-surface-300">
      {children}
    </span>
  );
}

function MiniPill({ children }: { children: ReactNode }) {
  return (
    <span className="rounded-full border border-surface-700 bg-surface-900 px-2 py-1 text-[10px] text-surface-300">
      {children}
    </span>
  );
}

const GRAPH_NODE_VISUAL: Record<string, { icon: string; color: string }> = {
  pending: { icon: "○", color: "text-surface-500" },
  running: { icon: "◐", color: "text-amber-700 dark:text-amber-300" },
  completed: { icon: "✓", color: "text-emerald-700 dark:text-emerald-300" },
  failed: { icon: "✕", color: "text-red-400" },
  blocked: { icon: "⊘", color: "text-surface-500" },
  stopped: { icon: "■", color: "text-surface-400" },
};

// DeepAskGraphPanel renders a deep-ask graph run (investigate → answer →
// verify) as a vertical step list: each node shows its status, the running
// node streams live output, and completed nodes show their grounded result.
// The verify node's output is the final, cross-checked answer.
function DeepAskGraphPanel({ run, liveOutput }: { run: AgentGraphRun | null; liveOutput: string }) {
  if (!run) {
    return (
      <div className="flex h-full min-h-[240px] items-center justify-center text-sm text-surface-500">
        Starting deep ask…
      </div>
    );
  }
  const finalAnswer =
    run.nodes.find((n) => n.spec.id === "verify" && n.status === "completed")?.summary?.trim() ||
    (run.status === "completed" ? run.summary?.trim() : "") ||
    "";
  return (
    <div className="space-y-3">
      <div className="text-[11px] text-surface-500">
        A broad question — answered by a read-only investigate → answer → verify chain. Every step cites file:line; nothing is changed without your OK.
      </div>
      {run.nodes.map((node) => {
        const v = GRAPH_NODE_VISUAL[node.status] ?? GRAPH_NODE_VISUAL.pending;
        const isRunning = node.status === "running";
        // Running node: hand the raw text to the memoized renderer below
        // instead of stripping it inline on every render of every node.
        const summary = node.summary?.trim() || node.error?.trim() || "";
        const body = isRunning ? liveOutput : summary;
        return (
          <div key={node.spec.id} className="rounded-2xl border border-surface-800 bg-surface-950/60 px-4 py-3">
            <div className="flex items-center gap-2">
              <span className={`text-sm ${v.color} ${isRunning ? "animate-pulse" : ""}`}>{v.icon}</span>
              <span className="text-sm font-semibold text-surface-100 flex-1">{node.spec.title}</span>
              <StatusPill>{node.status}</StatusPill>
            </div>
            {body ? (
              <div className="mt-2 text-[13px] leading-6 text-surface-200 break-words [&_pre]:whitespace-pre-wrap">
                <AssistantMarkdown text={body} />
              </div>
            ) : node.status === "pending" || node.status === "blocked" ? (
              <div className="mt-1 text-[11px] text-surface-600">waiting…</div>
            ) : null}
          </div>
        );
      })}
      {finalAnswer ? (
        <div className="rounded-2xl border border-emerald-500/30 bg-emerald-500/5 px-4 py-3">
          <div className="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-emerald-700 dark:text-emerald-300">
            Final answer (cross-checked)
          </div>
          <div className="text-[13px] leading-6 text-surface-100 break-words [&_pre]:whitespace-pre-wrap">
            <ReactMarkdown components={ASSISTANT_MARKDOWN_COMPONENTS}>{finalAnswer}</ReactMarkdown>
          </div>
        </div>
      ) : null}
    </div>
  );
}
