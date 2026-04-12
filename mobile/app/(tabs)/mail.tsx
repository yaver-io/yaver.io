import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Modal,
  Pressable,
  RefreshControl,
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
import type { MailMessage } from "../../src/lib/quic";

// mail.tsx — AI-boosted inbox. Talks to the agent at /mail/*
// so nothing touches Convex. Classification chips separate real
// 1:1 mail from newsletters / marketing / bulk, which Gmail's
// own Promotions tab loses every week.

type Filter = "personal" | "all" | "transactional";

const CHIP_COLORS: Record<string, string> = {
  personal: "#22c55e",
  transactional: "#3b82f6",
  marketing: "#f59e0b",
  bulk: "#ef4444",
};

export default function MailScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [filter, setFilter] = useState<Filter>("personal");
  const [loading, setLoading] = useState(false);
  const [messages, setMessages] = useState<MailMessage[]>([]);
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [selected, setSelected] = useState<MailMessage | null>(null);
  const [draftPrompt, setDraftPrompt] = useState<string | null>(null);
  const [draftText, setDraftText] = useState("");
  const [showConnect, setShowConnect] = useState(false);
  const [showCompose, setShowCompose] = useState(false);
  const [composeTo, setComposeTo] = useState("");
  const [composeSubject, setComposeSubject] = useState("");
  const [composeBody, setComposeBody] = useState("");

  // Setup wizard state — lets the user paste Google OAuth creds
  // without having to SSH into the Mac and run `yaver email setup`.
  const [showSetup, setShowSetup] = useState(false);
  const [setupProvider, setSetupProvider] = useState<"gmail" | "o365">("gmail");
  const [setupClientId, setSetupClientId] = useState("");
  const [setupClientSecret, setSetupClientSecret] = useState("");
  const [setupTenantId, setSetupTenantId] = useState("");
  const [setupRedirectUri, setSetupRedirectUri] = useState("");
  const [setupSaving, setSetupSaving] = useState(false);

  const loadRedirectUri = useCallback(async () => {
    try {
      const baseUrl = (quicClient as any).baseUrl;
      const headers = (quicClient as any).authHeaders;
      const res = await fetch(`${baseUrl}/mail/config`, { headers });
      if (res.ok) {
        const j = await res.json();
        setSetupRedirectUri(j.redirectUri || "");
      }
    } catch {}
  }, []);

  const submitSetup = useCallback(async () => {
    if (!setupClientId.trim() || !setupClientSecret.trim()) {
      Alert.alert("Missing fields", "Enter both Client ID and Client Secret.");
      return;
    }
    setSetupSaving(true);
    try {
      const baseUrl = (quicClient as any).baseUrl;
      const headers = { ...(quicClient as any).authHeaders, "Content-Type": "application/json" };
      const res = await fetch(`${baseUrl}/mail/config`, {
        method: "POST",
        headers,
        body: JSON.stringify({
          provider: setupProvider,
          clientId: setupClientId.trim(),
          clientSecret: setupClientSecret.trim(),
          tenantId: setupTenantId.trim() || undefined,
        }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok) {
        Alert.alert("Save failed", j?.error || `HTTP ${res.status}`);
        return;
      }
      setShowSetup(false);
      setSetupClientId("");
      setSetupClientSecret("");
      setSetupTenantId("");
      // Auto-start OAuth now that credentials are saved
      setTimeout(() => startConnectRef.current(setupProvider), 400);
    } finally {
      setSetupSaving(false);
    }
  }, [setupProvider, setupClientId, setupClientSecret, setupTenantId]);

  const startConnectRef = React.useRef<(provider: "gmail" | "o365") => void>(() => {});

  const load = useCallback(async () => {
    if (!connected) return;
    setLoading(true);
    try {
      const res = await quicClient.mailInbox({
        onlyPersonal: filter === "personal",
        limit: 50,
      });
      if (res) {
        let msgs = res.messages || [];
        if (filter === "transactional") {
          msgs = msgs.filter((m) => m.classification === "transactional");
        }
        setMessages(msgs);
        setCounts(res.counts || {});
      } else {
        setMessages([]);
      }
    } finally {
      setLoading(false);
    }
  }, [filter, connected]);

  useEffect(() => { load(); }, [load]);

  const openDraft = useCallback(async (m: MailMessage) => {
    setSelected(m);
    setDraftPrompt(null);
    setDraftText("");
    // execute=true pipes the prompt into the configured runner
    // (Claude/Codex/Aider/Ollama) and returns the draft text
    // inline — mobile doesn't have to paste the prompt anywhere.
    const res = await quicClient.mailDraft(m.id, undefined, undefined, true);
    if (res) {
      setDraftPrompt(res.prompt);
      setDraftText(res.draft ?? "");
    }
  }, []);

  const sendDraft = useCallback(async () => {
    if (!selected || !draftText.trim()) return;
    const ok = await quicClient.mailSend({
      to: [selected.from],
      subject: selected.subject.startsWith("Re:") ? selected.subject : `Re: ${selected.subject}`,
      body: draftText,
    });
    if (ok) {
      setSelected(null);
      setDraftPrompt(null);
      setDraftText("");
      Alert.alert("Sent");
    } else {
      Alert.alert("Send failed", "Check agent logs.");
    }
  }, [selected, draftText]);

  const startConnect = useCallback(async (provider: "gmail" | "o365") => {
    const res: any = await quicClient.mailConnectStart(provider);
    if (!res || res.error || !res.authUrl) {
      // Not configured yet — open the setup wizard instead of SSH-to-Mac.
      setShowConnect(false);
      setSetupProvider(provider);
      setSetupClientId("");
      setSetupClientSecret("");
      setSetupTenantId("");
      await loadRedirectUri();
      setShowSetup(true);
      return;
    }
    Linking.openURL(res.authUrl);
    setShowConnect(false);
    // Poll onboarding status every 3s for up to 2 min.
    let tries = 0;
    const iv = setInterval(async () => {
      tries++;
      const s = await quicClient.mailConnectStatus(res.sessionId);
      if (s?.ready || tries > 40) {
        clearInterval(iv);
        if (s?.ready) {
          Alert.alert("Connected!", `${provider} is now wired up.`);
          load();
        }
      }
    }, 3000);
  }, [load]);

  // Wire startConnect into the ref so the setup wizard can call it after save.
  useEffect(() => { startConnectRef.current = startConnect; }, [startConnect]);

  const send = useCallback(async () => {
    if (!composeTo.trim() || !composeSubject.trim()) return;
    const ok = await quicClient.mailSend({
      to: composeTo.split(",").map((s) => s.trim()).filter(Boolean),
      subject: composeSubject,
      body: composeBody,
    });
    if (ok) {
      setShowCompose(false);
      setComposeTo(""); setComposeSubject(""); setComposeBody("");
      Alert.alert("Sent");
    } else {
      Alert.alert("Send failed");
    }
  }, [composeTo, composeSubject, composeBody]);

  return (
    <View style={[s.container, { backgroundColor: c.bg }]}>
      <View style={[s.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Mail</Text>
        <Pressable onPress={() => setShowCompose(true)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Compose</Text>
        </Pressable>
      </View>

      <View style={[s.filterRow, { borderBottomColor: c.border }]}>
        {(["personal", "transactional", "all"] as Filter[]).map((f) => (
          <Pressable key={f} onPress={() => setFilter(f)} style={[s.filter, filter === f && { borderBottomColor: c.accent }]}>
            <Text style={{ color: filter === f ? c.accent : c.textMuted, fontWeight: "600", textTransform: "capitalize" }}>{f}</Text>
            {counts[f] != null ? <Text style={{ color: c.textMuted, fontSize: 11 }}>{counts[f]}</Text> : null}
          </Pressable>
        ))}
        <Pressable onPress={() => setShowConnect(true)} style={{ marginLeft: "auto", paddingVertical: 14, paddingHorizontal: 12 }}>
          <Text style={{ color: c.accent, fontSize: 14 }}>Connect</Text>
        </Pressable>
      </View>

      {loading ? (
        <ActivityIndicator style={{ marginTop: 24 }} />
      ) : (
        <ScrollView
          refreshControl={<RefreshControl refreshing={loading} onRefresh={load} />}
          contentContainerStyle={{ paddingBottom: 32 }}
        >
          {!connected ? (
            <Text style={{ color: c.textMuted, padding: 24, textAlign: "center" }}>Not connected to an agent.</Text>
          ) : messages.length === 0 ? (
            <Text style={{ color: c.textMuted, padding: 24, textAlign: "center" }}>
              No mail. Tap Connect to wire Gmail or O365.
            </Text>
          ) : messages.map((m) => (
            <Pressable key={m.id} onPress={() => openDraft(m)} style={[s.row, { borderBottomColor: c.border }]}>
              <View style={{ flex: 1 }}>
                <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                  <View style={[s.chip, { backgroundColor: CHIP_COLORS[m.classification] }]}>
                    <Text style={s.chipText}>{m.classification[0].toUpperCase()}</Text>
                  </View>
                  <Text style={[s.from, { color: c.textPrimary }]} numberOfLines={1}>{m.fromName || m.from}</Text>
                  <Text style={[s.date, { color: c.textMuted }]}>{shortDate(m.date)}</Text>
                </View>
                <Text style={[s.subject, { color: c.textPrimary }]} numberOfLines={1}>{m.subject}</Text>
                <Text style={[s.snippet, { color: c.textMuted }]} numberOfLines={1}>{m.snippet}</Text>
              </View>
            </Pressable>
          ))}
        </ScrollView>
      )}

      {/* Draft modal */}
      <Modal visible={!!selected} animationType="slide">
        <View style={[s.container, { backgroundColor: c.bg, paddingTop: insets.top + 12 }]}>
          <View style={[s.header, { borderBottomColor: c.border }]}>
            <Pressable onPress={() => setSelected(null)} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
            </Pressable>
            <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Reply</Text>
            <Pressable onPress={sendDraft} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Send</Text>
            </Pressable>
          </View>
          {selected ? (
            <ScrollView contentContainerStyle={{ padding: 16 }}>
              <Text style={[s.subject, { color: c.textPrimary, fontSize: 17 }]}>{selected.subject}</Text>
              <Text style={[s.from, { color: c.textMuted, marginTop: 4 }]}>from {selected.fromName || selected.from}</Text>
              <Text style={[s.snippet, { color: c.textPrimary, marginTop: 12 }]} numberOfLines={10}>
                {selected.body || selected.snippet}
              </Text>

              <Text style={[s.section, { color: c.textPrimary, marginTop: 20 }]}>Draft</Text>
              {draftPrompt == null ? (
                <ActivityIndicator />
              ) : (
                <>
                  <TextInput
                    multiline
                    value={draftText}
                    onChangeText={setDraftText}
                    placeholder="Write your reply (or paste the AI-generated draft)..."
                    placeholderTextColor={c.textMuted}
                    style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, height: 200, textAlignVertical: "top" }]}
                  />
                  <Text style={[s.section, { color: c.textPrimary, marginTop: 20 }]}>Prompt (paste into your AI runner)</Text>
                  <View style={[s.promptBox, { backgroundColor: c.bgCard, borderColor: c.border }]}>
                    <Text style={{ color: c.textMuted, fontFamily: "Menlo", fontSize: 11 }}>{draftPrompt}</Text>
                  </View>
                </>
              )}
            </ScrollView>
          ) : null}
        </View>
      </Modal>

      {/* Connect modal */}
      <Modal visible={showConnect} transparent animationType="fade">
        <View style={s.modalWrap}>
          <View style={[s.modalCard, { backgroundColor: c.bgCardElevated, borderColor: c.border }]}>
            <Text style={[s.section, { color: c.textPrimary }]}>Connect your inbox</Text>
            <Pressable onPress={() => startConnect("gmail")} style={[s.connectBtn, { backgroundColor: c.accent }]}>
              <Text style={s.btnText}>Connect Gmail</Text>
            </Pressable>
            <Pressable onPress={() => startConnect("o365")} style={[s.connectBtn, { backgroundColor: c.accent, marginTop: 10 }]}>
              <Text style={s.btnText}>Connect Microsoft / O365</Text>
            </Pressable>
            <Pressable onPress={() => setShowConnect(false)} style={{ marginTop: 12, alignSelf: "center" }}>
              <Text style={{ color: c.textMuted }}>Cancel</Text>
            </Pressable>
          </View>
        </View>
      </Modal>

      {/* Setup wizard — capture OAuth client ID + secret from mobile */}
      <Modal visible={showSetup} animationType="slide">
        <View style={[s.container, { backgroundColor: c.bg, paddingTop: insets.top + 12 }]}>
          <View style={[s.header, { borderBottomColor: c.border }]}>
            <Pressable onPress={() => setShowSetup(false)} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Cancel</Text>
            </Pressable>
            <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>
              Set up {setupProvider === "gmail" ? "Gmail" : "Microsoft"}
            </Text>
            <Pressable onPress={submitSetup} disabled={setupSaving} style={{ paddingVertical: 8 }}>
              {setupSaving ? <ActivityIndicator color={c.accent} /> :
                <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Save</Text>}
            </Pressable>
          </View>
          <ScrollView contentContainerStyle={{ padding: 16 }}>
            <Text style={{ color: c.textPrimary, fontSize: 15, lineHeight: 22, marginBottom: 14 }}>
              One-time setup. Yaver stores these credentials only on your Mac — never in the cloud.
            </Text>

            <Text style={[s.section, { color: c.textPrimary, marginTop: 4 }]}>Step 1 — Create OAuth credentials</Text>
            <Pressable
              onPress={() => Linking.openURL(setupProvider === "gmail"
                ? "https://console.cloud.google.com/apis/credentials"
                : "https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationsListBlade")}
              style={[s.connectBtn, { backgroundColor: c.accent, marginTop: 8 }]}
            >
              <Text style={s.btnText}>
                Open {setupProvider === "gmail" ? "Google Cloud Console" : "Azure Portal"}
              </Text>
            </Pressable>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8, lineHeight: 18 }}>
              {setupProvider === "gmail"
                ? "→ Create Credentials → OAuth 2.0 Client ID\n→ Application type: Web application\n→ Name: Yaver\n→ Enable Gmail API (APIs & Services → Library)"
                : "→ App registrations → New registration\n→ Supported account types: Personal + Work\n→ Redirect URI type: Web"}
            </Text>

            <Text style={[s.section, { color: c.textPrimary, marginTop: 22 }]}>Step 2 — Add redirect URI</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4 }}>
              Copy this into "Authorized redirect URIs":
            </Text>
            <Pressable
              onPress={() => {
                import("expo-clipboard").then(({ setStringAsync }) => {
                  setStringAsync(setupRedirectUri);
                  Alert.alert("Copied", setupRedirectUri);
                }).catch(() => {});
              }}
              style={{ marginTop: 8, padding: 12, borderRadius: 8, backgroundColor: c.bgInput, borderWidth: 1, borderColor: c.border }}
            >
              <Text style={{ color: c.textPrimary, fontFamily: "monospace", fontSize: 13 }} selectable>
                {setupRedirectUri || "Loading…"}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 4 }}>Tap to copy</Text>
            </Pressable>

            <Text style={[s.section, { color: c.textPrimary, marginTop: 22 }]}>Step 3 — Paste credentials</Text>
            <TextInput
              value={setupClientId}
              onChangeText={setSetupClientId}
              placeholder={setupProvider === "gmail" ? "Client ID (xxxxx.apps.googleusercontent.com)" : "Application (client) ID"}
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]}
            />
            <TextInput
              value={setupClientSecret}
              onChangeText={setSetupClientSecret}
              placeholder="Client Secret"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              secureTextEntry
              style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, marginTop: 10 }]}
            />
            {setupProvider === "o365" && (
              <TextInput
                value={setupTenantId}
                onChangeText={setSetupTenantId}
                placeholder="Tenant ID (use 'common' for personal accounts)"
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, marginTop: 10 }]}
              />
            )}

            <Pressable
              onPress={submitSetup}
              disabled={setupSaving || !setupClientId.trim() || !setupClientSecret.trim()}
              style={[s.connectBtn, {
                backgroundColor: (!setupClientId.trim() || !setupClientSecret.trim()) ? c.bgInput : "#22c55e",
                marginTop: 20,
                opacity: setupSaving ? 0.6 : 1,
              }]}
            >
              {setupSaving ? <ActivityIndicator color="#fff" /> :
                <Text style={s.btnText}>Save & continue to sign-in</Text>}
            </Pressable>
          </ScrollView>
        </View>
      </Modal>

      {/* Compose modal */}
      <Modal visible={showCompose} animationType="slide">
        <View style={[s.container, { backgroundColor: c.bg, paddingTop: insets.top + 12 }]}>
          <View style={[s.header, { borderBottomColor: c.border }]}>
            <Pressable onPress={() => setShowCompose(false)} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Cancel</Text>
            </Pressable>
            <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>New message</Text>
            <Pressable onPress={send} style={{ paddingVertical: 8 }}>
              <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>Send</Text>
            </Pressable>
          </View>
          <View style={{ padding: 16 }}>
            <TextInput value={composeTo} onChangeText={setComposeTo} placeholder="To (comma separated)" placeholderTextColor={c.textMuted} autoCapitalize="none" style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]} />
            <TextInput value={composeSubject} onChangeText={setComposeSubject} placeholder="Subject" placeholderTextColor={c.textMuted} style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, marginTop: 10 }]} />
            <TextInput value={composeBody} onChangeText={setComposeBody} placeholder="Body" placeholderTextColor={c.textMuted} multiline style={[s.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, marginTop: 10, height: 240, textAlignVertical: "top" }]} />
          </View>
        </View>
      </Modal>
    </View>
  );
}

