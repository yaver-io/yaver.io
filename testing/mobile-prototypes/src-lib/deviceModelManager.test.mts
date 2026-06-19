// deviceModelManager.test.mts — Tests for device and model management with nicknames
// Run: npx tsx mobile/src/lib/deviceModelManager.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  createDeviceModelManager,
  getDeviceModelManager,
  destroyDeviceModelManager,
  type DeviceConfig,
  type ModelConfig,
} from "./deviceModelManager";

// ── Test Setup ───────────────────────────────────────────────────────

async function withManager<T>(fn: (manager: Awaited<ReturnType<typeof createDeviceModelManager>>) => Promise<T>): Promise<T> {
  const manager = createDeviceModelManager();
  try {
    return await fn(manager);
  } finally {
    destroyDeviceModelManager();
  }
}

// ── Device Management Tests ─────────────────────────────────────────

test("addDevice creates device with ID, hostname, and nickname", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({
      hostname: "magara.yaver.local",
      nickname: "magara",
      platform: "remote",
      capabilities: ["car", "tv"],
    });
    
    assert.ok(device.id);
    assert.equal(device.hostname, "magara.yaver.local");
    assert.equal(device.nickname, "magara");
    assert.equal(device.platform, "remote");
    assert.deepEqual(device.capabilities, ["car", "tv"]);
    assert.equal(device.online, true);
    assert.ok(device.lastSeen > Date.now() - 1000);
  });
});

test("addDevice generates ID if not provided", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({
      hostname: "newbox.local",
      nickname: "newbox",
    });
    
    assert.ok(device.id);
    assert.ok(device.id.length > 5);
  });
});

test("updateDevice changes device properties", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({
      hostname: "box.local",
      nickname: "oldname",
      capabilities: ["tv"],
    });
    
    const updated = await manager.updateDevice(device.id, {
      nickname: "newname",
      capabilities: ["tv", "car", "watch"],
      metadata: { os: "macOS" },
    });
    
    assert.equal(updated.nickname, "newname");
    assert.deepEqual(updated.capabilities, ["tv", "car", "watch"]);
    assert.equal((updated.metadata as any).os, "macOS");
    assert.ok(updated.lastSeen > device.lastSeen);
  });
});

test("removeDevice deletes device and unregisters nickname", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({
      hostname: "todelete.local",
      nickname: "todelete",
    });
    
    await manager.removeDevice(device.id);
    
    assert.equal(await manager.getDevice(device.id), null);
    assert.equal(await manager.getDeviceByNickname("todelete"), null);
  });
});

test("getDeviceByNickname resolves nickname to device", async () => {
  await withManager(async (manager) => {
    await manager.addDevice({
      hostname: "mybox.local",
      nickname: "primary",
    });
    await manager.addDevice({
      hostname: "mybox2.local",
      nickname: "secondary",
    });
    
    const primary = await manager.getDeviceByNickname("primary");
    const secondary = await manager.getDeviceByNickname("secondary");
    
    assert.ok(primary);
    assert.equal(primary!.nickname, "primary");
    assert.ok(secondary);
    assert.equal(secondary!.nickname, "secondary");
    assert.notEqual(primary!.id, secondary!.id);
  });
});

test("getDeviceByNickname is case-insensitive", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({
      hostname: "case.local",
      nickname: "MyBox",
    });
    
    const found = await manager.getDeviceByNickname("mybox");
    assert.ok(found);
    assert.equal(found!.id, device.id);
  });
});

test("getDeviceByNickname returns null for unknown nickname", async () => {
  await withManager(async (manager) => {
    assert.equal(await manager.getDeviceByNickname("unknown"), null);
  });
});

test("resolveDevice accepts ID, nickname, or hostname", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({
      hostname: "resolve.local",
      nickname: "resolver",
    });
    
    assert.equal((await manager.resolveDevice(device.id))?.id, device.id);
    assert.equal((await manager.resolveDevice("resolver"))?.id, device.id);
    assert.equal((await manager.resolveDevice("resolve.local"))?.id, device.id);
    assert.equal(await manager.resolveDevice("unknown"), null);
  });
});

test("listDevices returns all devices", async () => {
  await withManager(async (manager) => {
    await manager.addDevice({ hostname: "d1.local", nickname: "dev1" });
    await manager.addDevice({ hostname: "d2.local", nickname: "dev2" });
    await manager.addDevice({ hostname: "d3.local", nickname: "dev3" });
    
    const devices = await manager.listDevices();
    assert.equal(devices.length, 3);
    assert.ok(devices.every(d => d.id));
  });
});

test("markDeviceOnline/offline updates online status", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({ hostname: "status.local", online: true });
    
    assert.equal(device.online, true);
    
    await manager.markDeviceOffline(device.id);
    const offline = await manager.getDevice(device.id);
    assert.equal(offline!.online, false);
    
    await manager.markDeviceOnline(device.id);
    const backOnline = await manager.getDevice(device.id);
    assert.equal(backOnline!.online, true);
  });
});

