import test from "node:test";
import assert from "node:assert/strict";

import { parseEVManualInput } from "./providers.ts";

test("manual EV input can force provider when code has no domain", () => {
  const parsed = parseEVManualInput("TRG-42-07", "trugo");
  assert.equal(parsed?.provider, "trugo");
  assert.equal(parsed?.chargerId, "TRG-42-07");
  assert.equal(parsed?.socketLabel, "TRG-42-07");
  assert.equal(parsed?.confidence, "medium");
});

test("manual EV input still extracts provider URL details", () => {
  const parsed = parseEVManualInput("https://esarj.com.tr/start?stationId=ST123&socket=2", "esarj");
  assert.equal(parsed?.provider, "esarj");
  assert.equal(parsed?.stationId, "ST123");
  assert.equal(parsed?.connectorId, "2");
  assert.equal(parsed?.normalizedUrl?.startsWith("https://esarj.com.tr/start"), true);
});
