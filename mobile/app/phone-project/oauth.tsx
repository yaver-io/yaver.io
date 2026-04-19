import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { AppBackButton } from "../../src/components/AppBackButton";
import {
  PhoneOAuthApple,
  PhoneOAuthConfig,
  PhoneOAuthGoogle,
  PhoneOAuthMicrosoft,
  PhoneOAuthResponse,
  PhoneOAuthStatus,
  getPhoneOAuth,
  setPhoneOAuth,
} from "../../src/lib/phoneProjects";
import { getYaverCloudBaseUrl } from "../../src/lib/yaverCloud";
import * as Clipboard from "expo-clipboard";

// Phone-hosted OAuth provider setup — the third and final surface of the
// "do everything from your phone" pitch (yc.md §Wedge Demo).
//
// The user still visits the provider's web console to create the app
// registration (Apple Developer portal has no API at all, Google and
// Microsoft do but onboarding OAuth of OAuth is multi-day work). Everything
// AFTER registration — capturing the IDs + keys, validating format, saving
// to the project — happens here. Tap "Open console" → register → come back
// → paste → Save. No laptop needed for day-to-day app building.

type ProviderId = "apple" | "google" | "microsoft";

interface ProviderCopy {
  id: ProviderId;
  label: string;
  color: string;
  consoleLabel: string;
  consoleURL: string;
  description: string;
  steps: string[];
}

const PROVIDER_COPY: Record<ProviderId, ProviderCopy> = {
  apple: {
    id: "apple",
    label: "Sign in with Apple",
    color: "#000000",
    consoleLabel: "Apple Developer portal",
    consoleURL: "https://developer.apple.com/account/resources/identifiers/list",
    description:
      "Requires a paid Apple Developer membership ($99/yr). You'll leave Yaver, register a Services ID and a Sign-in Key, then come back to paste the IDs + the .p8 private key.",
    steps: [
      "1. Open Apple Developer → Certificates, Identifiers & Profiles → Identifiers.",
      "2. Create an App ID with Sign in with Apple enabled. Copy the Team ID from the top-right (10 uppercase chars).",
      "3. Create a Services ID (used for web + universal redirect). Reverse-DNS format, e.g. com.example.signin.",
      "4. Under Keys, create a new Sign in with Apple key. Download the .p8 file IMMEDIATELY — Apple never shows it again.",
      "5. Copy the Key ID (10 uppercase chars) from the key detail page.",
      "6. Paste everything below. The .p8 contents go in the Private key box verbatim.",
    ],
  },
  google: {
    id: "google",
    label: "Sign in with Google",
    color: "#4285F4",
    consoleLabel: "Google Cloud Console",
    consoleURL: "https://console.cloud.google.com/apis/credentials",
    description:
      "Free. You'll create a Google Cloud project, configure the OAuth consent screen (once per project), then mint an OAuth 2.0 Client ID. Come back with the Client ID + secret.",
    steps: [
      "1. Open Google Cloud Console → APIs & Services → Credentials.",
      "2. If you haven't: OAuth consent screen → External → fill app name, support email, dev contact. Add test users if you're staying in testing mode.",
      "3. Create Credentials → OAuth client ID → Application type: Web application.",
      `4. Under Authorized redirect URIs, add the return URL Yaver will serve (e.g. ${getYaverCloudBaseUrl()}/auth/google/callback).`,
      "5. Copy the Client ID (ends with .apps.googleusercontent.com) and Client Secret (GOCSPX-...).",
      "6. Paste below.",
    ],
  },
  microsoft: {
    id: "microsoft",
    label: "Sign in with Microsoft / O365",
    color: "#0078D4",
    consoleLabel: "Azure Portal",
    consoleURL: "https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade",
    description:
      "Requires an Azure account (free tier works). You'll register an app, mint a client secret (record it immediately — Azure only shows it once), and capture the Tenant + Client IDs.",
    steps: [
      "1. Open Azure Portal → Microsoft Entra ID → App registrations → New registration.",
      "2. Supported account types: pick 'Any organizational directory + personal Microsoft accounts' for the widest reach. Tenant ID = 'common' in that case.",
      "3. After creation, copy the Application (client) ID and the Directory (tenant) ID from the Overview page.",
      "4. Certificates & secrets → New client secret → copy the Value IMMEDIATELY (not the Secret ID).",
      "5. Authentication → Add platform → Web → add the redirect URI Yaver will serve.",
      "6. API permissions → add Microsoft Graph → openid, profile, email, User.Read.",
      "7. Paste below.",
    ],
  },
};