test("setDeviceNickname updates nickname and resolves correctly", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({ hostname: "name.local", nickname: "old" });
    
    await manager.setDeviceNickname(device.id, "new");
    
    assert.equal((await manager.getDevice(device.id))?.nickname, "new");
    assert.equal((await manager.getDeviceByNickname("old"))?.id, null);
    assert.equal((await manager.getDeviceByNickname("new"))?.id, device.id);
  });
});

// ── Model Management Tests ───────────────────────────────────────────

test("addModel creates model with provider, model, and nickname", async () => {
  await withManager(async (manager) => {
    const model = await manager.addModel({
      provider: "anthropic",
      model: "claude-3-opus-20240229",
      nickname: "claude-opus",
      capabilities: ["code", "chat", "vision"],
      isLocal: false,
    });
    
    assert.equal(model.id, "anthropic/claude-3-opus-20240229");
    assert.equal(model.provider, "anthropic");
    assert.equal(model.model, "claude-3-opus-20240229");
    assert.equal(model.nickname, "claude-opus");
    assert.deepEqual(model.capabilities, ["code", "chat", "vision"]);
    assert.equal(model.isLocal, false);
    assert.equal(model.apiKeyRequired, true);
  });
});

test("addModel with custom provider generates correct ID", async () => {
  await withManager(async (manager) => {
    const model = await managerModelManager.addModel({
      provider: "custom",
      model: "llama-3-70b",
      nickname: "local-llama",
      isLocal: true,
      apiUrl: "http://localhost:11434",
    });
    
    assert.equal(model.id, "custom/llama-3-70b");
  });
});

test("updateModel changes model properties", async () => {
  await withManager(async (manager) => {
    const model = await managerModelManager.addModel({
      provider: "openai",
      model: "gpt-4",
      nickname: "gpt-4",
      capabilities: ["chat"],
    });
    
    const updated = await managerModelManager.updateModel(model.id, {
      nickname: "gpt-4-turbo",
      capabilities: ["chat", "code"],
      metadata: { contextWindow: 128000 },
    });
    
    assert.equal(updated.nickname, "gpt-4-turbo");
    assert.deepEqual(updated.capabilities, ["chat", "code"]);
    assert.equal((updated.metadata as any).contextWindow, 128000);
  });
});

test("removeModel deletes model and unregisters nickname", async () => {
  await withManager(async (manager) => {
    const model = await managerModelManager.addModel({
      provider: "ollama",
      model: "llama2",
      nickname: "local",
    });
    
    await managerModelManager.removeModel(model.id);
    
    assert.equal(await managerModelManager.getModel(model.id), null);
    assert.equal(await managerModelManager.getModelByNickname("local"), null);
  });
});

test("getModelByNickname resolves nickname to model", async () => {
  await withManager(async (manager) => {
    await manager.addModel({
      provider: "openai",
      model: "gpt-4-turbo",
      nickname: "turbo",
    });
    await manager.addModel({
      provider: "openai",
      model: "gpt-4",
      nickname: "gpt4",
    });
    
    const turbo = await manager.getModelByNickname("turbo");
    const gpt4 = await manager.getModelByNickname("gpt4");
    
    assert.ok(turbo);
    assert.equal(turbo!.nickname, "turbo");
    assert.ok(gpt4);
    assert.equal(gpt4!.nickname, "gpt4");
    assert.notEqual(turbo!.id, gpt4!.id);
  });
});

test("getModelByNickname is case-insensitive", async () => {
  await withManager(async (manager) => {
    const model = await manager.addModel({
      provider: "openai",
      model: "gpt-4",
      nickname: "GPT-4",
    });
    
    const found = await manager.getModelByNickname("gpt-4");
    assert.ok(found);
    assert.equal(found!.id, model.id);
  });
});

test("getModelByNickname returns null for unknown nickname", async () => {
  await withManager(async (manager) => {
    assert.equal(await manager.getModelByNickname("unknown"), null);
  });
});

test("resolveModel accepts ID, nickname, or provider/model", async () => {
  await withManager(async (manager) => {
    const model = await manager.addModel({
      provider: "anthropic",
      model: "claude-3-opus",
      nickname: "opus",
    });
    
    assert.equal((await manager.resolveModel(model.id))?.id, model.id);
    assert.equal((await manager.resolveModel("opus"))?.id, model.id);
    assert.equal((await manager.resolveModel("anthropic/claude-3-opus"))?.id, model.id);
    assert.equal(await manager.resolveModel("unknown"), null);
  });
});