function shortDate(iso: string): string {
  const d = new Date(iso);
  const now = new Date();
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return d.toLocaleDateString([], { month: "short", day: "numeric" });
}

const s = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
  filterRow: { flexDirection: "row", borderBottomWidth: 1, alignItems: "center" },
  filter: { paddingHorizontal: 16, paddingVertical: 14, borderBottomWidth: 2, borderBottomColor: "transparent", alignItems: "center" },
  row: { padding: 14, borderBottomWidth: 1 },
  chip: { width: 20, height: 20, borderRadius: 10, alignItems: "center", justifyContent: "center" },
  chipText: { color: "#fff", fontSize: 11, fontWeight: "700" },
  from: { flex: 1, fontSize: 14, fontWeight: "600" },
  date: { fontSize: 11 },
  subject: { fontSize: 14, fontWeight: "500", marginTop: 4 },
  snippet: { fontSize: 12, marginTop: 2 },
  section: { fontSize: 14, fontWeight: "700" },
  input: { borderWidth: 1, borderRadius: 8, padding: 12, fontSize: 15, marginTop: 10 },
  promptBox: { borderWidth: 1, borderRadius: 8, padding: 12, marginTop: 8 },
  modalWrap: { flex: 1, alignItems: "stretch", backgroundColor: "rgba(0,0,0,0.5)", paddingHorizontal: 20, paddingTop: 80 },
  modalCard: { width: "100%", borderWidth: 1, borderRadius: 12, padding: 18 },
  connectBtn: { paddingVertical: 14, borderRadius: 10, alignItems: "center", marginTop: 14 },
  btnText: { color: "#fff", fontWeight: "700" },
});
