// Interactive Browser — a remote headless browser the user can see and drive
// from their phone, so they can solve a captcha / log in to a site the agent
// is automating. Rides the agent's /browser/interactive/* endpoints over
// whatever transport connect() negotiated (direct LAN / Tailscale / tunnel /
// relay), exactly like the Remote Desktop screen.
//
// Same constraint as remote-desktop.tsx: iOS WKWebView can't render multipart
// MJPEG in an <img>, so we POLL single JPEG frames (/browser/interactive/frame)
// inside a WebView. That WebView also captures taps and normalizes them, which
// RN maps to device pixels (tap/imgW * width) and POSTs as click events to
// /browser/interactive/input. Text, scroll and a status line are driven from
// native RN chrome below the canvas.

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
import { useLocalSearchParams, useRouter } from "expo-router";
import { WebView, type WebViewMessageEvent } from "react-native-webview";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

type PrefillField = { selector: string; value: string };

type StartResponse = {
  session_id: string;
  frame_path: string;
  input_path: string;
  width: number;
  height: number;
};

type BrowserStatus = { url?: string; title?: string };

// The in-WebView page: polls JPEG frames and captures taps, posting a single
// normalized point back to RN per tap. RN converts [0,1] → device pixels using
// the session width/height the agent reported, so this page needn't know them.
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
    <div class="msg" id="msg">loading page…</div>
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

      var sx=0, sy=0, moved=false;
      cap.addEventListener('touchstart', function(e){
        if (e.touches.length === 1){ var t=e.touches[0]; sx=t.clientX; sy=t.clientY; moved=false; }
        e.preventDefault();
      }, {passive:false});
      cap.addEventListener('touchmove', function(e){
        if (e.touches.length >= 2){
          // two-finger vertical pan → scroll
          var t=e.touches[0]; var dy = t.clientY - sy;
          if (Math.abs(dy) > 24){ post({k:'scroll', dy: dy<0 ? 240 : -240}); sy=t.clientY; }
          e.preventDefault(); return;
        }
        var t=e.touches[0];
        if (Math.abs(t.clientX-sx) > 8 || Math.abs(t.clientY-sy) > 8) moved=true;
        e.preventDefault();
      }, {passive:false});
      cap.addEventListener('touchend', function(e){
        var ct = e.changedTouches[0];
        if (!moved){ var p = norm(ct.clientX, ct.clientY); post({k:'click', nx:p.nx, ny:p.ny}); }
        e.preventDefault();
      }, {passive:false});
    </script>
  </body></html>`;
}

export default function BrowserInteractiveScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { activeDevice, connectionStatus } = useDevice();
  const params = useLocalSearchParams<{
    url?: string;
    sessionId?: string;
    profile?: string;
    prefill?: string;
  }>();

  const connected = Boolean(activeDevice && connectionStatus === "connected");

  const [session, setSession] = useState<StartResponse | null>(null);
  const [frameUrl, setFrameUrl] = useState<string | null>(null);
  const [pageStatus, setPageStatus] = useState<BrowserStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [starting, setStarting] = useState(false);
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const webRef = useRef<WebView | null>(null);
  const stoppedRef = useRef(false);

  // Build the authed frame URL for a given frame_path, mirroring
  // quicClient.remoteDesktopFrameUrl(): token as ?token=, relay password as
  // &__rp= (a WebView <img> can't send bearer headers).
  const buildFrameUrl = useCallback((framePath: string): string => {
    const headers = quicClient.getAuthHeaders();
    const bearer = headers.Authorization || "";
    const token = bearer.replace(/^Bearer\s+/i, "");
    let url = `${quicClient.baseUrl}${framePath}?token=${encodeURIComponent(token)}`;
    const rp = quicClient.activeRelayPasswordValue;
    if (rp) url += `&__rp=${encodeURIComponent(rp)}`;
    return url;
  }, []);

  // POST an input event to the active session, then nudge the WebView to grab a
  // fresh frame shortly after (the page mutated on the agent side).
  const sendInput = useCallback(
    async (body: Record<string, unknown>) => {
      if (!activeDevice || !session) return;
      try {
        await quicClient.agentRequest(activeDevice.id, session.input_path, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body),
        });
        setTimeout(() => {
          webRef.current?.postMessage("refresh");
        }, 700);
      } catch {
        // Fire-and-forget; a dropped event just means the user taps again.
      }
    },
    [activeDevice, session],
  );

  // Start a session if we weren't handed one via route params.
  useEffect(() => {
    if (!connected || !activeDevice || session) return;

    // Resume an existing session passed in by the caller.
    if (params.sessionId) {
      const id = params.sessionId;
      setSession({
        session_id: id,
        frame_path: `/browser/interactive/frame/${id}`,
        input_path: `/browser/interactive/input/${id}`,
        width: 1280,
        height: 800,
      });
      return;
    }

    if (!params.url || starting) return;
    let cancelled = false;
    setStarting(true);
    (async () => {
      try {
        let prefill: PrefillField[] | undefined;
        if (params.prefill) {
          try {
            prefill = JSON.parse(params.prefill) as PrefillField[];
          } catch {
            prefill = undefined;
          }
        }
        const reqBody: Record<string, unknown> = { url: params.url };
        if (params.profile) reqBody.profile = params.profile;
        if (prefill) reqBody.prefill = prefill;

        const res = await quicClient.agentRequest(
          activeDevice.id,
          "/browser/interactive/start",
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(reqBody),
          },
        );
        if (!res.ok) throw new Error(`start ${res.status}`);
        const data = (await res.json()) as StartResponse;
        if (!cancelled) {
          setSession(data);
          setError(null);
        }
      } catch (e: any) {
        if (!cancelled) setError(e?.message || "couldn't start interactive browser");
      } finally {
        if (!cancelled) setStarting(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [connected, activeDevice, session, params.sessionId, params.url, params.profile, params.prefill, starting]);

  // Once we have a session, derive the authed frame URL.
  useEffect(() => {
    if (session) setFrameUrl(buildFrameUrl(session.frame_path));
  }, [session, buildFrameUrl]);

  // Poll the page status (url/title) for the status line.
  useEffect(() => {
    if (!connected || !activeDevice || !session) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const res = await quicClient.agentRequest(
          activeDevice.id,
          `/browser/interactive/status/${session.session_id}`,
        );
        if (res.ok && !cancelled) setPageStatus((await res.json()) as BrowserStatus);
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
  }, [connected, activeDevice, session]);

  const stopSession = useCallback(async () => {
    if (!activeDevice || !session || stoppedRef.current) return;
    stoppedRef.current = true;
    try {
      await quicClient.agentRequest(
        activeDevice.id,
        `/browser/interactive/stop/${session.session_id}`,
        { method: "POST" },
      );
    } catch {
      // Best-effort; the agent reaps idle sessions anyway.
    }
  }, [activeDevice, session]);

  // Stop the session when the screen unmounts so we don't leak browsers.
  useEffect(() => {
    return () => {
      void stopSession();
    };
  }, [stopSession]);

  const onMessage = useCallback(
    (e: WebViewMessageEvent) => {
      let m: any;
      try {
        m = JSON.parse(e.nativeEvent.data);
      } catch {
        return;
      }
      if (!session) return;
      if (m?.k === "err") {
        setError("No frame yet — the page may still be loading.");
        return;
      }
      if (m?.k === "click") {
        const x = Math.round(m.nx * session.width);
        const y = Math.round(m.ny * session.height);
        void sendInput({ type: "click", x, y });
        return;
      }
      if (m?.k === "scroll") {
        void sendInput({ type: "scroll", dy: m.dy });
      }
    },
    [session, sendInput],
  );

  const sendText = useCallback(async () => {
    if (!text.trim() || !session) return;
    setSending(true);
    try {
      await sendInput({ type: "key", text });
      setText("");
    } finally {
      setSending(false);
    }
  }, [text, session, sendInput]);

  const done = useCallback(async () => {
    await stopSession();
    router.back();
  }, [stopSession, router]);

  const statusLine =
    pageStatus?.title || pageStatus?.url || (params.url ? String(params.url) : "");

  return (
    <View style={[styles.root, { backgroundColor: "#000", paddingTop: insets.top }]}>
      <View style={[styles.header, { borderBottomColor: c.border, backgroundColor: c.bgCard }]}>
        <AppBackButton onPress={done} />
        <View style={{ flex: 1, marginLeft: 8 }}>
          <Text style={[styles.headerTitle, { color: c.textPrimary }]} numberOfLines={1}>
            Interactive Browser
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
            source={{ html: buildHtml(frameUrl, 2000) }}
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
                  {starting ? "Opening the page…" : "Preparing browser…"}
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

      {error ? (
        <View style={{ backgroundColor: "rgba(245,158,11,0.12)", paddingHorizontal: 14, paddingVertical: 8 }}>
          <Text style={{ color: "#fcd34d", fontSize: 11 }}>{error}</Text>
        </View>
      ) : null}

      <View style={[styles.controls, { borderTopColor: c.border, backgroundColor: c.bgCard, paddingBottom: insets.bottom + 8 }]}>
        <View style={styles.scrollRow}>
          <Pressable
            onPress={() => sendInput({ type: "scroll", dy: -360 })}
            disabled={!session}
            style={[styles.scrollBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>↑ Scroll up</Text>
          </Pressable>
          <Pressable
            onPress={() => sendInput({ type: "scroll", dy: 360 })}
            disabled={!session}
            style={[styles.scrollBtn, { borderColor: c.border }]}
          >
            <Text style={{ color: c.textSecondary, fontSize: 13, fontWeight: "600" }}>↓ Scroll down</Text>
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
            disabled={sending || !text.trim() || !session}
            style={[
              styles.sendBtn,
              { backgroundColor: !text.trim() || !session ? c.bgCardElevated : c.accent },
            ]}
          >
            <Text style={{ color: !text.trim() || !session ? c.textMuted : "#fff", fontSize: 13, fontWeight: "700" }}>
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
  scrollRow: { flexDirection: "row", gap: 8 },
  scrollBtn: {
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
