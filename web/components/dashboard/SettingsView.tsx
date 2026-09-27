"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { useDevices, type Device } from "@/lib/use-devices";
import { PasskeysCard } from "./PasskeyEnrollPrompt";
import YaverAgentSettings from "./YaverAgentSettings";
import McpServersCard from "./McpServersCard";
import BillingView from "./BillingView";
import GitSettingsCard from "./GitSettingsCard";
import SourceCodeStatusCard from "./SourceCodeStatusCard";
import VisionSettingsCard from "./VisionSettingsCard";
import OpenCodeSettingsView from "./OpenCodeSettingsView";
import PublisherSetupCard from "./PublisherSetupCard";
import { agentClient } from "@/lib/agent-client";
import type { DogfoodSourceStatus, RemoteRuntimeSession } from "@/lib/agent-client";
import RemoteRuntimeViewer from "./RemoteRuntimeViewer";
import { ENABLE_RELAY_PRO_UI } from "@/lib/launchFlags";
import { useAutoRenderVibing } from "@/lib/autoRenderVibing";
import {
  resolveRuntimeProjectPreference,
  runtimeProjectCatalogMap,
  runtimeProjectDefaultMap,
  runtimeProjectDisplayName,
  runtimeProjectMeta,
  runtimeProjectPreferenceFor,
  type RuntimeProjectCatalogRow,
  type RuntimeProjectPreference,
  type RuntimeProjectSeed,
} from "@/lib/runtimeProjectSettings";
import pkg from "../../package.json";

const WEB_VERSION = (pkg as { version?: string }).version ?? "unknown";

interface SettingsViewProps {
  user: {
    id: string;
    email: string;
    name?: string;
    provider?: string;
    avatarUrl?: string;
    isOwner?: boolean;
  } | null;
  onLogout: () => void;
  onOpenTwoFactor: () => void;
}

type GlobalModelDefaults = {
  claude: { model: string };
  codex: { model: string; reasoningEffort?: string };
  opencode: { model: string };
};

type GlobalModelRow = {
  modelId: string;
  runnerId: string;
  name: string;
  isDefault?: boolean;
  defaultReasoningEffort?: string;
  supportedReasoningEfforts?: string[];
};

// Deliberately empty: Convex owns both the choices and current defaults.
// Showing an embedded "temporary" catalog here would make a stale client look
// authoritative precisely when the backend is unreachable.
const EMPTY_GLOBAL_MODEL_DEFAULTS: GlobalModelDefaults = {
  claude: { model: "" },
  codex: { model: "" },
  opencode: { model: "" },
};

function GlobalModelDefaultsCard({ token }: { token: string | null }) {
  const [defaults, setDefaults] = useState<GlobalModelDefaults>(EMPTY_GLOBAL_MODEL_DEFAULTS);
  const [models, setModels] = useState<GlobalModelRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void Promise.all([
      fetch(`${CONVEX_URL}/config?ownerDefaults=${Date.now()}`, {
        cache: "no-store",
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      }),
      fetch(`${CONVEX_URL}/models`, { cache: "no-store" }),
    ])
      .then(async ([configResponse, modelsResponse]) => {
        const [configBody, modelsBody] = await Promise.all([
          configResponse.json().catch(() => ({})),
          modelsResponse.json().catch(() => ({})),
        ]);
        if (!configResponse.ok) throw new Error(configBody?.error || `Config request failed (HTTP ${configResponse.status})`);
        if (!cancelled && configBody?.modelDefaults) setDefaults(configBody.modelDefaults);
        if (!cancelled && modelsResponse.ok) setModels(Array.isArray(modelsBody?.models) ? modelsBody.models : []);
      })
      .catch((error) => {
        if (!cancelled) setMessage(error instanceof Error ? error.message : "Could not load global model defaults.");
      });
    return () => { cancelled = true; };
  }, [token]);

  const save = async () => {
    if (!token) return;
    setBusy(true);
    setMessage(null);
    try {
      const response = await fetch(`${CONVEX_URL}/config/model-defaults`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
        body: JSON.stringify({ defaults }),
      });
      const body = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(body?.error || `Save failed (HTTP ${response.status})`);
      setDefaults(body.modelDefaults);
      setMessage("Global runner defaults saved. Agents refresh from Convex within one minute.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Could not save global model defaults.");
    } finally {
      setBusy(false);
    }
  };

  const modelsForRunner = (runner: keyof GlobalModelDefaults) => models.filter((row) => {
    const canonical = row.runnerId === "claude-code" ? "claude" : row.runnerId;
    return canonical === runner;
  });
  const selectedCodexModel = modelsForRunner("codex").find((row) => row.modelId === defaults.codex.model);
  const codexReasoningEfforts = selectedCodexModel?.supportedReasoningEfforts || [];

  const setModel = (runner: keyof GlobalModelDefaults, model: string) => {
    setDefaults((current) => {
      const next = { ...current, [runner]: { ...current[runner], model } };
      if (runner !== "codex") return next;
      const selected = modelsForRunner("codex").find((row) => row.modelId === model);
      const efforts = selected?.supportedReasoningEfforts || [];
      const currentEffort = current.codex.reasoningEffort || "";
      return {
        ...next,
        codex: {
          ...next.codex,
          reasoningEffort: efforts.includes(currentEffort)
            ? currentEffort
            : selected?.defaultReasoningEffort || efforts[0],
        },
      };
    });
  };

  return (
    <details className="card mb-6" data-testid="global-model-defaults">
      <summary className="cursor-pointer text-sm font-medium uppercase tracking-wider text-surface-400">
        Global runner defaults · owner
      </summary>
      <p className="mt-3 text-xs leading-5 text-surface-500">
        Used only when a person has not selected a model, and once as recovery when an explicit model is rejected by its provider.
      </p>
      <div className="mt-4 grid gap-3">
        {(["claude", "codex", "opencode"] as const).map((runner) => {
          const choices = modelsForRunner(runner);
          return (
            <label key={runner} className="grid gap-1 sm:grid-cols-[7rem_minmax(0,1fr)] sm:items-center">
              <span className="text-xs font-medium capitalize text-surface-300">{runner}</span>
              {choices.length > 0 ? (
                <select
                  aria-label={`${runner} global model`}
                  value={defaults[runner].model}
                  onChange={(event) => setModel(runner, event.target.value)}
                  className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs text-surface-100"
                >
                  {!choices.some((row) => row.modelId === defaults[runner].model) ? (
                    <option value={defaults[runner].model}>{defaults[runner].model}</option>
                  ) : null}
                  {choices.map((row) => <option key={row.modelId} value={row.modelId}>{row.name || row.modelId}</option>)}
                </select>
              ) : <span className="rounded-md border border-surface-800 bg-surface-950 px-3 py-2 text-xs text-surface-500">Catalog unavailable</span>}
            </label>
          );
        })}
        {codexReasoningEfforts.length > 0 ? <label className="grid gap-1 sm:grid-cols-[7rem_minmax(0,1fr)] sm:items-center">
          <span className="text-xs font-medium text-surface-300">Codex effort</span>
          <select
            aria-label="Codex global reasoning effort"
            value={codexReasoningEfforts.includes(defaults.codex.reasoningEffort || "")
              ? defaults.codex.reasoningEffort
              : selectedCodexModel?.defaultReasoningEffort || codexReasoningEfforts[0]}
            onChange={(event) => setDefaults((current) => ({
              ...current,
              codex: { ...current.codex, reasoningEffort: event.target.value },
            }))}
            className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs text-surface-100"
          >
            {codexReasoningEfforts.map((effort) => (
              <option key={effort} value={effort}>
                {effort === "xhigh" ? "Extra high" : effort === "max" ? "More reasoning" : `${effort[0].toUpperCase()}${effort.slice(1)}`}
              </option>
            ))}
          </select>
        </label> : null}
      </div>
      <button
        type="button"
        onClick={() => void save()}
        disabled={busy || !token || Object.values(defaults).some((row) => !row.model.trim())}
        className="mt-4 rounded-md bg-violet-600 px-3 py-2 text-xs font-semibold text-white disabled:opacity-40"
      >
        {busy ? "Saving…" : "Save global defaults"}
      </button>
      {message ? <p className="mt-2 text-xs text-surface-400" role="status">{message}</p> : null}
    </details>
  );
}

