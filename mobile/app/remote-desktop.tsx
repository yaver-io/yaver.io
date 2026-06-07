// Mobile Remote Desktop — live screen of any of your devices + optional
// mouse/keyboard control, over the agent's /rd/* endpoints (rides direct LAN /
// Tailscale / tunnel / relay, whichever connect() negotiated).
//
// iOS WKWebView can't render multipart MJPEG in an <img> (a WebKit limitation,
// see CameraStream.tsx), so the viewer POLLS single JPEG snapshots
// (/rd/frame.jpg) inside a WebView. That same WebView also captures touch +
// soft-keyboard input and posts normalized events back to RN, which forwards
// them to /rd/input where the agent injects them via the cross-OS ghost engine.
//
// Control is OFF by default on the box; "Take control" flips the runtime
// consent policy (/rd/policy). A device picker hops between boxes; a fullscreen
// toggle locks landscape + hides chrome for a real desktop-sized canvas.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { WebView, type WebViewMessageEvent } from "react-native-webview";
import * as ScreenOrientation from "expo-screen-orientation";
import { useColors } from "../src/context/ThemeContext";
import { useDevice, type Device } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import { AppBackButton } from "../src/components/AppBackButton";

type RdStatus = {
  supported: boolean;
  viewEnabled: boolean;
  controlEnabled: boolean;
  allowRemoteControl: boolean;
  engineError?: string;
};

