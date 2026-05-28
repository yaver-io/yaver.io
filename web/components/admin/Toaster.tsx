// Tiny toast surface for admin-action feedback. One stack in the
// bottom-right corner, auto-dismiss after 5s, manual close anytime.
// Provider mounted at the admin layout; useToast() pushes from any
// component below.
"use client";

import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { X } from "./icons";

type Tone = "success" | "warning" | "danger" | "info";

type Toast = {
  id: number;
  tone: Tone;
  title: string;
  body?: string;
};

type Ctx = {
  push: (t: Omit<Toast, "id">) => void;
};

const ToastCtx = createContext<Ctx | null>(null);

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const counter = useRef(0);

  const remove = useCallback((id: number) => {
    setToasts((cur) => cur.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (t: Omit<Toast, "id">) => {
      const id = ++counter.current;
      setToasts((cur) => [...cur, { ...t, id }]);
      setTimeout(() => remove(id), 5000);
    },
    [remove],
  );

  return (
    <ToastCtx.Provider value={{ push }}>
      {children}
      <div className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-full max-w-sm flex-col gap-2">
        {toasts.map((t) => (
          <ToastCard key={t.id} toast={t} onClose={() => remove(t.id)} />
        ))}
      </div>
    </ToastCtx.Provider>
  );
}

function ToastCard({ toast, onClose }: { toast: Toast; onClose: () => void }) {
  const accent =
    toast.tone === "success"
      ? "border-l-success"
      : toast.tone === "warning"
        ? "border-l-warning"
        : toast.tone === "danger"
          ? "border-l-danger"
          : "border-l-info";

  return (
    <div
      role="status"
      className={`pointer-events-auto flex items-start gap-3 rounded-md border border-surface-800 ${accent} border-l-[3px] bg-surface-900 p-3 shadow-lg`}
    >
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-semibold text-surface-100">{toast.title}</div>
        {toast.body && (
          <div className="mt-0.5 text-[12px] leading-relaxed text-surface-300">{toast.body}</div>
        )}
      </div>
      <button
        onClick={onClose}
        aria-label="Dismiss"
        className="rounded p-1 text-surface-400 hover:text-surface-100"
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}

export function useToast(): Ctx {
  const ctx = useContext(ToastCtx);
  if (!ctx) {
    // Layout has not mounted the provider — emit a console warning
    // rather than throw so action handlers don't blow up the page
    // mid-keystroke during HMR.
    return {
      push: (t) => {
        if (typeof console !== "undefined") {
          console.warn(`[admin-toast outside provider]`, t);
        }
      },
    };
  }
  return ctx;
}
