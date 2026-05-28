// Tone-coded pill — sole color decision point for status display.
"use client";

import React from "react";

export type Tone = "muted" | "info" | "success" | "warning" | "danger" | "brand";

const TONE_CLASS: Record<Tone, string> = {
  muted: "bg-muted-soft text-muted-soft-fg",
  info: "bg-info-soft text-info-soft-fg",
  success: "bg-success-soft text-success-soft-fg",
  warning: "bg-warning-soft text-warning-soft-fg",
  danger: "bg-danger-soft text-danger-soft-fg",
  brand: "bg-brand-soft text-brand-softFg",
};

export function AdminBadge({
  tone = "muted",
  children,
  dot = false,
  uppercase = false,
}: {
  tone?: Tone;
  children: React.ReactNode;
  dot?: boolean;
  uppercase?: boolean;
}) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-[11px] font-medium leading-none ${
        uppercase ? "uppercase tracking-wider" : ""
      } ${TONE_CLASS[tone]}`}
    >
      {dot && (
        <span
          className="h-1.5 w-1.5 rounded-full"
          style={{ backgroundColor: "currentColor" }}
        />
      )}
      {children}
    </span>
  );
}