// The in-WebView page: polls JPEG snapshots and (when control is on) captures
// touch + keyboard, posting normalized events to RN. Tap→click, drag→drag,
// double-tap→double-click, two-finger vertical pan→scroll, typed text→text,
// Enter/Backspace/Tab→key.
function buildHtml(snapshotUrl: string, control: boolean, intervalMs: number): string {
  return `<!doctype html><html><head>
  <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
  <style>
    html,body{margin:0;height:100%;background:#000;overflow:hidden;touch-action:none}
    .wrap{position:fixed;inset:0;display:flex;align-items:center;justify-content:center}
    img{max-width:100%;max-height:100%;object-fit:contain;display:block;-webkit-user-select:none;user-select:none}
    #cap{position:fixed;inset:0;${control ? "" : "pointer-events:none;"}}
    #kb{position:fixed;left:-1000px;top:-1000px;opacity:0;width:1px;height:1px}
    .msg{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;color:#888;font:14px -apple-system,system-ui;text-align:center;padding:24px;pointer-events:none}
  </style></head>
  <body>
    <div class="msg" id="msg">connecting to screen…</div>
    <div class="wrap"><img id="scr"/></div>
    <div id="cap"></div>
    <textarea id="kb" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false"></textarea>
    <script>
      var RNWV = window.ReactNativeWebView;
      function post(o){ try{ RNWV && RNWV.postMessage(JSON.stringify(o)); }catch(e){} }
      var base = ${JSON.stringify(snapshotUrl)};
      var control = ${control ? "true" : "false"};
      var img = document.getElementById('scr');
      var msg = document.getElementById('msg');
      var cap = document.getElementById('cap');
      var kb = document.getElementById('kb');
      var fails = 0;
      function tick(){
        var n = new Image();
        n.onload = function(){ img.src = n.src; msg.style.display='none'; fails=0; };
        n.onerror = function(){ fails++; if(fails>3){ msg.style.display='flex'; msg.innerText='no screen yet\\n(grant Screen Recording on the box?)'; post({k:'err'}); } };
        n.src = base + (base.indexOf('?')>=0?'&':'?') + 't=' + Date.now();
      }
      setInterval(tick, ${intervalMs});
      tick();

      // Normalize a client point to [0,1] over the displayed image rect.
      function norm(cx, cy){
        var r = img.getBoundingClientRect();
        var nx = (cx - r.left) / Math.max(1, r.width);
        var ny = (cy - r.top) / Math.max(1, r.height);
        return { nx: Math.min(1, Math.max(0, nx)), ny: Math.min(1, Math.max(0, ny)) };
      }

      if (control){
        var sx=0, sy=0, st=0, moved=false, lastTap=0;
        cap.addEventListener('touchstart', function(e){
          if (e.touches.length === 1){
            var t=e.touches[0]; sx=t.clientX; sy=t.clientY; st=Date.now(); moved=false;
          }
          e.preventDefault();
        }, {passive:false});
        cap.addEventListener('touchmove', function(e){
          if (e.touches.length === 2){
            // two-finger vertical pan → scroll
            var t=e.touches[0];
            var dy = t.clientY - sy;
            if (Math.abs(dy) > 24){ post({k:'in', ev:{type:'scroll', dx:0, dy: dy<0 ? -1 : 1}}); sy=t.clientY; }
            e.preventDefault(); return;
          }
          var t=e.touches[0];
          if (Math.abs(t.clientX-sx) > 8 || Math.abs(t.clientY-sy) > 8) moved=true;
          e.preventDefault();
        }, {passive:false});
        cap.addEventListener('touchend', function(e){
          var ct = e.changedTouches[0];
          var p = norm(ct.clientX, ct.clientY);
          if (moved){
            var ps = norm(sx, sy);
            post({k:'in', ev:{type:'drag', nx:ps.nx, ny:ps.ny, tonx:p.nx, tony:p.ny, button:'left'}});
          } else {
            var now=Date.now();
            if (now - lastTap < 300){ post({k:'in', ev:{type:'double', nx:p.nx, ny:p.ny, button:'left'}}); lastTap=0; }
            else { post({k:'in', ev:{type:'click', nx:p.nx, ny:p.ny, button:'left'}}); lastTap=now; }
            // keep the soft keyboard available after a tap
            kb.focus();
          }
          e.preventDefault();
        }, {passive:false});
        // long-press → right click
        var lpTimer=null;
        cap.addEventListener('touchstart', function(e){
          if (e.touches.length!==1) return;
          var t=e.touches[0]; var cx=t.clientX, cy=t.clientY;
          lpTimer = setTimeout(function(){ var p=norm(cx,cy); post({k:'in', ev:{type:'click', nx:p.nx, ny:p.ny, button:'right'}}); moved=true; }, 600);
        }, {passive:false});
        cap.addEventListener('touchend', function(){ if(lpTimer){clearTimeout(lpTimer);lpTimer=null;} });
        cap.addEventListener('touchmove', function(){ if(lpTimer){clearTimeout(lpTimer);lpTimer=null;} });

        // Keyboard: typed chars via 'input'; special keys via 'keydown'.
        kb.addEventListener('input', function(e){
          var v = kb.value;
          if (v){ post({k:'in', ev:{type:'text', text:v}}); kb.value=''; }
        });
        kb.addEventListener('keydown', function(e){
          var map = {Enter:'enter', Backspace:'backspace', Tab:'tab'};
          var name = map[e.key];
          if (name){ post({k:'in', ev:{type:'key', keys:[name]}}); e.preventDefault(); }
        });
      }
    </script>
  </body></html>`;
}

