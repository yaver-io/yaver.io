// Empty state — never "No data". Always tells the user what would
// populate this surface and (if relevant) what to do next.
"use client";

import React from "react";

export function EmptyState({
  title,
  body,
  action,
}: {
  title: string;
  body: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-start gap-3 rounded-md border border-dashed border-surface-700 bg-surface-900/40 p-8">
      <div className="font-mono text-[12px] uppercase tracking-wider text-surface-400">
        {title}
      </div>
      <div className="max-w-xl text-[13px] leading-relaxed text-surface-300">
        {body}
      </div>
      {action}
    </div>
  );
}