test("resolveModel tries device preference first", async () => {
  await withManager(async (manager) => {
    const device1 = await manager.addDevice({ hostname: "d1.local", nickname: "dev1" });
    const device2 = await manager.addDevice({ hostname: "d2.local", nickname: "dev2" });
    
    const model1 = await manager.addModel({ provider: "openai", model: "gpt-4", nickname: "gpt4" });
    const model2 = await manager.addModel({ provider: "anthropic", model: "claude-3", nickname: "claude" });
    
    await manager.setDevicePreferredModel(device1.id, model2.id);
    
    // Device preference takes precedence
    assert.equal((await manager.resolveModel("gpt4", device1.id))?.id, model2.id);
    assert.equal((await manager.resolveModel("gpt4", device2.id))?.id, model1.id);
    // No device preference, fall back to global
    assert.equal((await manager.resolveModel("claude"))?.id, model2.id);
  });
});

test("listModels returns all models", async () => {
  await withManager(async (manager) => {
    await manager.addModel({ provider: "openai", model: "gpt-4", nickname: "gpt4" });
    await manager.addModel({ provider: "anthropic", model: "claude-3", nickname: "claude" });
    await manager.addModel({ provider: "ollama", model: "llama2", nickname: "local" });
    
    const models = await manager.listModels();
    assert.equal(models.length, 3);
    assert.ok(models.every(m => m.id));
  });
});

test("setModelNickname updates nickname and resolves correctly", async () => {
  await withManager(async (manager) => {
    const model = await manager.addModel({
      provider: "openai",
      model: "gpt-4",
      nickname: "oldname",
    });
    
    await manager.setModelNickname(model.id, "newname");
    
    assert.equal((await manager.getModel(model.id))?.nickname, "newname");
    assert.equal((await manager.getModelByNickname("oldname"))?.id, null);
    assert.equal((await manager.getModelByNickname("newname"))?.id, model.id);
  });
});

// ── Preference Tests ───────────────────────────────────────────────

test("setPreferredDevice marks device as default", async () => {
  await withManager(async (manager) => {
    await manager.addDevice({ hostname: "d1.local", nickname: "dev1" });
    await manager.addDevice({ hostname: d2.local", nickname: "dev2" });
    
    await manager.setPreferredDevice("d2.local");
    
    const pref = await manager.getPreferredDevice();
    assert.ok(pref);
    assert.equal(pref?.id, "d2.local");
  });
});

test("getPreferredDevice falls back to first online device", async () => {
  await withManager(async (manager) => {
    await manager.addDevice({ hostname: "d1.local", nickname: "local" });
    await manager.addDevice({ hostname: "offline.local", nickname: "offline", online: false });
    
    const pref = await manager.getPreferredDevice();
    assert.ok(pref);
    assert.equal(pref?.nickname, "local");
  });
});

test("setPreferredModel marks model as default", async () => {
  await withManager(async (manager) => {
    await manager.addModel({ provider: "openai", model: "gpt-4", nickname: "gpt4" });
    await manager.addModel({ provider: "anthropic", model: "claude-3", nickname: "claude" });
    
    await manager.setPreferredModel("anthropic/claude-3");
    
    const pref = await manager.getPreferredModel();
    assert.ok(pref);
    assert.equal(pref, "anthropic/claude-3");
  });
});

test("setDevicePreferredModel sets model per-device preference", async () => {
  await withManager(async (manager) => {
    const device1 = await manager.addDevice({ hostname: "d1.local", nickname: "dev1" });
    const device2 = await manager.addDevice({ hostname: "d2.local", nickname: "dev2" });
    const model1 = await manager.addModel({ provider: "openai", model: "gpt-4", nickname: "gpt4" });
    const model2 = await manager.addModel({ provider: "anthropic", model: "claude-3", nickname: "claude" });
    
    await manager.setDevicePreferredModel(device1.id, model2.id);
    await manager.setDevicePreferredModel(device2.id, model1.id);
    
    assert.equal((await manager.getDevicePreferredModel(device1.id))?.id, model2.id);
    assert.equal((await manager.getDevicePreferredModel(device2.id))?.id, model1.id);
  });
});

test("getDevicePreferredModel falls back to global preference", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({ hostname: "d1.local", nickname: "dev1" });
    const model = await manager.addModel({ provider: "openai", model: "gpt-4", nickname: "gpt4" });
    
    await manager.setPreferredModel("openai/gpt-4");
    
    const pref = await manager.getDevicePreferredModel(device.id);
    assert.ok(pref);
    assert.equal(pref?.id, "openai/gpt-4");
  });
});

// ── Event Tests ───────────────────────────────────────────────────────

