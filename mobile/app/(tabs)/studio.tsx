import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Modal,
  NativeModules,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { AuthenticatedVideoPlayer } from "../../src/components/AuthenticatedVideoPlayer";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

// studio.tsx — bundles the cosmetic self-hosted features that
// don't fit into the existing Solo Stack screen: screen
// recording (clips), live chat inbox, invoices, affiliates,
// A/B experiments, asciinema. Each is a thin pane inside one
// screen to keep the bottom tab bar uncluttered — everything
// reaches here from the More tab.

type Pane = "clips" | "chat" | "invoices" | "affiliates" | "ab" | "casts";

export default function StudioScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [pane, setPane] = useState<Pane>("clips");
  const [loading, setLoading] = useState(false);

  // Clips
  const [clips, setClips] = useState<any[]>([]);
  const [recording, setRecording] = useState(false);
  const [clipTitle, setClipTitle] = useState("");
  type RecordTarget = "agent" | "mobile" | "both";
  const [recordTarget, setRecordTarget] = useState<RecordTarget>("both");
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [uploadingMobile, setUploadingMobile] = useState(false);
  const [selectedClip, setSelectedClip] = useState<any | null>(null);

  // Chat
  const [conversations, setConversations] = useState<any[]>([]);
  const [activeVid, setActiveVid] = useState<string | null>(null);
  const [chatHistory, setChatHistory] = useState<any[]>([]);
  const [replyText, setReplyText] = useState("");

  // Invoices
  const [invoices, setInvoices] = useState<any[]>([]);
  const [customers, setCustomers] = useState<any[]>([]);

  // Affiliates
  const [affiliates, setAffiliates] = useState<any[]>([]);

  // A/B
  const [experiments, setExperiments] = useState<any[]>([]);
  const [abResults, setAbResults] = useState<Record<string, any>>({});

  // Asciinema
  const [casts, setCasts] = useState<any[]>([]);

  const load = useCallback(async () => {
    if (!connected) return;
    setLoading(true);
    try {
      if (pane === "clips") setClips(await quicClient.clipList());
      else if (pane === "chat") setConversations(await quicClient.chatConversations());
      else if (pane === "invoices") {
        setInvoices(await quicClient.invoicesList());
        setCustomers(await quicClient.customersList());
      } else if (pane === "affiliates") setAffiliates(await quicClient.affiliatesList());
      else if (pane === "ab") setExperiments(await quicClient.abExperiments());
      else if (pane === "casts") setCasts(await quicClient.asciinemaList());
    } finally { setLoading(false); }
  }, [pane, connected]);

  useEffect(() => { load(); }, [load]);

  const startClip = useCallback(async () => {
    const targets: string[] = [];
    const wantAgent = recordTarget === "agent" || recordTarget === "both";
    const wantMobile = recordTarget === "mobile" || recordTarget === "both";
    if (wantAgent) targets.push("agent-screen");
    if (wantMobile) targets.push("mobile-screen");

    // Start mobile screen recording first (native module).
    if (wantMobile) {
      try {
        if (NativeModules.ScreenRecorder) {
          await NativeModules.ScreenRecorder.startRecording();
        } else {
          Alert.alert(
            "On-Device Recording Unavailable",
            "This build can't record this phone's screen (the native recorder isn't available on this platform/build). You can still record the agent's screen — pick \"agent\" as the recording target.",
          );
          return;
        }
      } catch (e: any) {
        Alert.alert("Recording failed", e?.message || "Could not start screen recording.");
        return;
      }
    }

    // Start agent-side recording (creates the session).
    const res = await quicClient.clipStart({ title: clipTitle || "Untitled", targets });
    if (res?.session?.id) {
      setRecording(true);
      setActiveSessionId(res.session.id);
      setClipTitle("");
      const where = wantAgent && wantMobile ? "agent + mobile" : wantAgent ? "agent" : "mobile";
      Alert.alert("Recording", `Capture started on ${where}.`);
    } else {
      // Roll back mobile recording if agent start failed.
      if (wantMobile && NativeModules.ScreenRecorder) {
        try { await NativeModules.ScreenRecorder.stopRecording(); } catch {}
      }
      Alert.alert(
        "Couldn't Start Recording",
        "Yaver couldn't start the recording on the agent. This usually means ffmpeg isn't installed there (run `yaver doctor` on the dev machine) — but it can also be a connection issue, so check your connection and try again.",
      );
    }
  }, [clipTitle, recordTarget]);

  const stopClip = useCallback(async () => {
    const wantAgent = recordTarget === "agent" || recordTarget === "both";
    const wantMobile = recordTarget === "mobile" || recordTarget === "both";

    // Stop agent and mobile in parallel.
    const agentStop = wantAgent ? quicClient.clipStop() : Promise.resolve(null);
    let mobileFilePath: string | null = null;
    if (wantMobile && NativeModules.ScreenRecorder) {
      try {
        mobileFilePath = await NativeModules.ScreenRecorder.stopRecording();
      } catch {}
    }
    const agentRes = await agentStop;
    setRecording(false);

    const sessionId = activeSessionId || agentRes?.session?.id;
    if (!sessionId) {
      load();
      return;
    }

    // Upload mobile screen recording to agent.
    if (mobileFilePath && sessionId) {
      setUploadingMobile(true);
      await quicClient.clipUploadMobileScreen(sessionId, mobileFilePath);
      setUploadingMobile(false);

      // Auto-merge if both streams were recorded.
      if (wantAgent && wantMobile) {
        await quicClient.clipMerge(sessionId);
      }
    }

    setActiveSessionId(null);
    load();
    Alert.alert("Saved", `Clip ${sessionId} ready.`);
  }, [load, recordTarget, activeSessionId]);

  const openChat = useCallback(async (vid: string) => {
    setActiveVid(vid);
    setChatHistory(await quicClient.chatHistory(vid));
  }, []);

  const sendReply = useCallback(async () => {
    if (!activeVid || !replyText.trim()) return;
    const ok = await quicClient.chatReply(activeVid, replyText);
    if (ok) {
      setReplyText("");
      setChatHistory(await quicClient.chatHistory(activeVid));
    }
  }, [activeVid, replyText]);

  return (
    <View style={[s.container, { backgroundColor: c.bg }]}>
      <AppScreenHeader title="Studio" onBack={() => router.navigate("/(tabs)/more" as any)} style={{ paddingTop: insets.top + 12 }} />

      <ScrollView horizontal showsHorizontalScrollIndicator={false} style={[s.tabs, { borderBottomColor: c.border }]}>
        {(["clips", "chat", "invoices", "affiliates", "ab", "casts"] as Pane[]).map((p) => (
          <Pressable key={p} onPress={() => setPane(p)} style={[s.tab, pane === p && { borderBottomColor: c.accent }]}>
            <Text style={{ color: pane === p ? c.accent : c.textMuted, fontWeight: "600", textTransform: "capitalize" }}>
              {p === "ab" ? "A/B" : p === "casts" ? "Casts" : p}
            </Text>
          </Pressable>
        ))}
      </ScrollView>

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} />
      ) : !connected ? (
        <Text style={{ color: c.textMuted, textAlign: "center", marginTop: 40 }}>Not connected.</Text>
      ) : (
        <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 48 }}>
          {pane === "clips" ? (
            <View>
              {!recording && !uploadingMobile ? (
                <>
                  <TextInput value={clipTitle} onChangeText={setClipTitle} placeholder="Recording title" placeholderTextColor={c.textMuted} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
                  {/* Target selector */}
                  <View style={{ flexDirection: "row", marginTop: 10, borderRadius: 9, overflow: "hidden", borderWidth: 1, borderColor: c.border }}>
                    {(["mobile", "agent", "both"] as RecordTarget[]).map((t) => (
                      <Pressable
                        key={t}
                        onPress={() => setRecordTarget(t)}
                        style={{ flex: 1, paddingVertical: 10, alignItems: "center", backgroundColor: recordTarget === t ? c.accent : c.bgCard }}
                      >
                        <Text style={{ color: recordTarget === t ? "#fff" : c.textMuted, fontWeight: "600", fontSize: 13, textTransform: "capitalize" }}>{t === "both" ? "Both" : t === "agent" ? "Agent" : "Mobile"}</Text>
                      </Pressable>
                    ))}
                  </View>
                  <Pressable onPress={startClip} style={[s.btn, { backgroundColor: "#ef4444", marginTop: 10 }]}>
                    <Text style={s.btnText}>{"●"} Record</Text>
                  </Pressable>
                </>
              ) : uploadingMobile ? (
                <View style={{ alignItems: "center", paddingVertical: 20 }}>
                  <ActivityIndicator />
                  <Text style={{ color: c.textMuted, marginTop: 8 }}>Uploading mobile recording...</Text>
                </View>
              ) : (
                <Pressable onPress={stopClip} style={[s.btn, { backgroundColor: "#ef4444" }]}>
                  <Text style={s.btnText}>{"■"} Stop</Text>
                </Pressable>
              )}
              <Text style={[s.section, { color: c.textPrimary, marginTop: 16 }]}>Recent clips</Text>
              {clips.map((cl: any) => {
                const streams = (cl.streams || []) as { kind: string; uploaded: boolean }[];
                const hasMerged = streams.some((st) => st.kind === "merged" && st.uploaded);
                const hasAgent = streams.some((st) => st.kind === "agent-screen" && st.uploaded);
                const hasMobile = streams.some((st) => st.kind === "mobile-screen" && st.uploaded);
                const preferredFile = hasMerged ? "merged.mp4" : hasAgent ? "agent-screen.mp4" : hasMobile ? "mobile-screen.mp4" : null;
                return (
                  <Pressable
                    key={cl.id}
                    onPress={() => preferredFile && setSelectedClip({ ...cl, preferredFile })}
                    style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border, opacity: preferredFile ? 1 : 0.7 }]}
                  >
                    <Text style={[s.cardTitle, { color: c.textPrimary }]}>{cl.title || cl.id}</Text>
                    <Text style={[s.cardMeta, { color: c.textMuted }]}>/clips/{cl.id} · {cl.durationSec || 0}s</Text>
                    <View style={{ flexDirection: "row", marginTop: 6, gap: 6 }}>
                      {hasAgent && <Text style={s.badge}>Agent</Text>}
                      {hasMobile && <Text style={s.badge}>Mobile</Text>}
                      {hasMerged && <Text style={[s.badge, { backgroundColor: "#22c55e" }]}>Merged</Text>}
                    </View>
                  </Pressable>
                );
              })}
            </View>
          ) : pane === "chat" ? (
            <View>
              {activeVid ? (
                <View>
                  <Pressable onPress={() => { setActiveVid(null); setChatHistory([]); }} style={{ marginBottom: 10 }}>
                    <Text style={{ color: c.accent }}>{"\u2039"} Back to conversations</Text>
                  </Pressable>
                  {chatHistory.map((m: any) => (
                    <View key={m.id} style={{ alignSelf: m.from === "owner" ? "flex-end" : "flex-start", backgroundColor: m.from === "owner" ? c.accent : c.bgCard, borderRadius: 10, padding: 10, marginBottom: 6, maxWidth: "80%" }}>
                      <Text style={{ color: m.from === "owner" ? "#fff" : c.textPrimary }}>{m.text}</Text>
                    </View>
                  ))}
                  <View style={{ flexDirection: "row", marginTop: 10 }}>
                    <TextInput value={replyText} onChangeText={setReplyText} placeholder="Reply…" placeholderTextColor={c.textMuted} style={[s.input, { flex: 1, color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, marginTop: 0 }]} />
                    <Pressable onPress={sendReply} style={[s.btn, { backgroundColor: c.accent, marginLeft: 8, paddingHorizontal: 18 }]}>
                      <Text style={s.btnText}>Send</Text>
                    </Pressable>
                  </View>
                </View>
              ) : conversations.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No chats yet. Paste /chat/widget.js into your landing page and visitors will arrive here.</Text>
              ) : conversations.map((conv: any) => (
                <Pressable key={conv.vid} onPress={() => openChat(conv.vid)} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{conv.vid}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]} numberOfLines={1}>{conv.last}</Text>
                </Pressable>
              ))}
            </View>
          ) : pane === "invoices" ? (
            <View>
              <Text style={[s.section, { color: c.textPrimary }]}>Customers ({customers.length})</Text>
              {customers.slice(0, 5).map((cu: any) => (
                <Text key={cu.id} style={[s.cardMeta, { color: c.textMuted }]}>{cu.name} · {cu.email}</Text>
              ))}
              <Text style={[s.section, { color: c.textPrimary, marginTop: 16 }]}>Invoices ({invoices.length})</Text>
              {invoices.map((inv: any) => (
                <View key={inv.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{inv.number}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>{inv.status} · {inv.currency} {inv.total?.toFixed(2)}</Text>
                </View>
              ))}
              <Text style={{ color: c.textMuted, marginTop: 12, fontSize: 12 }}>
                Create from CLI or MCP — /customers then /invoices, then /invoices/:id/payment-link with Stripe or LemonSqueezy API key.
              </Text>
            </View>
          ) : pane === "affiliates" ? (
            <View>
              {affiliates.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No affiliates yet. POST /affiliates to create a partner.</Text>
              ) : affiliates.map((a: any) => (
                <View key={a.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{a.name || a.email}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>?ref={a.code} · {a.commissionPercent}%</Text>
                  <Text style={[s.cardMeta, { color: c.textPrimary }]}>owed: ${a.totalOwed?.toFixed(2)} · paid: ${a.totalPaid?.toFixed(2)}</Text>
                </View>
              ))}
            </View>
          ) : pane === "ab" ? (
            <View>
              {experiments.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No experiments yet. POST /ab/experiments with variants + weights.</Text>
              ) : experiments.map((e: any) => (
                <View key={e.key} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{e.name || e.key}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>{e.variants?.length ?? 0} variants · metric: {e.metric}</Text>
                </View>
              ))}
            </View>
          ) : (
            <View>
              {casts.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No terminal recordings yet. Run `asciinema rec` and POST the file to /asciinema/import, or hit /asciinema/start.</Text>
              ) : casts.map((ct: any) => (
                <View key={ct.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{ct.title || ct.id}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>/asciinema/{ct.id} · {Math.round(ct.duration || 0)}s</Text>
                </View>
              ))}
            </View>
          )}
        </ScrollView>
      )}
      <ClipPreviewModal clip={selectedClip} onClose={() => setSelectedClip(null)} />
    </View>
  );
}

