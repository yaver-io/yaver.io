// Gateway Gates — resolve the gateway Auth Broker's pending HUMAN GATES from
// your phone. A human gate is where the broker paused its automation for you:
// an OAuth re-consent, a 2FA/OTP, a captcha, a KYC upload, a payment confirm,
// etc. (gateway_broker.go — the resumable PendingGate, milestone M-G3). You
// have no physical access to the box, so for interactive challenges (captcha /
// login / KYC) this screen embeds a LIVE remote view: it polls JPEG frames of
// the box's page inside a WebView and forwards your taps + typed text back to
// the gate's input endpoint — the same frame/input mechanism the co-browse
// (browser-interactive) and remote-desktop screens use.
//
// Approve/deny-style gates (payment, region, push, tap-relay) get a confirm
// dialog with the same risk-aware posture the rest of the app uses for
// actionable operations. Higher-risk gates (payment / KYC) require an extra
// confirmation. The list degrades cleanly when the agent's /gateway/gate route
// isn't live yet (older daemon) — it just shows "no pending gates".

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useRouter } from "expo-router";
import { WebView, type WebViewMessageEvent } from "react-native-webview";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import {
  listGates,
  resolveGate,
  type GatewayGate,
} from "../src/lib/gatewayGateClient";
import {
  ageLabel,
  gateRisk,
  gateRiskColor,
  gateSummary,
  needsRemoteView,
  normalizeStep,
  stepLabel,
} from "../src/lib/gatewayGateFormat";

// The in-WebView page polls JPEG frames of the box's page and forwards taps +
// typed text back to RN. Mirrors browser-interactive.tsx / remote-desktop.tsx
// (iOS WKWebView can't render multipart MJPEG, so we poll single JPEGs).
function buildHtml(frameUrl: string, intervalMs: number): string {
  return `<!doctype html><html><head>
  <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
  <style>
    html,body{margin:0;height:100%;background:#000;overflow:hidden;touch-action:none}
    .wrap{position:fixed;inset:0;display:flex;align-items:center;justify-content:center}
    img{max-width:100%;max-height:100%;object-fit:contain;display:block;-webkit-user-select:none;user-select:none}
    #cap{position:fixed;inset:0}
    #kb{position:fixed;left:-1000px;top:-1000px;opacity:0;width:1px;height:1px}
    .msg{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;color:#888;font:14px -apple-system,system-ui;text-align:center;padding:24px;pointer-events:none}
  </style></head>
  <body>
    <div class="msg" id="msg">loading the page…</div>
    <div class="wrap"><img id="scr"/></div>
    <div id="cap"></div>
    <textarea id="kb" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false"></textarea>
    <script>
      var RNWV = window.ReactNativeWebView;
      function post(o){ try{ RNWV && RNWV.postMessage(JSON.stringify(o)); }catch(e){} }
      var base = ${JSON.stringify(frameUrl)};
      var img = document.getElementById('scr');
      var msg = document.getElementById('msg');
      var cap = document.getElementById('cap');
      var kb = document.getElementById('kb');
      var fails = 0;
      function tick(){
        var n = new Image();
        n.onload = function(){ img.src = n.src; msg.style.display='none'; fails=0; };
        n.onerror = function(){ fails++; if(fails>3){ msg.style.display='flex'; msg.innerText='no frame yet'; } };
        n.src = base + (base.indexOf('?')>=0?'&':'?') + 't=' + Date.now();
      }
      setInterval(tick, ${intervalMs});
      tick();
      document.addEventListener('message', function(e){ if(e.data==='refresh') tick(); });
      window.addEventListener('message', function(e){ if(e.data==='refresh') tick(); });
      function norm(cx, cy){
        var r = img.getBoundingClientRect();
        var nx = (cx - r.left) / Math.max(1, r.width);
        var ny = (cy - r.top) / Math.max(1, r.height);
        return { nx: Math.min(1, Math.max(0, nx)), ny: Math.min(1, Math.max(0, ny)) };
      }
      var sx=0, sy=0, moved=false;
      cap.addEventListener('touchstart', function(e){
        var t=e.touches[0]; sx=t.clientX; sy=t.clientY; moved=false; e.preventDefault();
      }, {passive:false});
      cap.addEventListener('touchmove', function(e){
        var t=e.touches[0];
        if (Math.abs(t.clientX-sx) > 8 || Math.abs(t.clientY-sy) > 8) moved=true;
        e.preventDefault();
      }, {passive:false});
      cap.addEventListener('touchend', function(e){
        var ct = e.changedTouches[0];
        var p = norm(ct.clientX, ct.clientY);
        post({k:'in', ev:{type:'click', nx:p.nx, ny:p.ny}});
        kb.focus();
        e.preventDefault();
      }, {passive:false});
      kb.addEventListener('input', function(){
        var v = kb.value;
        if (v){ post({k:'in', ev:{type:'text', text:v}}); kb.value=''; }
      });
      kb.addEventListener('keydown', function(e){
        var map = {Enter:'enter', Backspace:'backspace', Tab:'tab'};
        var name = map[e.key];
        if (name){ post({k:'in', ev:{type:'key', keys:[name]}}); e.preventDefault(); }
      });
    </script>
  </body></html>`;
}

