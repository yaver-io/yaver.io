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
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { useColors } from "../../src/context/ThemeContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

// solostack.tsx — a single screen that surfaces the "self-hosted
// SaaS replacement" features a solo dev cares about from their
// phone: forms, newsletter, and the background job queue. Each
// pane is a tab inside this one screen because none of them
// justify a dedicated tab in the root navigator.

type Pane = "forms" | "newsletter" | "jobs" | "pdf" | "oauth" | "shortener" | "waitlist" | "docs" | "meetings";

export default function SoloStackScreen() {
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
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

  // Shortener state
  const [shortLinks, setShortLinks] = useState<any[]>([]);
  const [showNewLink, setShowNewLink] = useState(false);
  const [newLinkUrl, setNewLinkUrl] = useState("");
  const [newLinkLabel, setNewLinkLabel] = useState("");

  // Waitlist state
  const [waitlist, setWaitlist] = useState<any[]>([]);
  const [waitlistCount, setWaitlistCount] = useState(0);

  // Docs state
  const [docsTree, setDocsTree] = useState<any[]>([]);
  const [docsConfig, setDocsConfig] = useState<any>(null);
  const [showDocsConfig, setShowDocsConfig] = useState(false);
  const [docsPath, setDocsPath] = useState("");
  const [docsTitle, setDocsTitle] = useState("");

  // Meetings state
  const [eventTypes, setEventTypes] = useState<any[]>([]);
  const [bookings, setBookings] = useState<any[]>([]);

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
      } else if (pane === "shortener") {
        setShortLinks(await quicClient.shortList());
      } else if (pane === "waitlist") {
        const res = await quicClient.waitlistList();
        setWaitlist(res?.entries ?? []);
        setWaitlistCount(res?.total ?? 0);
      } else if (pane === "docs") {
        const res = await quicClient.docsList();
        setDocsTree(res?.tree ?? []);
        setDocsConfig(res?.config ?? null);
      } else if (pane === "meetings") {
        setEventTypes(await quicClient.meetingsList());
        setBookings(await quicClient.meetingBookings());
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
      <AppScreenHeader title="Solo Stack" onBack={() => router.navigate("/(tabs)/more" as any)} style={{ paddingTop: insets.top + 12 }} />

      <View style={[s.chipRow, { borderBottomColor: c.border }]}>
        {(["forms", "newsletter", "jobs", "shortener", "waitlist", "docs", "meetings", "pdf", "oauth"] as Pane[]).map((p) => {
          const active = pane === p;
          return (
            <Pressable
              key={p}
              onPress={() => setPane(p)}
              style={[s.chip, { borderColor: active ? c.accent : c.border, backgroundColor: active ? c.accent + "18" : "transparent" }]}
            >
              <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 12, fontWeight: "600", textTransform: "capitalize" }}>{p}</Text>
            </Pressable>
          );
        })}
      </View>

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} />
      ) : (
        <ScrollView contentContainerStyle={[{ padding: 16, paddingBottom: 32 }, tabletContent]}>
          {!connected ? (
            <Text style={{ color: c.textMuted, marginTop: 12 }}>Not connected to an agent.</Text>
          ) : pane === "forms" ? (
            <View>
              <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 10 }}>
                <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>{forms.length} forms</Text>
                <Pressable onPress={() => setShowNewForm(true)} style={[s.btnSmall, { backgroundColor: c.accent }]}>
                  <Text style={s.btnText}>+ New</Text>
                </Pressable>
              </View>
              {forms.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No forms yet. POST submissions to /forms/:id/submit</Text>
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
          ) : pane === "shortener" ? (
            <View>
              <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 10 }}>
                <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>{shortLinks.length} links</Text>
                <Pressable onPress={() => setShowNewLink(true)} style={[s.btnSmall, { backgroundColor: c.accent }]}>
                  <Text style={s.btnText}>+ New</Text>
                </Pressable>
              </View>
              {shortLinks.length === 0 ? (
                <Text style={{ color: c.textMuted }}>No short links yet. URL pattern: /s/&lt;code&gt;</Text>
              ) : shortLinks.map((l) => (
                <View key={l.code} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>/s/{l.code}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]} numberOfLines={1}>→ {l.url}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>{l.clicks} clicks{l.label ? ` · ${l.label}` : ""}</Text>
                </View>
              ))}
            </View>
          ) : pane === "waitlist" ? (
            <View>
              <Text style={{ color: c.textPrimary, fontSize: 22, fontWeight: "700" }}>{waitlistCount} <Text style={{ fontSize: 13, fontWeight: "500", color: c.textMuted }}>signed up</Text></Text>
              <Text style={[s.cardMeta, { color: c.textMuted, marginTop: 4, marginBottom: 12 }]}>POST /waitlist/join · ?ref=CODE for referral</Text>
              {waitlist.slice(0, 30).map((e) => (
                <View key={e.email} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>#{e.slot} {e.email}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>
                    code: {e.code} · invited {e.invited}
                    {e.referrer ? ` · ref: ${e.referrer}` : ""}
                  </Text>
                </View>
              ))}
              {waitlist.length > 30 ? (
                <Text style={{ color: c.textMuted, textAlign: "center", marginTop: 12 }}>Showing 30 of {waitlist.length}</Text>
              ) : null}
            </View>
          ) : pane === "docs" ? (
            <View>
              <Pressable onPress={() => { setDocsPath(docsConfig?.path ?? ""); setDocsTitle(docsConfig?.title ?? ""); setShowDocsConfig(true); }} style={[s.btn, { backgroundColor: c.accent }]}>
                <Text style={s.btnText}>Configure docs site</Text>
              </Pressable>
              {docsConfig ? (
                <View style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{docsConfig.title || "Docs"}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]} numberOfLines={1}>{docsConfig.path}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>{docsTree.length} page(s)</Text>
                </View>
              ) : null}
              {docsTree.map((p: any) => (
                <View key={p.slug || "index"} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{p.title}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]} numberOfLines={1}>/docs{p.slug ? `/${p.slug}` : ""}</Text>
                </View>
              ))}
            </View>
          ) : pane === "meetings" ? (
            <View>
              <Text style={[s.section, { color: c.textPrimary }]}>Event types</Text>
              {eventTypes.length === 0 ? (
                <Text style={{ color: c.textMuted }}>
                  No event types yet. Use `meeting_create` from MCP or POST /meetings to define one. Uses your Gmail / O365 OAuth creds for real Meet / Teams links.
                </Text>
              ) : eventTypes.map((e: any) => (
                <View key={e.slug} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{e.title}</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>/meet/{e.slug} · {e.durationMin}min · {e.provider}/{e.hosting}</Text>
                </View>
              ))}
              <Text style={[s.section, { color: c.textPrimary, marginTop: 16 }]}>Confirmed bookings ({bookings.length})</Text>
              {bookings.slice(0, 10).map((b: any) => (
                <View key={b.id} style={[s.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                  <Text style={[s.cardTitle, { color: c.textPrimary }]}>{b.name} ({b.email})</Text>
                  <Text style={[s.cardMeta, { color: c.textMuted }]}>{b.eventSlug} · {new Date(b.startsAt).toLocaleString()}</Text>
                  {b.joinUrl ? <Text style={[s.cardMeta, { color: c.accent }]} numberOfLines={1}>{b.joinUrl}</Text> : null}
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

      <Modal visible={showNewLink} animationType="slide" transparent>
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.cardTitle, { color: c.textPrimary }]}>New short link</Text>
            <TextInput value={newLinkUrl} onChangeText={setNewLinkUrl} placeholder="https://..." placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={newLinkLabel} onChangeText={setNewLinkLabel} placeholder="label (optional)" placeholderTextColor={c.textMuted} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable onPress={() => setShowNewLink(false)} style={[s.btn, { flex: 1, backgroundColor: c.bgCard }]}><Text style={{ color: c.textPrimary, fontWeight: "600", textAlign: "center" }}>Cancel</Text></Pressable>
              <Pressable onPress={async () => {
                if (!newLinkUrl.trim()) return;
                await quicClient.shortCreate({ url: newLinkUrl.trim(), label: newLinkLabel.trim() });
                setNewLinkUrl(""); setNewLinkLabel(""); setShowNewLink(false); load();
              }} style={[s.btn, { flex: 1, backgroundColor: c.accent }]}><Text style={s.btnText}>Create</Text></Pressable>
            </View>
          </View>
        </View>
      </Modal>

      <Modal visible={showDocsConfig} animationType="slide" transparent>
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.cardTitle, { color: c.textPrimary }]}>Docs site</Text>
            <TextInput value={docsPath} onChangeText={setDocsPath} placeholder="absolute path to markdown folder" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={docsTitle} onChangeText={setDocsTitle} placeholder="site title" placeholderTextColor={c.textMuted} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <View style={{ flexDirection: "row", gap: 8, marginTop: 12 }}>
              <Pressable onPress={() => setShowDocsConfig(false)} style={[s.btn, { flex: 1, backgroundColor: c.bgCard }]}><Text style={{ color: c.textPrimary, fontWeight: "600", textAlign: "center" }}>Cancel</Text></Pressable>
              <Pressable onPress={async () => {
                if (!docsPath.trim()) return;
                await quicClient.docsConfig({ path: docsPath.trim(), title: docsTitle.trim() });
                setShowDocsConfig(false); load();
              }} style={[s.btn, { flex: 1, backgroundColor: c.accent }]}><Text style={s.btnText}>Save</Text></Pressable>
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
  chipRow: { flexDirection: "row", flexWrap: "wrap", gap: 6, paddingHorizontal: 16, paddingVertical: 10, borderBottomWidth: 1 },
  chip: { borderWidth: 1, borderRadius: 16, paddingHorizontal: 12, paddingVertical: 6 },
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
