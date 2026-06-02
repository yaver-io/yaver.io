// XtermView.tsx — a true VT terminal grid rendered by xterm.js inside a
// WebView, for the in-app glasses terminal. Replaces the old stripAnsi →
// <Text> scrollback, so full-screen TUIs (Claude Code's boxed UI, tmux pane
// borders + status bar, vim) render faithfully — cursor addressing, colors,
// scroll regions, the alternate-screen buffer.
//
// Fully OFFLINE: xterm.js + addon-fit + css are inlined from the vendored
// bundle (scripts/vendor-xterm.mjs) into a self-contained HTML document, so
// it works on the Beam Pro with no network. No CDN, no remote origin.
//
// Imperative API (via ref):
//   write(bytes)  — feed raw PTY stdout bytes into the grid
//   fit()         — refit to the current size and emit onResize
//   focus()       — focus the hidden textarea (hardware/BT keyboard capture)
// Callbacks:
//   onData(bytes)        — keystrokes from xterm → send to the PTY (binary)
//   onResize(cols, rows) — after a fit → send a {"resize"} frame to the PTY
//   onReady()            — xterm booted; safe to write the replay buffer

import React, {
  forwardRef,
  useCallback,
  useImperativeHandle,
  useMemo,
  useRef,
} from "react";
import { StyleSheet, View } from "react-native";
import { WebView, type WebViewMessageEvent } from "react-native-webview";

import { XTERM_CSS, XTERM_FIT_JS, XTERM_JS } from "./vendor/xtermBundle";
import { parseBridgeMessage, writeCommand } from "../lib/xtermBridge";

export interface XtermHandle {
  write(bytes: Uint8Array): void;
  fit(): void;
  focus(): void;
}

export interface XtermViewProps {
  onData?: (bytes: Uint8Array) => void;
  onResize?: (cols: number, rows: number) => void;
  onReady?: () => void;
  /** Terminal background (defaults to the glasses dark surface). */
  background?: string;
  /** Default foreground. */
  foreground?: string;
  fontSize?: number;
  style?: object;
}

// The bridge script: boot xterm, wire data/resize out, expose write/fit in.
// Kept tiny — the heavy lib is the inlined XTERM_JS above it.
function bridgeScript(opts: {
  background: string;
  foreground: string;
  fontSize: number;
}): string {
  return `
(function () {
  var RNWV = window.ReactNativeWebView;
  function post(o) { try { RNWV.postMessage(JSON.stringify(o)); } catch (e) {} }

  // base64 → Uint8Array (atob exists in WebView WebKit/Chrome).
  function b64ToBytes(b64) {
    var bin = atob(b64), n = bin.length, u = new Uint8Array(n);
    for (var i = 0; i < n; i++) u[i] = bin.charCodeAt(i);
    return u;
  }
  // string (xterm onData, UTF-8) → base64
  var enc = new TextEncoder();
  function bytesToB64(u) {
    var s = ""; for (var i = 0; i < u.length; i++) s += String.fromCharCode(u[i]);
    return btoa(s);
  }

  var term = new Terminal({
    cursorBlink: true,
    fontFamily: 'Menlo, Consolas, "DejaVu Sans Mono", monospace',
    fontSize: ${opts.fontSize},
    scrollback: 5000,
    allowProposedApi: true,
    convertEol: false,
    theme: { background: ${JSON.stringify(opts.background)}, foreground: ${JSON.stringify(opts.foreground)} },
  });
  var fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(document.getElementById("t"));

  function doFit() {
    try { fit.fit(); } catch (e) {}
    post({ t: "r", c: term.cols, r: term.rows });
  }

  // keystrokes (incl. escape sequences) → RN → PTY
  term.onData(function (d) { post({ t: "d", b: bytesToB64(enc.encode(d)) }); });

  // PTY stdout bytes → grid
  window.__yvWrite = function (b64) { term.write(b64ToBytes(b64)); };
  window.__yvFit = doFit;

  window.addEventListener("resize", doFit);
  // initial fit after layout settles, then announce ready
  requestAnimationFrame(function () {
    doFit();
    term.focus();
    post({ t: "ready" });
  });
})();
true;
`;
}

const XtermView = forwardRef<XtermHandle, XtermViewProps>(function XtermView(
  {
    onData,
    onResize,
    onReady,
    background = "#0b0e14",
    foreground = "#d7dce5",
    fontSize = 13,
    style,
  },
  ref,
) {
  const webRef = useRef<WebView | null>(null);
  // Writes that arrive before the bridge posts "ready" (e.g. the PTY replay
  // buffer on a fast attach) are queued, then flushed in order on ready —
  // otherwise window.__yvWrite is undefined and the replay is lost.
  const readyRef = useRef(false);
  const queueRef = useRef<Uint8Array[]>([]);

  const html = useMemo(
    () =>
      `<!doctype html><html><head>` +
      `<meta charset="utf-8" />` +
      `<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no" />` +
      `<style>${XTERM_CSS}\nhtml,body{margin:0;padding:0;height:100%;background:${background};overflow:hidden}` +
      `#t{height:100%;width:100%;padding:4px;box-sizing:border-box}` +
      `.xterm-viewport::-webkit-scrollbar{width:0;height:0}</style></head>` +
      `<body><div id="t"></div>` +
      `<script>${XTERM_JS}</script>` +
      `<script>${XTERM_FIT_JS}</script>` +
      `<script>${bridgeScript({ background, foreground, fontSize })}</script>` +
      `</body></html>`,
    [background, foreground, fontSize],
  );

  useImperativeHandle(
    ref,
    () => ({
      write(bytes: Uint8Array) {
        if (!bytes.length) return;
        if (!readyRef.current) {
          queueRef.current.push(bytes);
          return;
        }
        webRef.current?.injectJavaScript(writeCommand(bytes));
      },
      fit() {
        webRef.current?.injectJavaScript("window.__yvFit && window.__yvFit();true;");
      },
      focus() {
        webRef.current?.injectJavaScript("window.term && window.term.focus();true;");
      },
    }),
    [],
  );

  const onMessage = useCallback(
    (e: WebViewMessageEvent) => {
      const msg = parseBridgeMessage(e.nativeEvent.data);
      if (!msg) return;
      switch (msg.type) {
        case "ready":
          readyRef.current = true;
          // flush any replay/output that arrived before boot, in order
          if (queueRef.current.length) {
            const pending = queueRef.current;
            queueRef.current = [];
            for (const b of pending) webRef.current?.injectJavaScript(writeCommand(b));
          }
          onReady?.();
          break;
        case "data":
          onData?.(msg.bytes);
          break;
        case "resize":
          onResize?.(msg.cols, msg.rows);
          break;
      }
    },
    [onData, onResize, onReady],
  );

  return (
    <View style={[styles.fill, { backgroundColor: background }, style]}>
      <WebView
        ref={webRef}
        source={{ html }}
        originWhitelist={["*"]}
        style={styles.fill}
        onMessage={onMessage}
        // terminal owns its own scrolling + keyboard
        scrollEnabled={false}
        overScrollMode="never"
        keyboardDisplayRequiresUserAction={false}
        hideKeyboardAccessoryView
        automaticallyAdjustContentInsets={false}
        setSupportMultipleWindows={false}
        javaScriptEnabled
        // no remote loads — fully inlined
        cacheEnabled={false}
        androidLayerType="hardware"
      />
    </View>
  );
});

const styles = StyleSheet.create({
  fill: { flex: 1 },
});

export default XtermView;
