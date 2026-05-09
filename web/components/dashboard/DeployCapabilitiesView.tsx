"use client";

import { useEffect, useState } from "react";
import {
  agentClient,
  type DeployCapabilitiesReport,
  type DeployCapability,
} from "@/lib/agent-client";
import { UICard, Badge, Button, EmptyState } from "@/components/ui";

// DeployCapabilitiesView — dashboard panel that mirrors the mobile
// deploy-tokens screen. For the connected agent it renders, per
// deploy target (testflight / playstore / convex / cloudflare):
//
//  - a yes/no badge ("READY" / "WRONG OS" / "MISSING TOOLS" / etc.)
//  - the structured Reason headline when CanDeploy=false
//  - per-tool detail rows so the user can see which binary is
//    missing or which deep-probe failed (e.g. xcodebuild Command-
//    Line-Tools-only stub instead of a real Xcode.app)
//  - per-secret detail rows including path-existence checks for
//    *_PATH / *_FILE entries
//  - a "Try syncing from peer" button when the failure is fixable
//    via P2P vault sync (missing entries) — calls /vault/peer-sync
//    on the agent, which fans out to every online peer the user
//    owns and pulls newer entries.
//
// Older agents that don't expose /deploy/capabilities surface as
// an EmptyState rather than crashing — keeps the dashboard usable
// against a fleet of mixed-version agents.

const TARGET_LABELS: Record<string, string> = {
  testflight: "TestFlight (iOS)",
  playstore: "Play Store (Android)",
  convex: "Convex (backend)",
  cloudflare: "Cloudflare (web)",
};

interface Props {
  /** Optional vault project slug. When unset, the agent falls back
   *  to each target's canonical project (mobile / backend / web) so
   *  shared signing materials stored once still satisfy capability
   *  checks across every per-app deploy UI. */
  project?: string;
}

