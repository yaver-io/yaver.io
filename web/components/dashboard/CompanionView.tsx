"use client";

// CompanionView — UI over desktop/agent/companion.go. A "companion" is the
// always-on tail a serverless project (Supabase / Convex / Workers) can't run
// itself: scheduled crons + long-running workers, declared in
// yaver.companion.yaml and armed on a connected Yaver box. All P2P against the
// agent — no Convex round-trip for status.

import { useCallback, useEffect, useState } from "react";
import {
  agentClient,
  type CompanionDetectItem,
  type CompanionProjectSummary,
  type CompanionStatus,
  type MicroserviceWrapResult,
} from "@/lib/agent-client";

function StatusPill({ status }: { status: string }) {
  const tone =
    status === "proposed-missing-endpoint"
      ? "bg-amber-500/15 text-amber-700 dark:text-amber-300 border-amber-500/30"
      : status === "note"
        ? "bg-surface-800 text-foreground-muted border-border"
        : status === "failed"
          ? "bg-red-500/15 text-red-700 dark:text-red-300 border-red-500/30"
          : "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 border-emerald-500/30";
  return (
    <span className={`inline-block rounded-full border px-2 py-0.5 text-[11px] ${tone}`}>
      {status}
    </span>
  );
}

export default function CompanionView() {
  const [projects, setProjects] = useState<CompanionProjectSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [notice, setNotice] = useState<{ type: "ok" | "error"; text: string } | null>(null);

  const [repo, setRepo] = useState("");
  const [detecting, setDetecting] = useState(false);
  const [detectItems, setDetectItems] = useState<CompanionDetectItem[] | null>(null);
  const [manifestYaml, setManifestYaml] = useState("");
  const [arming, setArming] = useState(false);
  const [svcRepo, setSvcRepo] = useState("");
  const [svcName, setSvcName] = useState("");
  const [svcProject, setSvcProject] = useState("");
  const [svcCommand, setSvcCommand] = useState("");
  const [svcPort, setSvcPort] = useState("");
  const [svcEnvFile, setSvcEnvFile] = useState("");
  const [svcEnvVault, setSvcEnvVault] = useState("");
  const [svcAIWrap, setSvcAIWrap] = useState(true);
  const [svcOverwrite, setSvcOverwrite] = useState(false);
  const [wrapping, setWrapping] = useState(false);
  const [wrapResult, setWrapResult] = useState<MicroserviceWrapResult | null>(null);

  const [statusByProject, setStatusByProject] = useState<Record<string, CompanionStatus>>({});

  const showNotice = useCallback((type: "ok" | "error", text: string) => {
    setNotice({ type, text });
    setTimeout(() => setNotice((n) => (n?.text === text ? null : n)), 6000);
  }, []);

  const load = useCallback(async () => {
    setErr(null);
    try {
      const ps = await agentClient.companionListProjects();
      setProjects(ps);
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const detect = useCallback(async () => {
    if (!repo.trim()) return;
    setDetecting(true);
    setDetectItems(null);
    try {
      const res = await agentClient.companionDetect(repo.trim());
      setDetectItems(res.items);
      setManifestYaml(res.manifestYaml);
      if (res.items.length === 0) {
        showNotice("ok", "No companion needs detected — this project is fully serverless.");
      }
    } catch (e) {
      showNotice("error", e instanceof Error ? e.message : String(e));
    } finally {
      setDetecting(false);
    }
  }, [repo, showNotice]);

  const writeAndArm = useCallback(async () => {
    if (!repo.trim() || !manifestYaml.trim()) return;
    setArming(true);
    try {
      const w = await agentClient.companionWriteManifest(repo.trim(), manifestYaml);
      if (w.error) throw new Error(w.error);
      const status = await agentClient.companionUp(repo.trim());
      setStatusByProject((m) => ({ ...m, [status.project]: status }));
      showNotice("ok", `Armed ${status.crons.filter((c) => !c.proposed).length} cron(s) for ${status.project}.`);
      await load();
    } catch (e) {
      showNotice("error", e instanceof Error ? e.message : String(e));
    } finally {
      setArming(false);
    }
  }, [repo, manifestYaml, showNotice, load]);

  const wrapMicroservice = useCallback(async () => {
    if (!svcRepo.trim() || !svcCommand.trim()) return;
    setWrapping(true);
    try {
      const res = await agentClient.microserviceWrap({
        repo: svcRepo.trim(),
        project: svcProject.trim() || undefined,
        name: svcName.trim() || undefined,
        command: svcCommand.trim(),
        port: svcPort.trim() ? Number(svcPort.trim()) : undefined,
        env_file: svcEnvFile.trim() || undefined,
        env_vault: svcEnvVault.trim() || undefined,
        durable: true,
        write: true,
        arm: true,
        overwrite: svcOverwrite,
        ai_wrap: svcAIWrap,
        ai_work_kind: "analysis",
      });
      setWrapResult(res);
      if (res.status) setStatusByProject((m) => ({ ...m, [res.status!.project]: res.status! }));
      showNotice(
        "ok",
        res.armed
          ? `Wrapped and armed ${res.project}.`
          : res.written
            ? `Wrote ${res.manifestPath}.`
            : res.warnings?.[0] || `Prepared ${res.project}.`,
      );
      await load();
    } catch (e) {
      showNotice("error", e instanceof Error ? e.message : String(e));
    } finally {
      setWrapping(false);
    }
  }, [
    svcRepo,
    svcCommand,
    svcProject,
    svcName,
    svcPort,
    svcEnvFile,
    svcEnvVault,
    svcOverwrite,
    svcAIWrap,
    showNotice,
    load,
  ]);

  const refreshStatus = useCallback(async (project: string) => {
    try {
      const s = await agentClient.companionStatus(project);
      setStatusByProject((m) => ({ ...m, [project]: s }));
    } catch (e) {
      showNotice("error", e instanceof Error ? e.message : String(e));
    }
  }, [showNotice]);

  const disable = useCallback(async (project: string) => {
    try {
      await agentClient.companionDown(project);
      showNotice("ok", `Disabled ${project}.`);
      await load();
      await refreshStatus(project);
    } catch (e) {
      showNotice("error", e instanceof Error ? e.message : String(e));
    }
  }, [showNotice, load, refreshStatus]);

  return (
    <div className="mx-auto w-full max-w-4xl space-y-6 p-4">
      <div>
        <h1 className="text-xl font-semibold text-foreground">Companion</h1>
        <p className="mt-1 text-sm text-foreground-muted">
          Give a serverless project the always-on tail it can&apos;t run itself — scheduled crons and
          long-running workers — on a Yaver box. Point at a repo; Yaver detects what needs a companion
          and arms it, reboot-durable.
        </p>
      </div>

      {notice && (
        <div
          className={`rounded-lg border px-3 py-2 text-sm ${
            notice.type === "ok"
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
              : "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300"
          }`}
        >
          {notice.text}
        </div>
      )}

      {/* Detect + arm */}
      <section className="rounded-lg border border-border bg-surface-950 p-4">
        <h2 className="text-sm font-medium text-foreground">Add a companion</h2>
        <div className="mt-3 flex gap-2">
          <input
            value={repo}
            onChange={(e) => setRepo(e.target.value)}
            placeholder="/absolute/path/to/your/serverless/repo"
            className="flex-1 rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50"
          />
          <button
            onClick={detect}
            disabled={detecting || !repo.trim()}
            className="rounded-md bg-emerald-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
          >
            {detecting ? "Scanning…" : "Detect"}
          </button>
        </div>

        {detectItems && detectItems.length > 0 && (
          <div className="mt-4 space-y-2">
            {detectItems.map((it) => (
              <div key={`${it.kind}:${it.name}`} className="rounded-md border border-border bg-surface-900 p-3">
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span className="text-xs uppercase tracking-wide text-foreground-muted">{it.kind}</span>
                    <span className="text-sm font-medium text-foreground">{it.name}</span>
                    {it.schedule && (
                      <code className="rounded bg-surface-800 px-1.5 py-0.5 text-[11px] text-foreground-muted">
                        {it.schedule}
                      </code>
                    )}
                  </div>
                  <StatusPill status={it.status} />
                </div>
                <p className="mt-1 text-xs text-foreground-muted">{it.reason}</p>
              </div>
            ))}

            <details className="mt-2">
              <summary className="cursor-pointer text-xs text-foreground-muted">Review yaver.companion.yaml</summary>
              <textarea
                value={manifestYaml}
                onChange={(e) => setManifestYaml(e.target.value)}
                spellCheck={false}
                className="mt-2 h-56 w-full rounded-md border border-border bg-surface-900 p-2 font-mono text-xs text-foreground outline-none"
              />
            </details>

            <div className="flex items-center justify-end gap-2 pt-1">
              <button
                onClick={writeAndArm}
                disabled={arming}
                className="rounded-md bg-emerald-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
              >
                {arming ? "Arming…" : "Write manifest & arm"}
              </button>
            </div>
            <p className="text-[11px] text-foreground-muted">
              Crons marked <span className="text-amber-700 dark:text-amber-300">proposed-missing-endpoint</span> aren&apos;t
              armed — they need you to create the endpoint first.
            </p>
          </div>
        )}
      </section>

      {/* Explicit microservice wrapper */}
      <section className="rounded-lg border border-border bg-surface-950 p-4">
        <h2 className="text-sm font-medium text-foreground">Wrap a microservice</h2>
        <p className="mt-1 text-xs text-foreground-muted">
          Turn a repo command into a durable Yaver companion service, with MCP-visible status and optional AI analysis wrapping.
        </p>
        <div className="mt-3 grid gap-2 md:grid-cols-2">
          <input
            value={svcRepo}
            onChange={(e) => setSvcRepo(e.target.value)}
            placeholder="/absolute/path/to/repo"
            className="rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50 md:col-span-2"
          />
          <input
            value={svcCommand}
            onChange={(e) => setSvcCommand(e.target.value)}
            placeholder="npm run worker"
            className="rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50 md:col-span-2"
          />
          <input
            value={svcProject}
            onChange={(e) => setSvcProject(e.target.value)}
            placeholder="project slug"
            className="rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50"
          />
          <input
            value={svcName}
            onChange={(e) => setSvcName(e.target.value)}
            placeholder="service name"
            className="rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50"
          />
          <input
            value={svcPort}
            onChange={(e) => setSvcPort(e.target.value.replace(/[^0-9]/g, ""))}
            placeholder="port"
            inputMode="numeric"
            className="rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50"
          />
          <input
            value={svcEnvFile}
            onChange={(e) => setSvcEnvFile(e.target.value)}
            placeholder=".env"
            className="rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50"
          />
          <input
            value={svcEnvVault}
            onChange={(e) => setSvcEnvVault(e.target.value)}
            placeholder="vault project"
            className="rounded-md border border-border bg-surface-900 px-3 py-2 text-sm text-foreground outline-none focus:border-emerald-500/50"
          />
          <label className="flex items-center gap-2 rounded-md border border-border bg-surface-900 px-3 py-2 text-xs text-foreground-muted">
            <input type="checkbox" checked={svcAIWrap} onChange={(e) => setSvcAIWrap(e.target.checked)} />
            AI analysis wrapper
          </label>
          <label className="flex items-center gap-2 rounded-md border border-border bg-surface-900 px-3 py-2 text-xs text-foreground-muted">
            <input type="checkbox" checked={svcOverwrite} onChange={(e) => setSvcOverwrite(e.target.checked)} />
            Overwrite manifest
          </label>
        </div>
        <div className="mt-3 flex justify-end">
          <button
            onClick={wrapMicroservice}
            disabled={wrapping || !svcRepo.trim() || !svcCommand.trim()}
            className="rounded-md bg-emerald-600 px-3 py-2 text-sm font-medium text-white disabled:opacity-50"
          >
            {wrapping ? "Wrapping…" : "Write & arm service"}
          </button>
        </div>
        {wrapResult && (
          <div className="mt-3 rounded-md border border-border bg-surface-900 p-3 text-xs text-foreground-muted">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium text-foreground">{wrapResult.project}</span>
              <StatusPill status={wrapResult.armed ? "armed" : wrapResult.written ? "written" : "prepared"} />
              <code>{wrapResult.manifestPath}</code>
            </div>
            {wrapResult.warnings && wrapResult.warnings.length > 0 && (
              <ul className="mt-2 list-disc pl-5 text-amber-700 dark:text-amber-300">
                {wrapResult.warnings.map((wn, i) => <li key={i}>{wn}</li>)}
              </ul>
            )}
          </div>
        )}
      </section>

      {/* Active companions */}
      <section className="space-y-3">
        <h2 className="text-sm font-medium text-foreground">Active companions</h2>
        {loading ? (
          <p className="text-sm text-foreground-muted">Loading…</p>
        ) : err ? (
          <p className="text-sm text-red-700 dark:text-red-300">{err}</p>
        ) : projects.length === 0 ? (
          <p className="text-sm text-foreground-muted">No companions yet. Detect one above.</p>
        ) : (
          projects.map((p) => {
            const s = statusByProject[p.project];
            return (
              <div key={p.project} className="rounded-lg border border-border bg-surface-950 p-4">
                <div className="flex items-center justify-between">
                  <div>
                    <span className="text-sm font-medium text-foreground">{p.project}</span>
                    <span className="ml-2 text-xs text-foreground-muted">
                      {p.cronCount} cron{p.cronCount === 1 ? "" : "s"} · {p.svcCount} service
                      {p.svcCount === 1 ? "" : "s"} · {p.enabled ? "enabled" : "disabled"}
                    </span>
                  </div>
                  <div className="flex gap-2">
                    <button
                      onClick={() => refreshStatus(p.project)}
                      className="rounded-md border border-border px-2 py-1 text-xs text-foreground-muted hover:text-foreground"
                    >
                      Refresh
                    </button>
                    <button
                      onClick={() => disable(p.project)}
                      className="rounded-md border border-red-500/30 px-2 py-1 text-xs text-red-700 dark:text-red-300 hover:bg-red-500/10"
                    >
                      Disable
                    </button>
                  </div>
                </div>

                {s && s.crons.length > 0 && (
                  <table className="mt-3 w-full text-left text-xs">
                    <thead className="text-foreground-muted">
                      <tr>
                        <th className="py-1 font-normal">Cron</th>
                        <th className="py-1 font-normal">Schedule</th>
                        <th className="py-1 font-normal">Next run</th>
                        <th className="py-1 font-normal">Last</th>
                      </tr>
                    </thead>
                    <tbody className="text-foreground">
                      {s.crons.map((c) => (
                        <tr key={c.name} className="border-t border-border">
                          <td className="py-1">{c.name}</td>
                          <td className="py-1">
                            <code className="text-foreground-muted">{c.schedule}</code>
                          </td>
                          <td className="py-1 text-foreground-muted">{c.nextRunAt || "—"}</td>
                          <td className="py-1">{c.lastOutcome || "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                {s && s.services.length > 0 && (
                  <table className="mt-3 w-full text-left text-xs">
                    <thead className="text-foreground-muted">
                      <tr>
                        <th className="py-1 font-normal">Service</th>
                        <th className="py-1 font-normal">Durable</th>
                        <th className="py-1 font-normal">Unit</th>
                        <th className="py-1 font-normal">State</th>
                      </tr>
                    </thead>
                    <tbody className="text-foreground">
                      {s.services.map((svc) => (
                        <tr key={svc.name} className="border-t border-border">
                          <td className="py-1">{svc.name}</td>
                          <td className="py-1 text-foreground-muted">{svc.durable ? "yes" : "no"}</td>
                          <td className="py-1 text-foreground-muted">{svc.unit || "—"}</td>
                          <td className="py-1">
                            <StatusPill status={svc.running ? "running" : "stopped"} />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                {s && s.warnings && s.warnings.length > 0 && (
                  <ul className="mt-2 list-disc pl-5 text-[11px] text-amber-700 dark:text-amber-300">
                    {s.warnings.map((wn, i) => (
                      <li key={i}>{wn}</li>
                    ))}
                  </ul>
                )}
              </div>
            );
          })
        )}
      </section>
    </div>
  );
}
