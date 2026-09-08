/** iOS must use a real application-bundle resource. A Metro asset id looked
 * valid in JS but resolved to no readable model in the shipped TestFlight app.
 * The Xcode Resources phase copies this exact filename into NSBundle.main. */
export function whisperModelOptions(): { filePath: string; isBundleAsset: true } {
  return { filePath: "ggml-whisper-tiny.bin", isBundleAsset: true };
}