export default function PhoneOAuthScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const router = useRouter();
  const { slug } = useLocalSearchParams<{ slug: string }>();
  const slugStr = String(slug ?? "");

  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<ProviderId | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [status, setStatus] = useState<PhoneOAuthStatus>({ apple: false, google: false, microsoft: false });

  const [apple, setApple] = useState<PhoneOAuthApple>({});
  const [google, setGoogle] = useState<PhoneOAuthGoogle>({});
  const [microsoft, setMicrosoft] = useState<PhoneOAuthMicrosoft>({});
  const [expanded, setExpanded] = useState<ProviderId | null>(null);

  const hydrate = useCallback((r: PhoneOAuthResponse) => {
    setStatus(r.status);
    setApple(r.config.apple ?? {});
    setGoogle(r.config.google ?? {});
    setMicrosoft(r.config.microsoft ?? {});
  }, []);

  const load = useCallback(async () => {
    if (!slugStr) return;
    setLoading(true);
    setErr(null);
    try {
      const r = await getPhoneOAuth(slugStr);
      if (!r) throw new Error("agent unreachable");
      hydrate(r);
    } catch (e: any) {
      setErr(e?.message ?? "failed to load");
    } finally {
      setLoading(false);
    }
  }, [slugStr, hydrate]);

  useEffect(() => {
    void load();
  }, [load]);

  async function save(provider: ProviderId) {
    setSaving(provider);
    try {
      const patch: PhoneOAuthConfig = {};
      if (provider === "apple") patch.apple = apple;
      if (provider === "google") patch.google = google;
      if (provider === "microsoft") patch.microsoft = microsoft;
      const r = await setPhoneOAuth(slugStr, patch);
      if (!r) throw new Error("agent returned nothing");
      hydrate(r);
      Alert.alert("Saved", `${PROVIDER_COPY[provider].label} config stored for ${slugStr}.`);
    } catch (e: any) {
      const raw = e?.message ?? "unknown error";
      const lower = String(raw).toLowerCase();
      const hint = /network|fetch|timeout|econn|offline|unreach/.test(lower)
        ? "Yaver couldn't reach the dev machine. Check your connection and try again."
        : /401|403|unauth/.test(lower)
          ? "Your session may have expired — sign in again from Settings."
          : /invalid|schema|required|missing/.test(lower)
            ? "One of the fields failed validation — double-check the values you just entered."
            : "Your changes weren't saved. Retry or reopen this page.";
      Alert.alert("Save Failed", `${raw}\n\n${hint}`);
    } finally {
      setSaving(null);
    }
  }

  async function openConsole(url: string) {
    try {
      await Linking.openURL(url);
      return;
    } catch {
      // Fall through to the copy-to-clipboard fallback.
    }
    Alert.alert(
      "Can't Open Link",
      `No browser on this device could open the provider console. Copy the URL and paste it into a browser on another device.\n\n${url}`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Copy URL",
          onPress: async () => {
            try {
              await Clipboard.setStringAsync(url);
              Alert.alert("Copied", "Provider console URL is on your clipboard.");
            } catch (e) {
              Alert.alert("Clipboard Failed", e instanceof Error ? e.message : String(e));
            }
          },
        },
      ],
    );
  }

  const sections = useMemo(
    () => [
      {
        copy: PROVIDER_COPY.apple,
        ready: status.apple,
        body: (
          <>
            <LabeledInput
              c={c}
              label="Team ID"
              placeholder="ABCDE12345"
              value={apple.teamId ?? ""}
              onChange={(v) => setApple({ ...apple, teamId: v.toUpperCase() })}
              autoCapitalize="characters"
              maxLength={10}
              hint="10 uppercase alphanumeric chars (top-right of Apple Developer site)"
            />
            <LabeledInput
              c={c}
              label="Services ID"
              placeholder="com.example.signin"
              value={apple.servicesId ?? ""}
              onChange={(v) => setApple({ ...apple, servicesId: v })}
              autoCapitalize="none"
              hint="Reverse-DNS identifier you created for the Services ID"
            />
            <LabeledInput
              c={c}
              label="Key ID"
              placeholder="FGHIJ67890"
              value={apple.keyId ?? ""}
              onChange={(v) => setApple({ ...apple, keyId: v.toUpperCase() })}
              autoCapitalize="characters"
              maxLength={10}
              hint="10 uppercase alphanumeric chars (from the Sign-in key detail page)"
            />
            <LabeledInput
              c={c}
              label="Private key (.p8 contents)"
              placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
              value={apple.privateKey ?? ""}
              onChange={(v) => setApple({ ...apple, privateKey: v })}
              multiline
              autoCapitalize="none"
              autoCorrect={false}
              hint="Paste the whole file including the BEGIN/END lines"
            />
          </>
        ),
      },
      {
        copy: PROVIDER_COPY.google,
        ready: status.google,
        body: (
          <>
            <LabeledInput
              c={c}
              label="Client ID"
              placeholder="12345-abc.apps.googleusercontent.com"
              value={google.clientId ?? ""}
              onChange={(v) => setGoogle({ ...google, clientId: v })}
              autoCapitalize="none"
              hint="Ends with .apps.googleusercontent.com"
            />
            <LabeledInput
              c={c}
              label="Client Secret"
              placeholder="GOCSPX-..."
              value={google.clientSecret ?? ""}
              onChange={(v) => setGoogle({ ...google, clientSecret: v })}
              autoCapitalize="none"
              secure
              hint="Shown once when you mint the credential"
            />
          </>
        ),
      },
      {
        copy: PROVIDER_COPY.microsoft,
        ready: status.microsoft,
        body: (
          <>
            <LabeledInput
              c={c}
              label="Tenant ID"
              placeholder="common (or a GUID)"
              value={microsoft.tenantId ?? ""}
              onChange={(v) => setMicrosoft({ ...microsoft, tenantId: v })}
              autoCapitalize="none"
              hint="Use 'common' for multi-tenant, or a GUID for your org only"
            />
            <LabeledInput
              c={c}
              label="Client ID"
              placeholder="12345678-1234-1234-1234-123456789012"
              value={microsoft.clientId ?? ""}
              onChange={(v) => setMicrosoft({ ...microsoft, clientId: v })}
              autoCapitalize="none"
              hint="GUID from the app registration Overview page"
            />
            <LabeledInput
              c={c}
              label="Client Secret"
              placeholder="Azure secret Value (not Secret ID)"
              value={microsoft.clientSecret ?? ""}
              onChange={(v) => setMicrosoft({ ...microsoft, clientSecret: v })}
              autoCapitalize="none"
              secure
              hint="Azure shows the Value exactly once — capture it now"
            />
          </>
        ),
      },
    ],
    [c, apple, google, microsoft, status],
  );

  if (loading) {
    return (
      <View style={[styles.empty, { backgroundColor: c.bg }]}>
        <ActivityIndicator color={c.textMuted} />
      </View>
    );
  }

  return (
    <ScrollView
      style={{ backgroundColor: c.bg }}
      contentContainerStyle={{ paddingTop: insets.top + 8, paddingBottom: 60 + insets.bottom }}
    >
      <View style={{ paddingHorizontal: 16 }}>
        <AppBackButton onPress={() => router.back()} style={{ marginBottom: 8 }} />
        <Text style={[styles.h1, { color: c.textPrimary }]}>OAuth providers</Text>
        <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 4 }}>
          Configure Sign in with Apple / Google / Microsoft for{" "}
          <Text style={{ color: c.textPrimary }}>{slugStr}</Text>. Tap a provider to
          open its console, register your app, and paste the IDs + secrets back here.
          Values save to oauth-providers.yaml alongside the project's schema + auth,
          and travel with the project when you deploy to your dev machine.
        </Text>
        {err ? (
          <Text style={{ color: "#ff6b6b", marginTop: 10, fontSize: 13 }}>{err}</Text>
        ) : null}

        <View style={{ marginTop: 16 }}>
          {sections.map(({ copy, ready, body }) => {
            const isOpen = expanded === copy.id;
            return (
              <View
                key={copy.id}
                style={[
                  styles.card,
                  {
                    backgroundColor: c.bgCard,
                    borderColor: ready ? (c.success ?? "#22c55e") : c.border,
                    marginBottom: 10,
                  },
                ]}
              >
                <Pressable
                  onPress={() => setExpanded(isOpen ? null : copy.id)}
                  style={styles.cardHeader}
                >
                  <View style={[styles.dot, { backgroundColor: ready ? (c.success ?? "#22c55e") : c.border }]} />
                  <View style={{ flex: 1 }}>
                    <Text style={[styles.providerLabel, { color: c.textPrimary }]}>
                      {copy.label}
                    </Text>
                    <Text style={{ color: c.textMuted, fontSize: 12 }}>
                      {ready ? "Configured ✓" : "Not configured"}
                    </Text>
                  </View>
                  <Text style={{ color: c.textMuted, fontSize: 18 }}>{isOpen ? "▾" : "▸"}</Text>
                </Pressable>

                {isOpen ? (
                  <View style={{ paddingHorizontal: 14, paddingBottom: 12 }}>
                    <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 4, marginBottom: 10 }}>
                      {copy.description}
                    </Text>

                    <Pressable
                      onPress={() => openConsole(copy.consoleURL)}
                      style={[styles.consoleBtn, { borderColor: c.accent }]}
                    >
                      <Text style={{ color: c.accent, fontWeight: "600", fontSize: 13 }}>
                        Open {copy.consoleLabel} ↗
                      </Text>
                    </Pressable>

                    <View style={{ marginTop: 12 }}>
                      {copy.steps.map((step, i) => (
                        <Text
                          key={i}
                          style={{ color: c.textPrimary, fontSize: 12, marginBottom: 4, lineHeight: 18 }}
                        >
                          {step}
                        </Text>
                      ))}
                    </View>

                    <View style={{ marginTop: 16 }}>{body}</View>

                    <Pressable
                      onPress={() => save(copy.id)}
                      disabled={saving !== null}
                      style={[
                        styles.saveBtn,
                        { backgroundColor: c.accent, opacity: saving === copy.id ? 0.6 : 1 },
                      ]}
                    >
                      {saving === copy.id ? (
                        <ActivityIndicator color={c.bg} />
                      ) : (
                        <Text style={{ color: c.bg, fontWeight: "600" }}>Save {copy.label}</Text>
                      )}
                    </Pressable>
                  </View>
                ) : null}
              </View>
            );
          })}
        </View>
      </View>
    </ScrollView>
  );
}

