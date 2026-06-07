// app/git-accounts.tsx — settings for git provider credentials. Personal access
// tokens for GitHub / GitLab / Bitbucket (+ a self-hosted host), stored only in
// this device's keychain (SecureStore) and used to push/pull phone-local sandbox
// repos DIRECTLY FROM THE PHONE — no dev box, no Yaver server ever sees them.

import React, { useCallback, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useFocusEffect, useRouter } from "expo-router";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { useColors } from "../src/context/ThemeContext";
import { AppBackButton } from "../src/components/AppBackButton";
import {
  loadGitCredStatus,
  saveProviderToken,
  saveGenericGit,
  loadGenericGit,
  type GitCredStatus,
  type GenericGitConfig,
} from "../src/lib/gitProviderStore";
import { GIT_PROVIDERS } from "../src/lib/gitProviderAuth";

type ProviderId = "github" | "gitlab" | "bitbucket";

export default function GitAccountsScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();

  const [status, setStatus] = useState<GitCredStatus | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<string | null>(null);
  const [generic, setGeneric] = useState<GenericGitConfig>({ host: "", username: "", token: "" });

  const reload = useCallback(async () => {
    const s = await loadGitCredStatus();
    setStatus(s);
    const g = await loadGenericGit();
    if (g) setGeneric({ host: g.host, username: g.username ?? "", token: "" });
  }, []);

  useFocusEffect(
    useCallback(() => {
      void reload();
    }, [reload]),
  );

  const saveProvider = useCallback(
    async (id: ProviderId) => {
      setSaving(id);
      try {
        await saveProviderToken(id, (drafts[id] ?? "").trim());
        setDrafts((d) => ({ ...d, [id]: "" }));
        await reload();
      } finally {
        setSaving(null);
      }
    },
    [drafts, reload],
  );

  const saveGeneric = useCallback(async () => {
    setSaving("generic");
    try {
      await saveGenericGit(
        generic.host.trim() && generic.token.trim()
          ? { host: generic.host.trim(), username: generic.username?.trim() || undefined, token: generic.token.trim() }
          : null,
      );
      setGeneric((g) => ({ ...g, token: "" }));
      await reload();
    } finally {
      setSaving(null);
    }
  }, [generic, reload]);

  const has = (id: ProviderId) => (id === "github" ? status?.github : id === "gitlab" ? status?.gitlab : status?.bitbucket);

  return (
    <KeyboardAvoidingView
      behavior={Platform.OS === "ios" ? "padding" : undefined}
      style={{ flex: 1, backgroundColor: c.bg }}
    >
      <View style={{ paddingTop: insets.top + 8 }}>
        <View style={styles.header}>
          <AppBackButton onPress={() => router.back()} />
          <View style={{ marginLeft: 8 }}>
            <Text style={[styles.h1, { color: c.textPrimary }]}>Git Accounts</Text>
            <Text style={{ color: c.textMuted, fontSize: 12 }}>Push & pull sandbox repos from your phone</Text>
          </View>
        </View>
      </View>

      {!status ? (
        <View style={styles.center}>
          <ActivityIndicator color={c.textMuted} />
        </View>
      ) : (
        <ScrollView contentContainerStyle={{ padding: 12, paddingBottom: insets.bottom + 40 }}>
          {(["github", "gitlab", "bitbucket"] as const).map((id) => {
            const meta = GIT_PROVIDERS.find((p) => p.id === id)!;
            return (
              <View key={id} style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
                <View style={styles.cardHead}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600" }}>{meta.label}</Text>
                  <Text style={{ color: has(id) ? "#4caf50" : c.textMuted, fontSize: 12 }}>
                    {has(id) ? "saved" : "no token"}
                  </Text>
                </View>
                <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>{meta.tokenHint}</Text>
                <View style={{ flexDirection: "row", marginTop: 8 }}>
                  <TextInput
                    value={drafts[id] ?? ""}
                    onChangeText={(t) => setDrafts((d) => ({ ...d, [id]: t }))}
                    placeholder={has(id) ? "Replace token (blank + Save to remove)" : "Paste access token"}
                    placeholderTextColor={c.textMuted}
                    autoCapitalize="none"
                    autoCorrect={false}
                    secureTextEntry
                    style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
                  />
                  <Pressable onPress={() => saveProvider(id)} style={[styles.saveBtn, { backgroundColor: c.accent }]}>
                    {saving === id ? <ActivityIndicator color={c.bg} /> : <Text style={{ color: c.bg, fontWeight: "600" }}>Save</Text>}
                  </Pressable>
                </View>
              </View>
            );
          })}

          {/* Self-hosted / generic */}
          <View style={[styles.card, { borderColor: c.border, backgroundColor: c.bgCard }]}>
            <View style={styles.cardHead}>
              <Text style={{ color: c.textPrimary, fontWeight: "600" }}>Self-hosted</Text>
              <Text style={{ color: status.generic ? "#4caf50" : c.textMuted, fontSize: 12 }}>
                {status.generic ? `saved · ${status.generic.host}` : "none"}
              </Text>
            </View>
            <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 2 }}>
              GitLab/Gitea/etc on your own domain. Host is matched against the remote URL.
            </Text>
            <TextInput
              value={generic.host}
              onChangeText={(t) => setGeneric((g) => ({ ...g, host: t }))}
              placeholder="git.mycorp.io"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg, marginTop: 8 }]}
            />
            <TextInput
              value={generic.username ?? ""}
              onChangeText={(t) => setGeneric((g) => ({ ...g, username: t }))}
              placeholder="username (optional — for basic auth)"
              placeholderTextColor={c.textMuted}
              autoCapitalize="none"
              autoCorrect={false}
              style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg, marginTop: 8 }]}
            />
            <View style={{ flexDirection: "row", marginTop: 8 }}>
              <TextInput
                value={generic.token}
                onChangeText={(t) => setGeneric((g) => ({ ...g, token: t }))}
                placeholder={status.generic ? "Replace token (blank + Save to remove)" : "Access token"}
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
                style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg }]}
              />
              <Pressable onPress={saveGeneric} style={[styles.saveBtn, { backgroundColor: c.accent }]}>
                {saving === "generic" ? <ActivityIndicator color={c.bg} /> : <Text style={{ color: c.bg, fontWeight: "600" }}>Save</Text>}
              </Pressable>
            </View>
          </View>

          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 14, lineHeight: 16 }}>
            Tokens are stored only in this device's keychain and used directly from the phone to talk to your git
            host over HTTPS. They're never sent to Yaver's servers, and no dev box is involved.
          </Text>
        </ScrollView>
      )}
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  header: { flexDirection: "row", alignItems: "center", paddingHorizontal: 12, paddingBottom: 10 },
  h1: { fontSize: 18, fontWeight: "700" },
  center: { flex: 1, alignItems: "center", justifyContent: "center" },
  card: { borderWidth: 1, borderRadius: 10, padding: 12, marginBottom: 10 },
  cardHead: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  input: { flex: 1, borderWidth: 1, borderRadius: 8, paddingHorizontal: 10, paddingVertical: 8, fontSize: 13 },
  saveBtn: { borderRadius: 8, paddingHorizontal: 16, alignItems: "center", justifyContent: "center", marginLeft: 8 },
});
