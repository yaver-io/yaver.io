// Overview metric tile — label, big number, optional sub-line.
"use client";

import React from "react";

export function MetricCard({
  label,
  value,
  sub,
  tone = "neutral",
}: {
  label: string;
  value: React.ReactNode;
  sub?: React.ReactNode;
  tone?: "neutral" | "warning" | "danger";
}) {
  const accent =
    tone === "warning"
      ? "border-l-warning"
      : tone === "danger"
        ? "border-l-danger"
        : "border-l-surface-800";

  return (
    <div
      className={`relative rounded-md border border-surface-800 ${accent} border-l-[3px] bg-surface-900 p-4`}
    >
      <div className="text-[11px] font-medium uppercase tracking-wider text-surface-400">
        {label}
      </div>
      <div className="mt-2 font-mono text-2xl tabular-nums text-surface-100">
        {value}
      </div>
      {sub != null && (
        <div className="mt-1 text-[12px] text-surface-300">{sub}</div>
      )}
    </div>
  );
}
