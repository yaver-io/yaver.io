// hcloud.test.mts — phone-direct Hetzner client: builders, parsers, cost.
// Run: npx tsx src/lib/hcloud.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  HCLOUD_API,
  createServerReq,
  deleteServerReq,
  listServersReq,
  locationFor,
  looksLikeToken,
  monthlyEur,
  parseCreate,
  parseError,
  parseServers,
  parseSnapshot,
  serverTypeFor,
  snapshotReq,
  uptimeLabel,
} from "./hcloud.ts";

test("listServersReq: GET with bearer + paging", () => {
  const r = listServersReq("tok", 3);
  assert.equal(r.method, "GET");
  assert.equal(r.url, `${HCLOUD_API}/servers?per_page=50&page=3`);
  assert.equal(r.headers.Authorization, "Bearer tok");
});

test("createServerReq: posts type/image/location/user_data", () => {
  const r = createServerReq("tok", {
    name: "yaver-box",
    serverType: "cax21",
    location: "hel1",
    userData: "#cloud-config\n",
  });
  assert.equal(r.method, "POST");
  assert.equal(r.url, `${HCLOUD_API}/servers`);
  assert.equal(r.headers["Content-Type"], "application/json");
  const body = JSON.parse(r.body!);
  assert.equal(body.name, "yaver-box");
  assert.equal(body.server_type, "cax21");
  assert.equal(body.image, "ubuntu-24.04"); // default
  assert.equal(body.location, "hel1");
  assert.equal(body.user_data, "#cloud-config\n");
});

test("snapshot + delete request shapes", () => {
  assert.equal(snapshotReq("t", "42", "desc").url, `${HCLOUD_API}/servers/42/actions/create_image`);
  assert.equal(JSON.parse(snapshotReq("t", "42", "desc").body!).type, "snapshot");
  const d = deleteServerReq("t", "42");
  assert.equal(d.method, "DELETE");
  assert.equal(d.url, `${HCLOUD_API}/servers/42`);
});

test("parseServers maps the live shape", () => {
  const servers = parseServers({
    servers: [
      {
        id: 137276329,
        name: "yaver-freshtest3",
        status: "running",
        public_net: { ipv4: { ip: "1.2.3.4" } },
        server_type: { name: "cpx21" },
        datacenter: { location: { name: "ash" } },
        created: "2026-06-07T00:00:00+00:00",
      },
      { name: "no-id-entry" }, // dropped (missing id → "")
    ],
  });
  assert.equal(servers.length, 1);
  assert.deepEqual(servers[0], {
    id: "137276329",
    name: "yaver-freshtest3",
    status: "running",
    ip: "1.2.3.4",
    type: "cpx21",
    location: "ash",
    created: "2026-06-07T00:00:00+00:00",
  });
});

test("parseCreate + parseSnapshot extract ids, throw when absent", () => {
  assert.deepEqual(parseCreate({ server: { id: 5, public_net: { ipv4: { ip: "9.9.9.9" } } } }), {
    serverId: "5",
    ip: "9.9.9.9",
  });
  assert.throws(() => parseCreate({}));
  assert.equal(parseSnapshot({ image: { id: 77 } }).imageId, "77");
  assert.throws(() => parseSnapshot({}));
});

test("parseError surfaces hetzner envelope or null", () => {
  assert.equal(parseError({ error: { code: "unauthorized", message: "bad token" } }), "unauthorized: bad token");
  assert.equal(parseError({ servers: [] }), null);
});

test("cost: known type → eur, unknown → null", () => {
  assert.equal(monthlyEur("cpx21"), 8.49);
  assert.equal(monthlyEur("CAX21"), 7.49); // case-insensitive
  assert.equal(monthlyEur("exotic99"), null);
  assert.equal(monthlyEur(null), null);
});

test("uptimeLabel: days then hours, deterministic via nowMs", () => {
  const created = "2026-06-01T00:00:00Z";
  const now = Date.parse("2026-06-08T00:00:00Z");
  assert.equal(uptimeLabel(created, now), "up 7d");
  assert.equal(uptimeLabel(created, Date.parse("2026-06-01T05:00:00Z")), "up 5h");
  assert.equal(uptimeLabel("", now), "");
  assert.equal(uptimeLabel("garbage", now), "");
});

test("SKU mapping: arm in EU, amd in US; orderable types", () => {
  assert.equal(serverTypeFor("starter", "eu"), "cax21");
  assert.equal(serverTypeFor("scale", "us"), "cpx41");
  assert.equal(locationFor("eu"), "hel1");
  assert.equal(locationFor("us"), "ash");
});

test("looksLikeToken: structural guard", () => {
  assert.equal(looksLikeToken("a".repeat(64)), true);
  assert.equal(looksLikeToken("short"), false);
  assert.equal(looksLikeToken("has spaces in it ".repeat(4)), false);
});
