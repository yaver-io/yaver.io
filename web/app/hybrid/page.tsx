"use client";

// /hybrid — UI for yaver's hybrid planner+implementer mode.
//
// Keeps its own state/connection (vs. folding into the dashboard
// mega-component) so we can ship this feature without touching
// unrelated tab logic. Users land here from /dashboard via a link
// in the Ops/Extras tab; the page reuses the shared agentClient so
// it inherits the direct/relay transport strategy.
//
// The UX intentionally separates "plan" and "run":
//   1. User types a feature prompt.
//   2. POST /hybrid/plan → review subtasks Claude produced.
//   3. Click Execute → POST /hybrid/run → stream results in.
//
// A pure-run button is there too for callers who trust the planner
// and want one click to go.

import { useEffect, useMemo, useState } from "react";
import {
  agentClient,
  type ConnectionState,
  type HybridPlanResult,
  type HybridReport,
  type HybridRunRequest,
  type HybridStepResult,
} from "@/lib/agent-client";
import { useAuth } from "@/lib/use-auth";
import { useDevices } from "@/lib/use-devices";

const DEFAULT_MODEL = "ollama_chat/qwen2.5-coder:14b";

export default function HybridPage() {
  const { token, isAuthenticated, isLoading } = useAuth();
  const { devices } = useDevices(token);

  const [deviceId, setDeviceId] = useState<string>("");
  const [connState, setConnState] = useState<ConnectionState>("disconnected");
  const [req, setReq] = useState<HybridRunRequest>({
    planner: "claude",
    implementer: "aider-ollama",
    model: DEFAULT_MODEL,
    workDir: "",
    prompt: "",
    maxSubtasks: 15,
    timeoutSec: 1800,
  });
  const [plan, setPlan] = useState<HybridPlanResult | null>(null);
  const [report, setReport] = useState<HybridReport | null>(null);
  const [liveResults, setLiveResults] = useState<HybridStepResult[]>([]);
  const [currentStep, setCurrentStep] = useState<{index: number; total: number; title: string; retry: number} | null>(null);
  const [busy, setBusy] = useState<"idle" | "planning" | "running">("idle");
  const [error, setError] = useState<string | null>(null);

  // Auto-pick the first online device. Manual override is still
  // available in the device dropdown below — useful when the user
  // has an old Mac Mini dev box plus their laptop both online.
  useEffect(() => {
    if (!deviceId && devices.length > 0) {
      const online = devices.find((d) => d.online) ?? devices[0];
      if (online) setDeviceId(online.id);
    }
  }, [devices, deviceId]);

  const selectedDevice = useMemo(
    () => devices.find((d) => d.id === deviceId),
    [devices, deviceId],
  );

  useEffect(() => {
    if (!selectedDevice || !token) return;
    let cancelled = false;
    (async () => {
      try {
        setConnState("connecting");
        await agentClient.connect(
          selectedDevice.host ?? "localhost",
          selectedDevice.port ?? 18080,
          token,
          selectedDevice.id,
        );
        if (!cancelled) setConnState("connected");
      } catch (e: any) {
        if (!cancelled) {
          setConnState("disconnected");
          setError(e?.message ?? String(e));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedDevice?.id, token]);

  async function handlePlan() {
    if (!req.prompt.trim() || !req.workDir.trim()) {
      setError("Work directory and prompt are required.");
      return;
    }
    setError(null);
    setPlan(null);
    setReport(null);
    setBusy("planning");
    try {
      const p = await agentClient.hybridPlan(req);
      setPlan(p);
    } catch (e: any) {
      setError(e?.message ?? String(e));
    } finally {
      setBusy("idle");
    }
  }

  async function handleRun() {
    if (!req.prompt.trim() || !req.workDir.trim()) {
      setError("Work directory and prompt are required.");
      return;
    }
    setError(null);
    setReport(null);
    setPlan(null);
    setLiveResults([]);
    setCurrentStep(null);
    setBusy("running");
    try {
      // Stream over SSE — progress events update the UI live so
      // the user sees each subtask finishing instead of staring at
      // a spinner for 5+ minutes.
      const final = await agentClient.hybridStream(req, (ev) => {
        if (ev.type === "plan_done" && ev.plan) {
          setPlan({ spec: req, subtasks: ev.plan });
        }
        if (ev.type === "subtask_started" && ev.subtask) {
          setCurrentStep({
            index: ev.index ?? 0,
            total: ev.total ?? 0,
            title: ev.subtask.title,
            retry: ev.retry ?? 0,
          });
        }
        if (ev.type === "subtask_done" && ev.result) {
          setLiveResults((xs) => [...xs, ev.result!]);
          setCurrentStep(null);
        }
        if (ev.type === "replan_done" && ev.plan) {
          // Replace the plan display with the new subtask list.
          setPlan({ spec: req, subtasks: ev.plan });
        }
        if (ev.type === "error") {
          setError(ev.message ?? "unknown error");
        }
      });
      if (final) setReport(final);
    } catch (e: any) {
      setError(e?.message ?? String(e));
    } finally {
      setBusy("idle");
      setCurrentStep(null);
    }
  }

  if (isLoading) return <main className="p-8 text-surface-300">Loading…</main>;
  if (!isAuthenticated)
    return (
      <main className="p-8">
        <p className="text-surface-300">
          Please <a className="underline" href="/auth">sign in</a> to use hybrid mode.
        </p>
      </main>
    );

  return (
    <main className="mx-auto max-w-4xl p-6 text-surface-100">
      <header className="mb-6">
        <h1 className="text-2xl font-semibold">Hybrid Mode</h1>
        <p className="mt-2 text-sm text-surface-400">
          Let a frontier planner (Claude) break your feature into file-scoped
          subtasks, then execute each subtask with a free local model
          (Qwen 14B via Aider + Ollama). Typical API-cost reduction vs pure
          Claude Code: <strong className="text-emerald-300">15–30×</strong>.
        </p>
      </header>

      <section className="mb-6 rounded-lg border border-surface-700 bg-surface-900 p-4">
        <label className="block text-xs uppercase text-surface-400">Device</label>
        <select
          className="mt-1 w-full rounded bg-surface-800 px-3 py-2 text-sm"
          value={deviceId}
          onChange={(e) => setDeviceId(e.target.value)}
        >
          <option value="">-- pick one --</option>
          {devices.map((d) => (
            <option key={d.id} value={d.id}>
              {d.name} {d.online ? "●" : "(offline)"}
            </option>
          ))}
        </select>
        <p className="mt-1 text-xs text-surface-500">
          Connection: <span className="text-surface-300">{connState}</span>
        </p>
      </section>

      <section className="space-y-3 rounded-lg border border-surface-700 bg-surface-900 p-4">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Planner" value={req.planner ?? ""} onChange={(v) => setReq((r) => ({ ...r, planner: v }))} />
          <Field label="Implementer" value={req.implementer ?? ""} onChange={(v) => setReq((r) => ({ ...r, implementer: v }))} />
          <Field label="Model" value={req.model ?? ""} onChange={(v) => setReq((r) => ({ ...r, model: v }))} />
          <Field label="Max subtasks" value={String(req.maxSubtasks ?? 15)} onChange={(v) => setReq((r) => ({ ...r, maxSubtasks: Number(v) || 15 }))} />
        </div>
        <Field
          label="Work directory (absolute path on the dev machine)"
          value={req.workDir}
          onChange={(v) => setReq((r) => ({ ...r, workDir: v }))}
          placeholder="/Users/you/projects/my-app"
        />
        <div>
          <label className="block text-xs uppercase text-surface-400">Feature prompt</label>
          <textarea
            className="mt-1 h-36 w-full rounded bg-surface-800 px-3 py-2 text-sm"
            value={req.prompt}
            onChange={(e) => setReq((r) => ({ ...r, prompt: e.target.value }))}
            placeholder="Add a Convex mutation createPortfolio(name, startingCashUsd)…"
          />
        </div>
        <div className="flex gap-2">
          <button
            disabled={busy !== "idle" || connState !== "connected"}
            onClick={handlePlan}
            className="rounded bg-emerald-600 px-4 py-2 text-sm font-medium disabled:opacity-50"
          >
            {busy === "planning" ? "Planning…" : "Plan"}
          </button>
          <button
            disabled={busy !== "idle" || connState !== "connected"}
            onClick={handleRun}
            className="rounded bg-blue-600 px-4 py-2 text-sm font-medium disabled:opacity-50"
          >
            {busy === "running" ? "Running…" : "Plan & Run"}
          </button>
        </div>
        {error && (
          <p className="rounded bg-red-900/30 px-3 py-2 text-sm text-red-300">{error}</p>
        )}
      </section>

      {busy === "running" && (plan || currentStep) && !report && (
        <section className="mt-6 rounded-lg border border-blue-700 bg-blue-950/20 p-4">
          <h2 className="mb-2 text-lg font-semibold">Live progress</h2>
          {currentStep && (
            <p className="text-sm text-blue-200">
              Step {currentStep.index}/{currentStep.total}: {currentStep.title}
              {currentStep.retry > 0 && <span className="ml-2 text-amber-300">(retry #{currentStep.retry})</span>}
            </p>
          )}
          {liveResults.length > 0 && (
            <ol className="mt-3 space-y-1">
              {liveResults.map((r, i) => (
                <li key={i} className="text-xs">
                  <span className={r.status === "ok" ? "text-emerald-300" : "text-red-300"}>[{r.status}]</span>{" "}
                  {r.subtask.title} <span className="text-surface-500">({(r.durationMs / 1000).toFixed(1)}s)</span>
                </li>
              ))}
            </ol>
          )}
        </section>
      )}

      {plan && !report && busy !== "running" && (
        <section className="mt-6 rounded-lg border border-surface-700 bg-surface-900 p-4">
          <h2 className="mb-2 text-lg font-semibold">Plan ({plan.subtasks.length} subtasks)</h2>
          <ol className="space-y-2">
            {plan.subtasks.map((st, i) => (
              <li key={i} className="rounded bg-surface-800 p-3">
                <p className="text-sm font-medium">{i + 1}. {st.title}</p>
                <p className="mt-1 text-xs text-surface-400">Files: {st.files.join(", ")}</p>
                <p className="mt-2 text-xs whitespace-pre-wrap text-surface-300">{st.prompt}</p>
              </li>
            ))}
          </ol>
        </section>
      )}

      {report && (
        <section className="mt-6 rounded-lg border border-surface-700 bg-surface-900 p-4">
          <h2 className="mb-2 text-lg font-semibold">
            Results — {report.ok ? (
              <span className="text-emerald-400">all green</span>
            ) : (
              <span className="text-amber-400">{report.failedSteps} failed</span>
            )}
          </h2>
          <ol className="space-y-2">
            {report.results.map((r, i) => (
              <li key={i} className="rounded bg-surface-800 p-3">
                <p className="text-sm font-medium">
                  {i + 1}. <span className={r.status === "ok" ? "text-emerald-300" : "text-red-300"}>[{r.status}]</span> {r.subtask.title}
                  <span className="ml-2 text-xs text-surface-500">{(r.durationMs / 1000).toFixed(1)}s</span>
                </p>
                {r.error && <p className="mt-1 text-xs text-red-300 whitespace-pre-wrap">{r.error}</p>}
              </li>
            ))}
          </ol>
        </section>
      )}
    </main>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <div>
      <label className="block text-xs uppercase text-surface-400">{label}</label>
      <input
        className="mt-1 w-full rounded bg-surface-800 px-3 py-2 text-sm"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}
