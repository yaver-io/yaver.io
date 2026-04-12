// yaver-errors — zero-config error tracking that ships to your own Yaver agent.
//
//   import { init } from "yaver-errors";
//   init({ dsn: "https://myapp.com/errors/ingest" });
//
// Captures window.onerror + unhandledrejection automatically. Batches events
// (max 16 per flush or 5s interval). Attaches user/context you set via
// setUser / setContext. No third-party servers involved — events go directly
// to your agent's /errors/ingest endpoint.

export interface YaverErrorEvent {
  message: string;
  stack?: string;
  url?: string;
  userId?: string;
  project?: string;
  env?: string;
  context?: Record<string, unknown>;
}

export interface InitOpts {
  /** Full URL to your agent's /errors/ingest endpoint. Example:
   *  "https://api.myapp.com/errors/ingest" (through your Caddy domain), or
   *  "http://localhost:18080/errors/ingest" for local dev. */
  dsn: string;
  /** Project name/slug shown in the Errors tab. */
  project?: string;
  /** Environment tag (prod/staging/dev). */
  env?: string;
  /** Attach a user id to every event (can be updated later via setUser). */
  userId?: string;
  /** Extra context merged into every event. */
  context?: Record<string, unknown>;
  /** Max events in a batch (default 16). */
  batchSize?: number;
  /** Max ms to wait before flushing a partial batch (default 5000). */
  flushIntervalMs?: number;
  /** Disable console.error auto-capture (still captures thrown errors). */
  captureConsole?: boolean;
}

interface InternalState {
  dsn: string;
  project?: string;
  env?: string;
  userId?: string;
  context: Record<string, unknown>;
  queue: YaverErrorEvent[];
  timer?: ReturnType<typeof setTimeout>;
  batchSize: number;
  flushIntervalMs: number;
}

let state: InternalState | null = null;

function ensure(): InternalState {
  if (!state) throw new Error("yaver-errors: call init() first");
  return state;
}

/** Initialize the SDK. Call once at app startup. */
export function init(opts: InitOpts): void {
  if (state) return; // idempotent
  state = {
    dsn: opts.dsn,
    project: opts.project,
    env: opts.env,
    userId: opts.userId,
    context: opts.context ?? {},
    queue: [],
    batchSize: opts.batchSize ?? 16,
    flushIntervalMs: opts.flushIntervalMs ?? 5000,
  };

  // Browser — wire auto-capture.
  if (typeof window !== "undefined") {
    window.addEventListener("error", (e) => {
      captureException(e.error ?? new Error(String(e.message)), {
        source: e.filename, line: e.lineno, column: e.colno,
      });
    });
    window.addEventListener("unhandledrejection", (e: any) => {
      const reason = e.reason ?? new Error("Unhandled rejection");
      captureException(reason instanceof Error ? reason : new Error(String(reason)),
        { kind: "unhandled-rejection" });
    });
    if (opts.captureConsole !== false) {
      const origErr = console.error;
      console.error = (...args: any[]) => {
        captureMessage(args.map(stringify).join(" "), { kind: "console.error" });
        origErr.apply(console, args);
      };
    }
  }

  // Node — wire process-level handlers.
  if (typeof process !== "undefined" && typeof (process as any).on === "function") {
    (process as any).on("uncaughtException", (err: Error) => {
      captureException(err, { kind: "uncaughtException" });
      flush(); // best-effort sync flush
    });
    (process as any).on("unhandledRejection", (reason: unknown) => {
      const err = reason instanceof Error ? reason : new Error(stringify(reason));
      captureException(err, { kind: "unhandledRejection" });
    });
  }
}

/** Attach or update the current user id on subsequent events. */
export function setUser(id: string | undefined): void {
  ensure().userId = id;
}

/** Merge into the context attached to every event. */
export function setContext(ctx: Record<string, unknown>): void {
  const s = ensure();
  s.context = { ...s.context, ...ctx };
}

/** Capture a thrown Error. */
export function captureException(err: unknown, extra?: Record<string, unknown>): void {
  const s = ensure();
  const e: Error = err instanceof Error ? err : new Error(stringify(err));
  enqueue({
    message: e.message,
    stack: e.stack,
    url: typeof location !== "undefined" ? location.href : undefined,
    userId: s.userId,
    project: s.project,
    env: s.env,
    context: { ...s.context, ...extra },
  });
}

/** Capture a plain message (no stack). */
export function captureMessage(message: string, extra?: Record<string, unknown>): void {
  const s = ensure();
  enqueue({
    message,
    url: typeof location !== "undefined" ? location.href : undefined,
    userId: s.userId,
    project: s.project,
    env: s.env,
    context: { ...s.context, ...extra },
  });
}

/** Force flush the queue right now. Returns a Promise that resolves when sent. */
export async function flush(): Promise<void> {
  const s = ensure();
  if (s.timer) { clearTimeout(s.timer); s.timer = undefined; }
  if (s.queue.length === 0) return;
  const batch = s.queue.splice(0, s.queue.length);
  try {
    await fetch(s.dsn, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(batch.length === 1 ? batch[0] : batch),
      // Keepalive so we can still fire during page unload.
      keepalive: typeof navigator !== "undefined",
    });
  } catch {
    // Requeue silently on network failure (but cap to avoid memory growth).
    s.queue = [...batch.slice(0, 100), ...s.queue];
  }
}

function enqueue(ev: YaverErrorEvent) {
  const s = ensure();
  s.queue.push(ev);
  if (s.queue.length >= s.batchSize) {
    flush();
    return;
  }
  if (!s.timer) {
    s.timer = setTimeout(() => flush(), s.flushIntervalMs);
  }
}

function stringify(x: unknown): string {
  if (typeof x === "string") return x;
  try { return JSON.stringify(x); } catch { return String(x); }
}

// Auto-flush on page hide (browser) — sendBeacon is kinder to the agent than
// a pending fetch when the tab is closing.
if (typeof window !== "undefined") {
  window.addEventListener("pagehide", () => {
    const s = state;
    if (!s || s.queue.length === 0) return;
    try {
      const blob = new Blob([JSON.stringify(s.queue)], { type: "application/json" });
      if (typeof navigator !== "undefined" && (navigator as any).sendBeacon) {
        (navigator as any).sendBeacon(s.dsn, blob);
        s.queue = [];
      }
    } catch {}
  });
}
