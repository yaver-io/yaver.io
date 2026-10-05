import AsyncStorage from "@react-native-async-storage/async-storage";

export type SSHSoftwareKeyboardMode = "auto" | "never" | "always";

const SOFTWARE_KEYBOARD_MODE_KEY = "yaver.ssh.software_keyboard_mode";

export async function getSSHSoftwareKeyboardMode(): Promise<SSHSoftwareKeyboardMode> {
  const value = await AsyncStorage.getItem(SOFTWARE_KEYBOARD_MODE_KEY);
  return value === "never" || value === "always" ? value : "auto";
}

export async function setSSHSoftwareKeyboardMode(mode: SSHSoftwareKeyboardMode): Promise<void> {
  await AsyncStorage.setItem(SOFTWARE_KEYBOARD_MODE_KEY, mode);
}
