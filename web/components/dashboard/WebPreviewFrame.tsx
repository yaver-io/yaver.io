"use client";

// WebPreviewFrame — a boxed browser-chrome wrapper around the dev
// server iframe. Deliberately distinct from PreviewPane's phone
// mockup: this is for web apps (Next.js / Vite / Flutter Web), not
// React Native. Viewport presets resize the iframe *inside* the box
// so the user can sanity-check responsive breakpoints.

import { useMemo, useState } from "react";

export type ViewportId = "desktop" | "laptop" | "tablet" | "mobile" | "fluid";

export const WEB_PREVIEW_VIEWPORTS: { id: ViewportId; label: string; width: number; height: number }[] = [
  { id: "fluid",   label: "Fluid",   width: 0,    height: 0   }, // uses container width
  { id: "desktop", label: "Desktop", width: 1440, height: 900 },
  { id: "laptop",  label: "Laptop",  width: 1280, height: 800 },
  { id: "tablet",  label: "Tablet",  width: 768,  height: 1024 },
  { id: "mobile",  label: "Mobile",  width: 390,  height: 844 },
];

const VIEWPORTS = WEB_PREVIEW_VIEWPORTS;

interface Props {
  url: string | null;
  running: boolean;
  onHardReload?: () => void;
  onOpenInNewTab?: () => void;
  /** Optional connection-mode label shown on the right of the URL bar. */
  connectionLabel?: string;
  /** Inline notice shown instead of the iframe — used when the dev server
   *  is running but the underlying response can't be rendered in a
   *  browser (e.g. Metro returning a JS bundle, no expo web build). */
  notRenderableNotice?: { title: string; body: string } | null;
  /** Optional primary CTA inside the notRenderable notice — e.g.
   *  "Start Expo Web preview". When provided, renders a solid button
   *  above the secondary "Open raw response anyway" link. */
  notRenderableAction?: { label: string; onClick: () => void; disabled?: boolean } | null;
  /** When set, replaces the iframe area with an animated bundling
   *  progress UI. Used while a sibling dev server (e.g. Expo Web) is
   *  starting up and isn't yet ready to render HTML. */
  bundlingState?: {
    label: string;            // e.g. "Bundling Expo Web for sfmg…"
    detail?: string;          // sub-line, e.g. framework + estimated wait
    elapsedSec?: number;      // counts up while bundling
    expectedSec?: number;     // typical bundle time (~30s for Expo Web)
  } | null;
  /** When set, replaces the iframe with a build-failure card. Takes
   *  precedence over the iframe so a failed build for project A doesn't
   *  let the previously-rendered project B remain visible behind it
   *  (the staleness poll then promotes B back to "ready" 5s later, and
   *  the user concludes the project switch did nothing). */
  buildFailure?: {
    label: string;            // e.g. "Web build failed for yaver/mobile"
    error?: string;           // one-line agent error string
    tail?: string;            // bundler stdout/stderr tail (Metro / hermesc)
    onRetry?: () => void;     // wired to handleBuildStaticBundle
    retryLabel?: string;      // defaults to "Retry build"
  } | null;
  /** Fires when the iframe's onload event triggers — used by the
   *  static-bundle target (web-js-bundle) to POST /dev/web-bundle/ack
   *  so the agent's transport tracker transitions to phase=delivered
   *  with measured ms_to_load. */
  onIframeLoad?: () => void;
  /** Hide the internal viewport picker — the parent (WebReloadView)
   *  may render its own inline with the device-row header to save
   *  ~40 px of vertical space. The active viewport is still
   *  controlled internally; the parent and child sync via the
   *  controlled prop pair below. */
  hideViewportSelector?: boolean;
  /** Controlled viewport from the parent. When supplied the parent
   *  drives the value; otherwise WebPreviewFrame keeps its own
   *  local state. */
  viewport?: ViewportId;
  onViewportChange?: (v: ViewportId) => void;
}

