/** Android/default Whisper model locator. Metro packages the .bin asset and
 * whisper.rn resolves the numeric module id to the installed asset path. */
export function whisperModelOptions(): { filePath: string | number; isBundleAsset?: boolean } {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  return { filePath: require("../../assets/models/ggml-whisper-tiny.bin") };
}
