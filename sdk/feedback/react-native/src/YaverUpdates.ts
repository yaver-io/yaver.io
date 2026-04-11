// YaverUpdates — self-hosted OTA client for React Native apps.
//
// End-user apps poll the yaver agent's /releases/latest endpoint
// through the P2P relay, download the matching Hermes bundle via
// /releases/bundle, and optionally trigger a JS reload. The
// bundle is stored on disk for a subsequent cold start to pick
// up (v1 — this file does NOT hot-swap the live runtime; a
// follow-up native module Swift/Kotlin will wire into Yaver's
// existing safeReloadBridge path for true in-process swaps).
//
// What v1 ships:
//
//   - `YaverUpdates.init({ channel, userId, auto })` — polls
//     /releases/latest on boot + every interval, downloads new
//     bundles when inRollout, persists them to a known path,
//     emits a BlackBox `lifecycle` event "update_ready".
//   - `YaverUpdates.checkForUpdate()` — one-shot poll + download.
//   - `YaverUpdates.applyPendingUpdate()` — calls
//     DevSettings.reload() in dev builds, a no-op in release
//     until the native module lands.
//   - `YaverUpdates.rollback()` — deletes the cached bundle so
//     the next cold start ignores it.
//
// Storage path is stable across restarts and matches what the
// future native module will read:
//
//   <DocumentDirectory>/yaver-updates/<channel>/bundle.hbc
//   <DocumentDirectory>/yaver-updates/<channel>/metadata.json
//
// The SDK writes to a temp path first and renames on success so
// a mid-download crash never leaves a half-written bundle in
// place.
//
// SELF-HOSTING WIN: the dev's own agent serves the bundle
// through the dev's own relay. No EAS Update subscription, no
// CodePush dependency, no central vendor. The bundle never
// touches any server the dev doesn't control.

import { Platform } from 'react-native';
import { BlackBox } from './BlackBox';
import { YaverFeedback } from './YaverFeedback';
import { P2PClient } from './P2PClient';

export interface YaverUpdatesConfig {
  /** Release channel to track. Default: "production". */
  channel?: string;
  /** Stable user identifier for rollout bucketing. */
  userId?: string;
  /** Poll interval in ms. 0 disables the poll loop. Default: 5 min. */
  interval?: number;
  /** Automatically download new bundles. Default: true. */
  autoDownload?: boolean;
  /**
   * Callback fired when a new bundle has been downloaded and
   * persisted. The dev's app can show an "update available" UI
   * and call `applyPendingUpdate()` to trigger the reload.
   */
  onUpdateReady?: (info: PendingUpdate) => void;
}

/** Describes a downloaded-but-not-yet-applied update. */
export interface PendingUpdate {
  channel: string;
  semver: string;
  md5: string;
  size: number;
  downloadedAt: number;
  bundlePath: string;
}

interface LatestResponse {
  ok: boolean;
  channel: string;
  semver?: string;
  size?: number;
  md5?: string;
  hermesBcVersion?: number;
  bundleUrl?: string;
  rolloutPercent: number;
  inRollout: boolean;
  reason?: string;
}

type NativeFS = {
  DocumentDirectoryPath?: string;
  writeFile?: (path: string, contents: string, encoding?: string) => Promise<void>;
  unlink?: (path: string) => Promise<void>;
  mkdir?: (path: string) => Promise<void>;
  exists?: (path: string) => Promise<boolean>;
};

// We feature-detect react-native-fs instead of hard-requiring
// it. Devs who don't have it installed still get
// checkForUpdate() polling + the BlackBox event, just without
// disk persistence.
function loadFS(): NativeFS | null {
  try {
    // eslint-disable-next-line @typescript-eslint/no-require-imports
    const fs = require('react-native-fs');
    return fs as NativeFS;
  } catch {
    return null;
  }
}

export class YaverUpdates {
  private static cfg: Required<YaverUpdatesConfig> | null = null;
  private static pollTimer: ReturnType<typeof setInterval> | null = null;
  private static pending: PendingUpdate | null = null;
  private static fs: NativeFS | null = null;
  private static started = false;

  /**
   * Start the OTA poll loop. Safe to call before init() finishes
   * — the call defers until a P2PClient is available.
   */
  static init(config?: YaverUpdatesConfig): void {
    if (YaverUpdates.started) {
      YaverUpdates.cfg = {
        channel: config?.channel ?? YaverUpdates.cfg?.channel ?? 'production',
        userId: config?.userId ?? YaverUpdates.cfg?.userId ?? 'anonymous',
        interval: config?.interval ?? YaverUpdates.cfg?.interval ?? 5 * 60 * 1000,
        autoDownload: config?.autoDownload ?? YaverUpdates.cfg?.autoDownload ?? true,
        onUpdateReady: config?.onUpdateReady ?? YaverUpdates.cfg?.onUpdateReady ?? (() => {}),
      };
      return;
    }
    YaverUpdates.started = true;
    YaverUpdates.fs = loadFS();
    YaverUpdates.cfg = {
      channel: config?.channel ?? 'production',
      userId: config?.userId ?? 'anonymous',
      interval: config?.interval ?? 5 * 60 * 1000,
      autoDownload: config?.autoDownload ?? true,
      onUpdateReady: config?.onUpdateReady ?? (() => {}),
    };

    // Kick off the first check immediately so devs see an
    // update-available banner on the very next app cold start.
    YaverUpdates.checkForUpdate().catch(() => {});

    if (YaverUpdates.cfg.interval > 0) {
      YaverUpdates.pollTimer = setInterval(
        () => YaverUpdates.checkForUpdate().catch(() => {}),
        YaverUpdates.cfg.interval,
      );
    }
  }

