// localAgent/assistantNameStore.ts — AsyncStorage persistence adapter for the
// spoken wake name. Kept OUT of the pure assistantName.ts (and the localAgent
// barrel) so the .mts tests and pure logic never import a native dependency.
//
// The voice runtime reads getAssistantName() once at session start — the
// mobile equivalent of the Go agent reading VoiceConfig.AssistantName — and
// feeds it to stripWakeWord()/assistantWakeWords() from ./assistantName.

import AsyncStorage from "@react-native-async-storage/async-storage";
import { DEFAULT_ASSISTANT_NAME, effectiveAssistantName } from "./assistantName";

const ASSISTANT_NAME_KEY = "@yaver/voice/assistant_name";

/** Read the saved spoken name, defaulting to "yaver". */
export async function getAssistantName(): Promise<string> {
  try {
    return effectiveAssistantName(await AsyncStorage.getItem(ASSISTANT_NAME_KEY));
  } catch {
    return DEFAULT_ASSISTANT_NAME;
  }
}

/** Persist the spoken name (stored normalized; "" or "yaver" clears it). */
export async function setAssistantName(name: string): Promise<void> {
  const n = (name ?? "").trim().toLowerCase();
  if (n === "" || n === DEFAULT_ASSISTANT_NAME) {
    await AsyncStorage.removeItem(ASSISTANT_NAME_KEY);
    return;
  }
  await AsyncStorage.setItem(ASSISTANT_NAME_KEY, n);
}
