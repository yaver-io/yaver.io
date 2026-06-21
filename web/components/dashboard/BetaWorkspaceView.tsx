"use client";

import type { ComponentProps } from "react";
import VibeCodingView from "@/components/dashboard/VibeCodingView";
import type { BetaStatus } from "@/lib/subscription";

// BetaWorkspaceView — the focused surface a beta user sees on web/PC
// INSTEAD of the full dashboard. It is deliberately thin: a "Beta" header
// (+ the shared project, if any) wrapped around the REAL VibeCodingView
// coding engine, so the beta user gets vibe-coding + preview with none of
// the infra/device/guest/git chrome. The invisible owner-infra share
// (gateway key + hidden box grant) is enforced server-side; this view
// never surfaces it. Same component is reused on phone-width via the
// dashboard's responsive shell.
//
// We forward VibeCodingView's exact props (ComponentProps) so the coding
// engine, device routing, and preview behave identically to the normal
// dashboard — beta just changes the chrome, not the engine.
type VibeProps = ComponentProps<typeof VibeCodingView>;

export default function BetaWorkspaceView({
  beta,
  ...vibeProps
}: { token: string | null | undefined; beta: BetaStatus | null } & VibeProps) {
  const project = beta?.sharedProject ?? null;
  const hours = beta ? `${beta.usedHours}/${beta.includedHours}h` : "";

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center gap-3 border-b border-surface-200 px-4 py-2 dark:border-surface-800">
        <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-semibold text-amber-600 dark:text-amber-400">
          Beta
        </span>
        {project ? (
          <span className="text-sm font-medium text-surface-700 dark:text-surface-200">
            {project}
          </span>
        ) : (
          <span className="text-sm text-surface-500 dark:text-surface-400">
            Sandbox
          </span>
        )}
        {beta?.includedHours ? (
          <span className="ml-auto text-xs text-surface-400 dark:text-surface-500">
            {hours}
          </span>
        ) : null}
      </header>
      <div className="min-h-0 flex-1 overflow-hidden">
        <VibeCodingView {...vibeProps} />
      </div>
    </div>
  );
}
