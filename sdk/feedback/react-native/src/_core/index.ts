// AUTO-SYNCED from shared/client-core/src/index.ts.
// DO NOT EDIT IN PLACE. Edit the source and re-run
// scripts/sync-client-core.sh. CI checks drift via `--check`.

/**
 * @yaver/client-core — shared constants + endpoint paths used by every
 * Yaver client (mobile, feedback SDKs, web dashboard, desktop app).
 *
 * Phase 1 of the extraction documented in ARCHITECTURE_CLIENT_CORE.md.
 * Later phases add dedup helpers, Discovery, OAuth, BlackBox, etc. —
 * see that doc for the full roadmap.
 *
 * Not published to npm. Consumers mirror this directory as `_core/`
 * under their own `src/` and re-export. `scripts/sync-client-core.sh`
 * keeps the mirrors byte-identical; CI verifies it.
 */

export * from './constants';
export * from './endpoints';