export function WebPreviewFrame({ url, running, onHardReload, onOpenInNewTab, connectionLabel, notRenderableNotice, notRenderableAction, bundlingState, buildFailure, onIframeLoad, hideViewportSelector, viewport: controlledViewport, onViewportChange }: Props) {
  const [internalViewport, setInternalViewport] = useState<ViewportId>("fluid");
  const viewport = controlledViewport ?? internalViewport;
  const setViewport = (v: ViewportId) => {
    if (onViewportChange) {
      onViewportChange(v);
    } else {
      setInternalViewport(v);
    }
  };
  const [reloadNonce, setReloadNonce] = useState(0);

  // Append a reload nonce so the iframe re-fetches cleanly when the
  // user hits the reload button, even if the URL itself hasn't changed.
  const frameUrl = useMemo(() => {
    if (!url) return null;
    try {
      const u = new URL(url);
      u.searchParams.set("__preview_reload", String(reloadNonce));
      return u.toString();
    } catch {
      const sep = url.includes("?") ? "&" : "?";
      return `${url}${sep}__preview_reload=${reloadNonce}`;
    }
  }, [url, reloadNonce]);

  const activeVp = VIEWPORTS.find((v) => v.id === viewport) ?? VIEWPORTS[0];
  const fluid = activeVp.id === "fluid";

  const handleReload = () => {
    setReloadNonce((n) => n + 1);
    onHardReload?.();
  };

  return (
    <div className="flex h-full flex-col gap-2">
      {/* Viewport picker — suppressed when the parent renders its
          own version inline with the device-row header. */}
      {!hideViewportSelector ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[10px] uppercase tracking-widest text-surface-500">Viewport</span>
          <div className="flex rounded-md border border-surface-800 bg-surface-900">
            {VIEWPORTS.map((v) => (
              <button
                key={v.id}
                onClick={() => setViewport(v.id)}
                className={`px-2.5 py-1 text-[11px] transition-colors first:rounded-l-md last:rounded-r-md ${
                  viewport === v.id
                    ? "bg-indigo-500/20 text-indigo-200"
                    : "text-surface-400 hover:bg-surface-800 hover:text-surface-200"
                }`}
                title={v.id === "fluid" ? "Fill container" : `${v.width}×${v.height}`}
              >
                {v.label}
              </button>
            ))}
          </div>
          {!fluid && (
            <span className="text-[10px] text-surface-500">
              {activeVp.width} × {activeVp.height}
            </span>
          )}
        </div>
      ) : null}

      {/* Boxed browser chrome */}
      <div className="flex min-h-0 flex-1 items-start justify-center overflow-auto rounded-lg border border-surface-800 bg-surface-950/40 p-4">
        <div
          className="overflow-hidden rounded-lg border border-surface-700 bg-surface-900 shadow-2xl"
          style={
            fluid
              ? { width: "100%", height: "100%", minHeight: 400 }
              : { width: activeVp.width, height: activeVp.height, flexShrink: 0 }
          }
        >
          {/* URL bar */}
          <div className="flex items-center gap-2 border-b border-surface-800 bg-surface-900/80 px-3 py-2">
            <div className="flex items-center gap-1">
              <span className="h-3 w-3 rounded-full bg-red-500/60" />
              <span className="h-3 w-3 rounded-full bg-yellow-500/60" />
              <span className="h-3 w-3 rounded-full bg-emerald-500/60" />
            </div>
            <button
              onClick={handleReload}
              className="ml-2 rounded p-1 text-surface-400 hover:bg-surface-800 hover:text-surface-100"
              title="Hard reload the iframe"
              aria-label="Reload"
              disabled={!frameUrl}
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="23 4 23 10 17 10" />
                <polyline points="1 20 1 14 7 14" />
                <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10" />
                <path d="M20.49 15a9 9 0 0 1-14.85 3.36L1 14" />
              </svg>
            </button>
            <div className="flex-1 truncate rounded bg-surface-950/80 px-2 py-1 text-[11px] text-surface-400">
              {frameUrl ?? (running ? "…starting" : "no dev server")}
            </div>
            {connectionLabel && (
              <span className="rounded bg-surface-800 px-1.5 py-0.5 text-[10px] uppercase tracking-widest text-surface-400">
                {connectionLabel}
              </span>
            )}
            {onOpenInNewTab && frameUrl && (
              <button
                onClick={onOpenInNewTab}
                className="rounded p-1 text-surface-400 hover:bg-surface-800 hover:text-surface-100"
                title="Open in new tab"
                aria-label="Open in new tab"
              >
                <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
                  <polyline points="15 3 21 3 21 9" />
                  <line x1="10" y1="14" x2="21" y2="3" />
                </svg>
              </button>
            )}
          </div>

          {/* Iframe, build failure card, bundling progress, non-renderable
              notice, or placeholder. Build failure beats every other state
              because the iframe should not show the previous project's
              bundle when the user's *current* attempt failed — that's the
              "Start yaver/mobile but carrotbet stays on screen" bug. */}
          {buildFailure ? (
            <div
              className="flex flex-col items-stretch gap-3 px-6 pt-6 pb-4 text-left"
              style={{ height: `calc(100% - 41px)`, minHeight: 300 }}
            >
              <div className="flex items-start gap-3">
                <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" className="mt-0.5 flex-shrink-0 text-red-400/90">
                  <circle cx="12" cy="12" r="10" />
                  <line x1="12" y1="8" x2="12" y2="12" />
                  <line x1="12" y1="16" x2="12.01" y2="16" />
                </svg>
                <div className="min-w-0 flex-1">
                  <p className="text-[13px] font-semibold text-red-200">{buildFailure.label}</p>
                  {buildFailure.error ? (
                    <p className="mt-1 break-words font-mono text-[11px] leading-5 text-red-200/80">
                      {buildFailure.error}
                    </p>
                  ) : null}
                </div>
                {buildFailure.onRetry ? (
                  <button
                    onClick={buildFailure.onRetry}
                    className="flex-shrink-0 rounded border border-red-500/40 bg-red-500/10 px-3 py-1 text-[11px] font-medium text-red-200 hover:bg-red-500/20"
                  >
                    {buildFailure.retryLabel || "Retry build"}
                  </button>
                ) : null}
              </div>
              {buildFailure.tail ? (
                <pre className="min-h-0 flex-1 overflow-auto rounded border border-red-500/20 bg-surface-950/80 px-3 py-2 font-mono text-[10px] leading-4 text-red-200/70 whitespace-pre">
                  {buildFailure.tail}
                </pre>
              ) : null}
            </div>
          ) : bundlingState ? (
            (() => {
              const elapsed = bundlingState.elapsedSec ?? 0;
              const expected = bundlingState.expectedSec ?? 30;
              const pct = Math.max(2, Math.min(95, Math.round((elapsed / expected) * 100)));
              return (
                <div
                  className="flex flex-col items-center justify-center gap-4 px-6 text-center"
                  style={{ height: `calc(100% - 41px)`, minHeight: 300 }}
                >
                  {/* Animated dot ring */}
                  <div className="relative flex h-16 w-16 items-center justify-center">
                    <div className="absolute inset-0 animate-spin rounded-full border-2 border-sky-500/20 border-t-sky-400/80" />
                    <div className="absolute inset-2 animate-pulse rounded-full bg-sky-500/10" />
                    <span className="relative text-2xl">⚡</span>
                  </div>
                  <div className="space-y-1">
                    <p className="text-[13px] font-medium text-surface-100">{bundlingState.label}</p>
                    {bundlingState.detail ? (
                      <p className="text-[11px] text-surface-400">{bundlingState.detail}</p>
                    ) : null}
                  </div>
                  {/* Progress bar */}
                  <div className="w-72 max-w-full">
                    <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-800">
                      <div
                        className="h-full bg-gradient-to-r from-sky-500 to-emerald-400 transition-[width] duration-500"
                        style={{ width: `${pct}%` }}
                      />
                    </div>
                    <div className="mt-1.5 flex items-center justify-between text-[10px] text-surface-500">
                      <span>{elapsed}s elapsed</span>
                      <span>~{expected}s typical</span>
                    </div>
                  </div>
                  <p className="max-w-xs text-[10px] leading-5 text-surface-600">
                    Spawning a sibling <span className="font-mono">expo --web</span> process on
                    the remote box. Metro stays untouched so the phone keeps getting Hermes
                    bundles. The iframe will swap to the live web build the moment the
                    bundle resolves.
                  </p>
                </div>
              );
            })()
          ) : frameUrl && running && notRenderableNotice ? (
            <div
              className="flex flex-col items-center justify-center gap-3 px-6 text-center text-[12px] text-surface-300"
              style={{ height: `calc(100% - 41px)`, minHeight: 300 }}
            >
              <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-amber-400/80">
                <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0Z" />
                <line x1="12" y1="9" x2="12" y2="13" />
                <line x1="12" y1="17" x2="12.01" y2="17" />
              </svg>
              <p className="font-medium text-surface-100">{notRenderableNotice.title}</p>
              <p className="max-w-[420px] text-[11px] text-surface-400">{notRenderableNotice.body}</p>
              {notRenderableAction && (
                <button
                  onClick={notRenderableAction.onClick}
                  disabled={notRenderableAction.disabled}
                  className="mt-2 rounded border border-emerald-500/40 bg-emerald-500/10 px-4 py-1.5 text-[12px] font-medium text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
                >
                  {notRenderableAction.label}
                </button>
              )}
              {onOpenInNewTab && (
                <button
                  onClick={onOpenInNewTab}
                  className="mt-2 rounded border border-surface-700 bg-surface-900 px-3 py-1 text-[11px] text-surface-200 hover:bg-surface-800"
                >
                  Open raw response anyway
                </button>
              )}
            </div>
          ) : frameUrl && running ? (
            <iframe
              key={frameUrl}
              src={frameUrl}
              className="w-full border-none bg-white"
              style={{ height: `calc(100% - 41px)` }}
              sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals"
              title="Web preview"
              onLoad={onIframeLoad}
            />
          ) : (
            <div className="flex h-full flex-col items-center justify-center gap-2 text-center text-[12px] text-surface-500" style={{ minHeight: 300 }}>
              <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className="text-surface-700">
                <rect x="2" y="3" width="20" height="14" rx="2" ry="2" />
                <line x1="8" y1="21" x2="16" y2="21" />
                <line x1="12" y1="17" x2="12" y2="21" />
              </svg>
              <p>
                {running ? "Dev server starting…" : "No dev server running"}
              </p>
              <p className="max-w-[280px] text-[11px] text-surface-600">
                {running
                  ? "Preview will appear here once the compiler finishes."
                  : "Pick a project on the right and press Start."}
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
