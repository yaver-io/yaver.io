// evChargingFormat — pure, dependency-free helpers for the EV charging screen.
// Format a station for display, summarize its connectors, classify charging
// power, and build a navigation deep link. No network, no React — unit-tested
// in evChargingFormat.test.mts (run: npx tsx src/lib/evChargingFormat.test.mts).

import type { EVConnector, EVStation } from "./evChargingClient";

/** A charge speed bucket so the UI can colour-code stations at a glance. */
export type ChargeSpeed = "slow" | "fast" | "ultra" | "unknown";

/** classifyPower buckets a station's max power into a human speed tier.
 *  <50 kW → slow (AC / low DC), 50–149 → fast, ≥150 → ultra, 0/absent →
 *  unknown. Mirrors the common DC-fast vs ultra-fast split EV drivers use. */
export function classifyPower(maxPowerKW?: number): ChargeSpeed {
  if (!maxPowerKW || maxPowerKW <= 0) return "unknown";
  if (maxPowerKW >= 150) return "ultra";
  if (maxPowerKW >= 50) return "fast";
  return "slow";
}

/** A short label for a charge-speed bucket, e.g. "Ultra-fast". */
export function chargeSpeedLabel(speed: ChargeSpeed): string {
  switch (speed) {
    case "ultra":
      return "Ultra-fast";
    case "fast":
      return "Fast";
    case "slow":
      return "Standard";
    default:
      return "Unknown";
  }
}

/** formatDistance renders a distance in km as a compact human string:
 *  <1 km → "640 m", otherwise "12.3 km". Negative/NaN → "". */
export function formatDistance(distanceKM?: number): string {
  if (distanceKM == null || !isFinite(distanceKM) || distanceKM < 0) return "";
  if (distanceKM < 1) return `${Math.round(distanceKM * 1000)} m`;
  return `${distanceKM.toFixed(1)} km`;
}

/** formatPower renders kW with no trailing ".0", e.g. 50 → "50 kW",
 *  7.4 → "7.4 kW". 0/absent → "". */
export function formatPower(powerKW?: number): string {
  if (!powerKW || powerKW <= 0) return "";
  const n = Number.isInteger(powerKW) ? String(powerKW) : powerKW.toFixed(1);
  return `${n} kW`;
}

/** connectorSummary collapses a station's connectors into a deduped, ordered
 *  label like "CCS2 (DC) ×2 · Type 2". DC connectors come first (drivers care
 *  about fast charging), then AC. Empty list → "". */
export function connectorSummary(connectors?: EVConnector[]): string {
  if (!connectors || connectors.length === 0) return "";
  // Group by display type, summing counts.
  const groups = new Map<string, { label: string; count: number; dc: boolean; power: number }>();
  for (const conn of connectors) {
    const label = (conn.type || conn.type_id || "connector").trim();
    if (!label) continue;
    const dc = (conn.current || "").toUpperCase().includes("DC") || (conn.type || "").toUpperCase().includes("DC");
    const prev = groups.get(label);
    if (prev) {
      prev.count += conn.count || 1;
      prev.power = Math.max(prev.power, conn.power_kw || 0);
    } else {
      groups.set(label, { label, count: conn.count || 1, dc, power: conn.power_kw || 0 });
    }
  }
  const parts = [...groups.values()].sort((a, b) => {
    if (a.dc !== b.dc) return a.dc ? -1 : 1; // DC first
    return b.power - a.power;
  });
  return parts
    .map((p) => (p.count > 1 ? `${p.label} ×${p.count}` : p.label))
    .join(" · ");
}

/** stationTitle is the best human name for a station, falling back through
 *  name → operator → town → "Charging station". */
export function stationTitle(st: EVStation): string {
  return (st.name || st.operator || st.network || st.town || "Charging station").trim();
}

/** stationSubtitle composes the secondary line: network/operator and the
 *  town/address, separated by a middle dot. Skips empty parts. */
export function stationSubtitle(st: EVStation): string {
  const net = (st.network || st.operator || "").trim();
  const place = (st.town || st.address || "").trim();
  return [net, place].filter(Boolean).join(" · ");
}

/** navUrl returns the directions deep link for a station: the agent already
 *  fills deep_link with a Google Maps URL, but if it's missing we synthesise
 *  one from lat/lon so the Navigate button always works. */
export function navUrl(st: EVStation): string {
  if (st.deep_link && st.deep_link.trim()) return st.deep_link.trim();
  if (typeof st.lat === "number" && typeof st.lon === "number") {
    return `https://www.google.com/maps/dir/?api=1&destination=${st.lat.toFixed(6)},${st.lon.toFixed(6)}`;
  }
  return "";
}

/** Sensible Turkey-first defaults so the screen is useful out of the box for
 *  a Togg/MG ZS EV driver: CCS2 DC, TR. The screen seeds its filter state from
 *  this and lets the user override. */
export const EV_DEFAULTS = {
  connectorType: "ccs2",
  country: "tr",
  radiusKM: 25,
  minPowerKW: 0,
} as const;
