/**
 * Ring-buffer logger for connection diagnostics, with best-effort
 * persistence to disk.
 *
 * Why persistence: the in-app "Connection Logs" panel is often the only
 * record of a transient failure (e.g. a relay 401 during remote runner
 * auth). Previously the buffer lived only in memory, so the moment the
 * app restarted — or crashed — the evidence was gone and the problem
 * could not be re-attacked. We now mirror the last N entries to
 * AsyncStorage (debounced) and hydrate them on startup, so the logs that
 * explain "what happened last time" survive a relaunch.
 *
 * Import-safety: AsyncStorage is loaded lazily inside the persistence
 * helpers and every call is guarded, so this module remains safe to
 * import from RN-free, tsx-tested code paths (where AsyncStorage may be
 * absent). Logging never blocks on or throws from I/O.
 */

export interface LogEntry {
  timestamp: number;
  level: "info" | "warn" | "error";
  message: string;
}

const MAX_ENTRIES = 200;
const STORAGE_KEY = "yaver.connectionLogs.v1";
const PERSIST_DEBOUNCE_MS = 1500;

const entries: LogEntry[] = [];
const listeners: Array<() => void> = [];

let persistTimer: ReturnType<typeof setTimeout> | null = null;
let hydrated = false;

async function loadAsyncStorage(): Promise<{
  getItem: (k: string) => Promise<string | null>;
  setItem: (k: string, v: string) => Promise<void>;
  removeItem: (k: string) => Promise<void>;
} | null> {
  try {
    const mod = await import("@react-native-async-storage/async-storage");
    return (mod.default ?? mod) as any;
  } catch {
    return null; // not available (e.g. pure test env) — persistence is a no-op
  }
}

function schedulePersist(): void {
  if (persistTimer) return;
  persistTimer = setTimeout(() => {
    persistTimer = null;
    void persistNow();
  }, PERSIST_DEBOUNCE_MS);
}

async function persistNow(): Promise<void> {
  const storage = await loadAsyncStorage();
  if (!storage) return;
  try {
    await storage.setItem(STORAGE_KEY, JSON.stringify(entries.slice(-MAX_ENTRIES)));
  } catch {
    // Disk full / serialization issue — logging must never fail loudly.
  }
}

export function appLog(level: LogEntry["level"], message: string) {
  const entry: LogEntry = { timestamp: Date.now(), level, message };
  entries.push(entry);
  if (entries.length > MAX_ENTRIES) entries.shift();
  for (const cb of listeners) cb();
  // Also forward to console
  const fn = level === "error" ? console.error : level === "warn" ? console.warn : console.log;
  fn(`[App] ${message}`);
  schedulePersist();
}

export function getLogEntries(): LogEntry[] {
  return [...entries];
}

export function clearLogEntries() {
  entries.length = 0;
  for (const cb of listeners) cb();
  void (async () => {
    const storage = await loadAsyncStorage();
    if (storage) {
      try {
        await storage.removeItem(STORAGE_KEY);
      } catch {
        // ignore
      }
    }
  })();
}

export function onLogsChanged(cb: () => void): () => void {
  listeners.push(cb);
  return () => {
    const idx = listeners.indexOf(cb);
    if (idx >= 0) listeners.splice(idx, 1);
  };
}

/**
 * Load the previous session's logs from disk into the in-memory buffer.
 * Idempotent and best-effort: safe to call once at app startup (e.g. from
 * the runtime-debug installer). Persisted entries are prepended so the
 * panel reads oldest→newest across the restart boundary, then trimmed to
 * MAX_ENTRIES. A leading marker makes the boundary obvious in the UI.
 */
export async function hydratePersistedLogs(): Promise<void> {
  if (hydrated) return;
  hydrated = true;
  const storage = await loadAsyncStorage();
  if (!storage) return;
  try {
    const raw = await storage.getItem(STORAGE_KEY);
    if (!raw) return;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed) || parsed.length === 0) return;
    const restored: LogEntry[] = parsed
      .filter((e: any) => e && typeof e.message === "string" && typeof e.timestamp === "number")
      .slice(-MAX_ENTRIES);
    if (restored.length === 0) return;
    const marker: LogEntry = {
      timestamp: Date.now(),
      level: "info",
      message: `── ${restored.length} log(s) restored from previous session ──`,
    };
    entries.unshift(marker, ...restored);
    if (entries.length > MAX_ENTRIES) entries.splice(0, entries.length - MAX_ENTRIES);
    for (const cb of listeners) cb();
  } catch {
    // Corrupt payload — drop it silently rather than block startup.
  }
}