test("onDeviceChange fires when devices change", async () => {
  await withManager(async (manager) => {
    const events: unknown[] = [];
    const unsubscribe = manager.onDeviceChange((devices) => {
      events.push(devices.length);
    });
    
    await manager.addDevice({ hostname: "d1.local", nickname: "dev1" });
    assert.deepEqual(events, [1]);
    
    await manager.addDevice({ hostname: "d2.local", nickname: "dev2" });
    assert.deepEqual(events, [1, 2]);
    
    await manager.removeDevice((await manager.listDevices())[0].id);
    assert.deepEqual(events, [1, 2, 1]);
    
    unsubscribe();
  });
});

test("onModelChange fires when models change", async () => {
  await withManager(async (manager) => {
    const events: unknown[] = [];
    const unsubscribe = manager.onModelChange((models) => {
      events.push(models.length);
    });
    
    await manager.addModel({ provider: "openai", model: "gpt-4", nickname: "gpt4" });
    assert.deepEqual(events, [1]);
    
    await manager.addModel({ provider: "anthropic", model: "claude-3", nickname: "claude" });
    assert.deepEqual(events, [1, 2]);
    
    await manager.removeModel((await manager.listModels())[0].id);
    assert.deepEqual(events, [1, 2, 1]);
    
    unsubscribe();
  });
});

// ── Integration Tests ────────────────────────────────────────────────

test("end-to-end: manage devices and models with nicknames", async () => {
  await withManager(async (manager) => {
    // Add devices
    const magara = await manager.addDevice({
      hostname: "magara.yaver.local",
      nickname: "magara",
      platform: "remote",
      capabilities: ["car", "tv", "watch"],
    });
    
    const homePi = await manager.addDevice({
      hostname: "homepi.local",
      nickname: "home-pi",
      platform: "remote",
      connectionType: "tailscale",
    });
    
    // Add models
    const opus = await manager.addModel({
      provider: "anthropic",
      model: "claude-3-opus-20240229",
      nickname: "claude-opus",
      capabilities: ["code", "chat", "vision"],
    });
    
    const turbo = await manager.addModel({
      provider: "openai",
      model: "gpt-4-turbo",
      nickname: "gpt-4-turbo",
      capabilities: ["chat", "code"],
    });
    
    const localLlama = await manager.addModel({
      provider: "ollama",
      model: "llama3:70b",
      nickname: "local-llama",
      isLocal: true,
      apiUrl: "http://localhost:11434",
    });
    
    // Set preferences
    await manager.setPreferredDevice("magara.yaver.local");
    await manager.setPreferredModel("anthropic/claude-3-opus-20240229");
    await manager.setDevicePreferredModel("homepi.local", "ollama/llama3:70b");
    
    // Verify
    const prefDevice = await manager.getPreferredDevice();
    assert.equal(prefDevice?.id, magara.id);
    
    const prefModel = await manager.getPreferredModel();
    assert.equal(prefModel, "anthropic/claude-3-opus-20240229");
    
    const magaraModel = await manager.getDevicePreferredModel(magara.id);
    assert.equal(magaraModel?.id, opus.id);
    
    const piModel = await manager.getDevicePreferredModel(homePi.id);
    assert.equal(piModel?.id, localLlama.id);
    
    // Resolve by nickname
    assert.equal((await manager.resolveDevice("magara"))?.id, magara.id);
    assert.equal((await manager.resolveModel("claude-opus"))?.id, opus.id);
    assert.equal((await manager.resolveModel("gpt-4-turbo"))?.id, turbo.id);
    assert.equal((await manager.resolveModel("local-llama"))?.id, localLlama.id);
  });
});

test("resolveForSurface picks preferred device and model", async () => {
  await withManager(async (manager) => {
    const device = await manager.addDevice({
      hostname: "tv.local",
      nickname: "tv",
      platform: "remote",
      capabilities: ["tv"],
    });
    
    const model = await manager.addModel({
      provider: "anthropic",
      model: "claude-3-haiku",
      nickname: "claude-haiku",
      capabilities: ["code", "chat"],
    });
    
    await manager.setPreferredDevice("tv.local");
    await manager.setPreferredModel("anthropic/claude-3-haiku");
    
    const result = await manager.resolveForSurface("tv-apple");
    
    assert.ok(result);
    assert.equal(result.device.id, device.id);
    assert.equal(result.model.id, model.id);
  });
});

test("cleanup: destroy clears all caches", async () => {
  await withManager(async (manager) => {
    await manager.addDevice({ hostname: "d1.local", nickname: "dev1" });
    await manager.addModel({ provider: "openai", model: "gpt-4", nickname: "gpt4" });
    
    const devicesBefore = await manager.listDevices();
    const modelsBefore = await manager.listModels();
    assert.ok(devicesBefore.length > 0);
    assert.ok(modelsBefore.length > 0);
    
    destroyDeviceModelManager();
    
    const devicesAfter = await manager.listDevices();
    const modelsAfter = await manager.listModels();
    assert.equal(devicesAfter.length, 0);
    assert.equal(modelsAfter.length, 0);
  });
});