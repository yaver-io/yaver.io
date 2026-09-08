/** Web never calls this (speech.ts rejects on-device STT before requiring
 * whisper.rn), but the sibling keeps Metro from pulling a 31 MB native model
 * into RN-web merely because the shared speech module imports the locator. */
export function whisperModelOptions(): { filePath: string } {
  return { filePath: "" };
}
