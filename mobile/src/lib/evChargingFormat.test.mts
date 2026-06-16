// evChargingFormat.test.mts — pure helpers for the EV charging screen.
// Run: npx tsx src/lib/evChargingFormat.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  classifyPower,
  chargeSpeedLabel,
  formatDistance,
  formatPower,
  connectorSummary,
  stationTitle,
  stationSubtitle,
  navUrl,
  EV_DEFAULTS,
} from "./evChargingFormat.ts";
import type { EVStation } from "./evChargingClient.ts";

test("classifyPower buckets", () => {
  assert.equal(classifyPower(0), "unknown");
  assert.equal(classifyPower(undefined), "unknown");
  assert.equal(classifyPower(22), "slow");
  assert.equal(classifyPower(50), "fast");
  assert.equal(classifyPower(149), "fast");
  assert.equal(classifyPower(150), "ultra");
  assert.equal(classifyPower(350), "ultra");
});

test("chargeSpeedLabel", () => {
  assert.equal(chargeSpeedLabel("ultra"), "Ultra-fast");
  assert.equal(chargeSpeedLabel("fast"), "Fast");
  assert.equal(chargeSpeedLabel("slow"), "Standard");
  assert.equal(chargeSpeedLabel("unknown"), "Unknown");
});

test("formatDistance", () => {
  assert.equal(formatDistance(undefined), "");
  assert.equal(formatDistance(-1), "");
  assert.equal(formatDistance(0.64), "640 m");
  assert.equal(formatDistance(0.999), "999 m");
  assert.equal(formatDistance(12.34), "12.3 km");
  assert.equal(formatDistance(1), "1.0 km");
});

test("formatPower trims trailing .0", () => {
  assert.equal(formatPower(0), "");
  assert.equal(formatPower(50), "50 kW");
  assert.equal(formatPower(7.4), "7.4 kW");
});

test("connectorSummary dedups, sums counts, DC first", () => {
  const out = connectorSummary([
    { type: "Type 2 (AC)", current: "AC", count: 1, power_kw: 22 },
    { type: "CCS2 (DC)", current: "DC", count: 1, power_kw: 180 },
    { type: "CCS2 (DC)", current: "DC", count: 1, power_kw: 180 },
  ]);
  // CCS2 (DC, higher power) comes first; the two CCS2 collapse to ×2.
  assert.equal(out, "CCS2 (DC) ×2 · Type 2 (AC)");
  assert.equal(connectorSummary([]), "");
  assert.equal(connectorSummary(undefined), "");
});

test("stationTitle / stationSubtitle fallbacks", () => {
  const full: EVStation = {
    name: "Trugo Mall",
    operator: "Trugo",
    network: "Trugo",
    town: "Kadıköy",
    address: "Bağdat Cd 1",
    lat: 40.98,
    lon: 29.02,
  };
  assert.equal(stationTitle(full), "Trugo Mall");
  assert.equal(stationSubtitle(full), "Trugo · Kadıköy");

  const bare: EVStation = { name: "", lat: 1, lon: 2, operator: "ZES" } as EVStation;
  assert.equal(stationTitle(bare), "ZES");
  assert.equal(stationSubtitle(bare), "ZES");

  const empty: EVStation = { name: "", lat: 1, lon: 2 } as EVStation;
  assert.equal(stationTitle(empty), "Charging station");
});

test("navUrl prefers deep_link, else synthesises from lat/lon", () => {
  const withLink: EVStation = { name: "X", lat: 1, lon: 2, deep_link: "https://maps/x" } as EVStation;
  assert.equal(navUrl(withLink), "https://maps/x");

  const noLink: EVStation = { name: "X", lat: 40.987654, lon: 29.021 } as EVStation;
  assert.equal(navUrl(noLink), "https://www.google.com/maps/dir/?api=1&destination=40.987654,29.021000");
});

test("EV defaults are CCS2 / TR", () => {
  assert.equal(EV_DEFAULTS.connectorType, "ccs2");
  assert.equal(EV_DEFAULTS.country, "tr");
});
