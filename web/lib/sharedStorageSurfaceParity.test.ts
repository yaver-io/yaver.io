import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const root = path.resolve(import.meta.dirname, "../..");
const read = (relative: string) => fs.readFileSync(path.join(root, relative), "utf8");

test("shared-storage search consumes the agent hits envelope on every shipped surface", () => {
  const agent = read("desktop/agent/shared_storage.go");
  const web = read("web/components/dashboard/StorageView.tsx");
  const mobile = read("mobile/app/storage.tsx");
  const tv = read("tvos/YaverTV/AgentClient.swift");

  assert.match(agent, /"hits": hits/);
  assert.match(web, /Array\.isArray\(out\?\.hits\)/);
  assert.match(mobile, /Array\.isArray\(data\?\.hits\)/);
  assert.match(tv, /SharedStorageSearchEnvelope\.self/);

  for (const source of [web, mobile]) {
    assert.doesNotMatch(source, /sharedStorageSearch[\s\S]{0,400}out\?\.matches/);
    assert.doesNotMatch(source, /sharedStorageSearch[\s\S]{0,400}out\?\.results/);
  }
});

test("tvOS exposes authenticated browse and search without receiving S3 credentials", () => {
  const client = read("tvos/YaverTV/AgentClient.swift");
  const view = read("tvos/YaverTV/Views/SharedStorageView.swift");
  const dashboard = read("tvos/YaverTV/Views/DashboardView.swift");

  assert.match(client, /path: "\/shared-storage\/profiles"/);
  assert.match(client, /components\.path = "\/shared-storage\/list"/);
  assert.match(client, /components\.path = "\/shared-storage\/search"/);
  assert.doesNotMatch(client, /accessKey|secretKey/i);
  assert.match(view, /searchSharedStorage/);
  assert.match(dashboard, /destination: SharedStorageView\(\)/);
});
