/**
 * Canonical client-side constants shared between:
 *   - Yaver mobile (`mobile/`)
 *   - yaver-feedback-react-native (`sdk/feedback/react-native/`)
 *   - yaver-feedback-web           (`sdk/feedback/web/`)
 *   - web dashboard                (`web/`)
 *
 * This is THE source of truth. If you need to change one of these
 * values, change it here and re-run `scripts/sync-client-core.sh` so
 * every consumer picks it up. Each consumer ships its own compiled
 * copy so the shared/client-core directory never needs to be published
 * to npm — but the copies MUST remain byte-identical.
 *
 * DO NOT edit the copies under `mobile/src/_core/` or
 * `sdk/feedback/react-native/src/_core/` directly. They will be
 * overwritten by the sync script, and a CI check fails the build if
 * they drift.
 *
 * See ARCHITECTURE_CLIENT_CORE.md for the full plan.
 */

// ── Yaver.io production Convex deployment ────────────────────────────
//
// This is the Convex site every yaver.io client signs in against and
// every Go agent validates tokens against. If yaver.io migrates the
// deployment (as it did from shocking-echidna-394 → perceptive-minnow-
// 557 in early 2026), bump this constant and every client — mobile,
// both Feedback SDKs, web dashboard — picks it up on the next rebuild.
// No more "invalid token" 403s from clients sitting on a stale URL.
export const CONVEX_SITE_URL =
  'https://perceptive-minnow-557.eu-west-1.convex.site';

// Public web origin. Used for OAuth redirect URL building, the account
// merge approval page, and SDK deep-links. Separate from CONVEX_SITE_URL
// because the web app and Convex live on different origins.
export const WEB_BASE_URL = 'https://yaver.io';

// ── Transport ─────────────────────────────────────────────────────────

/** Default HTTP port `yaver serve` listens on. */
export const DEFAULT_AGENT_HTTP_PORT = 18080;

/** UDP port the LAN beacon is broadcast on. */
export const DEFAULT_BEACON_UDP_PORT = 19837;

// ── Freshness windows ─────────────────────────────────────────────────
//
// All three of these must match the corresponding constants on the
// backend (`backend/convex/devices.ts::HEARTBEAT_STALE_MS`) and the
// desktop agent. Drift between clients and backend produces "green on
// one, yellow on the other" UX glitches from clock-skew alone.

/**
 * How old an agent's last heartbeat can be before the device is
 * considered offline. Mirrors `backend/convex/devices.ts` so
 * Convex + every client agree on the same threshold.
 */
export const HEARTBEAT_STALE_MS = 90_000;

/**
 * How long after the last UDP beacon an agent is still considered
 * "locally present". Re-broadcast interval is 3 s, so 10 s covers
 * three missed beats without false offline.
 */
export const BEACON_STALE_MS = 10_000;

/** Timeout for a direct /health probe over LAN. */
export const PROBE_TIMEOUT_MS = 2_500;

/** Timeout for /health probe routed through a relay server. */
export const RELAY_PROBE_TIMEOUT_MS = 6_000;

// ── Auth ──────────────────────────────────────────────────────────────

/**
 * OAuth callback URL used by mobile + SDK deep-link flows. The yaver.io
 * web OAuth endpoints redirect here once the provider returns. Android
 * consumers must register the matching `<intent-filter>` in their
 * manifest; iOS consumers inside an `ASWebAuthenticationSession` don't
 * need to.
 */
export const OAUTH_REDIRECT = 'yaver://oauth-callback';
