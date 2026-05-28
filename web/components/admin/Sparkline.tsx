// 30-day activity sparkline — pure SVG, no chart library. Used on
// the Overview tile next to total-events count.
"use client";

import React from "react";

export function Sparkline({
  values,
  width = 240,
  height = 48,
  tone = "neutral",
}: {
  values: number[];
  width?: number;
  height?: number;
  tone?: "neutral" | "warning";
}) {
  if (!values || values.length === 0) {
    return (
      <div
        className="rounded-sm border border-dashed border-surface-800 text-[11px] text-surface-400"
        style={{ width, height, display: "flex", alignItems: "center", justifyContent: "center" }}
      >
        no activity in window
      </div>
    );
  }

  const max = Math.max(1, ...values);
  const stepX = values.length > 1 ? width / (values.length - 1) : width;
  const points = values
    .map((v, i) => {
      const x = i * stepX;
      const y = height - (v / max) * (height - 4) - 2;
      return `${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(" ");
  const lastX = (values.length - 1) * stepX;
  const lastY = height - (values[values.length - 1] / max) * (height - 4) - 2;
  const strokeColor = tone === "warning" ? "rgb(var(--warning))" : "rgb(var(--surface-200))";
  const dotColor = tone === "warning" ? "rgb(var(--warning))" : "rgb(var(--surface-100))";

  return (
    <svg width={width} height={height} viewBox={`0 0 ${width} ${height}`} className="block">
      <polyline
        fill="none"
        stroke={strokeColor}
        strokeWidth={1.25}
        strokeLinejoin="round"
        strokeLinecap="round"
        points={points}
      />
      <circle cx={lastX} cy={lastY} r={2.5} fill={dotColor} />
    </svg>
  );
}
