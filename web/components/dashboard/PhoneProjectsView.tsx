"use client";

// PhoneProjectsView — UI over desktop/agent/phone_backend.go. A phone project
// is a SQLite-backed Yaver project. Deploy section currently surfaces the
// "Your Dev Machine" path; the cloud deploy path is hidden at launch
// (`canUseYaverCloud` is permanently false until paid features ship).

import { useCallback, useEffect, useMemo, useState } from "react";
import {
  agentClient,
  type PhoneProject,
  type PhonePushResult,
  type PhonePushTarget,
  type PhoneTemplate,
} from "@/lib/agent-client";
import { useDevices, type Device } from "@/lib/use-devices";
import { useAuth } from "@/lib/use-auth";
import { useAgentConnected } from "@/lib/sandbox/useAgentConnected";
import BrowserSandbox from "./BrowserSandbox";
import { DesignStudioPanel, type DesignBackend } from "./DesignStudio";
import { attachAgentBridge } from "@/lib/sandbox/agentDataBridge";
import { draftDesignPatch } from "@/lib/sandbox/designChat";
import { gatewayConfigured } from "@/lib/sandbox/gateway";
import { buildImportedConversationBrief, mergeImportedConversationPrompt } from "@/lib/conversation-import";
import { getSelfHostedRuntimeBaseUrl, getSelfHostedRuntimeLabel, getYaverCloudBaseUrl } from "@/lib/yaver-cloud";

const ADVANCED_PROMOTE_TARGETS: Array<{ id: string; label: string; sub: string }> = [
  { id: "sqlite-local", label: "SQLite file", sub: "Copy to a real project dir" },
  { id: "sqlite-turso", label: "Turso", sub: "Managed LibSQL on the edge" },
  { id: "postgres-local", label: "Postgres (Docker)", sub: "Local Postgres 16" },
  { id: "supabase-cloud", label: "Supabase Cloud", sub: "Managed Postgres + auth" },
  { id: "postgres-neon", label: "Neon", sub: "Serverless Postgres" },
  { id: "convex-cloud", label: "Convex Cloud", sub: "AI-rewrite complexity" },
];

const YAVER_CLOUD_BASE = getYaverCloudBaseUrl();
const SELF_HOSTED_BASE = getSelfHostedRuntimeBaseUrl();
const SELF_HOSTED_LABEL = getSelfHostedRuntimeLabel();

function pickDevMachines(all: Device[], currentId: string | undefined): Device[] {
  return all.filter(
    (d) =>
      d.online &&
      !d.isGuest &&
      d.id !== currentId &&
      d.deviceClass !== "edge-mobile",
  );
}

function pickMobileDevices(all: Device[], currentId: string | undefined): Device[] {
  return all.filter(
    (d) =>
      d.online &&
      !d.isGuest &&
      d.id !== currentId &&
      d.deviceClass === "edge-mobile",
  );
}

function deriveTargetUrl(target: PhonePushTarget, result: PhonePushResult): string {
  const slug = encodeURIComponent(result.slug);
  switch (target.kind) {
    case "dev-hw":
      return `${target.relayHttpUrl.replace(/\/$/, "")}/d/${target.deviceId}/phone/projects/browse?slug=${slug}`;
    case "yaver-cloud":
      return `${(target.cloudBaseUrl ?? YAVER_CLOUD_BASE).replace(/\/$/, "")}/phone/projects/browse?slug=${slug}`;
    case "custom":
      return `${target.baseUrl.replace(/\/$/, "")}/phone/projects/browse?slug=${slug}`;
  }
}

