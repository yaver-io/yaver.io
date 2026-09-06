import test from "node:test";
import assert from "node:assert/strict";

import { makeYaverReadOnlyCodingTools } from "./yaverReadTools.ts";

test("makeYaverReadOnlyCodingTools exposes Yaver tools as read-only coding tools", async () => {
  const tools = makeYaverReadOnlyCodingTools({
    devices: () => [{ id: "dev-1", name: "Snowball", online: true, needsAuth: false } as any],
    primaryDeviceId: () => "dev-1",
    secondaryDeviceId: () => null,
    selectDevice: async () => undefined,
  });

  assert.ok(tools.length > 0);
  assert.ok(tools.every((tool) => tool.mutating === false));

  const deviceList = tools.find((tool) => tool.name === "device.list");
  assert.ok(deviceList);
  const result = await deviceList!.invoke({}, {} as any);
  assert.deepEqual(result, {
    devices: [{
      deviceId: "dev-1",
      name: "Snowball",
      alias: undefined,
      online: true,
      needsAuth: false,
      os: undefined,
      lastSeen: undefined,
      isPrimary: true,
      isSecondary: false,
    }],
  });
});