// Build an authed frame URL for an <img> inside a WebView (which can't send
// bearer headers): token as ?token=, relay password as &__rp=. Same precedence
// as quicClient.remoteDesktopFrameUrl().
function buildFrameUrl(framePath: string): string {
  const headers = quicClient.getAuthHeaders();
  const bearer = headers.Authorization || "";
  const token = bearer.replace(/^Bearer\s+/i, "");
  let url = `${quicClient.baseUrl}${framePath}?token=${encodeURIComponent(token)}`;
  const rp = quicClient.activeRelayPasswordValue;
  if (rp) url += `&__rp=${encodeURIComponent(rp)}`;
  return url;
}

function GateCard({
  gate,
  deviceId,
  onResolved,
  onOpenInteractive,
}: {
  gate: GatewayGate;
  deviceId: string;
  onResolved: () => void;
  onOpenInteractive: (g: GatewayGate) => void;
}) {
  const c = useColors();
  const step = normalizeStep(gate.step);
  const risk = gateRisk(step);
  const riskColor = gateRiskColor(risk);
  const interactive = needsRemoteView(gate);
  const [busy, setBusy] = useState(false);
  const [codeOpen, setCodeOpen] = useState(false);
  const [code, setCode] = useState("");

  const doResolve = useCallback(
    async (action: "approve" | "deny" | "submit", value?: string) => {
      setBusy(true);
      const r = await resolveGate(deviceId, gate.id, action, value);
      setBusy(false);
      if (r.ok) onResolved();
      else Alert.alert("Couldn't resolve", r.error || "Try again.");
    },
    [deviceId, gate.id, onResolved],
  );

  // Risk-aware confirm posture: high-risk gates require an extra confirm.
  const confirmApprove = useCallback(() => {
    const title = step === "two_factor" ? "Submit code" : "Approve";
    if (risk === "high") {
      Alert.alert(
        `${title}?`,
        `${gateSummary(gate)}\n\nThis is a ${risk}-risk action (e.g. payment / identity). Approve it?`,
        [
          { text: "Cancel", style: "cancel" },
          { text: "Approve", style: "destructive", onPress: () => doResolve("approve") },
        ],
      );
    } else {
      Alert.alert(title + "?", gateSummary(gate), [
        { text: "Cancel", style: "cancel" },
        { text: title, onPress: () => doResolve("approve") },
      ]);
    }
  }, [risk, step, gate, doResolve]);

  const confirmDeny = useCallback(() => {
    Alert.alert("Deny?", `${gateSummary(gate)}\n\nThe paused task will be cancelled.`, [
      { text: "Cancel", style: "cancel" },
      { text: "Deny", style: "destructive", onPress: () => doResolve("deny") },
    ]);
  }, [gate, doResolve]);

  return (
    <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginBottom: 6 }}>
        <View style={[s.badge, { backgroundColor: `${riskColor}22` }]}>
          <Text style={{ color: riskColor, fontSize: 10, fontWeight: "700" }}>{risk.toUpperCase()}</Text>
        </View>
        <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>{stepLabel(step)}</Text>
        <Text style={{ color: c.textMuted, fontSize: 11, marginLeft: "auto" }}>{ageLabel(gate.createdAt)}</Text>
      </View>

      <Text style={{ color: c.textPrimary, fontSize: 13 }} numberOfLines={3}>{gateSummary(gate)}</Text>
      {gate.url ? (
        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4, fontFamily: "monospace" }} numberOfLines={1}>{gate.url}</Text>
      ) : null}

      <View style={{ flexDirection: "row", gap: 8, marginTop: 10, flexWrap: "wrap" }}>
        {interactive ? (
          <Pressable onPress={() => onOpenInteractive(gate)} style={[s.btn, { backgroundColor: c.accent }]}>
            <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>Solve on screen</Text>
          </Pressable>
        ) : step === "two_factor" ? (
          <Pressable onPress={() => setCodeOpen((v) => !v)} style={[s.btn, { backgroundColor: c.accent }]} disabled={busy}>
            <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>Enter code</Text>
          </Pressable>
        ) : (
          <Pressable onPress={confirmApprove} style={[s.btn, { backgroundColor: c.accent }]} disabled={busy}>
            {busy ? <ActivityIndicator size="small" color="#fff" /> : <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>Approve</Text>}
          </Pressable>
        )}
        <Pressable onPress={confirmDeny} style={[s.btn, { backgroundColor: "#ef444422", borderWidth: 1, borderColor: "#ef444455" }]} disabled={busy}>
          <Text style={{ color: "#ef4444", fontSize: 12, fontWeight: "700" }}>Deny</Text>
        </Pressable>
      </View>

      {codeOpen && step === "two_factor" ? (
        <View style={{ marginTop: 10, gap: 8 }}>
          <TextInput
            value={code}
            onChangeText={setCode}
            placeholder="One-time code"
            placeholderTextColor={c.textMuted}
            keyboardType="number-pad"
            autoFocus
            style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
          />
          <Pressable
            onPress={() => { setCodeOpen(false); doResolve("submit", code.trim()); }}
            disabled={!code.trim() || busy}
            style={[s.btn, { backgroundColor: c.accent, alignSelf: "flex-start", opacity: !code.trim() || busy ? 0.5 : 1 }]}
          >
            <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>Submit code</Text>
          </Pressable>
        </View>
      ) : null}
    </View>
  );
}

