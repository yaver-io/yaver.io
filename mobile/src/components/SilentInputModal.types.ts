import type { ThemeColors } from "../constants/colors";
import type { VSRBackend } from "../lib/silentInput/types";

/** Shared contract for Metro's native and web Silent Input implementations. */
export type SilentInputModalProps = {
  visible: boolean;
  colors: ThemeColors;
  targetDeviceId: string;
  projectName?: string;
  backend: VSRBackend;
  onCancel(): void;
  onTranscription(text: string): void;
  onConfigure?(): void;
};
