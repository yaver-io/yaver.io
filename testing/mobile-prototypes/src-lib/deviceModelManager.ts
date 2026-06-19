// deviceModelManager.ts — Device and model management with nickname support
//
// Provides:
//   1. Device management (ID, hostname, nickname, platform, online status)
//   2. Model management (ID, provider, nickname, capabilities)
//   3. Per-device model preferences
//   4. Quick switching between devices and models
//   5. MCP-friendly API
//   6. Convex seeding/persistence (with local fallback)
//
// This makes it easy to:
//   - Give your boxes friendly names ("magara", "home-rpi", "work-mac")
//   - Give models friendly names ("gpt-4-turbo", "claude-opus", "local-llama")
//   - Quickly switch: "run on magara with claude-opus"
//   - Call by nickname in MCP tools

import { EventEmitter } from "events";

// ── Types ───────────────────────────────────────────────────────────────

export interface DeviceConfig {
  id: string;                    // Unique ID (UUID or hostname-based)
  hostname: string;               // Machine hostname
  nickname: string;               // User-friendly name
  platform: "local" | "remote" | "managed-cloud";
  online: boolean;
  lastSeen: number;              // Unix timestamp
  capabilities?: string[];       // ["car", "tv", "watch", "phone", "desktop"]
  connectionType?: string;       // "yaver", "ssh", "relay", "tailscale"
  preferredModel?: string;        // Model ID (or nickname)
  metadata?: Record<string, unknown>; // Extra data (OS, CPU, etc.)
}

export interface ModelConfig {
  id: string;                    // Unique model ID (provider/model or UUID)
  provider: "openai" | "anthropic" | "ollama" | "zai" | "custom";
  model: string;                 // Provider-specific model name
  nickname: string;               // User-friendly name
  capabilities?: string[];       // ["code", "chat", "vision", "embed"]
  isLocal: boolean;              // True for Ollama/local models
  apiUrl?: string;                // Custom API URL (for local/custom)
  apiKeyRequired: boolean;        // Whether this model needs an API key
  metadata?: Record<string, unknown>; // Token limits, pricing, etc.
}

export interface DevicePreference {
  deviceId: string;
  preferredModel?: string;
  defaultForInteraction?: "voice" | "chat" | "code";
}

export interface ModelPreference {
  modelId: string;
  preferredForSurface?: RuntimeSurface;
}

export type RuntimeSurface =
  | "mobile-phone"
  | "mobile-tablet"
  | "wearable-watch"
  | "wearable-wear"
  | "car-audio"
  | "car-android-auto"
  | "car-carplay"
  | "tv-living-room"
  | "tv-android"
  | "tv-apple"
  | "mcp"
  | "cli";

// ── Storage Interface (Convex + Local Fallback) ────────────────────────

export interface DeviceModelStorage {
  // Devices
  getDevices(): Promise<DeviceConfig[]>;
  getDevice(id: string): Promise<DeviceConfig | null>;
  setDevice(device: DeviceConfig): Promise<void>;
  deleteDevice(id: string): Promise<void>;
  
  // Models
  getModels(): Promise<ModelConfig[]>;
  getModel(id: string): Promise<ModelConfig | null>;
  setModel(model: ModelConfig): Promise<void>;
  deleteModel(id: string): Promise<void>;
  
  // Preferences
  getDevicePreference(deviceId: string): Promise<DevicePreference | null>;
  setDevicePreference(pref: DevicePreference): Promise<void>;
  getModelPreference(modelId: string): Promise<ModelPreference | null>;
  setModelPreference(pref: ModelPreference): Promise<void>;
  
  // Global defaults
  getPreferredDevice(): Promise<string | null>;
  setPreferredDevice(deviceId: string): Promise<void>;
  getPreferredModel(): Promise<string | null>;
  setPreferredModel(modelId: string): Promise<void>;
}

// ── Local Storage Implementation ─────────────────────────────────────

class LocalStorage implements DeviceModelStorage {
  private DEVICES_KEY = "yaver_devices";
  private MODELS_KEY = "yaver_models";
  private PREFS_KEY = "yaver_prefs";
  
  async getDevices(): Promise<DeviceConfig[]> {
    const data = localStorage.getItem(this.DEVICES_KEY);
    return data ? JSON.parse(data) : [];
  }
  