export default function GatewayGatesScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice, devices, selectDevice } = useDevice();

  const [gates, setGates] = useState<GatewayGate[]>([]);
  const [supported, setSupported] = useState(true);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);

  // Interactive-gate live view.
  const [active, setActive] = useState<GatewayGate | null>(null);
  const webRef = useRef<WebView | null>(null);

  const deviceId = activeDevice?.id || "";

  const load = useCallback(async () => {
    if (!deviceId) { setLoading(false); return; }
    setErr(null);
    const r = await listGates(deviceId);
    setGates(r.gates);
    setSupported(r.supported);
    if (r.error) setErr(r.error);
    setLoading(false);
  }, [deviceId]);

  useEffect(() => {
    setLoading(true);
    void load();
    const iv = setInterval(() => { void load(); }, 5000);
    return () => clearInterval(iv);
  }, [load]);

  // Forward a tap / typed text from the embedded view to the gate's input
  // endpoint (or the box's generic /rd/input as a fallback).
  const onMessage = useCallback(
    (e: WebViewMessageEvent) => {
      if (!active || !deviceId) return;
      let m: any;
      try { m = JSON.parse(e.nativeEvent.data); } catch { return; }
      if (m?.k === "in" && m.ev) {
        const path = active.inputPath || "/rd/input";
        const body = active.inputPath ? m.ev : { events: [m.ev] };
        quicClient
          .agentRequest(deviceId, path, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          })
          .then(() => setTimeout(() => webRef.current?.postMessage("refresh"), 600))
          .catch(() => {});
      }
    },
    [active, deviceId],
  );

  const resolveActive = useCallback(
    async (action: "submit" | "deny") => {
      if (!active || !deviceId) return;
      const r = await resolveGate(deviceId, active.id, action);
      if (r.ok) { setActive(null); void load(); }
      else Alert.alert("Couldn't resolve", r.error || "Try again.");
    },
    [active, deviceId, load],
  );

  const framePath = active?.framePath || "/rd/frame.jpg";
  const frameUrl = active ? buildFrameUrl(framePath) : null;
  const deviceLabel = activeDevice ? (activeDevice.alias ? `@${activeDevice.alias}` : activeDevice.name) : "no device";

  return (
    <View style={[s.root, { backgroundColor: c.bg }]}>
      <AppScreenHeader
        title="Gateway Gates"
        onBack={() => router.back()}
        right={
          <Pressable onPress={() => setPickerOpen(true)}>
            <Text style={{ color: c.accent, fontSize: 13, fontWeight: "600" }} numberOfLines={1}>{deviceLabel} ▾</Text>
          </Pressable>
        }
      />

      <ScrollView contentContainerStyle={{ padding: 14, paddingBottom: 40, gap: 12 }}>
        {!deviceId ? (
          <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center", marginTop: 24 }}>
            Connect to a device first to see its pending gates.
          </Text>
        ) : loading ? (
          <View style={{ alignItems: "center", marginTop: 24 }}><ActivityIndicator color={c.accent} /></View>
        ) : (
          <>
            {err ? <Text style={{ color: "#f59e0b", fontSize: 12 }}>{err}</Text> : null}
            {!supported ? (
              <Text style={{ color: c.textMuted, fontSize: 12 }}>
                This box's agent doesn't expose the gateway gate route yet. Update the daemon to manage human gates here.
              </Text>
            ) : gates.length === 0 ? (
              <Text style={{ color: c.textMuted, fontSize: 13, textAlign: "center", marginTop: 24 }}>
                No pending gates. When a gateway task pauses for your approval, it shows up here.
              </Text>
            ) : (
              gates.map((g) => (
                <GateCard
                  key={g.id}
                  gate={g}
                  deviceId={deviceId}
                  onResolved={load}
                  onOpenInteractive={setActive}
                />
              ))
            )}
          </>
        )}
      </ScrollView>

      {/* Interactive live-view modal — solve a captcha / sign in on the box */}
      <Modal visible={!!active} animationType="slide" onRequestClose={() => setActive(null)}>
        <View style={{ flex: 1, backgroundColor: "#000" }}>
          <View style={[s.modalBar, { backgroundColor: c.bgCard, borderBottomColor: c.border }]}>
            <Pressable onPress={() => setActive(null)} style={s.modalBtn}>
              <Text style={{ color: c.textMuted, fontSize: 13 }}>Close</Text>
            </Pressable>
            <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700", flex: 1, textAlign: "center" }} numberOfLines={1}>
              {active ? stepLabel(normalizeStep(active.step)) : ""}
            </Text>
            <Pressable onPress={() => resolveActive("submit")} style={[s.modalBtn, { backgroundColor: `${c.accent}22`, borderRadius: 8 }]}>
              <Text style={{ color: c.accent, fontSize: 13, fontWeight: "700" }}>Done</Text>
            </Pressable>
          </View>
          {frameUrl ? (
            <WebView
              ref={webRef}
              key={frameUrl}
              source={{ html: buildHtml(frameUrl, 1500) }}
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
          ) : null}
          <View style={[s.modalHint, { backgroundColor: c.bgCard }]}>
            <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center" }}>
              Tap and type to solve the challenge on the box. Tap Done when finished, or Deny it from the list.
            </Text>
          </View>
        </View>
      </Modal>

      {/* Device picker */}
      <Modal visible={pickerOpen} transparent animationType="fade" onRequestClose={() => setPickerOpen(false)}>
        <Pressable style={s.backdrop} onPress={() => setPickerOpen(false)}>
          <Pressable style={[s.pickerCard, { backgroundColor: c.bgCard, borderColor: c.border }]} onPress={() => {}}>
            <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginBottom: 10 }}>Choose a device</Text>
            <ScrollView style={{ maxHeight: 360 }}>
              {devices.map((d) => {
                const isActive = activeDevice?.id === d.id;
                return (
                  <Pressable
                    key={d.id}
                    onPress={() => { setPickerOpen(false); selectDevice(d).catch(() => {}); }}
                    style={[s.deviceRow, { borderBottomColor: c.border }]}
                  >
                    <View style={[s.dot, { backgroundColor: d.online ? "#34d399" : "#6b7280" }]} />
                    <Text style={{ color: isActive ? c.accent : c.textPrimary, fontSize: 14, fontWeight: isActive ? "700" : "500", flex: 1 }} numberOfLines={1}>
                      {d.alias ? `@${d.alias}` : d.name}
                    </Text>
                    {isActive ? <Text style={{ color: c.accent, fontSize: 11 }}>active</Text> : null}
                  </Pressable>
                );
              })}
              {devices.length === 0 ? <Text style={{ color: c.textMuted, fontSize: 13 }}>No devices.</Text> : null}
            </ScrollView>
          </Pressable>
        </Pressable>
      </Modal>
    </View>
  );
}

const s = StyleSheet.create({
  root: { flex: 1 },
  card: { borderWidth: 1, borderRadius: 12, padding: 14 },
  badge: { paddingHorizontal: 8, paddingVertical: 3, borderRadius: 6 },
  btn: { borderRadius: 8, paddingHorizontal: 14, paddingVertical: 9, alignItems: "center" },
  input: { borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 9, fontSize: 15 },
  modalBar: { flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 12, paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth, paddingTop: 48 },
  modalBtn: { paddingHorizontal: 12, paddingVertical: 7 },
  modalHint: { paddingHorizontal: 14, paddingVertical: 10 },
  backdrop: { flex: 1, backgroundColor: "rgba(0,0,0,0.6)", alignItems: "center", justifyContent: "center", padding: 24 },
  pickerCard: { width: "100%", maxWidth: 420, borderRadius: 14, borderWidth: 1, padding: 16 },
  deviceRow: { flexDirection: "row", alignItems: "center", gap: 10, paddingVertical: 12, borderBottomWidth: StyleSheet.hairlineWidth },
  dot: { width: 8, height: 8, borderRadius: 4 },
});
