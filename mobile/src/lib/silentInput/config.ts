import AsyncStorage from "@react-native-async-storage/async-storage";
import { DEFAULT_SILENT_INPUT_CONFIG, type SilentInputConfig } from "./types";

const KEY = "experimental.silentInput";

export async function loadSilentInputConfig(): Promise<SilentInputConfig> {
  const raw = await AsyncStorage.getItem(KEY);
  if (!raw) return DEFAULT_SILENT_INPUT_CONFIG;
  try {
    const value = JSON.parse(raw) as Partial<SilentInputConfig>;
    return {
      ...DEFAULT_SILENT_INPUT_CONFIG,
      ...value,
      language: "en",
      autoSend: false,
      sendFullVideo: false,
      mouthCropOnly: true,
      backend: value.backend === "mobile" || value.backend === "cloud" ? value.backend : "user-machine",
    };
  } catch {
    return DEFAULT_SILENT_INPUT_CONFIG;
  }
}

export async function saveSilentInputConfig(config: SilentInputConfig): Promise<void> {
  await AsyncStorage.setItem(KEY, JSON.stringify({
    ...config,
    language: "en",
    autoSend: false,
    sendFullVideo: false,
    mouthCropOnly: true,
  }));
}