export default function RemoteDesktopScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { activeDevice, devices, selectDevice, connectionStatus } = useDevice();

  const [status, setStatus] = useState<RdStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [snapUrl, setSnapUrl] = useState<string | null>(null);
  const webRef = useRef<WebView | null>(null);

  const connected = Boolean(activeDevice && connectionStatus === "connected");
  const controlActive = Boolean(status?.controlEnabled);

  const refreshStatus = useCallback(async () => {
    if (!activeDevice || !connected) return;
    try {
      const res = await quicClient.agentRequest(activeDevice.id, "/rd/status");
      if (!res.ok) throw new Error(`status ${res.status}`);
      setStatus(await res.json());
      setError(null);
    } catch (e: any) {
      setError(e?.message || "couldn't load Remote Desktop status");
    }
  }, [activeDevice, connected]);

  useEffect(() => {
    if (connected) {
      setSnapUrl(quicClient.remoteDesktopFrameUrl());
      void refreshStatus();
    } else {
      setSnapUrl(null);
    }
  }, [connected, refreshStatus, activeDevice?.id]);

  // Restore portrait on unmount (the screen may have locked landscape).
  useEffect(() => {
    return () => {
      ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP).catch(() => {});
    };
  }, []);

  const toggleControl = useCallback(async () => {
    if (!activeDevice) return;
    setBusy(true);
    try {
      const next = !controlActive;
      const res = await quicClient.agentRequest(activeDevice.id, "/rd/policy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ controlEnabled: next }),
      });
      if (!res.ok) throw new Error(`policy ${res.status}`);
      await refreshStatus();
    } catch (e: any) {
      setError(e?.message || "couldn't toggle control");
    } finally {
      setBusy(false);
    }
  }, [activeDevice, controlActive, refreshStatus]);

  const toggleFullscreen = useCallback(async () => {
    try {
      if (!fullscreen) {
        await ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.LANDSCAPE);
        setFullscreen(true);
      } else {
        await ScreenOrientation.lockAsync(ScreenOrientation.OrientationLock.PORTRAIT_UP);
        setFullscreen(false);
      }
    } catch {
      setFullscreen((f) => !f);
    }
  }, [fullscreen]);

  const onMessage = useCallback((e: WebViewMessageEvent) => {
    let m: any;
    try { m = JSON.parse(e.nativeEvent.data); } catch { return; }
    if (m?.k === "err") {
      setError("No screen yet — the box may need Screen Recording permission.");
      return;
    }
    if (m?.k === "in" && m.ev && activeDevice) {
      // Fire-and-forget; input is idempotent enough that a dropped event is fine.
      quicClient
        .agentRequest(activeDevice.id, "/rd/input", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ events: [m.ev] }),
        })
        .then((res) => {
          if (res.status === 403) setStatus((s) => (s ? { ...s, controlEnabled: false } : s));
        })
        .catch(() => {});
    }
  }, [activeDevice]);

  const pickDevice = useCallback((d: Device) => {
    setPickerOpen(false);
    selectDevice(d).catch(() => {});
  }, [selectDevice]);

  const deviceLabel = activeDevice ? (activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name) : "no device";

  return (
    <View style={[styles.root, { backgroundColor: "#000", paddingTop: fullscreen ? 0 : insets.top }]}>
      {!fullscreen ? (
        <View style={[styles.header, { borderBottomColor: c.border, backgroundColor: c.bgCard }]}>
          <AppBackButton onPress={() => router.back()} />
          <Pressable style={{ flex: 1, marginLeft: 8 }} onPress={() => setPickerOpen(true)}>
            <Text style={[styles.headerTitle, { color: c.textPrimary }]} numberOfLines={1}>
              Desktop · {deviceLabel} {"▾"}
            </Text>
          </Pressable>
          <Pressable onPress={toggleControl} disabled={busy || !connected} style={[styles.btn, { borderColor: controlActive ? "rgba(245,158,11,0.5)" : "rgba(16,185,129,0.5)", backgroundColor: controlActive ? "rgba(245,158,11,0.12)" : "rgba(16,185,129,0.12)" }]}>
            <Text style={{ color: controlActive ? "#fcd34d" : "#6ee7b7", fontSize: 12, fontWeight: "700" }}>
              {controlActive ? "Stop control" : "Take control"}
            </Text>
          </Pressable>
          <Pressable onPress={toggleFullscreen} style={[styles.btn, { borderColor: c.border }]}>
            <Text style={{ color: c.textMuted, fontSize: 14 }}>⛶</Text>
          </Pressable>
        </View>
      ) : null}

      <View style={{ flex: 1, backgroundColor: "#000" }}>
        {connected && snapUrl ? (
          <WebView
            ref={webRef}
            key={`${snapUrl}|${controlActive ? "ctl" : "view"}`}
            source={{ html: buildHtml(snapUrl, controlActive, 600) }}
            style={{ flex: 1, backgroundColor: "#000" }}
            originWhitelist={["*"]}
            scrollEnabled={false}
            javaScriptEnabled
            domStorageEnabled={false}
            keyboardDisplayRequiresUserAction={false}
            allowsInlineMediaPlayback
            mediaPlaybackRequiresUserAction={false}
            androidLayerType="hardware"
            onMessage={onMessage}
          />
        ) : (
          <View style={styles.center}>
            {connected ? <ActivityIndicator color="#888" /> : (
              <Text style={{ color: "#888", fontSize: 13, textAlign: "center", paddingHorizontal: 24 }}>
                Connect to a device first — open it from Devices and tap Desktop.
              </Text>
            )}
          </View>
        )}

        {fullscreen ? (
          <View style={[styles.fsBar, { top: insets.top + 6 }]}>
            <Pressable onPress={toggleControl} disabled={busy} style={[styles.fsBtn, { backgroundColor: controlActive ? "rgba(245,158,11,0.25)" : "rgba(16,185,129,0.25)" }]}>
              <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>{controlActive ? "Stop" : "Control"}</Text>
            </Pressable>
            <Pressable onPress={toggleFullscreen} style={[styles.fsBtn, { backgroundColor: "rgba(0,0,0,0.5)" }]}>
              <Text style={{ color: "#fff", fontSize: 14 }}>✕</Text>
            </Pressable>
          </View>
        ) : null}
      </View>

      {error && !fullscreen ? (
        <View style={{ backgroundColor: "rgba(245,158,11,0.12)", paddingHorizontal: 14, paddingVertical: 8 }}>
          <Text style={{ color: "#fcd34d", fontSize: 11 }}>{error}</Text>
        </View>
      ) : null}

      <Modal visible={pickerOpen} transparent animationType="fade" onRequestClose={() => setPickerOpen(false)}>
        <Pressable style={styles.modalBackdrop} onPress={() => setPickerOpen(false)}>
          <Pressable style={[styles.modalCard, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={() => {}}>
            <Text style={[styles.modalTitle, { color: c.textPrimary }]}>Choose a device</Text>
            <ScrollView style={{ maxHeight: 360 }}>
              {devices.map((d) => {
                const isActive = activeDevice?.id === d.id;
                return (
                  <Pressable key={d.id} onPress={() => pickDevice(d)} style={[styles.deviceRow, { borderBottomColor: c.border }]}>
                    <View style={[styles.dot, { backgroundColor: d.online ? "#34d399" : "#6b7280" }]} />
                    <Text style={{ color: isActive ? "#f0abfc" : c.textPrimary, fontSize: 14, fontWeight: isActive ? "700" : "500", flex: 1 }} numberOfLines={1}>
                      {d.alias ? `@${d.alias}` : d.name}
                    </Text>
                    {isActive ? <Text style={{ color: "#f0abfc", fontSize: 11 }}>active</Text> : null}
                  </Pressable>
                );
              })}
            </ScrollView>
          </Pressable>
        </Pressable>
      </Modal>
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 8, borderBottomWidth: StyleSheet.hairlineWidth },
  headerTitle: { fontSize: 15, fontWeight: "700" },
  btn: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 6 },
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  fsBar: { position: "absolute", right: 10, flexDirection: "row", gap: 8 },
  fsBtn: { borderRadius: 8, paddingHorizontal: 12, paddingVertical: 8 },
  modalBackdrop: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", alignItems: "center", justifyContent: "center", padding: 24 },
  modalCard: { width: "100%", maxWidth: 420, borderRadius: 14, borderWidth: 1, padding: 16 },
  modalTitle: { fontSize: 16, fontWeight: "700", marginBottom: 10 },
  deviceRow: { flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  dot: { width: 8, height: 8, borderRadius: 4 },
});