type ThirdPartyDogfoodApp = { _id: string; appId: string; label: string; allowedScopes: string[]; enabled: boolean };
type ThirdPartyDogfoodInstallation = { _id: string; appId: string; installationId: string; label?: string; platform: string; status: string; proofVerifiedAt?: number; tester?: { name?: string; email: string }; controlPresentation?: "auto" | "minimized-y"; gestureSupported?: boolean; gestureCapabilityReason?: string; controlOnboardingSeenAt?: number };
type ThirdPartyDogfoodTester = { _id: string; appId: string; testerEmail: string; testerUserId?: string; status: "active" | "revoked"; tester?: { name?: string; email: string } };

function ThirdPartyDogfoodCard({ token }: { token: string | null }) {
  const [apps, setApps] = useState<ThirdPartyDogfoodApp[]>([]);
  const [installations, setInstallations] = useState<ThirdPartyDogfoodInstallation[]>([]);
  const [testers, setTesters] = useState<ThirdPartyDogfoodTester[]>([]);
  const [appId, setAppId] = useState("");
  const [label, setLabel] = useState("");
  const [projectSlug, setProjectSlug] = useState("");
  const [testerAppId, setTesterAppId] = useState("");
  const [testerEmail, setTesterEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const pendingInstallations = useMemo(() => installations.filter((row) => row.status === "pending"), [installations]);
  const activeInstallations = useMemo(() => installations.filter((row) => row.status === "active"), [installations]);
  const activeTesters = useMemo(() => testers.filter((row) => row.status === "active"), [testers]);

  const call = useCallback(async (path: string, init?: RequestInit) => {
    if (!token) throw new Error("Sign in to manage Dogfood apps.");
    const response = await fetch(`${CONVEX_URL}${path}`, {
      ...init,
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", ...(init?.headers || {}) },
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body?.error || `Dogfood request failed (HTTP ${response.status})`);
    return body;
  }, [token]);

  const refresh = useCallback(async () => {
    if (!token) return;
    const [appResult, installationResult, testerResult] = await Promise.all([call("/dogfood/apps"), call("/dogfood/installations"), call("/dogfood/testers")]);
    setApps(appResult.apps || []);
    setTesterAppId((current) => current && (appResult.apps || []).some((app: ThirdPartyDogfoodApp) => app.appId === current) ? current : appResult.apps?.[0]?.appId || "");
    setInstallations(installationResult.installations || []);
    setTesters(testerResult.testers || []);
  }, [call, token]);

  useEffect(() => { void refresh().catch((error) => setMessage(error instanceof Error ? error.message : "Dogfood registry unavailable.")); }, [refresh]);

  const save = async () => {
    setBusy(true); setMessage(null);
    try {
      await call("/dogfood/apps", { method: "POST", body: JSON.stringify({ appId: appId.trim(), label: label.trim(), projectSlug: projectSlug.trim() || undefined, allowedScopes: ["feedback", "blackbox"], enabled: true }) });
      setAppId(""); setLabel(""); setProjectSlug("");
      await refresh();
    } catch (error) { setMessage(error instanceof Error ? error.message : "Could not enable app."); }
    finally { setBusy(false); }
  };

  const act = async (installationId: string, action: "approve" | "cancel" | "revoke") => {
    setBusy(true); setMessage(null);
    try {
      await call("/dogfood/installations/action", { method: "POST", body: JSON.stringify({ installationId, action }) });
      await refresh();
    } catch (error) { setMessage(error instanceof Error ? error.message : "Could not update installation."); }
    finally { setBusy(false); }
  };

  const setTester = async (email: string, enabled: boolean, targetAppId = testerAppId) => {
    if (!targetAppId) return;
    setBusy(true); setMessage(null);
    try {
      const result = await call("/dogfood/testers", { method: "POST", body: JSON.stringify({ appId: targetAppId, testerEmail: email, enabled }) });
      setMessage(enabled ? `${email} can request ${targetAppId} Dogfood.` : `${email} revoked${result.revokedInstallations ? ` · ${result.revokedInstallations} installation(s) disabled` : ""}.`);
      if (enabled) setTesterEmail("");
      await refresh();
    } catch (error) { setMessage(error instanceof Error ? error.message : "Could not update tester access."); }
    finally { setBusy(false); }
  };

  return <div className="card mb-6 border-violet-500/20" data-testid="third-party-dogfood-section">
    <div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
      <div>
        <h3 className="text-sm font-medium uppercase tracking-wider text-violet-400/80">Developer management</h3>
        <p className="mt-1 max-w-2xl text-xs text-surface-500">Generic app registration, trusted-account approvals, and QR-based device handoff for third-party developer builds. Web UI and desktop GUI share this exact control surface.</p>
      </div>
      <div className="flex flex-wrap gap-2 text-[11px]">
        <a href="/add-device" className="rounded-md border border-surface-700 px-3 py-2 text-surface-200 hover:bg-surface-800">Register device QR</a>
        <a href="/dashboard?tab=settings" className="rounded-md border border-surface-700 px-3 py-2 text-surface-200 hover:bg-surface-800">Desktop GUI same UI</a>
      </div>
    </div>
    <div className="mt-4 grid gap-2 sm:grid-cols-3">
      <div className="rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-3">
        <div className="text-lg font-semibold text-violet-300">{apps.length}</div>
        <div className="mt-1 text-[11px] uppercase tracking-[0.16em] text-surface-500">Apps</div>
      </div>
      <div className="rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-3">
        <div className="text-lg font-semibold text-violet-300">{activeTesters.length}</div>
        <div className="mt-1 text-[11px] uppercase tracking-[0.16em] text-surface-500">Trusted accounts</div>
      </div>
      <div className="rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-3">
        <div className="text-lg font-semibold text-violet-300">{pendingInstallations.length}</div>
        <div className="mt-1 text-[11px] uppercase tracking-[0.16em] text-surface-500">Pending installs</div>
      </div>
    </div>
    <details className="mt-4 rounded-md border border-surface-800 p-3" open>
      <summary className="cursor-pointer text-sm font-semibold text-surface-200">Third-party apps</summary>
      <p className="mt-2 text-[11px] text-surface-500">Create a generic app record when the app has no OAuth or backend yet. Feedback and BlackBox remain the default scopes.</p>
      <div className="mt-3 grid gap-2 sm:grid-cols-3">
        <input aria-label="Dogfood app id" value={appId} onChange={(event) => setAppId(event.target.value)} placeholder="io.example.app" className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs" />
        <input aria-label="Dogfood app label" value={label} onChange={(event) => setLabel(event.target.value)} placeholder="App name" className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs" />
        <input aria-label="Dogfood project slug" value={projectSlug} onChange={(event) => setProjectSlug(event.target.value)} placeholder="Project slug (optional)" className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs" />
      </div>
      <button onClick={() => void save()} disabled={busy || !appId.trim() || !label.trim()} className="mt-2 rounded-md bg-violet-600 px-3 py-1.5 text-xs font-semibold text-white disabled:opacity-40">Enable app</button>
      {apps.length ? <div className="mt-3 space-y-2">{apps.map((app) => <div key={app._id} className="rounded-md border border-surface-800 p-2 text-xs"><span className="font-medium text-surface-200">{app.label}</span><span className="ml-2 text-surface-500">{app.appId} · {app.allowedScopes.join(", ")}</span></div>)}</div> : null}
    </details>
    {apps.length ? <details className="mt-3 rounded-md border border-surface-800 p-3">
      <summary className="cursor-pointer text-sm font-semibold text-surface-200">Trusted accounts</summary>
      <p className="mt-2 text-[11px] text-surface-500">Allow a Yaver account by email for one app. Revoking access also disables that account&apos;s pending and active installations.</p>
      <div className="mt-2 grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto]">
        <select aria-label="Dogfood tester app" value={testerAppId} onChange={(event) => setTesterAppId(event.target.value)} className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs">
          {apps.map((app) => <option key={app.appId} value={app.appId}>{app.label}</option>)}
        </select>
        <input aria-label="Dogfood tester email" type="email" value={testerEmail} onChange={(event) => setTesterEmail(event.target.value)} placeholder="tester@example.com" className="rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-xs" />
        <button onClick={() => void setTester(testerEmail.trim(), true)} disabled={busy || !testerAppId || !testerEmail.trim()} className="rounded-md bg-violet-600 px-3 py-2 text-xs font-semibold text-white disabled:opacity-40">Allow</button>
      </div>
      <div className="mt-2 space-y-2">{testers.filter((row) => row.appId === testerAppId).map((row) => <div key={row._id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-surface-800 px-2 py-2 text-xs">
        <div><span className="font-medium text-surface-200">{row.tester?.name || row.testerEmail}</span><span className="ml-2 text-surface-500">{row.testerEmail} · {row.status}{row.testerUserId ? " · linked" : " · activates after sign-in"}</span></div>
        <button onClick={() => void setTester(row.testerEmail, row.status !== "active", row.appId)} disabled={busy} className={row.status === "active" ? "text-red-400" : "text-violet-400"}>{row.status === "active" ? "Revoke" : "Restore"}</button>
      </div>)}</div>
    </details> : null}
    {installations.length ? <details className="mt-3 rounded-md border border-surface-800 p-3">
      <summary className="cursor-pointer text-sm font-semibold text-surface-200">Install approvals</summary>
      <p className="mt-2 text-[11px] text-surface-500">Approve verified requests, cancel enrollment, or revoke active access. Gesture and onboarding detail stays here instead of on the landing surface.</p>
      <div className="mt-3 space-y-2">{installations.map((row) => <div key={row._id} className="rounded-md border border-surface-800 p-2 text-xs">
        <div><span className="font-medium text-surface-200">{row.label || row.platform}</span><span className="ml-2 text-surface-500">{row.appId} · {row.status}{row.proofVerifiedAt ? " · key verified" : ""}{row.tester?.email ? ` · ${row.tester.email}` : ""}</span></div>
        {row.status === "active" ? <div className="mt-1 text-surface-500">
          {row.gestureSupported === true
            ? `Three-finger supported · ${row.controlPresentation === "auto" ? "gesture mode" : "Y mode"}`
            : row.gestureSupported === false ? "Y mode · gesture unavailable" : "Control capability not reported yet"}
          {row.controlOnboardingSeenAt ? " · onboarded" : " · onboarding pending"}
        </div> : null}
        <div className="mt-2 flex flex-wrap gap-2">
          {row.status === "pending" && row.proofVerifiedAt ? <button onClick={() => void act(row._id, "approve")} disabled={busy} className="rounded bg-emerald-700 px-2 py-1 text-white">Approve</button> : null}
          {row.status === "pending" ? <button onClick={() => void act(row._id, "cancel")} disabled={busy} className="text-red-400">Cancel</button> : null}
          {row.status === "active" ? <button onClick={() => void act(row._id, "revoke")} disabled={busy} className="text-red-400">Revoke</button> : null}
        </div>
      </div>)}</div>
    </details> : null}
    {message ? <p className="mt-3 text-xs text-surface-400">{message}</p> : null}
    <p className="mt-3 text-[11px] text-surface-600">UUID is a public handle, never a credential · approval requires a verified Ed25519 key proof · re-registration supersedes only the same installation slot</p>
    {activeInstallations.length ? <p className="mt-1 text-[11px] text-surface-600">{activeInstallations.length} installation(s) currently active across approved developer apps.</p> : null}
  </div>;
}

function DogfoodCard({ devices }: { devices: Device[] }) {
  const [source, setSource] = useState<DogfoodSourceStatus | null>(null);
  const [method, setMethod] = useState<"browser" | "webrtc">("browser");
  const [runner, setRunner] = useState("measuring…");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [browserURL, setBrowserURL] = useState<string | null>(null);
  const [session, setSession] = useState<RemoteRuntimeSession | null>(null);

  const surface: "web" | "desktop-gui" = typeof window !== "undefined" &&
    (window as unknown as { yaver?: { surface?: string } }).yaver?.surface === "desktop-gui"
      ? "desktop-gui"
      : "web";
  const renderID = agentClient.renderRouteDeviceId ?? agentClient.connectedDeviceId;
  const box = devices.find((device) => device.id === renderID);

  const refresh = useCallback(async () => {
    if (!agentClient.isConnected) {
      setSource(null);
      setMessage("Connect a render box to measure Dogfood readiness.");
      return;
    }
    setBusy(true);
    setMessage(null);
    try {
      const [nextSource, runners] = await Promise.all([
        agentClient.getDogfoodSourceStatus(),
        agentClient.getRunners(),
      ]);
      setSource(nextSource);
      const preferred = runners.find((row) => row.isDefault && row.ready !== false)
        ?? runners.find((row) => row.ready !== false && row.installed);
      setRunner(preferred ? `${preferred.name}${preferred.models?.find((model) => model.isDefault)?.name ? ` · ${preferred.models.find((model) => model.isDefault)!.name}` : ""}` : "No ready runner");
      setMessage(nextSource.message);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Dogfood readiness probe failed.");
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh, renderID]);

  const start = async () => {
    if (!source?.ready || !source.path) {
      setMessage(source?.remedy || "Yaver source is not ready on the render box.");
      return;
    }
    setBusy(true);
    setMessage("Preparing the checkout against canonical main…");
    try {
      await agentClient.prepareDogfoodCheckout(source.path);
      const caps = await agentClient.getProjectPreviewCapabilities(source.path, "expo", surface, true);
      const required = method === "browser" ? "dev-server" : "remote-runtime";
      const option = caps.options.find((row) => row.id === required);
      if (!caps.selfDevelopment || !option?.supported) {
        throw new Error(option?.reason || caps.reason || `${method} Dogfood is unavailable on this box.`);
      }
      if (method === "browser") {
        await agentClient.prepareRemoteRuntimeBrowserLane(source.path, "expo");
        const url = agentClient.devWebPreviewUrl;
        if (!url) throw new Error("The browser lane started but no same-owner preview URL was produced.");
        setSession(null);
        setBrowserURL(url);
        setMessage("Browser lane live. Authorization remains in headers; no credential is placed in this URL.");
      } else {
        const runtime = await agentClient.getRemoteRuntimeCapabilities(source.path, "expo", true);
        const target = runtime.targets.find((row) => row.id === "browser-window" && row.enabled)
          ?? runtime.targets.find((row) => row.enabled);
        if (!target) throw new Error(runtime.targets[0]?.reason || "No measured WebRTC target is available.");
        const next = await agentClient.startRemoteRuntimeSession(source.path, "expo", target.id, "direct-webrtc");
        setBrowserURL(null);
        setSession(next);
        setMessage(`WebRTC live on ${target.label}. The agent retains the same owner/access-graph checks.`);
      }
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Dogfood could not start.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="card mb-6 border-sky-500/20" data-testid="dogfood-section">
      <div className="mb-3 flex items-start justify-between gap-3">
        <div>
          <h3 className="text-sm font-medium uppercase tracking-wider text-sky-400/80">Dogfood</h3>
          <p className="mt-1 text-xs text-surface-500">Yaver rendering Yaver · measured on the selected render box</p>
        </div>
        <button onClick={() => void refresh()} disabled={busy} className="rounded-md border border-surface-700 px-2.5 py-1 text-xs text-surface-300 disabled:opacity-40">Refresh</button>
      </div>
      <div className="grid gap-2 text-xs sm:grid-cols-2">
        <div className="rounded-md border border-surface-800 p-2"><span className="text-surface-500">Box</span><div className="mt-1 truncate text-surface-200">{box?.alias || box?.name || (renderID ? `${renderID.slice(0, 8)}…` : "Not connected")}</div></div>
        <div className="rounded-md border border-surface-800 p-2"><span className="text-surface-500">Runner · model</span><div className="mt-1 truncate text-surface-200">{runner}</div></div>
        <div className="rounded-md border border-surface-800 p-2"><span className="text-surface-500">Checkout</span><div className="mt-1 truncate text-surface-200">{source?.ready ? `${source.branch || "ready"} · canonical origin verified` : source?.code || "Not measured"}</div></div>
        <label className="rounded-md border border-surface-800 p-2"><span className="text-surface-500">Method</span><select value={method} onChange={(event) => setMethod(event.target.value as "browser" | "webrtc")} className="mt-1 w-full bg-transparent text-surface-200"><option value="browser">Browser lane</option><option value="webrtc">WebRTC</option></select></label>
      </div>
      {message ? <p className="mt-3 text-xs text-surface-400">{message}</p> : null}
      <button onClick={() => void start()} disabled={busy || !source?.ready} className="mt-3 rounded-md bg-sky-600 px-3 py-1.5 text-xs font-semibold text-white disabled:cursor-not-allowed disabled:opacity-40">{busy ? "Working…" : "Start Dogfood"}</button>
      {browserURL ? <iframe src={browserURL} title="Yaver Dogfood browser lane" className="mt-4 h-[520px] w-full rounded-md border border-surface-800 bg-white" /> : null}
      {session ? <div className="mt-4"><RemoteRuntimeViewer session={session} onSessionChange={setSession} onClose={() => setSession(null)} /></div> : null}
      <p className="mt-3 text-[11px] text-surface-600">Bearer auth · same-owner/access-graph routing · origin allowlist · no token in URLs · relay is transport, never authority</p>
    </div>
  );
}

function AuthProviderIcon({
  provider,
  className = "h-5 w-5",
}: {
  provider: string;
  className?: string;
}) {
  switch (provider) {
    case "apple":
      return (
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
          <path d="M16.87 12.62c.03 2.82 2.47 3.76 2.5 3.77-.02.07-.39 1.34-1.28 2.66-.77 1.15-1.58 2.3-2.84 2.33-1.24.03-1.64-.73-3.06-.73-1.43 0-1.87.7-3.03.75-1.21.05-2.13-1.21-2.91-2.35-1.6-2.31-2.82-6.53-1.18-9.39.81-1.42 2.26-2.31 3.83-2.34 1.19-.03 2.31.8 3.06.8.74 0 2.13-.99 3.59-.84.61.03 2.31.25 3.41 1.86-.09.05-2.04 1.19-2.02 3.48ZM14.5 4.29c.64-.78 1.08-1.88.96-2.96-.92.04-2.04.61-2.7 1.39-.59.68-1.1 1.79-.96 2.85 1.03.08 2.06-.52 2.7-1.28Z" />
        </svg>
      );
    case "gitlab":
      return (
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
          <path d="M12 22.4 16.4 8.9h-8.8L12 22.4Z" />
          <path d="M12 22.4 7.6 8.9H1.8L12 22.4Z" />
          <path d="M1.8 8.9.5 13a.9.9 0 0 0 .33 1.01L12 22.4 1.8 8.9Z" />
          <path d="M1.8 8.9h5.8L5.1 1.2a.45.45 0 0 0-.86 0L1.8 8.9Z" />
          <path d="M12 22.4 16.4 8.9h5.8L12 22.4Z" />
          <path d="M22.2 8.9 23.5 13a.9.9 0 0 1-.33 1.01L12 22.4 22.2 8.9Z" />
          <path d="M22.2 8.9h-5.8l2.5-7.7a.45.45 0 0 1 .86 0l2.5 7.7Z" />
        </svg>
      );
    case "github":
      return (
        <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
          <path d="M12 .75a11.25 11.25 0 0 0-3.56 21.92c.56.1.76-.24.76-.54v-2.07c-3.1.67-3.76-1.31-3.76-1.31-.5-1.29-1.24-1.63-1.24-1.63-1.02-.69.08-.67.08-.67 1.12.08 1.72 1.16 1.72 1.16 1 .17 1.96 1.42 1.96 1.42.89 1.52 2.33 1.08 2.9.82.09-.72.35-1.08.63-1.33-2.47-.28-5.07-1.23-5.07-5.5 0-1.22.43-2.22 1.15-3-.12-.28-.5-1.42.11-2.96 0 0 .93-.3 3.06 1.14a10.7 10.7 0 0 1 5.58 0c2.13-1.44 3.06-1.14 3.06-1.14.61 1.54.23 2.68.11 2.96.72.78 1.15 1.78 1.15 3 0 4.28-2.61 5.22-5.1 5.49.4.35.75 1.04.75 2.1v3.11c0 .3.2.65.77.54A11.25 11.25 0 0 0 12 .75Z" />
        </svg>
      );
    case "google":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true" className={className}>
          <path fill="#EA4335" d="M12 10.2v3.9h5.5c-.24 1.25-.96 2.3-2.02 3.01l3.27 2.54c1.91-1.76 3.01-4.36 3.01-7.45 0-.72-.06-1.4-.18-2.05H12Z" />
          <path fill="#34A853" d="M12 22c2.7 0 4.97-.9 6.63-2.44l-3.27-2.54c-.9.6-2.05.95-3.36.95-2.58 0-4.76-1.74-5.54-4.08H3.08v2.62A10 10 0 0 0 12 22Z" />
          <path fill="#4A90E2" d="M6.46 13.9A5.98 5.98 0 0 1 6.15 12c0-.66.11-1.3.31-1.9V7.48H3.08A10 10 0 0 0 2 12c0 1.61.38 3.13 1.08 4.52l3.38-2.62Z" />
          <path fill="#FBBC05" d="M12 6.03c1.47 0 2.79.5 3.83 1.49l2.87-2.87C16.96 2.99 14.7 2 12 2A10 10 0 0 0 3.08 7.48l3.38 2.62C7.24 7.76 9.42 6.03 12 6.03Z" />
        </svg>
      );
    case "microsoft":
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true" className={className}>
          <path fill="#F25022" d="M2 2h9.5v9.5H2z" />
          <path fill="#7FBA00" d="M12.5 2H22v9.5h-9.5z" />
          <path fill="#00A4EF" d="M2 12.5h9.5V22H2z" />
          <path fill="#FFB900" d="M12.5 12.5H22V22h-9.5z" />
        </svg>
      );
    default:
      return (
        <span className={`inline-flex items-center justify-center rounded-full border border-surface-700 text-[10px] uppercase ${className}`}>
          {provider.slice(0, 1)}
        </span>
      );
  }
}

function StatusIcon({ primary }: { primary: boolean }) {
  if (primary) {
    return (
      <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className="h-4 w-4 text-emerald-700 dark:text-emerald-300">
        <path d="m9.05 2.93-1.1 2.24a1 1 0 0 1-.75.55l-2.47.36c-.82.12-1.15 1.13-.56 1.7l1.79 1.75c.25.24.36.6.3.94l-.42 2.46c-.14.82.72 1.45 1.45 1.07L10 14.9l2.21 1.16c.73.38 1.59-.25 1.45-1.07l-.42-2.46a1 1 0 0 1 .3-.94l1.79-1.75c.59-.57.26-1.58-.56-1.7l-2.47-.36a1 1 0 0 1-.75-.55l-1.1-2.24c-.37-.76-1.46-.76-1.83 0Z" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 20 20" fill="currentColor" aria-hidden="true" className="h-4 w-4 text-surface-400">
      <path fillRule="evenodd" d="M16.7 5.3a1 1 0 0 1 0 1.4l-7.2 7.2a1 1 0 0 1-1.4 0l-4-4a1 1 0 1 1 1.4-1.4l3.3 3.29 6.5-6.5a1 1 0 0 1 1.4 0Z" clipRule="evenodd" />
    </svg>
  );
}

function DeviceSurfaceIcon({ platform }: { platform: string }) {
  const value = String(platform || "").trim().toLowerCase();
  const isMobile = value === "ios" || value === "android";
  if (isMobile) {
    return (
      <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" aria-hidden="true">
        <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3m-3 18.75h3" />
      </svg>
    );
  }
  return (
    <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor" aria-hidden="true">
      <path strokeLinecap="round" strokeLinejoin="round" d="M9 17.25v1.007a3 3 0 01-.879 2.122L7.5 21h9l-.621-.621A3 3 0 0115 18.257V17.25m6-12V15a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 15V5.25A2.25 2.25 0 015.25 3h13.5A2.25 2.25 0 0121 5.25z" />
    </svg>
  );
}

function platformLabel(platform: string): string {
  switch (String(platform || "").trim().toLowerCase()) {
    case "darwin":
    case "macos":
      return "macOS";
    case "linux":
      return "Linux";
    case "windows":
      return "Windows";
    case "android":
      return "Android";
    case "ios":
      return "iOS";
    default:
      return platform || "Unknown OS";
  }
}

function RuntimeProjectDefaultsCard({ token, devices }: { token: string | null; devices: Device[] }) {
  const [catalogs, setCatalogs] = useState<Record<string, RuntimeProjectCatalogRow>>({});
  const [defaults, setDefaults] = useState<Record<string, RuntimeProjectPreference>>({});
  const [expandedDeviceId, setExpandedDeviceId] = useState<string | null>(null);
  const [saving, setSaving] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const cachedCatalogs = useMemo(() => {
    const out: Record<string, RuntimeProjectCatalogRow> = {};
    for (const device of devices) {
      if (Array.isArray(device.runtimeProjectCatalog) && device.runtimeProjectCatalog.length > 0) {
        out[device.id] = {
          deviceId: device.id,
          projects: device.runtimeProjectCatalog,
        };
      }
    }
    return out;
  }, [devices]);

  const cachedDefaults = useMemo(() => {
    const out: Record<string, RuntimeProjectPreference> = {};
    for (const device of devices) {
      if (device.defaultRuntimeProject?.projectName) {
        out[device.id] = runtimeProjectPreferenceFor(device.id, device.defaultRuntimeProject);
      }
    }
    return out;
  }, [devices]);

  useEffect(() => {
    if (!token) return;
    let cancelled = false;
    fetch(`${CONVEX_URL}/settings`, {
      headers: { Authorization: `Bearer ${token}` },
      cache: "no-store",
    })
      .then(async (res) => {
        const data = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(data?.error || `settings: HTTP ${res.status}`);
        return data?.settings || {};
      })
      .then((settings) => {
        if (cancelled) return;
        setCatalogs(runtimeProjectCatalogMap(settings.runtimeProjectCatalogByDevice));
        setDefaults(runtimeProjectDefaultMap(settings.defaultRuntimeProjectByDevice));
      })
      .catch((err) => {
        if (!cancelled) setMessage(err instanceof Error ? err.message : "Could not load runtime project defaults.");
      });
    return () => { cancelled = true; };
  }, [token]);

  const effectiveCatalogs = useMemo(
    () => ({ ...cachedCatalogs, ...catalogs }),
    [cachedCatalogs, catalogs],
  );
  const effectiveDefaults = useMemo(
    () => ({ ...cachedDefaults, ...defaults }),
    [cachedDefaults, defaults],
  );

  const postRuntimeSettings = useCallback(async (body: Record<string, unknown>) => {
    if (!token) throw new Error("Not signed in.");
    const res = await fetch(`${CONVEX_URL}/settings`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data?.error || `settings: HTTP ${res.status}`);
  }, [token]);

  const saveForDevice = useCallback(async (deviceId: string, project: RuntimeProjectSeed) => {
    const pref = runtimeProjectPreferenceFor(deviceId, project);
    setSaving(deviceId);
    setMessage(null);
    try {
      await postRuntimeSettings({ defaultRuntimeProjectForDevice: pref });
      setDefaults((prev) => ({ ...prev, [deviceId]: pref }));
      setMessage(`Default render project saved for ${runtimeProjectDisplayName(project)}.`);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not save default render project.");
    } finally {
      setSaving(null);
    }
  }, [postRuntimeSettings]);

  const applyToAll = useCallback(async (project: RuntimeProjectSeed) => {
    setSaving("__all__");
    setMessage(null);
    try {
      const next: Record<string, RuntimeProjectPreference> = {};
      for (const device of devices) {
        const match = resolveRuntimeProjectPreference(effectiveCatalogs[device.id]?.projects || [], project);
        if (!match) continue;
        const pref = runtimeProjectPreferenceFor(device.id, match);
        await postRuntimeSettings({ defaultRuntimeProjectForDevice: pref });
        next[device.id] = pref;
      }
      const count = Object.keys(next).length;
      if (count === 0) throw new Error("No machine has a matching synced project catalog yet.");
      setDefaults((prev) => ({ ...prev, ...next }));
      setMessage(`Applied to ${count} machine${count === 1 ? "" : "s"} with a matching project.`);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Could not apply default render project.");
    } finally {
      setSaving(null);
    }
  }, [devices, effectiveCatalogs, postRuntimeSettings]);

  if (devices.length === 0) return null;

  return (
    <div className="card mb-6">
      <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
        Default Render Project
      </h3>
      <p className="mb-4 text-xs leading-5 text-surface-500">
        Pick the project Yaver opens by default in Runtime for each machine. This stores only project names and remote repo identity, never local folder paths.
      </p>
      {message ? (
        <p className="mb-3 rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2 text-xs text-surface-300">
          {message}
        </p>
      ) : null}
      <div className="space-y-2">
        {devices.map((device) => {
          const catalog = effectiveCatalogs[device.id]?.projects || [];
          const saved = effectiveDefaults[device.id];
          const savedProject = resolveRuntimeProjectPreference(catalog, saved);
          const expanded = expandedDeviceId === device.id;
          const label = savedProject
            ? runtimeProjectDisplayName(savedProject)
            : saved
              ? runtimeProjectDisplayName(saved)
              : "No default";
          return (
            <div key={device.id} className="rounded-lg border border-surface-800 bg-surface-900/60">
              <button
                type="button"
                onClick={() => setExpandedDeviceId(expanded ? null : device.id)}
                className="flex w-full items-center justify-between gap-3 px-3 py-3 text-left"
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-surface-200">{device.name || device.id}</div>
                  <div className="mt-1 truncate text-xs text-surface-500">
                    {label}{saved ? ` · ${runtimeProjectMeta(saved) || "synced"}` : ""}
                  </div>
                </div>
                <span className={`shrink-0 rounded-full border px-2 py-1 text-[10px] uppercase tracking-[0.16em] ${catalog.length ? "border-sky-500/30 text-sky-700 dark:text-sky-300" : "border-surface-700 text-surface-500"}`}>
                  {catalog.length ? `${catalog.length} projects ${expanded ? "Hide" : "Show"}` : "No catalog"}
                </span>
              </button>
              {expanded ? (
                <div className="space-y-2 border-t border-surface-800 p-3">
                  {catalog.length === 0 ? (
                    <p className="text-xs leading-5 text-surface-500">
                      Open Runtime for this machine once so it can sync repo/project names, then pick a default here.
                    </p>
                  ) : (
                    catalog.map((project, index) => {
                      const active = savedProject === project;
                      const busy = saving === device.id || saving === "__all__";
                      return (
                        <div key={`${runtimeProjectDisplayName(project)}:${project.gitRemote || project.repoName || index}`} className={`flex items-center justify-between gap-3 rounded-lg border px-3 py-2 ${active ? "border-sky-500/40 bg-sky-500/10" : "border-surface-800 bg-surface-950/40"}`}>
                          <div className="min-w-0">
                            <div className={`truncate text-sm font-medium ${active ? "text-sky-700 dark:text-sky-300" : "text-surface-200"}`}>
                              {runtimeProjectDisplayName(project)}
                            </div>
                            <div className="mt-1 truncate text-xs text-surface-500">{runtimeProjectMeta(project) || "repo identity synced"}</div>
                          </div>
                          <div className="flex shrink-0 items-center gap-2">
                            <button
                              type="button"
                              disabled={busy}
                              onClick={() => void saveForDevice(device.id, project)}
                              className={`h-8 rounded-md border px-3 text-[11px] font-semibold uppercase tracking-[0.14em] disabled:opacity-40 ${active ? "border-sky-500 bg-sky-500 text-white" : "border-surface-700 text-surface-300 hover:bg-surface-800"}`}
                            >
                              {active ? "Default" : "Use"}
                            </button>
                            <button
                              type="button"
                              disabled={busy}
                              onClick={() => void applyToAll(project)}
                              className="h-8 rounded-md border border-surface-700 px-3 text-[11px] font-semibold uppercase tracking-[0.14em] text-surface-300 hover:bg-surface-800 disabled:opacity-40"
                            >
                              All
                            </button>
                          </div>
                        </div>
                      );
                    })
                  )}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );
}

export default function SettingsView({ user, onLogout, onOpenTwoFactor }: SettingsViewProps) {
  const autoRenderVibing = useAutoRenderVibing();
  const [autoRenderError, setAutoRenderError] = useState<string | null>(null);
  const [autoRenderSaving, setAutoRenderSaving] = useState(false);
  const [identities, setIdentities] = useState<Array<{ provider: string; email: string | null; isPrimary: boolean }>>([]);
  const [emailPasswordEnabled, setEmailPasswordEnabled] = useState(false);
  const [newPassword, setNewPassword] = useState("");
  const [confirmNewPassword, setConfirmNewPassword] = useState("");
  const [passwordBusy, setPasswordBusy] = useState(false);
  // Second-factor code, revealed only when the backend asks for it (HTTP 428).
  // Not shown up-front: most accounts have no TOTP, and an always-visible field
  // reads as "you must have 2FA to do this", which is not true.
  const [passwordTotp, setPasswordTotp] = useState("");
  const [needsTotp, setNeedsTotp] = useState(false);
  const [passwordMessage, setPasswordMessage] = useState<string | null>(null);
  const [passwordError, setPasswordError] = useState<string | null>(null);
  const [linkingProvider, setLinkingProvider] = useState<string | null>(null);
  const [linkError, setLinkError] = useState<string | null>(null);
  const [unlinkingProvider, setUnlinkingProvider] = useState<string | null>(null);
  const [unlinkError, setUnlinkError] = useState<string | null>(null);
  const [mergeStarting, setMergeStarting] = useState(false);
  const [mergeIntent, setMergeIntent] = useState<{ token: string; approvalUrl: string; expiresAt: number } | null>(null);
  const [mergeError, setMergeError] = useState<string | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState("");
  const [deleteLoading, setDeleteLoading] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const refreshIdentities = async (authToken: string) => {
    try {
      const res = await fetch(`${CONVEX_URL}/auth/providers`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });
      if (!res.ok) return;
      const data = await res.json();
      setIdentities(data?.identities || []);
    } catch {
      // ignore
    }
  };

  const [unlinkSuccess, setUnlinkSuccess] = useState<string | null>(null);
  const [mergeUrlCopied, setMergeUrlCopied] = useState(false);
  const [mergeCountdown, setMergeCountdown] = useState<string>("");

  const unlinkProvider = async (provider: string) => {
    if (!token) return;
    const confirmed = window.confirm(
      `Remove ${provider} from this Yaver account? You won't be able to sign in with ${provider} afterwards.`,
    );
    if (!confirmed) return;
    setUnlinkingProvider(provider);
    setUnlinkError(null);
    setUnlinkSuccess(null);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/oauth-link/${encodeURIComponent(provider)}`, {
        method: "DELETE",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: "{}",
      });
      if (res.status === 412) {
        const code = window.prompt("2FA is enabled on this account. Enter your current 6-digit code:") ?? "";
        if (!code.trim()) {
          setUnlinkingProvider(null);
          return;
        }
        const retry = await fetch(`${CONVEX_URL}/auth/oauth-link/${encodeURIComponent(provider)}`, {
          method: "DELETE",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({ totpCode: code.trim() }),
        });
        if (!retry.ok) {
          const text = await retry.text();
          setUnlinkError(text || "Failed to unlink");
        } else {
          setUnlinkSuccess(`${provider} unlinked.`);
          await refreshIdentities(token);
        }
      } else if (!res.ok) {
        const text = await res.text();
        setUnlinkError(text || "Failed to unlink");
      } else {
        setUnlinkSuccess(`${provider} unlinked.`);
        await refreshIdentities(token);
      }
    } catch {
      setUnlinkError("Network error — try again.");
    } finally {
      setUnlinkingProvider(null);
    }
  };

  const copyMergeUrl = async () => {
    if (!mergeIntent) return;
    try {
      await navigator.clipboard.writeText(mergeIntent.approvalUrl);
      setMergeUrlCopied(true);
      window.setTimeout(() => setMergeUrlCopied(false), 2000);
    } catch {
      setMergeUrlCopied(false);
    }
  };

  // Live countdown under the merge approval URL so the user sees the
  // 30-minute window tick down rather than a static timestamp.
  useEffect(() => {
    if (!mergeIntent) {
      setMergeCountdown("");
      return;
    }
    const tick = () => {
      const remainingMs = mergeIntent.expiresAt - Date.now();
      if (remainingMs <= 0) {
        setMergeCountdown("expired");
        setMergeIntent(null);
        return;
      }
      const mins = Math.floor(remainingMs / 60_000);
      const secs = Math.floor((remainingMs % 60_000) / 1000);
      setMergeCountdown(`${mins}m ${String(secs).padStart(2, "0")}s`);
    };
    tick();
    const id = window.setInterval(tick, 1000);
    return () => window.clearInterval(id);
  }, [mergeIntent]);

  const startMerge = async () => {
    if (!token) return;
    setMergeStarting(true);
    setMergeError(null);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/account/merge/start`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ client: "web" }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.mergeToken) {
        setMergeError(data?.error || "Failed to start merge");
        return;
      }
      setMergeIntent({
        token: data.mergeToken,
        approvalUrl: `${window.location.origin}/account/merge?token=${encodeURIComponent(data.mergeToken)}`,
        expiresAt: data.expiresAt,
      });
    } catch {
      setMergeError("Network error — try again.");
    } finally {
      setMergeStarting(false);
    }
  };

  const cancelMerge = async () => {
    if (!token || !mergeIntent) return;
    try {
      await fetch(`${CONVEX_URL}/auth/account/merge/cancel`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ mergeToken: mergeIntent.token }),
      });
    } catch {
      // best-effort
    }
    setMergeIntent(null);
  };

  const handleDeleteAccount = async () => {
    setDeleteLoading(true);
    setDeleteError(null);

    try {
      const convexSiteUrl = CONVEX_URL;

      const token =
        localStorage.getItem("yaver_auth_token") ||
        document.cookie
          .split(";")
          .find((c) => c.trim().startsWith("yaver_session="))
          ?.split("=")[1];

      if (!token) {
        setDeleteError("Not authenticated. Please sign in again.");
        setDeleteLoading(false);
        return;
      }

      const res = await fetch(`${convexSiteUrl}/auth/delete-account`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
        },
      });

      if (!res.ok) {
        const text = await res.text();
        setDeleteError(text || "Failed to delete account.");
        setDeleteLoading(false);
        return;
      }

      // Clear auth and redirect
      localStorage.removeItem("yaver_auth_token");
      document.cookie = "yaver_auth_token=; path=/; max-age=0; secure; samesite=lax";
      document.cookie = "yaver_session=; path=/; max-age=0; secure; samesite=lax";
      window.location.href = "/";
    } catch {
      setDeleteError("Network error. Please try again.");
      setDeleteLoading(false);
    }
  };

  const token =
    typeof window !== "undefined"
      ? localStorage.getItem("yaver_auth_token") ||
        document.cookie
          .split(";")
          .find((c) => c.trim().startsWith("yaver_auth_token="))
          ?.split("=")[1] ||
        null
      : null;
  const { devices } = useDevices(token);
  const ownedDevices = devices;

  useEffect(() => {
    if (!token) return;
    refreshIdentities(token);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  useEffect(() => {
    fetch(`${CONVEX_URL}/auth/config`)
      .then((res) => (res.ok ? res.json() : null))
      .then((json) => setEmailPasswordEnabled(json?.emailPasswordEnabled === true))
      .catch(() => setEmailPasswordEnabled(false));
  }, []);

  const hasEmailPassword = identities.some((identity) => identity.provider === "email");

  const setAccountPassword = async () => {
    if (!token) return;
    setPasswordError(null);
    setPasswordMessage(null);
    if (newPassword.length < 8) {
      setPasswordError("Password must be at least 8 characters.");
      return;
    }
    if (newPassword !== confirmNewPassword) {
      setPasswordError("Passwords do not match.");
      return;
    }
    setPasswordBusy(true);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/set-password`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ password: newPassword, totp: passwordTotp.trim() || undefined }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        const err = String(data?.error || "");
        // 428 = the backend wants a second factor before REPLACING a password.
        // Reveal the field and say why, instead of showing a dead-end error.
        if (res.status === 428 || err.includes("TOTP_REQUIRED")) {
          setNeedsTotp(true);
          setPasswordError("Enter your two-factor code to replace the existing password.");
          return;
        }
        if (err.includes("INVALID_TOTP")) {
          setNeedsTotp(true);
          setPasswordError("That two-factor code is not valid — try the current one.");
          return;
        }
        setPasswordError(err || "Could not set password.");
        return;
      }
      setNewPassword("");
      setConfirmNewPassword("");
      setPasswordTotp("");
      setNeedsTotp(false);
      setPasswordMessage(
        data?.replaced
          ? "Password changed. Email sign-in now uses the new password on this same account."
          : "Email/password sign-in is now linked to this account.",
      );
      await refreshIdentities(token);
    } catch {
      setPasswordError("Network error — try again.");
    } finally {
      setPasswordBusy(false);
    }
  };

  const startLink = async (provider: "apple" | "github" | "google" | "microsoft" | "gitlab") => {
    if (!token) return;
    setLinkError(null);
    setLinkingProvider(provider);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/oauth-link/start`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          provider,
          client: "web",
          returnTo: "/dashboard",
        }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok || !data?.token) {
        throw new Error(data?.error || "Failed to start link");
      }
      window.location.href = `/api/auth/oauth/${provider}?client=web&intent=link&linkToken=${encodeURIComponent(data.token)}&return=${encodeURIComponent("/dashboard")}`;
    } catch (error) {
      console.error(error);
      const message = error instanceof Error ? error.message : `Failed to start ${provider} link`;
      setLinkError(message);
      setLinkingProvider(null);
    }
  };

  return (
    <>
      {/* Account security belongs in Settings. Keep the full enrollment flow
          one level deeper so the default settings surface stays compact. */}
      <button
        type="button"
        onClick={onOpenTwoFactor}
        className="card mb-6 flex w-full items-center justify-between gap-4 text-left transition-colors hover:border-surface-700 hover:bg-surface-800/60"
      >
        <span>
          <span className="block text-sm font-medium text-surface-100">Two-factor authentication</span>
          <span className="mt-1 block text-xs leading-5 text-surface-500">
            Optional · Google Authenticator, Microsoft Authenticator, 1Password, and other TOTP apps
          </span>
        </span>
        <span aria-hidden className="shrink-0 text-surface-500">→</span>
      </button>
      <PasskeysCard />
      {user?.isOwner ? <GlobalModelDefaultsCard token={token} /> : null}

      <div className="card mb-6">
        <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
          Sign-In Methods
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Link Apple, GitHub, GitLab, Google, or Microsoft to this same Yaver account. Future sign-ins with any linked provider open the same machines and devices.
        </p>
        <div className="mb-4 space-y-2">
          {identities.length === 0 ? (
            <p className="text-xs text-surface-500">No linked providers loaded yet.</p>
          ) : (
            identities.map((identity) => {
              const canUnlink = identities.length > 1;
              return (
                <div key={`${identity.provider}:${identity.email || "none"}`} className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2">
                  <div className="flex items-center gap-3">
                    <span className={`flex h-9 w-9 items-center justify-center rounded-full border border-surface-800 bg-surface-950 ${
                      identity.provider === "gitlab" ? "text-orange-700 dark:text-orange-300" : "text-surface-200"
                    }`}>
                      <AuthProviderIcon provider={identity.provider} className="h-4 w-4" />
                    </span>
                    <div>
                      <div className="flex items-center gap-2">
                        <p className="text-sm text-surface-200">{identity.provider}</p>
                        <span
                          className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[10px] uppercase tracking-[0.16em] ${
                            identity.isPrimary
                              ? "border-emerald-500/30 text-emerald-700 dark:text-emerald-300"
                              : "border-surface-700 text-surface-400"
                          }`}
                          title={identity.isPrimary ? "Primary sign-in method" : "Linked sign-in method"}
                        >
                          <StatusIcon primary={identity.isPrimary} />
                          {identity.isPrimary ? "Primary" : "Linked"}
                        </span>
                      </div>
                      <p className="text-xs text-surface-500">{identity.email || "No email reported by provider"}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => unlinkProvider(identity.provider)}
                      disabled={!canUnlink || unlinkingProvider === identity.provider}
                      title={canUnlink ? `Remove ${identity.provider} from this account` : "Cannot unlink — this is your only sign-in method"}
                      className="rounded-full border border-surface-700 px-2 py-1 text-[10px] uppercase tracking-[0.16em] text-surface-300 transition-colors hover:border-red-500/40 hover:text-red-700 dark:hover:text-red-300 disabled:cursor-not-allowed disabled:opacity-30"
                    >
                      {unlinkingProvider === identity.provider ? "…" : "Unlink"}
                    </button>
                  </div>
                </div>
              );
            })
          )}
        </div>
        {unlinkSuccess && (
          <p className="mb-3 rounded-md border border-emerald-500/30 bg-emerald-500/5 px-3 py-2 text-xs text-emerald-700 dark:text-emerald-300">
            {unlinkSuccess}
          </p>
        )}
        {linkError && <p className="mb-3 text-xs text-red-400">{linkError}</p>}
        {unlinkError && <p className="mb-3 text-xs text-red-400">{unlinkError}</p>}
        {(() => {
          const unlinked = (["apple", "github", "gitlab", "google", "microsoft"] as const).filter(
            (provider) => !identities.some((identity) => identity.provider === provider),
          );
          if (unlinked.length === 0) return null;
          return (
            <div className="grid gap-2 sm:grid-cols-4">
              {unlinked.map((provider) => (
                <button
                  key={provider}
                  onClick={() => startLink(provider)}
                  disabled={linkingProvider !== null}
                  className="rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50 disabled:opacity-40"
                >
                  {linkingProvider === provider ? "Connecting..." : `Connect ${provider}`}
                </button>
              ))}
            </div>
          );
        })()}
      </div>

      <div className="card mb-6 flex items-center justify-between gap-4">
        <div>
          <h3 className="text-sm font-medium text-surface-100">Auto-render Vibing mode</h3>
          <p className="mt-1 max-w-2xl text-xs leading-5 text-surface-500">
            Off by default. When enabled, Yaver may refresh after the coding agent reports UI-visible changes. Explicit “render again” requests always work.
          </p>
          {autoRenderError ? <p className="mt-2 text-xs text-red-400">{autoRenderError}</p> : null}
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={autoRenderVibing.enabled}
          disabled={!autoRenderVibing.loaded || autoRenderSaving}
          onClick={() => {
            const next = !autoRenderVibing.enabled;
            setAutoRenderSaving(true);
            setAutoRenderError(null);
            void autoRenderVibing.save(next)
              .catch((error) => setAutoRenderError(error instanceof Error ? error.message : String(error)))
              .finally(() => setAutoRenderSaving(false));
          }}
          className={`relative h-7 w-12 shrink-0 rounded-full border transition-colors disabled:opacity-40 ${
            autoRenderVibing.enabled ? "border-emerald-400/50 bg-emerald-500/30" : "border-surface-700 bg-surface-900"
          }`}
        >
          <span className={`absolute top-1 h-5 w-5 rounded-full bg-surface-100 transition-transform ${autoRenderVibing.enabled ? "translate-x-5" : "translate-x-1"}`} />
          <span className="sr-only">{autoRenderVibing.enabled ? "Disable" : "Enable"} automatic Vibing renders</span>
        </button>
      </div>

      <GitSettingsCard devices={ownedDevices} />
      <PublisherSetupCard token={token} />
      <SourceCodeStatusCard devices={ownedDevices} />
      <RuntimeProjectDefaultsCard token={token} devices={ownedDevices} />

      {ENABLE_RELAY_PRO_UI ? (
        <div className="mb-6">
          <BillingView token={token} />
        </div>
      ) : null}

      <div className="card mb-6">
        <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
          Email / Password
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Add a password credential to this same account for automation on web,
          redroid, and iOS simulators. The raw password belongs in your local
          keychain or GitHub Secrets; Yaver stores only a PBKDF2 password hash.
        </p>
        {!emailPasswordEnabled ? (
          <div className="rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-3 text-sm text-surface-400">
            Email/password sign-in is closed on this deployment. Open it only
            for a test window with <code className="rounded bg-surface-950 px-1 py-0.5 text-surface-200">yaver set emailOauth enable</code>{" "}
            and an allowed-email list. Convex env stores only the gate and
            allowlist, never the raw password.
          </div>
        ) : (
          /* One form for both cases. This branch used to be a DEAD END when a
             password already existed: it printed "Email/password is linked" and
             offered no control, so an Apple/Microsoft/Google account — which
             never knew a password — had no way to change it from inside the
             product. Reported 2026-07-25. */
          <div className="space-y-3">
            {hasEmailPassword ? (
              <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 px-3 py-3 text-xs text-emerald-700 dark:text-emerald-300">
                Email sign-in is linked as <span className="font-mono">{user?.email}</span>.
                You can change the password here — your {user?.provider || "OAuth"} session is
                the proof of identity, so the old password is not required. If you have
                two-factor enabled, you will be asked for a code.
              </div>
            ) : (
              <div className="rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-3 text-xs text-amber-700 dark:text-amber-200">
                This adds an <span className="font-mono">email</span> sign-in
                identity to your existing {user?.provider || "OAuth"} account. It
                does not create a second Yaver user. Other users and runners
                cannot fetch this credential; the server stores only a salted
                hash.
              </div>
            )}
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="New automation password"
              autoComplete="new-password"
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
            />
            <input
              type="password"
              value={confirmNewPassword}
              onChange={(e) => setConfirmNewPassword(e.target.value)}
              placeholder="Confirm password"
              autoComplete="new-password"
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
            />
            {needsTotp ? (
              <input
                type="text"
                inputMode="numeric"
                autoComplete="one-time-code"
                value={passwordTotp}
                onChange={(e) => setPasswordTotp(e.target.value)}
                placeholder="Two-factor code (or a recovery code)"
                className="w-full rounded-lg border border-amber-500/40 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-amber-400"
              />
            ) : null}
            {passwordError ? <p className="text-xs text-red-400">{passwordError}</p> : null}
            {passwordMessage ? <p className="text-xs text-emerald-700 dark:text-emerald-300">{passwordMessage}</p> : null}
            <button
              type="button"
              onClick={() => void setAccountPassword()}
              disabled={passwordBusy}
              className="w-full rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50 disabled:opacity-50"
            >
              {passwordBusy
                ? "Saving..."
                : hasEmailPassword
                  ? "Change password"
                  : "Enable email/password on this account"}
            </button>
          </div>
        )}
      </div>

      {/* Merge another account into this one */}
      <div className="card mb-6">
        <h3 className="mb-3 text-sm font-medium uppercase tracking-wider text-surface-400">
          Merge Another Account
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Accidentally created two Yaver accounts? Merge them into one. Start
          here, then open the approval URL on any browser where the OTHER
          account is signed in and confirm. The OTHER account&apos;s devices,
          sessions, linked providers, and settings move onto this one. The
          other account is deleted afterwards.
        </p>
        {!mergeIntent ? (
          <>
            {mergeError && <p className="mb-3 text-xs text-red-400">{mergeError}</p>}
            <button
              onClick={startMerge}
              disabled={mergeStarting}
              className="w-full rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50 disabled:opacity-50"
            >
              {mergeStarting ? "Starting…" : "Start merge"}
            </button>
          </>
        ) : (
          <div className="space-y-3">
            <p className="text-xs text-surface-400">
              Open this link on a browser where the OTHER Yaver account is
              signed in. The page will confirm that <span className="text-surface-200">{user?.email}</span> is the account receiving the data, then ask for confirmation.
            </p>
            <div className="rounded-lg border border-surface-800 bg-surface-900/60 p-3">
              <p className="break-all font-mono text-xs text-surface-200">{mergeIntent.approvalUrl}</p>
              <button
                onClick={copyMergeUrl}
                className="mt-3 rounded-md border border-surface-700 px-3 py-1.5 text-[11px] uppercase tracking-[0.16em] text-surface-300 transition-colors hover:bg-surface-800/60 hover:text-surface-50 focus:outline-none focus:ring-2 focus:ring-surface-600"
              >
                {mergeUrlCopied ? "Copied" : "Copy URL"}
              </button>
            </div>
            <div className="flex items-center justify-between">
              <p className="text-[10px] uppercase tracking-[0.16em] text-surface-500">
                Expires in <span className="font-mono text-surface-300">{mergeCountdown || "—"}</span>
              </p>
              <button
                onClick={cancelMerge}
                className="rounded-full border border-surface-700 px-3 py-1 text-[10px] uppercase tracking-[0.16em] text-surface-300 hover:border-red-500/40 hover:text-red-700 dark:hover:text-red-300 focus:outline-none focus:ring-2 focus:ring-red-500/40"
              >
                Cancel
              </button>
            </div>
          </div>
        )}
      </div>

      {/* About */}
      <div className="card mb-6">
        <h3 className="mb-3 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-surface-400">
          <span aria-hidden>ℹ️</span> About
        </h3>
        <div className="flex items-center justify-between text-sm">
          <span className="flex items-center gap-2 text-surface-400">
            <span aria-hidden>🌐</span> yaver.io web
          </span>
          <span className="font-mono text-surface-200">v{WEB_VERSION}</span>
        </div>
        <div className="mt-4 border-t border-surface-800 pt-4">
          <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.16em] text-surface-500">
            Your Boxes
          </div>
          {ownedDevices.length === 0 ? (
            <p className="text-sm text-surface-500">No boxes connected yet.</p>
          ) : (
            <div className="space-y-2">
              {ownedDevices.map((device) => (
                <div key={device.id} className="flex items-center justify-between rounded-lg border border-surface-800 bg-surface-900/60 px-3 py-2">
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="flex h-8 w-8 items-center justify-center rounded-full border border-surface-800 bg-surface-950 text-surface-300">
                      <DeviceSurfaceIcon platform={device.platform} />
                    </span>
                    <div className="min-w-0">
                      <div className="truncate text-sm text-surface-200">{device.name || device.id}</div>
                      <div className="text-xs text-surface-500">
                        {platformLabel(device.platform)} · {device.agentVersion || "no version info"}
                      </div>
                    </div>
                  </div>
                  <span className={`ml-3 h-2.5 w-2.5 shrink-0 rounded-full ${device.online ? "bg-emerald-400" : "bg-surface-700"}`} title={device.online ? "Online" : "Offline"} />
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Yaver Agent (control-plane LLM) provider config */}
      <YaverAgentSettings connected={agentClient.isConnected} />

      <McpServersCard connected={agentClient.isConnected} />

      {/* Vision — screenshots/crash logs/UI failures → text via Yaver MCP */}
      <VisionSettingsCard />

      {/* Coding model — which model opencode uses per machine. The
          consolidated hub: provider selection, API-key entry, model pickers,
          add/edit/delete providers, diagnostics (2026-08-12). Supersedes the
          model-only OpenCodeModelCard. */}
      <OpenCodeSettingsView devices={ownedDevices} />

      {/* Legal */}
      <div className="card mb-6">
        <h3 className="mb-3 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-surface-400">
          <span aria-hidden>📜</span> Legal
        </h3>
        <div className="space-y-2">
          <a
            href="https://yaver.io/privacy"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 text-sm text-surface-400 transition-colors hover:text-surface-50"
          >
            <span aria-hidden>🔒</span> Privacy Policy
          </a>
          <a
            href="https://yaver.io/terms"
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 text-sm text-surface-400 transition-colors hover:text-surface-50"
          >
            <span aria-hidden>📄</span> Terms of Service
          </a>
          <a
            href="mailto:kivanc.cakmak@simkab.com?subject=Report%20inappropriate%20Yaver%20AI%20output"
            className="flex items-center gap-2 text-sm text-surface-400 transition-colors hover:text-surface-50"
          >
            <span aria-hidden>⚑</span> Report inappropriate AI output
          </a>
        </div>
      </div>

      {/* Sign out */}
      <button
        onClick={onLogout}
        className="mb-6 flex w-full items-center justify-center gap-2 rounded-lg border border-surface-700 px-4 py-3 text-sm text-surface-300 transition-colors hover:bg-surface-800/50 hover:text-surface-50"
      >
        <span aria-hidden>🚪</span> Sign Out
      </button>

      <ThirdPartyDogfoodCard token={token} />
      <DogfoodCard devices={ownedDevices} />

      {/* Delete Account */}
      <div className="card mb-6 border-red-500/20">
        <h3 className="mb-2 flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-red-400/80">
          <span aria-hidden>⚠️</span> Danger Zone
        </h3>
        <p className="mb-4 text-xs text-surface-500">
          Permanently delete your account and all associated data. This action cannot be undone.
        </p>
        <p className="mb-3 text-xs text-surface-500">
          Type <span className="font-mono text-surface-300">delete my account</span> to confirm:
        </p>
        <input
          type="text"
          value={deleteConfirm}
          onChange={(e) => setDeleteConfirm(e.target.value)}
          placeholder="delete my account"
          disabled={deleteLoading}
          className="mb-3 w-full rounded-lg border border-surface-700 bg-surface-850 px-4 py-2.5 text-sm text-surface-200 placeholder-surface-600 outline-none transition-colors focus:border-red-500/50 disabled:opacity-50"
        />
        {deleteError && (
          <p className="mb-3 text-sm text-red-400">{deleteError}</p>
        )}
        <button
          onClick={handleDeleteAccount}
          disabled={deleteConfirm !== "delete my account" || deleteLoading}
          className="w-full rounded-lg border border-red-500/30 px-4 py-3 text-sm font-medium text-red-400 transition-colors hover:bg-red-500/10 disabled:opacity-30 disabled:hover:bg-transparent"
        >
          {deleteLoading ? "Deleting..." : "Delete My Account"}
        </button>
      </div>
    </>
  );
}
