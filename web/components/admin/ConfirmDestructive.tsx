// Type-to-confirm destructive modal. The user MUST type the target's
// canonical name (email/alias/id) verbatim before the destructive
// button enables. The modal copy explicitly tells them the action is
// logged in the audit trail.
"use client";

import React, { useEffect, useRef, useState } from "react";
import { ShieldAlert, X } from "./icons";

export function ConfirmDestructive({
  open,
  title,
  body,
  confirmLabel,
  confirmPhrase,
  destructive = true,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  body: React.ReactNode;
  /** Label on the destructive button. */
  confirmLabel: string;
  /** Verbatim string the user has to type to enable the button. */
  confirmPhrase: string;
  destructive?: boolean;
  onConfirm: () => Promise<void> | void;
  onClose: () => void;
}) {
  const [typed, setTyped] = useState("");
  const [running, setRunning] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (open) {
      setTyped("");
      setErr(null);
      setRunning(false);
      const t = setTimeout(() => inputRef.current?.focus(), 50);
      return () => clearTimeout(t);
    }
  }, [open]);

  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const matches = typed.trim() === confirmPhrase;
  const tone = destructive ? "danger" : "warning";
  const buttonClass = destructive
    ? "bg-danger text-danger-fg hover:opacity-95 disabled:opacity-40"
    : "bg-warning text-warning-fg hover:opacity-95 disabled:opacity-40";

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-surface-50/40 backdrop-blur-sm">
      <div
        role="dialog"
        aria-modal="true"
        className="relative w-full max-w-md rounded-lg border border-surface-800 bg-surface-900 p-5 shadow-2xl"
      >
        <button
          onClick={onClose}
          aria-label="Close"
          className="absolute right-3 top-3 rounded p-1 text-surface-400 hover:text-surface-100"
        >
          <X className="h-4 w-4" />
        </button>

        <div className="flex items-start gap-3">
          <div
            className={`mt-0.5 rounded-md border p-1.5 ${
              tone === "danger"
                ? "border-danger/40 bg-danger-soft text-danger-softFg"
                : "border-warning/40 bg-warning-soft text-warning-softFg"
            }`}
          >
            <ShieldAlert className="h-4 w-4" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="text-[14px] font-semibold text-surface-100">{title}</div>
            <div className="mt-2 text-[13px] leading-relaxed text-surface-300">{body}</div>
          </div>
        </div>

        <div className="mt-4">
          <label className="block text-[11px] font-medium uppercase tracking-wider text-surface-400">
            Type <span className="font-mono text-surface-100">{confirmPhrase}</span> to confirm
          </label>
          <input
            ref={inputRef}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            spellCheck={false}
            autoComplete="off"
            className="mt-1.5 w-full rounded border border-surface-700 bg-surface-950 px-3 py-1.5 font-mono text-[13px] text-surface-100 outline-none focus:border-warning"
          />
        </div>

        <div className="mt-2 text-[11px] text-surface-400">
          This action will be recorded in the audit log.
        </div>

        {err && (
          <div className="mt-3 rounded border border-danger/40 bg-danger-soft p-2 text-[12px] text-danger-softFg">
            {err}
          </div>
        )}

        <div className="mt-4 flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            disabled={running}
            className="rounded border border-surface-700 px-3 py-1.5 text-[13px] text-surface-200 hover:border-surface-500 hover:text-surface-100 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            disabled={!matches || running}
            onClick={async () => {
              setErr(null);
              setRunning(true);
              try {
                await onConfirm();
                onClose();
              } catch (e: any) {
                setErr(String(e?.message || e));
                setRunning(false);
              }
            }}
            className={`rounded px-3 py-1.5 text-[13px] font-medium ${buttonClass}`}
          >
            {running ? "Working…" : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
