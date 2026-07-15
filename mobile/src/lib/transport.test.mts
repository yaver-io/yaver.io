import test from "node:test";
import assert from "node:assert/strict";

import { classifyTransport } from "./transport.ts";

test("classifyTransport reports Yaver Mesh before Tailscale for mesh overlay IPs", () => {
  const info = classifyTransport({
    localIps: ["100.96.5.17", "100.89.155.25"],
    port: 18080,
  });

  assert.equal(info.primary, "yaver-mesh");
  assert.equal(info.label, "Yaver Mesh");
  assert.match(info.detail, /100\.96\.5\.17/);
});

test("classifyTransport still reports Tailscale for non-mesh CGNAT IPs", () => {
  const info = classifyTransport({
    localIps: ["100.89.155.25"],
    port: 18080,
  });

  assert.equal(info.primary, "tailscale");
  assert.equal(info.label, "Tailscale");
  assert.match(info.detail, /100\.89\.155\.25/);
});