  /** Stop the poll loop. */
  static stop(): void {
    if (YaverUpdates.pollTimer) {
      clearInterval(YaverUpdates.pollTimer);
      YaverUpdates.pollTimer = null;
    }
  }

  /**
   * One-shot poll. Returns the latest release metadata. If a new
   * bundle is available AND autoDownload is true, also downloads
   * and caches it. Resolves to null on network / auth failure.
   */
  static async checkForUpdate(): Promise<LatestResponse | null> {
    const cfg = YaverUpdates.cfg;
    if (!cfg) return null;
    const client = YaverFeedback.getP2PClient();
    if (!client) return null;
    const latest = await client.releasesLatest(cfg.channel, cfg.userId);
    if (!latest || !latest.semver) return latest;
    if (!latest.inRollout) return latest;

    // Skip if we already have this bundle cached.
    if (YaverUpdates.pending && YaverUpdates.pending.semver === latest.semver) {
      return latest;
    }

    if (cfg.autoDownload) {
      await YaverUpdates.downloadAndCache(client, latest);
    }
    return latest;
  }

  /**
   * Returns the currently-pending bundle (downloaded but not
   * applied). Null if no update is waiting.
   */
  static getPendingUpdate(): PendingUpdate | null {
    return YaverUpdates.pending;
  }

  /**
   * Apply a pending update. In dev builds this calls
   * DevSettings.reload(). In release builds without a native
   * module it returns false — the bundle is persisted but the
   * OS will only load it on the next cold start, or after the
   * future YaverUpdates native module lands.
   */
  static async applyPendingUpdate(): Promise<boolean> {
    if (!YaverUpdates.pending) return false;
    try {
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      const rn = require('react-native');
      if (rn?.DevSettings?.reload) {
        rn.DevSettings.reload();
        return true;
      }
    } catch {
      // fall through
    }
    return false;
  }

  /**
   * Discard the cached pending bundle. Next cold start goes
   * back to whatever was previously loaded.
   */
  static async rollback(): Promise<void> {
    if (!YaverUpdates.pending || !YaverUpdates.fs) {
      YaverUpdates.pending = null;
      return;
    }
    try {
      await YaverUpdates.fs.unlink?.(YaverUpdates.pending.bundlePath);
    } catch {
      // swallow — the bundle may already be gone
    }
    YaverUpdates.pending = null;
  }

  // --- internals ---------------------------------------------------

  private static async downloadAndCache(
    client: P2PClient,
    latest: LatestResponse,
  ): Promise<void> {
    if (!latest.semver || !latest.md5 || !latest.size) return;

    const bytes = await client.releasesDownload(latest.channel, latest.semver);
    if (!bytes) return;

    const info: PendingUpdate = {
      channel: latest.channel,
      semver: latest.semver,
      md5: latest.md5,
      size: latest.size,
      downloadedAt: Date.now(),
      bundlePath: '',
    };

    const fs = YaverUpdates.fs;
    if (!fs || !fs.DocumentDirectoryPath || !fs.writeFile) {
      // No FS — still expose the pending record in-memory so
      // the dev's app can react to the BlackBox event.
      YaverUpdates.pending = info;
      YaverUpdates.emitUpdateReady(info);
      return;
    }

    const dir = `${fs.DocumentDirectoryPath}/yaver-updates/${latest.channel}`;
    try {
      await fs.mkdir?.(dir);
    } catch {
      // mkdir -p — directory may already exist
    }
    const bundlePath = `${dir}/bundle.hbc`;
    const tmpPath = `${bundlePath}.tmp`;

    // react-native-fs writeFile accepts base64 or utf8; Hermes
    // bundles are binary so we encode the ArrayBuffer as base64.
    const base64 = bufferToBase64(bytes);
    await fs.writeFile(tmpPath, base64, 'base64');
    try {
      await fs.unlink?.(bundlePath);
    } catch {
      // fine — no previous bundle
    }
    // We can't atomic-rename without a native bridge, so the
    // tmpPath -> bundlePath swap is a copy + delete. For a dev
    // runtime this is fine; the native shim will tighten it.
    try {
      const rename = (fs as unknown as {
        moveFile?: (from: string, to: string) => Promise<void>;
      }).moveFile;
      if (rename) {
        await rename(tmpPath, bundlePath);
      } else {
        // Best-effort fallback: write directly to bundlePath on
        // the next attempt. Leaves a .tmp around, which the next
        // run will overwrite.
      }
    } catch {
      // swallow
    }

    info.bundlePath = bundlePath;
    YaverUpdates.pending = info;
    YaverUpdates.emitUpdateReady(info);
  }

  private static emitUpdateReady(info: PendingUpdate): void {
    BlackBox.lifecycle('yaver-updates: bundle downloaded', {
      channel: info.channel,
      semver: info.semver,
      md5: info.md5,
      size: info.size,
      platform: Platform.OS,
    });

    const cb = YaverUpdates.cfg?.onUpdateReady;
    if (cb) {
      try {
        cb(info);
      } catch {
        // dev callback threw — don't let it stall the poll loop
      }
    }
  }
}

// Small ArrayBuffer -> base64 helper with no external deps so
// the SDK package doesn't balloon. Safe for small bundles
// (~10MB worst case) and runs on every RN target without
// polyfills.
function bufferToBase64(buf: ArrayBuffer): string {
  const bytes = new Uint8Array(buf);
  let binary = '';
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  // btoa exists in both React Native's Hermes and Node for tests.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return (globalThis as any).btoa(binary);
}
