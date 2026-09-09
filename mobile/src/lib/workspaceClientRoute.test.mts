import assert from "node:assert/strict";
import { workspaceClientRoute } from "./workspaceClientRoute.ts";

assert.deepEqual(
  workspaceClientRoute("ubuntu", "other-box", ["ubuntu", "other-box"]),
  { useSelectedClient: true },
  "a connected selected box must bypass the unrelated focused box",
);
assert.deepEqual(
  workspaceClientRoute("ubuntu", "other-box", ["other-box"]),
  { useSelectedClient: false, peerTarget: "ubuntu" },
  "an unpooled selection must retain the peer-proxy fallback",
);
assert.deepEqual(
  workspaceClientRoute("ubuntu", "ubuntu", []),
  { useSelectedClient: false },
  "the focused box must never proxy to itself",
);
assert.deepEqual(workspaceClientRoute(null, "other-box", []), { useSelectedClient: false });

console.log("workspace client route tests passed");