function ClipPreviewModal({ clip, onClose }: { clip: any | null; onClose: () => void }) {
  const c = useColors();
  const req = clip ? quicClient.clipPrivateVideoRequest(clip.id, clip.preferredFile) : null;

  return (
    <Modal visible={!!clip} animationType="slide" onRequestClose={onClose}>
      <View style={[s.modalWrap, { backgroundColor: c.bg }]}>
        <View style={[s.header, { borderBottomColor: c.border, paddingTop: 20 }]}>
          <Pressable onPress={onClose} style={{ paddingVertical: 8 }}>
            <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Done</Text>
          </Pressable>
          <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }} numberOfLines={1}>
            {clip?.title || clip?.id || "Clip"}
          </Text>
          <View style={{ width: 50 }} />
        </View>
        <View style={s.modalBody}>
          {req ? (
            <AuthenticatedVideoPlayer
              uri={req.uri}
              headers={req.headers}
              style={s.previewVideo}
            />
          ) : (
            <Text style={{ color: c.textMuted, textAlign: "center" }}>No playable stream for this clip.</Text>
          )}
        </View>
        {clip ? (
          <View style={s.modalCopy}>
            <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{clip.title || clip.id}</Text>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              Private replay stays on the authenticated agent path. Public share links remain separate under /clips/{clip.id}.
            </Text>
          </View>
        ) : null}
      </View>
    </Modal>
  );
}

const s = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  tabs: { borderBottomWidth: 1, maxHeight: 48, minHeight: 48 },
  tab: { paddingHorizontal: 16, paddingVertical: 14, borderBottomWidth: 2, borderBottomColor: "transparent" },
  section: { fontSize: 15, fontWeight: "700", marginBottom: 8 },
  card: { borderWidth: 1, borderRadius: 10, padding: 14, marginTop: 10 },
  cardTitle: { fontSize: 15, fontWeight: "600" },
  cardMeta: { fontSize: 12, marginTop: 4 },
  btn: { paddingVertical: 12, borderRadius: 9, alignItems: "center" },
  btnText: { color: "#fff", fontWeight: "700" },
  input: { borderWidth: 1, borderRadius: 8, padding: 12, marginTop: 10, fontSize: 15 },
  badge: { fontSize: 10, fontWeight: "700", color: "#fff", backgroundColor: "#334155", paddingHorizontal: 6, paddingVertical: 2, borderRadius: 4, overflow: "hidden" },
  modalWrap: { flex: 1 },
  modalBody: { flex: 1, justifyContent: "center" },
  previewVideo: { width: "100%", aspectRatio: 16 / 9, backgroundColor: "#000" },
  modalCopy: { padding: 16, gap: 6 },
});
