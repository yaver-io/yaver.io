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

type Pane = "forms" | "newsletter" | "jobs" | "pdf" | "oauth";

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

  // PDF state — tiny composer that renders the agent's PDF
  // endpoint with ad-hoc HTML and hands back a data URL so the
  // mobile layer can preview inline.
  const [pdfHtml, setPdfHtml] = useState("<h1>Invoice #001</h1><p>Thanks for your business.</p>");
  const [pdfDataUrl, setPdfDataUrl] = useState<string | null>(null);

  // OAuth provider state
  const [oauthClients, setOauthClients] = useState<any[]>([]);
  const [oauthUsers, setOauthUsers] = useState<any[]>([]);
  const [showNewClient, setShowNewClient] = useState(false);
  const [newClientName, setNewClientName] = useState("");
  const [newClientRedirect, setNewClientRedirect] = useState("");
  const [newClientSecret, setNewClientSecret] = useState<string | null>(null);
  const [showNewUser, setShowNewUser] = useState(false);
  const [newUserEmail, setNewUserEmail] = useState("");
  const [newUserPass, setNewUserPass] = useState("");

  // Newsletter compose-from-git state
  const [showCompose, setShowCompose] = useState(false);
  const [composeRepo, setComposeRepo] = useState("");
  const [composeDays, setComposeDays] = useState("7");

  const load = useCallback(async () => {
    if (!connected) return;
    setLoading(true);
    try {
      if (pane === "forms") {
        setForms(await quicClient.formsList());
      } else if (pane === "newsletter") {
        setSubs(await quicClient.newsletterSubscribers());
        setCampaigns(await quicClient.newsletterCampaigns());
      } else if (pane === "jobs") {
        const data = await quicClient.jobsList();
        setQueue(data?.queue ?? []);
        setDlq(data?.dlq ?? []);
      } else if (pane === "oauth") {
        setOauthClients(await quicClient.oauthClients());
        setOauthUsers(await quicClient.oauthUsers());
      }
    } finally {
      setLoading(false);
    }
  }, [pane, connected]);

  const renderPdf = useCallback(async () => {
    setLoading(true);
    const url = await quicClient.pdfRender({ html: pdfHtml, printBackground: true });
    setPdfDataUrl(url);
    setLoading(false);
  }, [pdfHtml]);

  const createOauthClient = useCallback(async () => {
    if (!newClientName || !newClientRedirect) return;
    const res = await quicClient.oauthClientCreate({
      name: newClientName,
      redirectUris: newClientRedirect.split(",").map((s) => s.trim()).filter(Boolean),
    });
    if (res) {
      setNewClientSecret(res.client_secret);
      load();
    }
  }, [newClientName, newClientRedirect, load]);

  const createOauthUser = useCallback(async () => {
    if (!newUserEmail || !newUserPass) return;
    const ok = await quicClient.oauthUserCreate({ email: newUserEmail, password: newUserPass });
    if (ok) {
      setNewUserEmail(""); setNewUserPass(""); setShowNewUser(false);
      load();
    }
  }, [newUserEmail, newUserPass, load]);

  const composeFromGit = useCallback(async () => {
    if (!composeRepo.trim()) return;
    setLoading(true);
    const res = await quicClient.newsletterCompose({
      repo: composeRepo.trim(),
      sinceDays: parseInt(composeDays, 10) || 7,
      includePrs: true,
      includeIssues: true,
      saveDraft: true,
    });
    setLoading(false);
    setShowCompose(false);
    if (res) {
      setCampaignSubject(res.subject);
      setCampaignBody(res.draft);
      load();
    } else {
      Alert.alert("Compose failed", "Check the repo path — run `yaver repo list` to see discovered roots.");
    }
  }, [composeRepo, composeDays, load]);

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

      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        style={[s.tabs, { borderBottomColor: c.border }]}
      >
        {(["forms", "newsletter", "jobs", "pdf", "oauth"] as Pane[]).map((p) => (
          <Pressable key={p} onPress={() => setPane(p)} style={[s.tab, pane === p && { borderBottomColor: c.accent }]}>
            <Text style={{ color: pane === p ? c.accent : c.textMuted, fontWeight: "600", textTransform: "capitalize" }}>{p}</Text>
          </Pressable>
        ))}
      </ScrollView>

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
              <View style={{ flexDirection: "row", gap: 8 }}>
                <Pressable onPress={() => setShowNewCampaign(true)} style={[s.btn, { backgroundColor: c.accent, flex: 1 }]}>
                  <Text style={s.btnText}>+ New campaign</Text>
                </Pressable>
                <Pressable onPress={() => setShowCompose(true)} style={[s.btn, { backgroundColor: c.bgCardElevated, borderWidth: 1, borderColor: c.border, flex: 1 }]}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700", textAlign: "center" }}>From git</Text>
                </Pressable>
              </View>
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
          ) : pane === "jobs" ? (
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
          ) : pane === "pdf" ? (
            <View>
              <Text style={[s.section, { color: c.textPrimary }]}>Render HTML to PDF</Text>
              <TextInput
                value={pdfHtml}
                onChangeText={setPdfHtml}
                multiline
                numberOfLines={8}
                placeholder="<h1>Hello</h1>"
                placeholderTextColor={c.textMuted}
                style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, height: 180, textAlignVertical: "top", fontFamily: "Menlo", fontSize: 12 }]}
              />
              <Pressable onPress={renderPdf} style={[s.btn, { backgroundColor: c.accent, marginTop: 10 }]}>
                <Text style={s.btnText}>Render</Text>
              </Pressable>
              {pdfDataUrl ? (
                <Text style={[s.cardMeta, { color: c.textMuted, marginTop: 12 }]} numberOfLines={1}>
                  PDF ready ({Math.round(pdfDataUrl.length / 1024)} KB) — data URL returned. Tap Render again for a fresh copy.
                </Text>
              ) : null}
            </View>
          ) : (
            <View>
              <Text style={[s.section, { color: c.textPrimary }]}>OAuth Clients</Text>
              <Pressable onPress={() => setShowNewClient(true)} style={[s.btn, { backgroundColor: c.accent, marginTop: 8 }]}>
                <Text style={s.btnText}>+ New client</Text>
              </Pressable>
              {oauthClients.length === 0 ? (
                <Text style={{ color: c.textMuted, marginTop: 12 }}>No clients registered yet. Use these to let apps sign in against your self-hosted OIDC provider.</Text>
              ) : oauthClients.map((cl) => (
                <View key={cl.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{cl.name}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>client_id: {cl.id}</Text>
                  {(cl.redirectUris || []).map((u: string) => (
                    <Text key={u} style={[s.cardMeta, { color: c.textMuted }]} numberOfLines={1}>{u}</Text>
                  ))}
                </View>
              ))}

              <Text style={[s.section, { color: c.textPrimary, marginTop: 16 }]}>Users</Text>
              <Pressable onPress={() => setShowNewUser(true)} style={[s.btn, { backgroundColor: c.accent, marginTop: 8 }]}>
                <Text style={s.btnText}>+ New user</Text>
              </Pressable>
              {oauthUsers.map((u: any) => (
                <View key={u.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{u.email}</Text>
                  {u.name ? <Text style={[s.cardMeta, { color: c.textMuted }]}>{u.name}</Text> : null}
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

      <Modal visible={showCompose} animationType="slide" transparent>
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.cardTitle, { color: c.textPrimary }]}>Compose from git activity</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
              Pulls commits / merged PRs / closed issues from the last N days and drafts a weekly newsletter.
            </Text>
            <TextInput value={composeRepo} onChangeText={setComposeRepo} placeholder="absolute repo path" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={composeDays} onChangeText={setComposeDays} placeholder="days (default 7)" placeholderTextColor={c.textMuted} keyboardType="number-pad" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable onPress={() => setShowCompose(false)} style={[s.btn, { flex: 1, backgroundColor: c.bgCard }]}><Text style={{ color: c.textPrimary, fontWeight: "600", textAlign: "center" }}>Cancel</Text></Pressable>
              <Pressable onPress={composeFromGit} style={[s.btn, { flex: 1, backgroundColor: c.accent }]}><Text style={s.btnText}>Compose</Text></Pressable>
            </View>
          </View>
        </View>
      </Modal>

      <Modal visible={showNewClient} animationType="slide" transparent>
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.cardTitle, { color: c.textPrimary }]}>New OAuth client</Text>
            <TextInput value={newClientName} onChangeText={setNewClientName} placeholder="name" placeholderTextColor={c.textMuted} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={newClientRedirect} onChangeText={setNewClientRedirect} placeholder="redirect URI (comma separated)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            {newClientSecret ? (
              <View style={{ marginTop: 12, padding: 10, backgroundColor: c.bgCard, borderRadius: 8 }}>
                <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700" }}>SAVE THIS — SHOWN ONCE</Text>
                <Text style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 11, marginTop: 4 }} selectable>{newClientSecret}</Text>
              </View>
            ) : null}
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable onPress={() => { setShowNewClient(false); setNewClientSecret(null); setNewClientName(""); setNewClientRedirect(""); }} style={[s.btn, { flex: 1, backgroundColor: c.bgCard }]}><Text style={{ color: c.textPrimary, fontWeight: "600", textAlign: "center" }}>Close</Text></Pressable>
              <Pressable onPress={createOauthClient} style={[s.btn, { flex: 1, backgroundColor: c.accent }]}><Text style={s.btnText}>Create</Text></Pressable>
            </View>
          </View>
        </View>
      </Modal>

      <Modal visible={showNewUser} animationType="slide" transparent>
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.cardTitle, { color: c.textPrimary }]}>New user</Text>
            <TextInput value={newUserEmail} onChangeText={setNewUserEmail} placeholder="email" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={newUserPass} onChangeText={setNewUserPass} placeholder="password" placeholderTextColor={c.textMuted} secureTextEntry style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable onPress={() => setShowNewUser(false)} style={[s.btn, { flex: 1, backgroundColor: c.bgCard }]}><Text style={{ color: c.textPrimary, fontWeight: "600", textAlign: "center" }}>Cancel</Text></Pressable>
              <Pressable onPress={createOauthUser} style={[s.btn, { flex: 1, backgroundColor: c.accent }]}><Text style={s.btnText}>Create</Text></Pressable>
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
