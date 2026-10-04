import { expect, test } from "bun:test";
import {
  getHetznerServer,
  listHetznerServerActions,
  powerOnHetznerServer,
  renameHetznerServer,
  shutdownHetznerServer,
} from "@yaver/mobile-lib/hetznerDirectCore";

const token = process.env.HCLOUD_TOKEN?.trim() || "";
const serverId = Number(process.env.YAVER_LIVE_HETZNER_SERVER_ID || "0");
const expectedIp = process.env.YAVER_LIVE_HETZNER_EXPECTED_IP?.trim() || "";
const finalName = process.env.YAVER_LIVE_HETZNER_FINAL_NAME?.trim() || "";
const live = process.env.YAVER_LIVE_HETZNER === "1" && Boolean(token && serverId && expectedIp && finalName);

test.if(live)("real mobile core renames, shuts down, starts, and reads Hetzner actions", async () => {
  const temporaryName = `${finalName}-headless-audit`;
  try {
    const before = await getHetznerServer(token, serverId);
    expect(before).toMatchObject({ id: serverId, ip: expectedIp });

    const temporary = await renameHetznerServer(token, serverId, temporaryName);
    expect(temporary).toMatchObject({ id: serverId, ip: expectedIp, name: temporaryName });
    const renamed = await renameHetznerServer(token, serverId, finalName);
    expect(renamed).toMatchObject({ id: serverId, ip: expectedIp, name: finalName });

    const off = await shutdownHetznerServer(token, serverId);
    expect(off).toMatchObject({ id: serverId, ip: expectedIp, status: "off" });
    const running = await powerOnHetznerServer(token, serverId);
    expect(running).toMatchObject({ id: serverId, ip: expectedIp, status: "running", name: finalName });

    const actions = await listHetznerServerActions(token, serverId);
    expect(actions.some((action) => action.status === "success" && /shutdown|poweron|start/.test(action.command))).toBe(true);
  } finally {
    // A failed assertion must not strand the user's VPS off or under the
    // temporary audit name. Recovery uses the same endpoint-only mobile core.
    const current = await getHetznerServer(token, serverId).catch(() => null);
    if (current?.name !== finalName) await renameHetznerServer(token, serverId, finalName);
    if (current?.status === "off") await powerOnHetznerServer(token, serverId);
  }
}, 180_000);
