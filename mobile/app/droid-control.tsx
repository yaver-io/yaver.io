// Droid Control — a paired Android device the user can see and drive from their
// phone, mirroring the Interactive Browser screen but riding the agent's new
// /droid/* endpoints instead of /browser/interactive/*. Same transport: rides
// whatever connect() negotiated (direct LAN / Tailscale / tunnel / relay).
//
// Same constraint as browser-interactive.tsx / remote-desktop.tsx: iOS WKWebView
// can't render multipart MJPEG in an <img>, so we POLL single PNG frames
// (/droid/frame) inside a WebView. That WebView captures taps and normalizes
// them to [0,1], which RN maps to device pixels (nx*w, ny*h using the w/h the
// agent reports from /droid/status) and POSTs to /droid/input. Nav buttons,
// text input, scroll and a status line are driven from native RN chrome below.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { WebView, type WebViewMessageEvent } from "react-native-webview";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

// Shape of GET /droid/status. device is null when nothing is paired — handle
// that gracefully rather than crashing.
type DroidStatus = {
  device: string | null;
  w?: number;
  h?: number;
  focus?: string;
};

// Android keycodes used by the nav buttons (sent as {type:"key",keycode}).
const KEY_BACK = 4;
const KEY_HOME = 3;
const KEY_RECENTS = 187;
const KEY_ENTER = 66;

// The in-WebView page: polls PNG frames and captures taps, posting a single
// normalized point back to RN per tap. RN converts [0,1] → device pixels using
// the w/h the agent reported, so this page needn't know them.
function buildHtml(frameUrl: string, intervalMs: number): string {
  return `<!doctype html><html><head>
  <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
  <style>
    html,body{margin:0;height:100%;background:#000;overflow:hidden;touch-action:none}
    .wrap{position:fixed;inset:0;display:flex;align-items:flex-start;justify-content:center}
    img{max-width:100%;max-height:100%;object-fit:contain;display:block;-webkit-user-select:none;user-select:none}
    #cap{position:fixed;inset:0}
    .msg{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;color:#888;font:14px -apple-system,system-ui;text-align:center;padding:24px;pointer-events:none}
  </style></head>
  <body>
    <div class="msg" id="msg">loading device…</div>
    <div class="wrap"><img id="scr"/></div>
    <div id="cap"></div>
    <script>
      var RNWV = window.ReactNativeWebView;
      function post(o){ try{ RNWV && RNWV.postMessage(JSON.stringify(o)); }catch(e){} }
      var base = ${JSON.stringify(frameUrl)};
      var img = document.getElementById('scr');
      var msg = document.getElementById('msg');
      var cap = document.getElementById('cap');
      var fails = 0;
      function tick(){
        var n = new Image();
        n.onload = function(){ img.src = n.src; msg.style.display='none'; fails=0; };
        n.onerror = function(){ fails++; if(fails>3){ msg.style.display='flex'; msg.innerText='no frame yet'; post({k:'err'}); } };
        n.src = base + (base.indexOf('?')>=0?'&':'?') + 't=' + Date.now();
      }
      var timer = setInterval(tick, ${intervalMs});
      tick();

      // RN asks for an immediate refresh after it POSTs an input event.
      document.addEventListener('message', onRefresh);
      window.addEventListener('message', onRefresh);
      function onRefresh(e){ if(String(e.data)==='refresh') tick(); }

      // Normalize a client point to [0,1] over the displayed image rect.
      function norm(cx, cy){
        var r = img.getBoundingClientRect();
        var nx = (cx - r.left) / Math.max(1, r.width);
        var ny = (cy - r.top) / Math.max(1, r.height);
        return { nx: Math.min(1, Math.max(0, nx)), ny: Math.min(1, Math.max(0, ny)) };
      }

      var sx=0, sy=0, snx=0, sny=0, moved=false;
      cap.addEventListener('touchstart', function(e){
        if (e.touches.length === 1){
          var t=e.touches[0]; sx=t.clientX; sy=t.clientY; moved=false;
          var p = norm(sx, sy); snx=p.nx; sny=p.ny;
        }
        e.preventDefault();
      }, {passive:false});
      cap.addEventListener('touchmove', function(e){
        var t=e.touches[0];
        if (Math.abs(t.clientX-sx) > 12 || Math.abs(t.clientY-sy) > 12) moved=true;
        e.preventDefault();
      }, {passive:false});
      cap.addEventListener('touchend', function(e){
        var ct = e.changedTouches[0];
        if (!moved){
          var p = norm(ct.clientX, ct.clientY);
          post({k:'tap', nx:p.nx, ny:p.ny});
        } else {
          // Drag → swipe from start to end (both normalized).
          var p = norm(ct.clientX, ct.clientY);
          post({k:'swipe', nx1:snx, ny1:sny, nx2:p.nx, ny2:p.ny});
        }
        e.preventDefault();
      }, {passive:false});
    </script>
  </body></html>`;
}