export function DeployCapabilitiesView({ project }: Props) {
  const [report, setReport] = useState<DeployCapabilitiesReport | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [syncMessage, setSyncMessage] = useState<string | null>(null);

  const refresh = async () => {
    setLoading(true);
    setError(null);
    try {
      const r = await agentClient.deployCapabilities(project ? { project } : undefined);
      setReport(r);
    } catch (err: any) {
      setError(err?.message ?? "Failed to load deploy capabilities.");
      setReport(null);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project]);

  const trySyncFromPeers = async () => {
    setSyncing(true);
    setSyncMessage(null);
    try {
      const result = await agentClient.vaultPeerSync();
      if (result.peers.length === 0) {
        setSyncMessage(result.note ?? "No peer devices found.");
      } else {
        setSyncMessage(
          `Pulled ${result.totals.pulled}, pushed ${result.totals.pushed} across ${result.peers.length} peers.`,
        );
      }
      await refresh();
    } catch (err: any) {
      setSyncMessage(err?.message ?? "Sync failed.");
    } finally {
      setSyncing(false);
    }
  };

  if (loading) {
    return (
      <div className="text-sm text-surface-400 dark:text-surface-500">Loading deploy capabilities…</div>
    );
  }
  if (error || !report) {
    return (
      <EmptyState
        title="Deploy capabilities unavailable"
        description={
          error ??
          "The connected agent doesn't expose /deploy/capabilities. Update yaver-cli to 1.99.171+ to see deploy gating here."
        }
      />
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <div className="text-xs text-surface-400 dark:text-surface-500">
          Connected agent: {report.platform}
          {report.isWsl ? " (WSL)" : ""} {report.arch}
          {project ? ` · project ${project}` : ""}
        </div>
        <Button variant="ghost" size="sm" onClick={refresh}>
          Refresh
        </Button>
      </div>
      {syncMessage ? (
        <div className="rounded-md border border-info/40 bg-info/5 px-3 py-2 text-xs text-info">
          {syncMessage}
        </div>
      ) : null}
      {report.targets.map((t) => (
        <DeployTargetRow
          key={t.target}
          cap={t}
          onSyncFromPeers={trySyncFromPeers}
          syncing={syncing}
        />
      ))}
    </div>
  );
}

function DeployTargetRow({
  cap,
  onSyncFromPeers,
  syncing,
}: {
  cap: DeployCapability;
  onSyncFromPeers: () => void;
  syncing: boolean;
}) {
  const label = TARGET_LABELS[cap.target] ?? cap.target;
  const platformBlocked = !cap.canDeploy && !!cap.platformLock;
  const tone = cap.canDeploy ? "success" : "default";
  const status = cap.canDeploy
    ? "READY"
    : platformBlocked
      ? "WRONG OS"
      : (cap.missingTools?.length ?? 0) > 0
        ? "MISSING TOOLS"
        : (cap.missingSecrets?.length ?? 0) > 0
          ? "MISSING SECRETS"
          : "BLOCKED";
  const badgeTone = cap.canDeploy ? "success" : platformBlocked ? "muted" : "danger";

  // Sync-from-peer is only useful when the failure is missing
  // vault entries — platform locks and missing tools can't be
  // fixed by pulling more secrets from another device.
  const canFixViaSync =
    !cap.canDeploy && !platformBlocked && (cap.missingSecrets?.length ?? 0) > 0;

  return (
    <UICard tone={tone} padding="md">
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm font-semibold text-surface-100 dark:text-surface-50">{label}</div>
          {cap.stack ? (
            <div className="text-xs text-surface-400 dark:text-surface-500">{cap.stack}</div>
          ) : null}
        </div>
        <Badge tone={badgeTone} variant="solid">
          {status}
        </Badge>
      </div>
      {!cap.canDeploy ? (
        <div className="mt-3 space-y-1.5 rounded-md bg-surface-800/40 px-3 py-2.5 dark:bg-surface-900/40">
          <div className="text-xs font-semibold text-danger">Can't deploy from this agent</div>
          <div className="text-xs text-surface-300 dark:text-surface-400">{cap.reason}</div>
          {/* Per-tool failure rows. Skip platform-skipped entries —
           * they're already covered by the platform-lock badge. */}
          {(cap.tools ?? [])
            .filter((tool) => tool.required && !tool.found && !tool.platformSkipped)
            .map((tool) => (
              <div key={`tool-${tool.name}`} className="text-xs text-surface-300 dark:text-surface-400">
                ✗ {tool.name}: not found
                {tool.installHint ? ` — ${tool.installHint}` : ""}
              </div>
            ))}
          {(cap.tools ?? [])
            .filter((tool) => tool.deepValid === false && tool.deepError)
            .map((tool) => (
              <div key={`tool-deep-${tool.name}`} className="text-xs text-warning">
                ⚠ {tool.name}: {tool.deepError}
              </div>
            ))}
          {(cap.secrets ?? [])
            .filter((sec) => !sec.found || sec.pathValid === false)
            .map((sec) => (
              <div
                key={`sec-${sec.name}`}
                className={
                  !sec.found
                    ? "text-xs text-surface-300 dark:text-surface-400"
                    : "text-xs text-warning"
                }
              >
                {!sec.found
                  ? `✗ ${sec.name}: not in vault`
                  : `⚠ ${sec.name}: ${sec.pathError ?? "path invalid"}`}
              </div>
            ))}
          <div className="flex flex-wrap items-center gap-2 pt-1">
            {canFixViaSync ? (
              <Button variant="primary" size="sm" onClick={onSyncFromPeers} disabled={syncing}>
                {syncing ? "Syncing…" : "Try syncing from another device"}
              </Button>
            ) : null}
            {cap.ciAlternative ? (
              <span className="text-xs text-surface-400 dark:text-surface-500">
                CI fallback: {cap.ciAlternative}
              </span>
            ) : null}
          </div>
        </div>
      ) : (
        // Ready row: still surface the sourcing info so the operator
        // can see which vault project a secret resolved against.
        <div className="mt-2 space-y-0.5">
          {(cap.secrets ?? [])
            .filter((sec) => sec.found)
            .map((sec) => (
              <div
                key={`ready-sec-${sec.name}`}
                className="text-xs text-surface-400 dark:text-surface-500"
              >
                ✓ {sec.name}
                {sec.source ? ` (${sec.source}${sec.project ? `:${sec.project}` : ""})` : ""}
              </div>
            ))}
        </div>
      )}
    </UICard>
  );
}
