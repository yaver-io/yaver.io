/**
 * Screen Monitor (screenlog) — the local-only "screen as a stream of images"
 * black box on a Yaver agent's machine. Lets an SDK consumer (talos, a
 * third-party app, your own tooling) drive screen recording, pull the smart
 * activity report, and feed an input-event trace — all over the agent's
 * authenticated HTTP surface, P2P, never via Convex.
 *
 * Access via `client.screenlog` (see YaverClient). Example:
 *
 * ```ts
 * const sl = client.screenlog;
 * await sl.policySet({ enabled: true });
 * const { session } = await sl.start({ displays: 'all' });
 * // ... later
 * await sl.stop();
 * const { report, topActivity } = await sl.analyze(session.id);
 * // talos can also act as an input PRODUCER:
 * await sl.ingestEvents(session.id, [{ t: Date.now(), type: 'click', x: 840, y: 210, button: 'left' }]);
 * ```
 */

/** Capture configuration. All fields optional — the agent fills defaults
 *  (whole screen, jpg, de-duped). */
export interface ScreenlogConfig {
  intervalSec?: number;
  format?: 'png' | 'jpg';
  quality?: number;
  maxWidth?: number;
  displays?: 'all' | 'primary';
  dedup?: boolean;
  maxDiskMB?: number;
  maxFrames?: number;
  retentionDays?: number;
  tagWindow?: boolean;
  wslTarget?: 'auto' | 'host' | 'wslg';
  captureInput?: boolean;
  allowRawText?: boolean;
  ephemeralFrames?: boolean;
}

/** Consent policy on the RECORDED machine. */
export interface ScreenlogPolicy {
  enabled?: boolean;
  allowRemoteControl?: boolean;
  requireMeshGrant?: boolean;
  notifyOnStart?: boolean;
  allowInputCapture?: boolean;
  allowPeer?: string;
  revokePeer?: string;
}

/** One recorded input action — the standard {screenshot,action} training
 *  schema. Pixel coords are absolute; screenW/H let consumers normalize. */
export interface InputEvent {
  t: number;
  type: 'click' | 'mousedown' | 'mouseup' | 'move' | 'scroll' | 'keydown' | 'keyup' | 'key' | 'text';
  x?: number;
  y?: number;
  button?: 'left' | 'right' | 'middle';
  key?: string;
  text?: string;
  dx?: number;
  dy?: number;
  display?: number;
  screenW?: number;
  screenH?: number;
}

export interface ScreenlogFrame {
  idx: number;
  capturedAt: number;
  display: number;
  file: string;
  bytes?: number;
  activeApp?: string;
  activeWindow?: string;
  activeToMs?: number;
}

export interface ScreenlogSession {
  id: string;
  title?: string;
  host?: string;
  startedAt: number;
  stoppedAt?: number;
  frames: number;
}

export interface ActivityReport {
  source: string;
  subject: string;
  activeSec: number;
  idleSec: number;
  byCategory: { name: string; seconds: number; percent: number }[];
  topLabels: { name: string; seconds: number; percent: number }[];
}

/** The `client.screenlog` surface. */
export interface ScreenlogAPI {
  /** Probe whether capture works here + which driver / deps are needed. */
  drivers(): Promise<any>;
  /** Install the OS capture dependency (Linux: scrot). Best-effort. */
  // (install runs agent-side; exposed as a drivers hint, not a method)
  start(config?: ScreenlogConfig, title?: string): Promise<{ session: ScreenlogSession; viewUrl: string }>;
  stop(): Promise<{ id: string; frames: number; viewUrl: string }>;
  status(): Promise<{ status: any }>;
  list(): Promise<{ sessions: ScreenlogSession[] }>;
  /** Deterministic "what did it spend time on" report + runner prompt. */
  analyze(id: string, opts?: { idleGapSec?: number; maxAttributeSec?: number }): Promise<{ report: ActivityReport; topActivity: string; narrativePrompt: string }>;
  /** Frame metadata for a session. */
  frames(id: string): Promise<{ session: { frames: ScreenlogFrame[] } }>;
  /** Read the input-event companion stream + stats. */
  events(id: string): Promise<{ events: InputEvent[]; stats: any }>;
  /** Feed input events as a PRODUCER (needs policy.allowInputCapture). */
  ingestEvents(id: string, events: InputEvent[]): Promise<{ ingested: number; redacted: boolean }>;
  policyGet(): Promise<{ policy: Required<Omit<ScreenlogPolicy, 'allowPeer' | 'revokePeer'>> }>;
  policySet(patch: ScreenlogPolicy): Promise<{ policy: any }>;
  audit(): Promise<{ audit: any[] }>;
  /** Direct URL to a frame image (attach `Authorization` yourself). */
  frameUrl(id: string, file: string): string;
  /** Direct URL to the tar.gz bundle of a whole session. */
  exportUrl(id: string): string;
}