  async getDevice(id: string): Promise<DeviceConfig | null> {
    const devices = await this.getDevices();
    return devices.find(d => d.id === id) || null;
  }
  
  async setDevice(device: DeviceConfig): Promise<void> {
    const devices = await this.getDevices();
    const existing = devices.findIndex(d => d.id === device.id);
    if (existing >= 0) {
      devices[existing] = { ...devices[existing], ...device, lastSeen: Date.now() };
    } else {
      devices.push({ ...device, lastSeen: Date.now() });
    }
    localStorage.setItem(this.DEVICES_KEY, JSON.stringify(devices));
  }
  
  async deleteDevice(id: string): Promise<void> {
    const devices = await this.getDevices();
    const filtered = devices.filter(d => d.id !== id);
    localStorage.setItem(this.DEVICES_KEY, JSON.stringify(filtered));
  }
  
  async getModels(): Promise<ModelConfig[]> {
    const data = localStorage.getItem(this.MODELS_KEY);
    return data ? JSON.parse(data) : [];
  }
  
  async getModel(id: string): Promise<ModelConfig | null> {
    const models = await this.getModels();
    return models.find(m => m.id === id) || null;
  }
  
  async setModel(model: ModelConfig): Promise<void> {
    const models = await this.getModels();
    const existing = models.findIndex(m => m.id === model.id);
    if (existing >= 0) {
      models[existing] = { ...models[existing], ...model };
    } else {
      models.push(model);
    }
    localStorage.setItem(this.MODELS_KEY, JSON.stringify(models));
  }
  
  async deleteModel(id: string): Promise<void> {
    const models = await this.getModels();
    const filtered = models.filter(m => m.id !== id);
    localStorage.setItem(this.MODELS_KEY, JSON.stringify(filtered));
  }
  
  async getDevicePreference(deviceId: string): Promise<DevicePreference | null> {
    const prefs = this.getPrefs();
    return prefs.devices?.[deviceId] || null;
  }
  
  async setDevicePreference(pref: DevicePreference): Promise<void> {
    const prefs = this.getPrefs();
    if (!prefs.devices) prefs.devices = {};
    prefs.devices[pref.deviceId] = pref;
    localStorage.setItem(this.PREFS_KEY, JSON.stringify(prefs));
  }
  
  async getModelPreference(modelId: string): Promise<ModelPreference | null> {
    const prefs = this.getPrefs();
    return prefs.models?.[modelId] || null;
  }
  
  async setModelPreference(pref: ModelPreference): Promise<void> {
    const prefs = this.getPrefs();
    if (!prefs.models) prefs.models = {};
    prefs.models[pref.modelId] = pref;
    localStorage.setItem(this.PREFS_KEY, JSON.stringify(prefs));
  }
  
  async getPreferredDevice(): Promise<string | null> {
    const prefs = this.getPrefs();
    return prefs.preferredDevice || null;
  }
  
  async setPreferredDevice(deviceId: string): Promise<void> {
    const prefs = this.getPrefs();
    prefs.preferredDevice = deviceId;
    localStorage.setItem(this.PREFS_KEY, JSON.stringify(prefs));
  }
  
  async getPreferredModel(): Promise<string | null> {
    const prefs = this.getPrefs();
    return prefs.preferredModel || null;
  }
  
  async setPreferredModel(modelId: string): Promise<void> {
    const prefs = this.getPrefs();
    prefs.preferredModel = modelId;
    localStorage.setItem(this.PREFS_KEY, JSON.stringify(prefs));
  }
  
  private getPrefs(): any {
    const data = localStorage.getItem(this.PREFS_KEY);
    return data ? JSON.parse(data) : {};
  }
}

// ── DeviceModelManager ─────────────────────────────────────────────────

export interface DeviceModelManager {
  // Device operations
  listDevices(): Promise<DeviceConfig[]>;
  getDevice(id: string): Promise<DeviceConfig | null>;
  getDeviceByNickname(nickname: string): Promise<DeviceConfig | null>;
  addDevice(device: Partial<DeviceConfig>): Promise<DeviceConfig>;
  updateDevice(id: string, updates: Partial<DeviceConfig>): Promise<DeviceConfig>;
  removeDevice(id: string): Promise<void>;
  setDeviceNickname(id: string, nickname: string): Promise<void>;
  markDeviceOnline(id: string): Promise<void>;
  markDeviceOffline(id: string): Promise<void>;
  