export default function DroidControlScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { activeDevice, connectionStatus } = useDevice();

  const connected = Boolean(activeDevice && connectionStatus === "connected");

  const [status, setStatus] = useState<DroidStatus | null>(null);
  const [frameUrl, setFrameUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const webRef = useRef<WebView | null>(null);

  // Build the authed frame URL, mirroring browser-interactive.tsx /
  // quicClient.remoteDesktopFrameUrl(): token as ?token=, relay password as
  // &__rp= (a WebView <img> can't send bearer headers).
  const buildFrameUrl = useCallback((): string => {
    const headers = quicClient.getAuthHeaders();
    const bearer = headers.Authorization || "";
    const token = bearer.replace(/^Bearer\s+/i, "");
    let url = `${quicClient.baseUrl}/droid/frame?token=${encodeURIComponent(token)}`;
    const rp = quicClient.activeRelayPasswordValue;
    if (rp) url += `&__rp=${encodeURIComponent(rp)}`;
    return url;
  }, []);

  // POST an input event to /droid/input, then nudge the WebView to grab a fresh
  // frame shortly after (the device screen mutated on the agent side).
  const sendInput = useCallback(
    async (body: Record<string, unknown>) => {
      if (!activeDevice) return;
      try {
        await quicClient.agentRequest(activeDevice.id, "/droid/input", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        setTimeout(() => {
          webRef.current?.postMessage("refresh");
        }, 600);
      } catch {
        // Fire-and-forget; a dropped event just means the user taps again.
      }
    },
    [activeDevice],
  );

  // Derive the authed frame URL once we're connected.
  useEffect(() => {
    if (connected) setFrameUrl(buildFrameUrl());
  }, [connected, buildFrameUrl]);

  // Poll GET /droid/status for the status line and device dimensions.
  useEffect(() => {
    if (!connected || !activeDevice) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const res = await quicClient.agentRequest(activeDevice.id, "/droid/status");
        if (res.ok && !cancelled) {
          const data = (await res.json()) as DroidStatus;
          setStatus(data);
          if (data.device) setError(null);
        }
      } catch {
        // Status is best-effort.
      }
    };
    void poll();
    const id = setInterval(poll, 4000);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [connected, activeDevice]);

  // Translate a normalized point to device pixels using the reported w/h.
  // Falls back to no-op if we don't yet know the dimensions or no device.
  const onMessage = useCallback(
    (e: WebViewMessageEvent) => {
      let m: any;
      try {
        m = JSON.parse(e.nativeEvent.data);
      } catch {
        return;
      }
      if (m?.k === "err") {
        setError("No frame yet — is a device paired?");
        return;
      }
      const w = status?.w;
      const h = status?.h;
      if (!status?.device || !w || !h) return;
      if (m?.k === "tap") {
        void sendInput({
          type: "tap",
          x: Math.round(m.nx * w),
          y: Math.round(m.ny * h),
        });
        return;
      }
      if (m?.k === "swipe") {
        void sendInput({
          type: "swipe",
          x1: Math.round(m.nx1 * w),
          y1: Math.round(m.ny1 * h),
          x2: Math.round(m.nx2 * w),
          y2: Math.round(m.ny2 * h),
          dur: 200,
        });
      }
    },
    [status, sendInput],
  );

  // Vertical scroll via swipe down the middle of the screen.
  const scroll = useCallback(
    (dir: "up" | "down") => {
      const w = status?.w;
      const h = status?.h;
      if (!status?.device || !w || !h) return;
      const cx = Math.round(w / 2);
      // To scroll content up (see lower content) we swipe finger upward.
      const y1 = dir === "down" ? Math.round(h * 0.7) : Math.round(h * 0.3);
      const y2 = dir === "down" ? Math.round(h * 0.3) : Math.round(h * 0.7);
      void sendInput({ type: "swipe", x1: cx, y1, x2: cx, y2, dur: 250 });
    },
    [status, sendInput],
  );

  const sendKey = useCallback(
    (keycode: number) => {
      if (!status?.device) return;
      void sendInput({ type: "key", keycode });
    },
    [status, sendInput],
  );

  const sendText = useCallback(async () => {
    if (!text.trim() || !status?.device) return;
    setSending(true);
    try {
      await sendInput({ type: "text", text });
      setText("");
    } finally {
      setSending(false);
    }
  }, [text, status, sendInput]);

  const done = useCallback(() => {
    router.back();
  }, [router]);

  const hasDevice = Boolean(status?.device);
  const statusLine = status
    ? status.device
      ? status.focus
        ? `${status.device} · ${status.focus}`
        : status.device
      : "no device paired"
    : "";

  return (
    <View style={[styles.root, { backgroundColor: "#000", paddingTop: insets.top }]}>
      <View style={[styles.header, { borderBottomColor: c.border, backgroundColor: c.bgCard }]}>
        <AppBackButton onPress={done} />
        <View style={{ flex: 1, marginLeft: 8 }}>
          <Text style={[styles.headerTitle, { color: c.textPrimary }]} numberOfLines={1}>
            Droid Control
          </Text>
          {statusLine ? (
            <Text style={{ color: c.textMuted, fontSize: 11 }} numberOfLines={1}>
              {statusLine}
            </Text>
          ) : null}
        </View>
        <Pressable
          onPress={done}
          style={[styles.btn, { borderColor: "rgba(16,185,129,0.5)", backgroundColor: "rgba(16,185,129,0.12)" }]}
        >
          <Text style={{ color: "#6ee7b7", fontSize: 12, fontWeight: "700" }}>Done</Text>
        </Pressable>
      </View>

      <View style={{ flex: 1, backgroundColor: "#000" }}>
        {connected && frameUrl ? (
          <WebView
            ref={webRef}
            key={frameUrl}
            source={{ html: buildHtml(frameUrl, 1500) }}
            style={{ flex: 1, backgroundColor: "#000" }}
            originWhitelist={["*"]}
            scrollEnabled={false}
            javaScriptEnabled
            domStorageEnabled={false}
            allowsInlineMediaPlayback
            mediaPlaybackRequiresUserAction={false}
            androidLayerType="hardware"
            onMessage={onMessage}
          />
        ) : (
          <View style={styles.center}>
            {connected ? (
              <>
                <ActivityIndicator color="#888" />
                <Text style={{ color: "#888", fontSize: 13, marginTop: 12, textAlign: "center", paddingHorizontal: 24 }}>
                  Connecting to device…
                </Text>
              </>
            ) : (
              <Text style={{ color: "#888", fontSize: 13, textAlign: "center", paddingHorizontal: 24 }}>
                Connect to a device first.
              </Text>
            )}
          </View>
        )}
      </View>

      {connected && status && !hasDevice ? (
        <View style={{ backgroundColor: "rgba(245,158,11,0.12)", paddingHorizontal: 14, paddingVertical: 8 }}>
          <Text style={{ color: "#fcd34d", fontSize: 11 }}>
            No Android device paired. Pair one to view and control it here.
          </Text>
        </View>
      ) : error ? (
        <View style={{ backgroundColor: "rgba(245,158,11,0.12)", paddingHorizontal: 14, paddingVertical: 8 }}>
          <Text style={{ color: "#fcd34d", fontSize: 11 }}>{error}</Text>
        </View>
      ) : null}

      <View style={[styles.controls, { borderTopColor: c.border, backgroundColor: c.bgCard, paddingBottom: insets.bottom + 8 }]}>
        <View style={styles.navRow}>
          <Pressable
            onPress={() => sendKey(KEY_BACK)}
            disabled={!hasDevice}
            style={[styles.navBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>◁ Back</Text>
          </Pressable>
          <Pressable
            onPress={() => sendKey(KEY_HOME)}
            disabled={!hasDevice}
            style={[styles.navBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>○ Home</Text>
          </Pressable>
          <Pressable
            onPress={() => sendKey(KEY_RECENTS)}
            disabled={!hasDevice}
            style={[styles.navBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>▢ Recents</Text>
          </Pressable>
        </View>
        <View style={styles.navRow}>
          <Pressable
            onPress={() => scroll("up")}
            disabled={!hasDevice}
            style={[styles.navBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>↑ Scroll up</Text>
          </Pressable>
          <Pressable
            onPress={() => scroll("down")}
            disabled={!hasDevice}
            style={[styles.navBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>↓ Scroll down</Text>
          </Pressable>
          <Pressable
            onPress={() => sendKey(KEY_ENTER)}
            disabled={!hasDevice}
            style={[styles.navBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>⏎ Enter</Text>
          </Pressable>
        </View>
        <View style={styles.inputRow}>
          <TextInput
            value={text}
            onChangeText={setText}
            placeholder="Type text, then Send…"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgCardElevated }]}
            onSubmitEditing={sendText}
            returnKeyType="send"
          />
          <Pressable
            onPress={sendText}
            disabled={sending || !text.trim() || !hasDevice}
            style={[
              styles.sendBtn,
              { backgroundColor: !text.trim() || !hasDevice ? c.bgCardElevated : c.accent },
            ]}
          >
            <Text style={{ color: !text.trim() || !hasDevice ? c.textMuted : "#fff", fontSize: 13, fontWeight: "700" }}>
              Send
            </Text>
          </Pressable>
        </View>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  headerTitle: { fontSize: 15, fontWeight: "700" },
  btn: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6 },
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  controls: {
    borderTopWidth: StyleSheet.hairlineWidth,
    paddingHorizontal: 12,
    paddingTop: 10,
    gap: 8,
  },
  navRow: { flexDirection: "row", gap: 8 },
  navBtn: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 8,
    paddingVertical: 8,
    alignItems: "center",
  },
  inputRow: { flexDirection: "row", gap: 8, alignItems: "center" },
  input: {
    flex: 1,
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 8,
    fontSize: 14,
  },
  sendBtn: { borderRadius: 8, paddingHorizontal: 16, paddingVertical: 10 },
});