function formatBytes(n: number): string {
  if (!n) return "0 B";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export default function PhoneProjectsView() {
  const [projects, setProjects] = useState<PhoneProject[]>([]);
  const [templates, setTemplates] = useState<PhoneTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  // Non-blocking inline feedback for actions (insert/deploy/promote/etc),
  // replacing native alert() so failures don't dump raw error text.
  const [notice, setNotice] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  const showNotice = useCallback((type: "ok" | "error", text: string) => {
    setNotice({ type, text });
    setTimeout(() => setNotice((n) => (n?.text === text ? null : n)), 6000);
  }, []);

  function cleanMessage(e: unknown, fallback: string): string {
    const raw = e instanceof Error ? e.message : typeof e === "string" ? e : "";
    return raw.trim() && raw.trim().length <= 180 ? raw.trim() : fallback;
  }

  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [templateId, setTemplateId] = useState("todos");
  const [prompt, setPrompt] = useState("");
  const [importedConversation, setImportedConversation] = useState("");
  const [analyzingImport, setAnalyzingImport] = useState(false);
  const [creating, setCreating] = useState(false);

  const [selected, setSelected] = useState<PhoneProject | null>(null);
  const [tables, setTables] = useState<Array<{ name: string; rowCount?: number }>>([]);
  const [activeTable, setActiveTable] = useState<string | null>(null);
  const [rows, setRows] = useState<Array<Record<string, unknown>>>([]);
  const [insertJSON, setInsertJSON] = useState("{}");
  const [promoting, setPromoting] = useState<string | null>(null);
  const [showDesign, setShowDesign] = useState(false);

  // Deploy state (roadmap §Wedge Demo)
  const { token } = useAuth();
  const { devices } = useDevices(token);

  // Agent-relay backend for the shared design studio: schema/app + design come
  // from the connected agent over HTTP; edits persist into the project's app.yaml.
  const designBackend = useMemo<DesignBackend | null>(() => {
    const slug = selected?.slug;
    if (!slug) return null;
    return {
      loadSchemaApp: async () => {
        const p = await agentClient.getPhoneProject(slug);
        if (!p) return null;
        return { schema: p.schema ?? { tables: [] }, app: p.app ?? {} };
      },
      attachData: (onMutate) => attachAgentBridge(slug, { onMutate }),
      loadDesign: async () => (await agentClient.getPhoneDesign(slug)) ?? {},
      saveDesign: async (d) => {
        await agentClient.setPhoneDesign(slug, d);
      },
    };
  }, [selected?.slug]);

  const designColumns = useMemo(() => {
    const t = selected?.schema?.tables?.find((x) => x.name === activeTable);
    return t ? t.columns.map((cc) => cc.name) : [];
  }, [selected, activeTable]);

  const designAi = useMemo(
    () =>
      gatewayConfigured() && token
        ? (text: string, ctx?: { nodeId: string; kind: string }) => draftDesignPatch(text, token, ctx)
        : undefined,
    [token],
  );

  // Browser-local sandbox vs agent-relay view. Default to local when no agent
  // is connected; a connected user can still opt into the browser sandbox.
  const agentConnected = useAgentConnected();
  const [forceLocal, setForceLocal] = useState(false);
  const localMode = forceLocal || !agentConnected;
  // Yaver Cloud deploy path is hidden at launch — no paid features yet.
  const canUseCloudPreview = false;
  const canUseYaverCloud = false;
  const [currentDeviceId] = useState<string | undefined>(undefined);
  const devMachines = useMemo(
    () => pickDevMachines(devices, currentDeviceId),
    [devices, currentDeviceId],
  );
  const mobileDevices = useMemo(
    () => pickMobileDevices(devices, currentDeviceId),
    [devices, currentDeviceId],
  );
  const [selectedDevMachineId, setSelectedDevMachineId] = useState<string | null>(null);
  const [selectedMobileDeviceId, setSelectedMobileDeviceId] = useState<string | null>(null);
  useEffect(() => {
    if (!selectedDevMachineId && devMachines.length) {
      setSelectedDevMachineId(devMachines[0].id);
    }
  }, [devMachines, selectedDevMachineId]);
  useEffect(() => {
    if (!selectedMobileDeviceId && mobileDevices.length) {
      setSelectedMobileDeviceId(mobileDevices[0].id);
    }
  }, [mobileDevices, selectedMobileDeviceId]);
  const selectedDevMachine = useMemo(
    () => devMachines.find((d) => d.id === selectedDevMachineId) ?? null,
    [devMachines, selectedDevMachineId],
  );
  const selectedMobileDevice = useMemo(
    () => mobileDevices.find((d) => d.id === selectedMobileDeviceId) ?? null,
    [mobileDevices, selectedMobileDeviceId],
  );
  const [deploying, setDeploying] = useState<"dev-hw" | "yaver-cloud" | "custom" | "both" | null>(null);
  const [lastDeploy, setLastDeploy] = useState<{ kind: "dev-hw" | "yaver-cloud" | "custom"; url: string; via: string } | null>(null);
  const [showAdvanced, setShowAdvanced] = useState(false);
  const importedBrief = useMemo(
    () => (importedConversation.trim() ? buildImportedConversationBrief(importedConversation) : null),
    [importedConversation],
  );

  const load = useCallback(async () => {
    setErr(null);
    try {
      const [ps, ts] = await Promise.all([
        agentClient.listPhoneProjects(),
        templates.length ? Promise.resolve(templates) : agentClient.listPhoneTemplates(),
      ]);
      setProjects(ps);
      if (!templates.length) setTemplates(ts);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [templates]);

  useEffect(() => {
    void load();
  }, [load]);

  const loadDetail = useCallback(async (slug: string) => {
    const [p, ts] = await Promise.all([
      agentClient.getPhoneProject(slug),
      agentClient.listPhoneTables(slug),
    ]);
    setSelected(p);
    setTables(ts);
    if (ts.length) {
      setActiveTable(ts[0].name);
      const r = await agentClient.browsePhoneTable(slug, ts[0].name);
      setRows(r.rows);
    } else {
      setActiveTable(null);
      setRows([]);
    }
  }, []);

  const switchTable = useCallback(async (table: string) => {
    if (!selected) return;
    setActiveTable(table);
    const r = await agentClient.browsePhoneTable(selected.slug, table);
    setRows(r.rows);
  }, [selected]);

  async function create() {
    const suggestedName = importedBrief?.suggestedName ?? "";
    const projectName = name.trim() || suggestedName;
    if (!projectName) return;
    setCreating(true);
    try {
      const effectivePrompt = mergeImportedConversationPrompt(prompt, importedConversation);
      const p = await agentClient.createPhoneProject({
        name: projectName,
        template: effectivePrompt ? undefined : templateId,
        prompt: effectivePrompt || undefined,
        importUrl: !effectivePrompt && importedConversation.trim() ? importedBrief?.sourceUrl : undefined,
        importContent: !effectivePrompt && importedConversation.trim() ? importedConversation.trim() : undefined,
        importTitle: !effectivePrompt && importedConversation.trim() ? importedBrief?.title : undefined,
      });
      setName("");
      setPrompt("");
      setImportedConversation("");
      setShowForm(false);
      await load();
      await loadDetail(p.slug);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setCreating(false);
    }
  }

  async function analyzeImportedConversation() {
    if (!importedBrief) return;
    setAnalyzingImport(true);
    try {
      const plan = await agentClient.analyzeConversationImport({
        url: importedBrief.sourceUrl,
        content: importedConversation,
        title: importedBrief.title,
      });
      if (!name.trim() && plan.suggestedName) setName(plan.suggestedName);
      setPrompt(plan.generatedPrompt);
    } catch (e) {
      showNotice("error", cleanMessage(e, "Couldn't analyze the conversation. Try again or write the brief manually."));
    } finally {
      setAnalyzingImport(false);
    }
  }

  async function doDelete(slug: string) {
    if (!window.confirm(`Delete project "${slug}"? This removes the SQLite file.`)) return;
    await agentClient.deletePhoneProject(slug);
    if (selected?.slug === slug) {
      setSelected(null);
      setTables([]);
      setRows([]);
    }
    await load();
  }

  async function doExport(slug: string) {
    const blob = await agentClient.exportPhoneProjectBlob(slug);
    if (!blob) {
      showNotice("error", "Export failed — the agent isn't reachable. Check your connection and try again.");
      return;
    }
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${slug}.tgz`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  }

  async function doInsert() {
    if (!selected || !activeTable) return;
    try {
      const doc = JSON.parse(insertJSON || "{}");
      if (!doc || typeof doc !== "object") throw new Error("JSON must be an object");
      await agentClient.insertPhoneRow(selected.slug, activeTable, doc);
      setInsertJSON("{}");
      await switchTable(activeTable);
    } catch (e) {
      showNotice("error", cleanMessage(e, "Insert failed. Check the JSON is a valid object and try again."));
    }
  }

  async function doDeleteRow(id: unknown) {
    if (!selected || !activeTable || !id) return;
    if (!window.confirm(`Delete row ${id}?`)) return;
    await agentClient.deletePhoneRow(selected.slug, activeTable, String(id));
    await switchTable(activeTable);
  }

  async function doPromote(targetID: string, label: string, dryRun: boolean) {
    if (!selected) return;
    setPromoting(targetID);
    try {
      const r = await agentClient.promotePhoneProject(selected.slug, targetID, { dryRun, run: true });
      if (r.error) showNotice("error", `${label}: ${cleanMessage(r.error, "promotion failed")}`);
      else showNotice("ok", `Plan ${r.state?.id} saved (complexity: ${r.state?.complexity}). See the Switch tab for details.`);
    } catch (e) {
      showNotice("error", cleanMessage(e, `Couldn't plan ${label} — the agent may be unreachable.`));
    } finally {
      setPromoting(null);
    }
  }

  // ── Deploy (roadmap §Wedge Demo) ─────────────────────────────────────

  async function runPush(target: PhonePushTarget, kind: "dev-hw" | "yaver-cloud" | "custom", via: string) {
    if (!selected) return;
    setDeploying(kind);
    try {
      const res = await agentClient.pushPhoneProject(selected.slug, target, { onConflict: "overwrite", includeData: true });
      const url = res.browseUrl?.startsWith("http") ? res.browseUrl : deriveTargetUrl(target, res);
      setLastDeploy({ kind, url, via });
    } catch (e) {
      showNotice("error", cleanMessage(e, `Deploy to ${via} failed. The target may be offline — try again.`));
    } finally {
      setDeploying(null);
    }
  }

  async function deployToDevMachine() {
    if (!selectedDevMachine) {
      showNotice("error", "No dev machine paired. Install Yaver on your Mac/Linux/Pi and sign in with the same account.");
      return;
    }
    const relayHttpUrl = agentClient.activeRelayHttpUrl;
    if (!relayHttpUrl) {
      showNotice("error", "This dashboard isn't relay-routed, so it can't reach your other devices. Reconnect and try again.");
      return;
    }
    await runPush(
      { kind: "dev-hw", deviceId: selectedDevMachine.id, relayHttpUrl },
      "dev-hw",
      selectedDevMachine.name,
    );
  }

  async function deployToMobile() {
    if (!selectedMobileDevice) {
      showNotice("error", "No mobile device online. Open Yaver on the target phone and sign in with the same account.");
      return;
    }
    const relayHttpUrl = agentClient.activeRelayHttpUrl;
    if (!relayHttpUrl) {
      showNotice("error", "This dashboard isn't relay-routed, so it can't reach your other devices. Reconnect and try again.");
      return;
    }
    await runPush(
      { kind: "dev-hw", deviceId: selectedMobileDevice.id, relayHttpUrl },
      "dev-hw",
      `${selectedMobileDevice.name} (mobile)`,
    );
  }

  async function deployToCloud() {
    await runPush(
      { kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE, cloudAuthToken: token ?? undefined },
      "yaver-cloud",
      "Yaver Cloud",
    );
  }

  async function deployToSelfHosted() {
    if (!selected || !SELF_HOSTED_BASE) {
      showNotice("error", "This dashboard build has no self-hosted runtime configured — use your paired dev machine instead.");
      return;
    }
    await runPush(
      { kind: "custom", baseUrl: SELF_HOSTED_BASE },
      "custom",
      SELF_HOSTED_LABEL,
    );
  }

  async function deployToBoth() {
    if (!selected) return;
    if (!selectedDevMachine) {
      showNotice("error", "No dev machine paired. Install Yaver on your Mac/Linux/Pi and sign in with the same account.");
      return;
    }
    const relayHttpUrl = agentClient.activeRelayHttpUrl;
    if (!relayHttpUrl) {
      showNotice("error", "This dashboard isn't relay-routed, so it can't reach your other devices. Reconnect and try again.");
      return;
    }
    setDeploying("both");
    try {
      const result = await agentClient.deployPhoneProjectRuntime({
        slug: selected.slug,
        includeData: true,
        exports: [
          { kind: "dev-hw", deviceId: selectedDevMachine.id, relayHttpUrl, onConflict: "overwrite" },
          { kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE, cloudAuthToken: token ?? undefined, onConflict: "overwrite" },
        ],
      });
      const cloud = result.pushes.find((push) => push.kind === "yaver-cloud");
      const local = result.pushes.find((push) => push.kind === "dev-hw");
      if (cloud) {
        setLastDeploy({ kind: "yaver-cloud", via: "Yaver Cloud + Dev Machine", url: cloud.result.browseUrl || deriveTargetUrl({ kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE, cloudAuthToken: token ?? undefined }, cloud.result) });
      } else if (local) {
        setLastDeploy({ kind: "dev-hw", via: "Dev Machine + Yaver Cloud", url: local.result.browseUrl || deriveTargetUrl({ kind: "dev-hw", deviceId: selectedDevMachine.id, relayHttpUrl }, local.result) });
      }
    } catch (e) {
      showNotice("error", cleanMessage(e, "Deploy failed. One or more targets may be offline — try again."));
    } finally {
      setDeploying(null);
    }
  }

  async function deployToSelfHostedAndCloud() {
    if (!selected || !SELF_HOSTED_BASE) {
      showNotice("error", "This dashboard build has no self-hosted runtime configured — use your paired dev machine instead.");
      return;
    }
    setDeploying("both");
    try {
      const result = await agentClient.deployPhoneProjectRuntime({
        slug: selected.slug,
        includeData: true,
        exports: [
          { kind: "custom", baseUrl: SELF_HOSTED_BASE, onConflict: "overwrite" },
          { kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE, cloudAuthToken: token ?? undefined, onConflict: "overwrite" },
        ],
      });
      const cloud = result.pushes.find((push) => push.kind === "yaver-cloud");
      const selfHosted = result.pushes.find((push) => push.kind === "custom");
      if (cloud) {
        setLastDeploy({
          kind: "yaver-cloud",
          via: `${SELF_HOSTED_LABEL} + Yaver Cloud`,
          url: cloud.result.browseUrl || deriveTargetUrl({ kind: "yaver-cloud", cloudBaseUrl: YAVER_CLOUD_BASE, cloudAuthToken: token ?? undefined }, cloud.result),
        });
      } else if (selfHosted) {
        setLastDeploy({
          kind: "custom",
          via: `${SELF_HOSTED_LABEL} + Yaver Cloud`,
          url: selfHosted.result.browseUrl || deriveTargetUrl({ kind: "custom", baseUrl: SELF_HOSTED_BASE }, selfHosted.result),
        });
      }
    } catch (e) {
      showNotice("error", cleanMessage(e, "Deploy failed. One or more targets may be offline — try again."));
    } finally {
      setDeploying(null);
    }
  }

  const modeBar = (
    <div className="flex items-center gap-1 rounded-full border border-surface-800 bg-surface-950 p-1 text-xs">
      <button
        onClick={() => setForceLocal(true)}
        className={`rounded-full px-3 py-1 transition ${localMode ? "bg-indigo-600 text-white" : "text-surface-400 hover:text-surface-200"}`}
      >
        🖥️ This browser
      </button>
      <button
        onClick={() => setForceLocal(false)}
        disabled={!agentConnected}
        title={agentConnected ? "" : "No agent connected"}
        className={`rounded-full px-3 py-1 transition disabled:opacity-40 ${!localMode ? "bg-indigo-600 text-white" : "text-surface-400 hover:text-surface-200"}`}
      >
        ☁️ Connected agent
      </button>
    </div>
  );

  if (localMode) {
    return (
      <div className="flex flex-col gap-4">
        <div className="flex justify-end">{modeBar}</div>
        <BrowserSandbox />
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between gap-3">
        <h1 className="text-xl font-semibold text-surface-100">Phone Backend</h1>
        {modeBar}
      </div>
      <div>
        <p className="mt-1 text-sm text-surface-400">
          SQLite-backed mini backend hosted on your Mac, editable from the phone.
          Each project is a portable manifest — promote it to Convex, Supabase,
          Neon, Turso, or Postgres when you're ready. The switch engine keeps a
          7-day rollback window for every migration.
        </p>
      </div>

      {err ? (
        <div className="rounded border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-700 dark:text-red-300">
          {err}
        </div>
      ) : null}

      {notice ? (
        <div
          className={`rounded border p-3 text-sm ${
            notice.type === "ok"
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200"
              : "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300"
          }`}
        >
          {notice.text}
        </div>
      ) : null}

      <div className="flex items-center gap-3">
        {!showForm ? (
          <button
            onClick={() => setShowForm(true)}
            className="rounded bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500"
          >
            + New phone project
          </button>
        ) : null}
        <button
          onClick={() => void load()}
          className="rounded border border-surface-700 px-3 py-1.5 text-sm text-surface-300 hover:bg-surface-800"
        >
          Refresh
        </button>
      </div>

      {showForm ? (
        <div className="rounded border border-surface-800 bg-surface-900 p-4">
          <label className="text-xs uppercase tracking-wide text-surface-400">Project name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="My app"
            className="mt-1 w-full rounded border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
          />
          {importedBrief?.suggestedName && !name.trim() ? (
            <div className="mt-2 text-xs text-emerald-700 dark:text-emerald-300">
              Suggested name from import: {importedBrief.suggestedName}
            </div>
          ) : null}
          <label className="mt-4 block text-xs uppercase tracking-wide text-surface-400">Template</label>
          <div className="mt-2 grid grid-cols-2 gap-2">
            {templates.map((t) => (
              <button
                key={t.id}
                onClick={() => setTemplateId(t.id)}
                className={`rounded border p-3 text-left text-sm transition ${
                  templateId === t.id
                    ? "border-indigo-500 bg-indigo-500/10"
                    : "border-surface-800 bg-surface-950 hover:border-surface-600"
                }`}
              >
                <div className="font-medium text-surface-100">{t.label}</div>
                <div className="mt-0.5 text-xs text-surface-400">{t.description}</div>
              </button>
            ))}
          </div>
          <label className="mt-4 block text-xs uppercase tracking-wide text-surface-400">Project brief</label>
          <textarea
            value={prompt}
            onChange={(e) => setPrompt(e.target.value)}
            placeholder="Describe the app directly, or leave this empty and add a conversation/share URL below."
            className="mt-1 min-h-24 w-full rounded border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
          />
          <label className="mt-4 block text-xs uppercase tracking-wide text-surface-400">Add Conversation Or Share URL (Optional)</label>
          <textarea
            value={importedConversation}
            onChange={(e) => setImportedConversation(e.target.value)}
            placeholder="Optional: paste a share URL or copied conversation."
            className="mt-1 min-h-32 w-full rounded border border-surface-700 bg-surface-950 px-3 py-2 text-sm text-surface-100"
          />
          {importedBrief ? (
            <div className="mt-2 rounded border border-indigo-500/30 bg-indigo-500/10 p-3 text-xs text-indigo-800 dark:text-indigo-100">
              <div className="font-medium">{importedBrief.sourceLabel}</div>
              <div className="mt-1 text-indigo-700 dark:text-indigo-200/80">
                {importedBrief.title || `${importedBrief.charCount} chars imported`}
              </div>
              <button
                type="button"
                onClick={() => void analyzeImportedConversation()}
                disabled={analyzingImport}
                className="mt-3 rounded border border-indigo-400/40 px-3 py-1.5 text-xs font-medium text-indigo-800 dark:text-indigo-100 hover:bg-indigo-500/10 disabled:opacity-50"
              >
                {analyzingImport ? "Analyzing…" : "Analyze thread and generate technical plan"}
              </button>
            </div>
          ) : (
            <div className="mt-2 text-xs text-surface-500">
              You can create from a plain app brief, or add a conversation/share URL if you want Yaver to infer the technical plan from it.
            </div>
          )}
          <div className="mt-4 flex justify-end gap-2">
            <button
              onClick={() => setShowForm(false)}
              className="rounded border border-surface-700 px-3 py-1.5 text-sm text-surface-300 hover:bg-surface-800"
            >
              Cancel
            </button>
            <button
              disabled={creating || (!name.trim() && !importedBrief?.suggestedName)}
              onClick={create}
              className="rounded bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white disabled:opacity-50 hover:bg-indigo-500"
            >
              {creating ? "Creating…" : "Create"}
            </button>
          </div>
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        <div className="lg:col-span-1">
          <div className="mb-2 text-xs uppercase tracking-wide text-surface-500">Projects</div>
          {loading ? (
            <div className="text-sm text-surface-500">Loading…</div>
          ) : projects.length === 0 ? (
            <div className="rounded border border-surface-800 bg-surface-950 p-4 text-sm text-surface-400">
              No projects yet. Click “New phone project” above to create one.
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              {projects.map((p) => (
                <button
                  key={p.slug}
                  onClick={() => void loadDetail(p.slug)}
                  className={`rounded border p-3 text-left transition ${
                    selected?.slug === p.slug
                      ? "border-indigo-500 bg-indigo-500/10"
                      : "border-surface-800 bg-surface-950 hover:border-surface-600"
                  }`}
                >
                  <div className="text-sm font-medium text-surface-100">{p.name}</div>
                  <div className="mt-0.5 text-xs text-surface-400">
                    {p.slug}
                    {p.template ? ` · ${p.template}` : ""}
                  </div>
                  {p.stats ? (
                    <div className="mt-1 text-[11px] text-surface-500">
                      {p.stats.tableCount} tables · {p.stats.rowCount} rows · {formatBytes(p.stats.dbBytes)}
                    </div>
                  ) : null}
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="lg:col-span-2">
          {!selected ? (
            <div className="rounded border border-dashed border-surface-800 bg-surface-950 p-6 text-sm text-surface-500">
              Pick a project to browse its tables, insert rows, export a .tgz, or
              promote it to a real backend target.
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-lg font-semibold text-surface-100">{selected.name}</div>
                  <div className="text-xs text-surface-500">
                    {selected.slug} · updated {new Date(selected.updatedAt).toLocaleString()}
                  </div>
                </div>
                <div className="flex gap-2">
                  <button
                    onClick={() => setShowDesign((v) => !v)}
                    className="rounded border border-indigo-500 px-3 py-1.5 text-sm text-indigo-300 hover:bg-indigo-500/10"
                  >
                    {showDesign ? "Hide design studio" : "Design studio"}
                  </button>
                  <button
                    onClick={() => void doExport(selected.slug)}
                    className="rounded border border-surface-700 px-3 py-1.5 text-sm text-surface-200 hover:bg-surface-800"
                  >
                    Export .tgz
                  </button>
                  <button
                    onClick={() => void doDelete(selected.slug)}
                    className="rounded border border-red-500/50 px-3 py-1.5 text-sm text-red-700 dark:text-red-300 hover:bg-red-500/10"
                  >
                    Delete
                  </button>
                </div>
              </div>

              {showDesign && designBackend ? (
                <DesignStudioPanel
                  key={selected.slug}
                  backend={designBackend}
                  columns={designColumns}
                  aiDraft={designAi}
                  onDataMutate={() => {
                    if (activeTable) void switchTable(activeTable);
                  }}
                />
              ) : null}

              <div>
                <div className="mb-2 text-xs uppercase tracking-wide text-surface-500">Tables</div>
                <div className="flex flex-wrap gap-2">
                  {tables.length === 0 ? (
                    <div className="text-sm text-surface-500">No tables yet.</div>
                  ) : (
                    tables.map((t) => (
                      <button
                        key={t.name}
                        onClick={() => void switchTable(t.name)}
                        className={`rounded-full border px-3 py-1 text-xs transition ${
                          activeTable === t.name
                            ? "border-indigo-400 bg-indigo-500 text-white"
                            : "border-surface-700 bg-surface-950 text-surface-300 hover:border-surface-600"
                        }`}
                      >
                        {t.name}
                        {typeof t.rowCount === "number" ? (
                          <span className="ml-1 opacity-70">({t.rowCount})</span>
                        ) : null}
                      </button>
                    ))
                  )}
                </div>
              </div>

              {activeTable ? (
                <div className="flex flex-col gap-2">
                  <div className="flex items-start gap-2">
                    <input
                      value={insertJSON}
                      onChange={(e) => setInsertJSON(e.target.value)}
                      placeholder='{"id":"x","title":"hello"}'
                      className="flex-1 rounded border border-surface-700 bg-surface-950 px-3 py-2 font-mono text-xs text-surface-100"
                    />
                    <button
                      onClick={doInsert}
                      className="rounded bg-indigo-600 px-3 py-2 text-xs font-medium text-white hover:bg-indigo-500"
                    >
                      Insert
                    </button>
                  </div>
                  <div className="overflow-auto rounded border border-surface-800 bg-surface-950">
                    {rows.length === 0 ? (
                      <div className="p-4 text-sm text-surface-500">No rows.</div>
                    ) : (
                      <table className="w-full text-xs">
                        <thead className="bg-surface-900 text-surface-400">
                          <tr>
                            {Object.keys(rows[0]).map((k) => (
                              <th key={k} className="px-3 py-2 text-left font-medium">
                                {k}
                              </th>
                            ))}
                            <th />
                          </tr>
                        </thead>
                        <tbody>
                          {rows.map((r, i) => (
                            <tr key={i} className="border-t border-surface-800">
                              {Object.entries(r).map(([k, v]) => (
                                <td key={k} className="px-3 py-2 text-surface-200">
                                  {v === null || v === undefined
                                    ? "—"
                                    : typeof v === "object"
                                    ? JSON.stringify(v)
                                    : String(v)}
                                </td>
                              ))}
                              <td className="px-2 py-2 text-right">
                                <button
                                  onClick={() => void doDeleteRow(r.id ?? Object.values(r)[0])}
                                  className="text-xs text-red-400 hover:text-red-700 dark:hover:text-red-300"
                                >
                                  ×
                                </button>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                  </div>
                </div>
              ) : null}

              <div>
                <div className="mb-2 text-xs uppercase tracking-wide text-surface-500">Deploy</div>
                <p className="mb-3 text-xs text-surface-400">
                  Ship this mini-backend in one tap to another Yaver peer. The bundle moves agent-to-agent over direct/relay transport; Convex only provides account auth and device discovery, not project contents.
                </p>

                <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
                  {/* [Your Dev Machine] */}
                  <div className="rounded-lg border-2 border-indigo-500 bg-indigo-500/10 p-4">
                    <div className="text-base font-semibold text-indigo-800 dark:text-indigo-100">Your Dev Machine</div>
                    <div className="mt-0.5 text-xs text-indigo-700 dark:text-indigo-200/70">
                      {selectedDevMachine
                        ? `→ ${selectedDevMachine.name} · via relay`
                        : "No dev machine online yet."}
                    </div>
                    <div className="mt-3 flex items-center gap-2">
                      <button
                        disabled={deploying !== null || !selectedDevMachine}
                        onClick={() => void deployToDevMachine()}
                        className="rounded bg-indigo-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
                      >
                        {deploying === "dev-hw" ? "Deploying…" : "Deploy →"}
                      </button>
                      {devMachines.length > 1 ? (
                        <select
                          value={selectedDevMachineId ?? ""}
                          onChange={(e) => setSelectedDevMachineId(e.target.value || null)}
                          className="rounded border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
                        >
                          {devMachines.map((d) => (
                            <option key={d.id} value={d.id}>
                              {d.name}
                            </option>
                          ))}
                        </select>
                      ) : null}
                      {canUseYaverCloud ? (
                        <button
                          disabled={deploying !== null || !selectedDevMachine}
                          onClick={() => void deployToBoth()}
                          className="rounded border border-indigo-300/40 px-3 py-1.5 text-xs font-medium text-indigo-800 dark:text-indigo-100 hover:bg-indigo-500/10 disabled:opacity-50"
                        >
                          {deploying === "both" ? "Deploying Both…" : "Deploy Both →"}
                        </button>
                      ) : null}
                    </div>
                  </div>

                  <div className="rounded-lg border-2 border-sky-500/40 bg-sky-500/10 p-4">
                    <div className="text-base font-semibold text-sky-800 dark:text-sky-100">Your Mobile Device</div>
                    <div className="mt-0.5 text-xs text-sky-700 dark:text-sky-200/70">
                      {selectedMobileDevice
                        ? `→ ${selectedMobileDevice.name} · relay peer`
                        : "No mobile device online yet."}
                    </div>
                    <div className="mt-3 flex items-center gap-2">
                      <button
                        disabled={deploying !== null || !selectedMobileDevice}
                        onClick={() => void deployToMobile()}
                        className="rounded bg-sky-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-sky-500 disabled:opacity-50"
                      >
                        {deploying === "dev-hw" ? "Exporting…" : "Export →"}
                      </button>
                      {mobileDevices.length > 1 ? (
                        <select
                          value={selectedMobileDeviceId ?? ""}
                          onChange={(e) => setSelectedMobileDeviceId(e.target.value || null)}
                          className="rounded border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
                        >
                          {mobileDevices.map((d) => (
                            <option key={d.id} value={d.id}>
                              {d.name}
                            </option>
                          ))}
                        </select>
                      ) : null}
                    </div>
                    <div className="mt-3 text-[11px] leading-5 text-sky-800 dark:text-sky-100/80">
                      Uses the same peer path as machine export: source agent `GET /phone/projects/export`, target mobile agent `POST /phone/projects/receive`.
                    </div>
                  </div>

                  {canUseYaverCloud ? (
                    <div className="rounded-lg border-2 border-surface-700 bg-surface-950 p-4">
                      <div className="text-base font-semibold text-surface-100">Yaver Cloud</div>
                      <div className="mt-0.5 text-xs text-surface-400">
                        {canUseCloudPreview ? "Private preview" : "Managed machine"} at {YAVER_CLOUD_BASE.replace(/^https?:\/\//, "")}
                      </div>
                      <div className="mt-3">
                        <button
                          disabled={deploying !== null}
                          onClick={() => void deployToCloud()}
                          className="rounded border border-indigo-500 px-3 py-1.5 text-xs font-medium text-indigo-700 dark:text-indigo-200 hover:bg-indigo-500/10 disabled:opacity-50"
                        >
                          {deploying === "yaver-cloud" ? "Deploying…" : "Deploy →"}
                        </button>
                      </div>
                    </div>
                  ) : null}

                  {SELF_HOSTED_BASE ? (
                    <div className="rounded-lg border-2 border-surface-700 bg-surface-950 p-4">
                      <div className="text-base font-semibold text-surface-100">{SELF_HOSTED_LABEL}</div>
                      <div className="mt-0.5 text-xs text-surface-400">
                        {SELF_HOSTED_BASE.replace(/^https?:\/\//, "")}
                      </div>
                      <div className="mt-3">
                        <button
                          disabled={deploying !== null}
                          onClick={() => void deployToSelfHosted()}
                          className="rounded border border-indigo-500 px-3 py-1.5 text-xs font-medium text-indigo-700 dark:text-indigo-200 hover:bg-indigo-500/10 disabled:opacity-50"
                        >
                          {deploying === "custom" ? "Deploying…" : "Deploy →"}
                        </button>
                      </div>
                    </div>
                  ) : null}

                  {SELF_HOSTED_BASE && canUseYaverCloud ? (
                    <div className="rounded-lg border-2 border-surface-700 bg-surface-950 p-4">
                      <div className="text-base font-semibold text-surface-100">{SELF_HOSTED_LABEL} + Cloud</div>
                      <div className="mt-0.5 text-xs text-surface-400">
                        Push the same sandbox to your self-hosted runtime and Yaver Cloud in one run.
                      </div>
                      <div className="mt-3">
                        <button
                          disabled={deploying !== null}
                          onClick={() => void deployToSelfHostedAndCloud()}
                          className="rounded border border-indigo-500 px-3 py-1.5 text-xs font-medium text-indigo-700 dark:text-indigo-200 hover:bg-indigo-500/10 disabled:opacity-50"
                        >
                          {deploying === "both" ? "Deploying Both…" : "Deploy Both →"}
                        </button>
                      </div>
                    </div>
                  ) : null}
                </div>

                {lastDeploy ? (
                  <a
                    href={lastDeploy.url}
                    target="_blank"
                    rel="noreferrer"
                    className="mt-3 block rounded border border-emerald-500/40 bg-emerald-500/10 p-3 text-xs text-emerald-700 dark:text-emerald-200 hover:bg-emerald-500/15"
                  >
                    ✓ Running on {lastDeploy.via} — <span className="underline">{lastDeploy.url}</span>
                  </a>
                ) : null}

                <button
                  onClick={() => setShowAdvanced((v) => !v)}
                  className="mt-4 text-xs text-surface-400 hover:text-surface-200"
                >
                  {showAdvanced ? "▾" : "▸"} Advanced — promote to a switch-engine target
                </button>

                {showAdvanced ? (
                  <div className="mt-2 grid grid-cols-1 gap-2 md:grid-cols-2">
                    {ADVANCED_PROMOTE_TARGETS.map((t) => (
                      <div
                        key={t.id}
                        className="rounded border border-surface-800 bg-surface-950 p-3"
                      >
                        <div className="text-sm font-medium text-surface-100">{t.label}</div>
                        <div className="mt-0.5 text-xs text-surface-400">{t.sub}</div>
                        <div className="mt-2 flex gap-2">
                          <button
                            disabled={promoting === t.id}
                            onClick={() => void doPromote(t.id, t.label, true)}
                            className="rounded border border-surface-700 px-2 py-1 text-xs text-surface-200 hover:bg-surface-800 disabled:opacity-50"
                          >
                            Dry run
                          </button>
                          <button
                            disabled={promoting === t.id}
                            onClick={() => void doPromote(t.id, t.label, false)}
                            className="rounded bg-indigo-600 px-2 py-1 text-xs font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
                          >
                            Plan
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