  // Model operations
  listModels(): Promise<ModelConfig[]>;
  getModel(id: string): Promise<ModelConfig | null>;
  getModelByNickname(nickname: string): Promise<ModelConfig | null>;
  addModel(model: Partial<ModelConfig>): Promise<ModelConfig>;
  updateModel(id: string, updates: Partial<ModelConfig>): Promise<ModelConfig>;
  removeModel(id: string): Promise<void>;
  setModelNickname(id: string, nickname: string): Promise<void>;
  
  // Preference operations
  getPreferredDevice(): Promise<DeviceConfig | null>;
  setPreferredDevice(id: string): Promise<void>;
  getPreferredModel(): Promise<ModelConfig | null>;
  setPreferredModel(id: string): Promise<void>;
  getDevicePreferredModel(deviceId: string): Promise<ModelConfig | null>;
  setDevicePreferredModel(deviceId: string, modelId: string): Promise<void>;
  
  // Quick resolution helpers
  resolveDevice(input: string): Promise<DeviceConfig | null>;
  resolveModel(input: string, deviceId?: string): Promise<ModelConfig | null>;
  resolveForSurface(surface: RuntimeSurface): Promise<{ device: DeviceConfig; model: ModelConfig } | null>;
  
  // Events
  onDeviceChange(callback: (devices: DeviceConfig[]) => void): () => void;
  onModelChange(callback: (models: ModelConfig[]) => void): () => void;
  
  // Cleanup
  destroy(): void;
}

class DeviceModelManagerImpl implements DeviceModelManager {
  private storage: DeviceModelStorage;
  private emitter = new EventEmitter();
  private deviceCache = new Map<string, DeviceConfig>();
  private modelCache = new Map<string, ModelConfig>();
  private nicknameToDevice = new Map<string, string>();
  private nicknameToModel = new Map<string, string>();
  
  constructor(storage: DeviceModelStorage) {
    this.storage = storage;
    this.initCaches();
  }
  
  private async initCaches() {
    const devices = await this.storage.getDevices();
    const models = await this.storage.getModels();
    
    this.deviceCache.clear();
    this.modelCache.clear();
    this.nicknameToDevice.clear();
    this.nicknameToModel.clear();
    
    for (const device of devices) {
      this.deviceCache.set(device.id, device);
      if (device.nickname) {
        this.nicknameToDevice.set(device.nickname.toLowerCase(), device.id);
      }
    }
    
    for (const model of models) {
      this.modelCache.set(model.id, model);
      if (model.nickname) {
        this.nicknameToModel.set(model.nickname.toLowerCase(), model.id);
      }
    }
  }
  
  // ── Device operations ─────────────────────────────────────────────
  
  async listDevices(): Promise<DeviceConfig[]> {
    return Array.from(this.deviceCache.values());
  }
  
  async getDevice(id: string): Promise<DeviceConfig | null> {
    return this.deviceCache.get(id) || null;
  }
  
  async getDeviceByNickname(nickname: string): Promise<DeviceConfig | null> {
    const id = this.nicknameToDevice.get(nickname.toLowerCase());
    return id ? (this.deviceCache.get(id) || null) : null;
  }
  
  async addDevice(device: Partial<DeviceConfig>): Promise<DeviceConfig> {
    const id = device.id || `device-${Date.now()}-${Math.random().toString(36).slice(2, 11)}`;
    const fullDevice: DeviceConfig = {
      id,
      hostname: device.hostname || id,
      nickname: device.nickname || device.hostname || id,
      platform: device.platform || "remote",
      online: device.online ?? true,
      lastSeen: Date.now(),
      capabilities: device.capabilities || [],
      connectionType: device.connectionType || "yaver",
      ...device,
    };
    
    await this.storage.setDevice(fullDevice);
    this.deviceCache.set(id, fullDevice);
    if (fullDevice.nickname) {
      this.nicknameToDevice.set(fullDevice.nickname.toLowerCase(), id);
    }
    this.emitter.emit("devices", Array.from(this.deviceCache.values()));
    return fullDevice;
  }
  
