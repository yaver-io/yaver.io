// AUTO-SYNCED from shared/client-core/src/urlHost.ts.
// DO NOT EDIT IN PLACE. Edit the source and re-run
// scripts/sync-client-core.sh. CI checks drift via `--check`.

/** RFC 3986 host formatting for URLs. Network APIs keep raw host literals. */
export function urlHost(host: string | null | undefined): string {
  const value = String(host || '').trim();
  if (value.startsWith('[') && value.endsWith(']')) return value;
  return value.includes(':') ? `[${value}]` : value;
}
