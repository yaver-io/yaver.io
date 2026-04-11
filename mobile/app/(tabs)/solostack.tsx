import React, { useCallback, useEffect, useState } from "react";
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
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

// solostack.tsx — a single screen that surfaces the "self-hosted
// SaaS replacement" features a solo dev cares about from their
// phone: forms, newsletter, and the background job queue. Each
// pane is a tab inside this one screen because none of them
// justify a dedicated tab in the root navigator.

type Pane = "forms" | "newsletter" | "jobs";

export default function SoloStackScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [pane, setPane] = useState<Pane>("forms");
  const [loading, setLoading] = useState(false);

  // Forms state
  const [forms, setForms] = useState<any[]>([]);
  const [showNewForm, setShowNewForm] = useState(false);
  const [newFormName, setNewFormName] = useState("");
  const [newFormEmail, setNewFormEmail] = useState("");

  // Newsletter state
  const [subs, setSubs] = useState<any>(null);
  const [campaigns, setCampaigns] = useState<any[]>([]);
  const [showNewCampaign, setShowNewCampaign] = useState(false);
  const [campaignSubject, setCampaignSubject] = useState("");
  const [campaignBody, setCampaignBody] = useState("");

  // Jobs state
  const [queue, setQueue] = useState<any[]>([]);
  const [dlq, setDlq] = useState<any[]>([]);

  const load = useCallback(async () => {
    if (!connected) return;
    setLoading(true);
    try {
      if (pane === "forms") {
        setForms(await quicClient.formsList());
      } else if (pane === "newsletter") {
        setSubs(await quicClient.newsletterSubscribers());
        setCampaigns(await quicClient.newsletterCampaigns());
      } else {
        const data = await quicClient.jobsList();
        setQueue(data?.queue ?? []);
        setDlq(data?.dlq ?? []);
      }
    } finally {
      setLoading(false);
    }
  }, [pane, connected]);

  useEffect(() => { load(); }, [load]);

  const createForm = useCallback(async () => {
    if (!newFormName.trim()) return;
    const body: any = { name: newFormName.trim(), rateLimitPerHour: 60 };
    if (newFormEmail.trim()) body.notifyEmail = newFormEmail.trim();
    await quicClient.formCreate(body);
    setNewFormName(""); setNewFormEmail(""); setShowNewForm(false);
    load();
  }, [newFormName, newFormEmail, load]);

  const createCampaign = useCallback(async () => {
    if (!campaignSubject.trim() || !campaignBody.trim()) return;
    await quicClient.newsletterCreate({ subject: campaignSubject, body: campaignBody });
    setCampaignSubject(""); setCampaignBody(""); setShowNewCampaign(false);
    load();
  }, [campaignSubject, campaignBody, load]);

  const sendCampaign = useCallback(async (id: string, subject: string) => {
    Alert.alert(`Send "${subject}"?`, "Goes to every confirmed subscriber. Cannot be undone.", [
      { text: "Cancel" },
      { text: "Send", style: "destructive", onPress: async () => { await quicClient.newsletterSend(id); load(); } },
    ]);
  }, [load]);

  return (
    <View style={[s.container, { backgroundColor: c.bg }]}>
      <View style={[s.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.back()} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Solo Stack</Text>
        <View style={{ width: 50 }} />
      </View>

      <View style={[s.tabs, { borderBottomColor: c.border }]}>
        {(["forms", "newsletter", "jobs"] as Pane[]).map((p) => (
          <Pressable key={p} onPress={() => setPane(p)} style={[s.tab, pane === p && { borderBottomColor: c.accent }]}>
            <Text style={{ color: pane === p ? c.accent : c.textMuted, fontWeight: "600", textTransform: "capitalize" }}>{p}</Text>
          </Pressable>
        ))}
      </View>

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} />
      ) : (
        <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 32 }}>
          {!connected ? (
            <Text style={{ color: c.textMuted, textAlign: "center", marginTop: 40 }}>Not connected to an agent.</Text>
          ) : pane === "forms" ? (
            <View>
              <Pressable onPress={() => setShowNewForm(true)} style={[s.btn, { backgroundColor: c.accent }]}>
                <Text style={s.btnText}>+ New form</Text>
              </Pressable>
              {forms.length === 0 ? (
                <Text style={{ color: c.textMuted, marginTop: 16 }}>No forms yet. Create one and POST submissions to /forms/:id/submit.</Text>
              ) : forms.map((f) => (
                <View key={f.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{f.name}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>id: {f.id}</Text>
                  {f.notifyEmail ? <Text style={[s.cardMeta, { color: c.textMuted }]}>notify: {f.notifyEmail}</Text> : null}
                </View>
              ))}
            </View>
          ) : pane === "newsletter" ? (
            <View>
              {subs ? (
                <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>Subscribers</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>
                    {subs.count.confirmed} confirmed · {subs.count.pending} pending · {subs.count.unsubscribed} unsubscribed
                  </Text>
                </View>
              ) : null}
              <Pressable onPress={() => setShowNewCampaign(true)} style={[s.btn, { backgroundColor: c.accent }]}>
                <Text style={s.btnText}>+ New campaign</Text>
              </Pressable>
              {campaigns.map((c_) => (
                <View key={c_.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{c_.subject}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>{c_.status} · {c_.stats?.delivered ?? 0}/{c_.stats?.total ?? 0} delivered</Text>
                  {c_.status === "draft" ? (
                    <Pressable onPress={() => sendCampaign(c_.id, c_.subject)} style={[s.btnSmall, { backgroundColor: c.accent, marginTop: 8 }]}>
                      <Text style={s.btnText}>Send now</Text>
                    </Pressable>
                  ) : null}
                </View>
              ))}
            </View>
          ) : (
            <View>
              <Text style={[s.section, { color: c.textPrimary }]}>Queue ({queue.length})</Text>
              {queue.map((j) => (
                <View key={j.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{j.handler}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>attempts: {j.attempts}/{j.maxAttempts}</Text>
                </View>
              ))}
              <Text style={[s.section, { color: c.textPrimary, marginTop: 16 }]}>Dead-letter ({dlq.length})</Text>
              {dlq.map((j) => (
                <View key={j.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{j.handler}</Text>
                  <Text style={[s.cardMeta, { color: c.error }]} numberOfLines={2}>{j.lastError}</Text>
                  <Pressable onPress={async () => { await quicClient.jobRetry(j.id); load(); }} style={[s.btnSmall, { backgroundColor: c.accent, marginTop: 8 }]}>
                    <Text style={s.btnText}>Retry</Text>
                  </Pressable>
                </View>
              ))}
            </View>
          )}
        </ScrollView>
      )}

      <Modal visible={showNewForm} animationType="slide" transparent>
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.cardTitle, { color: c.textPrimary }]}>New form</Text>
            <TextInput value={newFormName} onChangeText={setNewFormName} placeholder="name" placeholderTextColor={c.textMuted} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={newFormEmail} onChangeText={setNewFormEmail} placeholder="notify email (optional)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable onPress={() => setShowNewForm(false)} style={[s.btn, { flex: 1, backgroundColor: c.bgCard }]}><Text style={{ color: c.textPrimary, fontWeight: "600", textAlign: "center" }}>Cancel</Text></Pressable>
              <Pressable onPress={createForm} style={[s.btn, { flex: 1, backgroundColor: c.accent }]}><Text style={s.btnText}>Create</Text></Pressable>
            </View>
          </View>
        </View>
      </Modal>

      <Modal visible={showNewCampaign} animationType="slide" transparent>
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.cardTitle, { color: c.textPrimary }]}>New campaign</Text>
            <TextInput value={campaignSubject} onChangeText={setCampaignSubject} placeholder="subject" placeholderTextColor={c.textMuted} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={campaignBody} onChangeText={setCampaignBody} placeholder="body" placeholderTextColor={c.textMuted} multiline numberOfLines={6} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, height: 120, textAlignVertical: "top" }]} />
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable onPress={() => setShowNewCampaign(false)} style={[s.btn, { flex: 1, backgroundColor: c.bgCard }]}><Text style={{ color: c.textPrimary, fontWeight: "600", textAlign: "center" }}>Cancel</Text></Pressable>
              <Pressable onPress={createCampaign} style={[s.btn, { flex: 1, backgroundColor: c.accent }]}><Text style={s.btnText}>Save</Text></Pressable>
            </View>
          </View>
        </View>
      </Modal>
    </View>
  );
}

const s = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  tabs: { flexDirection: "row", borderBottomWidth: 1 },
  tab: { flex: 1, paddingVertical: 14, alignItems: "center", borderBottomWidth: 2, borderBottomColor: "transparent" },
  section: { fontSize: 15, fontWeight: "700", marginBottom: 8 },
  card: { borderWidth: 1, borderRadius: 10, padding: 14, marginTop: 10 },
  cardTitle: { fontSize: 15, fontWeight: "600" },
  cardMeta: { fontSize: 12, marginTop: 4 },
  btn: { paddingVertical: 12, borderRadius: 9, alignItems: "center" },
  btnSmall: { paddingVertical: 8, paddingHorizontal: 12, borderRadius: 8, alignSelf: "flex-start" },
  btnText: { color: "#fff", fontWeight: "700" },
  input: { borderWidth: 1, borderRadius: 8, padding: 12, marginTop: 10, fontSize: 15 },
  modalWrap: { flex: 1, justifyContent: "center", alignItems: "center", backgroundColor: "rgba(0,0,0,0.5)", padding: 20 },
  modalCard: { width: "100%", borderWidth: 1, borderRadius: 12, padding: 18 },
});