  async updateDevice(id: string, updates: Partial<DeviceConfig>): Promise<DeviceConfig> {
    const existing = this.deviceCache.get(id);
    if (!existing) {
      throw new Error(`Device ${id} not found`);
    }
    
    const updated = { ...existing, ...updates, lastSeen: Date.now() };
    
    // Update nickname cache
    if (updates.nickname && existing.nickname) {
      this.nicknameToDevice.delete(existing.nickname.toLowerCase());
    }
    if (updated.nickname) {
      this.nicknameToDevice.set(updated.nickname.toLowerCase(), id);
    }
    
    await this.storage.setDevice(updated);
    this.deviceCache.set(id, updated);
    this.emitter.emit("devices", Array.from(this.deviceCache.values()));
    return updated;
  }
  
  async removeDevice(id: string): Promise<void> {
    const device = this.deviceCache.get(id);
    if (!device) return;
    
    await this.storage.deleteDevice(id);
    this.deviceCache.delete(id);
    if (device.nickname) {
      this.nicknameToDevice.delete(device.nickname.toLowerCase());
    }
    this.emitter.emit("devices", Array.from(this.deviceCache.values()));
  }
  
  async setDeviceNickname(id: string, nickname: string): Promise<void> {
    await this.updateDevice(id, { nickname });
  }
  
  async markDeviceOnline(id: string): Promise<void> {
    await this.updateDevice(id, { online: true, lastSeen: Date.now() });
  }
  
  async markDeviceOffline(id: string): Promise<void> {
    await this.updateDevice(id, { online: false, lastSeen: Date.now() });
  }
  
  // ── Model operations ───────────────────────────────────────────────
  
  async listModels(): Promise<ModelConfig[]> {
    return Array.from(this.modelCache.values());
  }
  
  async getModel(id: string): Promise<ModelConfig | null> {
    return this.modelCache.get(id) || null;
  }
  
  async getModelByNickname(nickname: string): Promise<ModelConfig | null> {
    const id = this.nicknameToModel.get(nickname.toLowerCase());
    return id ? (this.modelCache.get(id) || null) : null;
  }
  
  async addModel(model: Partial<ModelConfig>): Promise<ModelConfig> {
    const id = model.id || `${model.provider}/${model.model}`;
    const fullModel: ModelConfig = {
      id,
      provider: model.provider || "custom",
      model: model.model || "unknown",
      nickname: model.nickname || model.model || id,
      capabilities: model.capabilities || [],
      isLocal: model.isLocal ?? false,
      apiUrl: model.apiUrl,
      apiKeyRequired: model.apiKeyRequired ?? false,
      ...model,
    };
    
    await this.storage.setModel(fullModel);
    this.modelCache.set(id, fullModel);
    if (fullModel.nickname) {
      this.nicknameToModel.set(fullModel.nickname.toLowerCase(), id);
    }
    this.emitter.emit("models", Array.from(this.modelCache.values()));
    return fullModel;
  }
  
  async updateModel(id: string, updates: Partial<ModelConfig>): Promise<ModelConfig> {
    const existing = this.modelCache.get(id);
    if (!existing) {
      throw new Error(`Model ${id} not found`);
    }
    
    const updated = { ...existing, ...updates };
    
    // Update nickname cache
    if (updates.nickname && existing.nickname) {
      this.nicknameToModel.delete(existing.nickname.toLowerCase());
    }
    if (updated.nickname) {
      this.nicknameToModel.set(updated.nickname.toLowerCase(), id);
    }
    
    await this.storage.setModel(updated);
    this.modelCache.set(id, updated);
    this.emitter.emit("models", Array.from(this.modelCache.values()));
    return updated;
  }
  
  async removeModel(id: string): Promise<void> {
    const model = this.modelCache.get(id);
    if (!model) return;
    
    await this.storage.deleteModel(id);
    this.modelCache.delete(id);
    if (model.nickname) {
      this.nicknameToModel.delete(model.nickname.toLowerCase());
    }
    this.emitter.emit("models", Array.from(this.modelCache.values()));
  }
  
  async setModelNickname(id: string, nickname: string): Promise<void> {
    await this.updateModel(id, { nickname });
  }
  
  // ── Preference operations ───────────────────────────────────────────
  
  async getPreferredDevice(): Promise<DeviceConfig | null> {
    const preferredId = await this.storage.getPreferredDevice();
    if (preferredId) {
      return this.deviceCache.get(preferredId) || null;
    }
    // Fall back to first online device
    for (const device of this.deviceCache.values()) {
      if (device.online && device.platform !== "local") {
        return device;
      }
    }
    return null;
  }
  