interface LabeledInputProps {
  c: ReturnType<typeof useColors>;
  label: string;
  placeholder?: string;
  value: string;
  onChange: (v: string) => void;
  multiline?: boolean;
  autoCapitalize?: "none" | "characters" | "sentences" | "words";
  autoCorrect?: boolean;
  maxLength?: number;
  secure?: boolean;
  hint?: string;
}

function LabeledInput({ c, label, placeholder, value, onChange, multiline, autoCapitalize, autoCorrect, maxLength, secure, hint }: LabeledInputProps) {
  return (
    <View style={{ marginBottom: 12 }}>
      <Text style={{ color: c.textMuted, fontSize: 11, marginBottom: 4, textTransform: "uppercase", letterSpacing: 0.5 }}>
        {label}
      </Text>
      <TextInput
        value={value}
        onChangeText={onChange}
        placeholder={placeholder}
        placeholderTextColor={c.textMuted}
        multiline={multiline}
        autoCapitalize={autoCapitalize}
        autoCorrect={autoCorrect}
        maxLength={maxLength}
        secureTextEntry={secure}
        style={[
          styles.input,
          {
            color: c.textPrimary,
            borderColor: c.border,
            minHeight: multiline ? 90 : undefined,
            fontFamily: multiline ? Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" }) : undefined,
            textAlignVertical: multiline ? "top" : "center",
          },
        ]}
      />
      {hint ? (
        <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 3 }}>{hint}</Text>
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  h1: { fontSize: 24, fontWeight: "700" },
  card: { borderWidth: 1, borderRadius: 10 },
  cardHeader: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 14,
  },
  dot: { width: 8, height: 8, borderRadius: 4, marginRight: 10 },
  providerLabel: { fontSize: 15, fontWeight: "600" },
  consoleBtn: { borderWidth: 1, borderRadius: 8, paddingVertical: 10, alignItems: "center" },
  input: { borderWidth: 1, borderRadius: 8, padding: 10, fontSize: 14 },
  saveBtn: { paddingVertical: 12, borderRadius: 8, alignItems: "center", marginTop: 12 },
  empty: { flex: 1, alignItems: "center", justifyContent: "center" },
});
