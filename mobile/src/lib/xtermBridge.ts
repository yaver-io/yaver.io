// xtermBridge.ts — PURE protocol helpers between the RN side and the
// xterm.js WebView (XtermView.tsx), and between XtermView and the agent's
// /ws/terminal PTY. RN-free + dependency-free so it's unit-testable.
//
// Wire contract with desktop/agent/console_terminal.go:
//   client → server : binary frame = stdin bytes; text frame
//                      {"resize":{"cols":N,"rows":M}} = resize
//   server → client : binary frame = stdout bytes; text frame
//                      {"type":"terminal_session",...} = meta
//
// Bridge contract with the WebView (postMessage payloads, all JSON):
//   WebView → RN : {t:"ready"} | {t:"d", b:<base64 utf8>} | {t:"r", c, r}
//   RN → WebView : window.__yvWrite("<base64>")  (PTY bytes → term.write)
//                  window.__yvFit()              (refit + emit resize)

// ── base64 (no atob/btoa, no Buffer — RN-safe + deterministic) ─────────────

const B64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

export function bytesToBase64(bytes: Uint8Array): string {
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i];
    const b1 = i + 1 < bytes.length ? bytes[i + 1] : 0;
    const b2 = i + 2 < bytes.length ? bytes[i + 2] : 0;
    out += B64[b0 >> 2];
    out += B64[((b0 & 3) << 4) | (b1 >> 4)];
    out += i + 1 < bytes.length ? B64[((b1 & 15) << 2) | (b2 >> 6)] : "=";
    out += i + 2 < bytes.length ? B64[b2 & 63] : "=";
  }
  return out;
}

const B64_INV = (() => {
  const m = new Int16Array(256).fill(-1);
  for (let i = 0; i < B64.length; i++) m[B64.charCodeAt(i)] = i;
  return m;
})();

export function base64ToBytes(b64: string): Uint8Array {
  let len = b64.length;
  while (len > 0 && (b64[len - 1] === "=" || b64[len - 1] === "\n")) len--;
  const out = new Uint8Array((len * 3) >> 2);
  let o = 0;
  let acc = 0;
  let bits = 0;
  for (let i = 0; i < len; i++) {
    const v = B64_INV[b64.charCodeAt(i)];
    if (v < 0) continue;
    acc = (acc << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out[o++] = (acc >> bits) & 0xff;
    }
  }
  return o === out.length ? out : out.subarray(0, o);
}

// ── server resize frame (text, matches console_terminal.go) ───────────────

export function resizeFrame(cols: number, rows: number): string {
  return JSON.stringify({ resize: { cols: Math.max(1, cols | 0), rows: Math.max(1, rows | 0) } });
}

/** True when a server→client TEXT frame is a control/meta message (session
 *  id, sudo prompt, errors) that must NOT be written to the terminal grid.
 *  Binary frames (pty output) always go straight to term.write. */
export function isTerminalMetaFrame(text: string): boolean {
  const t = text.trim();
  if (!t.startsWith("{")) return false;
  try {
    const o = JSON.parse(t) as Record<string, unknown>;
    return typeof o.type === "string" || typeof o.sessionId === "string";
  } catch {
    return false;
  }
}

// ── WebView → RN message parsing ──────────────────────────────────────────

export type BridgeMessage =
  | { type: "ready" }
  | { type: "data"; bytes: Uint8Array }
  | { type: "resize"; cols: number; rows: number };

/** Parse a postMessage payload from the WebView. Returns null on anything
 *  unrecognized so the caller can ignore noise without throwing. */
export function parseBridgeMessage(raw: string): BridgeMessage | null {
  let o: Record<string, unknown>;
  try {
    o = JSON.parse(raw) as Record<string, unknown>;
  } catch {
    return null;
  }
  switch (o.t) {
    case "ready":
      return { type: "ready" };
    case "d":
      if (typeof o.b === "string") return { type: "data", bytes: base64ToBytes(o.b) };
      return null;
    case "r":
      if (typeof o.c === "number" && typeof o.r === "number") {
        return { type: "resize", cols: o.c, rows: o.r };
      }
      return null;
    default:
      return null;
  }
}

/** Build the injectable JS that writes PTY bytes into the terminal grid. */
export function writeCommand(bytes: Uint8Array): string {
  // trailing `true;` keeps Android's evaluateJavascript from warning on a
  // non-serializable return value.
  return `window.__yvWrite(${JSON.stringify(bytesToBase64(bytes))});true;`;
}
