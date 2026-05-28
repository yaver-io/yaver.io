// Honest "this control is not yet wired" banner. Use ONLY when the
// UI scaffolding ships but the backend behavior does not — never as
// decoration. Includes a tracking-issue link so the operator can vote.
"use client";

import { ExternalLink } from "./icons";

export function RoadmapBanner({
  title,
  body,
  issueUrl,
}: {
  title: string;
  body: string;
  issueUrl?: string;
}) {
  return (
    <div className="flex items-start gap-3 rounded-md border border-warning/40 bg-warning-soft/60 p-3 text-warning-softFg">
      <div className="mt-0.5 h-1.5 w-1.5 shrink-0 rounded-full bg-warning" />
      <div className="min-w-0 flex-1">
        <div className="text-[12px] font-semibold uppercase tracking-wider">
          {title}
        </div>
        <div className="mt-1 text-[12px] leading-relaxed">{body}</div>
        {issueUrl && (
          <a
            href={issueUrl}
            target="_blank"
            rel="noreferrer"
            className="mt-2 inline-flex items-center gap-1 text-[11px] font-medium underline-offset-2 hover:underline"
          >
            Track on GitHub <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </div>
    </div>
  );
}
