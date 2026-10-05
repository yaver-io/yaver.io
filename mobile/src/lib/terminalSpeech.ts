const ANSI_ESCAPE = /[\u001b\u009b][[\]()#;?]*(?:(?:(?:[a-zA-Z\d]*(?:;[-a-zA-Z\d/#&.:=?%@~_]+)*)?\u0007)|(?:(?:\d{1,4}(?:[;:]\d{0,4})*)?[\dA-PR-TZcf-nq-uy=><~]))/g;
const CONTROL = /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g;

/** Keep a bounded, readable mirror of PTY output for explicit TTS playback. */
export function appendTerminalSpeechText(previous: string, chunk: string, maxChars = 12_000): string {
  const clean = chunk.replace(ANSI_ESCAPE, "").replace(CONTROL, "").replace(/\r/g, "");
  return `${previous}${clean}`.slice(-maxChars);
}

/** Read the latest useful terminal output, never an unbounded scrollback dump. */
export function terminalSpeechExcerpt(text: string, maxChars = 900): string {
  const lines = text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .filter((line) => !/^[>$#%]\s*$/.test(line));
  if (!lines.length) return "";
  return lines.slice(-8).join(". ").slice(-maxChars);
}