  async setPreferredDevice(id: string): Promise<void> {
    await this.storage.setPreferredDevice(id);
    this.emitter.emit("preferences");
  }
  
  async getPreferredModel(): Promise<ModelConfig | null> {
    const preferredId = await this.storage.getPreferredModel();
    if (preferredId) {
      return this.modelCache.get(preferredId) || null;
    }
    return null;
  }
  
  async setPreferredModel(id: string): Promise<void> {
    await this.storage.setPreferredModel(id);
    this.emitter.emit("preferences");
  }
  
  async getDevicePreferredModel(deviceId: string): Promise<ModelConfig | null> {
    const pref = await this.storage.getDevicePreference(deviceId);
    if (pref?.preferredModel) {
      return this.modelCache.get(pref.preferredModel) || null;
    }
    return await this.getPreferredModel();
  }
  
  async setDevicePreferredModel(deviceId: string, modelId: string): Promise<void> {
    await this.storage.setDevicePreference({ deviceId, preferredModel: modelId });
    this.emitter.emit("preferences");
  }
  
  // ── Quick resolution helpers ────────────────────────────────────────
  
  async resolveDevice(input: string): Promise<DeviceConfig | null> {
    // Try as nickname first
    const byNickname = await this.getDeviceByNickname(input);
    if (byNickname) return byNickname;
    
    // Try as ID
    const byId = this.deviceCache.get(input);
    if (byId) return byId;
    
    // Try as hostname
    for (const device of this.deviceCache.values()) {
      if (device.hostname === input) return device;
    }
    
    return null;
  }
  
  async resolveModel(input: string, deviceId?: string): Promise<ModelConfig | null> {
    // If deviceId provided, try device's preferred model first
    if (deviceId) {
      const devicePref = await this.getDevicePreferredModel(deviceId);
      if (devicePref) return devicePref;
    }
    
    // Try as nickname
    const byNickname = await this.getModelByNickname(input);
    if (byNickname) return byNickname;
    
    // Try as ID
    const byId = this.modelCache.get(input);
    if (byId) return byId;
    
    // Try as provider/model format
    const byProviderModel = this.modelCache.get(input);
    if (byProviderModel) return byProviderModel;
    
    // Try just model name (find first matching)
    for (const model of this.modelCache.values()) {
      if (model.model === input) return model;
    }
    
    return null;
  }
  
  async resolveForSurface(surface: RuntimeSurface): Promise<{ device: DeviceConfig; model: ModelConfig } | null> {
    const device = await this.getPreferredDevice();
    if (!device) return null;
    
    const model = await this.getDevicePreferredModel(device.id);
    if (!model) {
      // Fall back to global preferred model
      const globalPref = await this.getPreferredModel();
      if (!globalPref) return null;
      return { device, model: globalPref };
    }
    
    return { device, model };
  }
  
  // ── Events ────────────────────────────────────────────────────────────
  
  onDeviceChange(callback: (devices: DeviceConfig[]) => void): () => void {
    this.emitter.on("devices", callback);
    return () => this.emitter.removeListener("devices", callback);
  }
  
  onModelChange(callback: (models: ModelConfig[]) => void): () => void {
    this.emitter.on("models", callback);
    return () => this.emitter.removeListener("models", callback);
  }
  
  // ── Cleanup ────────────────────────────────────────────────────────
  
  destroy(): void {
    this.emitter.removeAllListeners();
    this.deviceCache.clear();
    this.modelCache.clear();
    this.nicknameToDevice.clear();
    this.nicknameToModel.clear();
  }
}

// ── Factory ───────────────────────────────────────────────────────────

let managerInstance: DeviceModelManager | null = null;

export function createDeviceModelManager(storage?: DeviceModelStorage): DeviceModelManager {
  if (managerInstance) {
    managerInstance.destroy();
  }
  managerInstance = new DeviceModelManagerImpl(storage || new LocalStorage());
  return managerInstance;
}

export function getDeviceModelManager(): DeviceModelManager | null {
  return managerInstance;
}

export function destroyDeviceModelManager(): void {
  if (managerInstance) {
    managerInstance.destroy();
    managerInstance = null;
  }
}

// Export types
export type {
  DeviceConfig,
  ModelConfig,
  DevicePreference,
  ModelPreference,
  RuntimeSurface,
  DeviceModelStorage,
};