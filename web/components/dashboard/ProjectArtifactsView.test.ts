import test from "node:test";
import assert from "node:assert/strict";

import {
  ownerArtifactStorageLabel,
  planIncludesYaverArtifactStorage,
  projectArtifactMeteredLabel,
} from "./ProjectArtifactsView";

test("project artifact Yaver storage is Cloud Workspace only", () => {
  assert.equal(planIncludesYaverArtifactStorage("cloud-workspace"), true);
  assert.equal(planIncludesYaverArtifactStorage("cloud-agent"), true);
  assert.equal(planIncludesYaverArtifactStorage("yaver-cloud-byok"), true);

  assert.equal(planIncludesYaverArtifactStorage("relay-pro"), false);
  assert.equal(planIncludesYaverArtifactStorage("relay-monthly"), false);
  assert.equal(planIncludesYaverArtifactStorage("free"), false);
  assert.equal(planIncludesYaverArtifactStorage(null), false);
});

test("project artifact usage labels include pending reservations", () => {
  assert.equal(projectArtifactMeteredLabel({
    storageBytes: 1024,
    reservedUploadBytes: 2048,
    totalMeteredBytes: 3072,
  }), "3.0 KB metered · 2.0 KB pending upload");

  assert.equal(ownerArtifactStorageLabel({
    storageBytes: 1024,
    reservedUploadBytes: 2048,
    totalMeteredBytes: 3072,
    remainingBytes: 4096,
  }), "4.0 KB remaining · 2.0 KB reserved");
});
