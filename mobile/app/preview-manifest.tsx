import AsyncStorage from "@react-native-async-storage/async-storage";
import { useLocalSearchParams, useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import React, { useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../src/context/ThemeContext";
import { quicClient } from "../src/lib/quic";
import {
  checkThirdPartyPreviewCompatibility,
  fetchThirdPartyPreviewManifest,
  launchThirdPartyPreview,
  type ThirdPartyPreviewManifest,
} from "../src/lib/thirdPartyPreview";

const RECENT_MANIFEST_KEY = "@yaver/third_party_preview_manifest_url";

export default function PreviewManifestScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{ project?: string; path?: string; framework?: string }>();
  const [manifestUrl, setManifestUrl] = useState("");
  const [manifest, setManifest] = useState<ThirdPartyPreviewManifest | null>(null);
  const [loading, setLoading] = useState(false);
  const [launching, setLaunching] = useState(false);

  useEffect(() => {
    AsyncStorage.getItem(RECENT_MANIFEST_KEY)
      .then((value) => {
        if (value) setManifestUrl(value);
      })
      .catch(() => {});
  }, []);

  async function handleFetch() {
    const url = manifestUrl.trim();
    if (!url) {
      Alert.alert("Manifest URL Required", "Paste a preview manifest URL first.");
      return;
    }
    setLoading(true);
    try {
      const next = await fetchThirdPartyPreviewManifest(url, quicClient.getAuthHeaders());
      setManifest(next);
      await AsyncStorage.setItem(RECENT_MANIFEST_KEY, url);
    } catch (e) {
      Alert.alert("Preview Manifest Failed", e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  async function handleLaunch() {
    if (!manifest) return;
    setLaunching(true);
    try {
      const compatibility = await checkThirdPartyPreviewCompatibility(manifest);
      if (!compatibility.ok) {
        Alert.alert(
          "Native Modules Missing",
          `This preview needs modules not compiled into Yaver: ${compatibility.missingModules.join(", ")}`,
        );
        return;
      }
      await launchThirdPartyPreview(manifest, {
        requestHeaders: quicClient.getAuthHeaders(),
      });
      Alert.alert("Preview Loaded", `${manifest.name} is now running inside Yaver.`);
    } catch (e) {
      Alert.alert("Preview Launch Failed", e instanceof Error ? e.message : String(e));
    } finally {
      setLaunching(false);
    }
  }

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]}>
      <ScrollView contentContainerStyle={styles.content}>
        <Pressable
          onPress={() => (router.canGoBack() ? router.back() : router.replace("/(tabs)"))}
          hitSlop={12}
          style={{ flexDirection: "row", alignItems: "center", marginBottom: 12, alignSelf: "flex-start" }}
          accessibilityRole="button"
          accessibilityLabel="Back"
        >
          <Ionicons name="chevron-back" size={22} color={c.textSecondary} />
          <Text style={{ color: c.textSecondary, fontSize: 16, marginLeft: 2, fontWeight: "600" }}>Back</Text>
        </Pressable>
        <Text style={[styles.title, { color: c.textPrimary }]}>Third-Party Preview</Text>
        <Text style={[styles.subtitle, { color: c.textMuted }]}>
          Load a Hermes preview manifest and run that bundle inside Yaver without rebuilding the app binary.
        </Text>

        {(params.project || params.path) ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Current Project Context</Text>
            {params.project ? <Text style={[styles.meta, { color: c.textMuted }]}>project: {params.project}</Text> : null}
            {params.path ? <Text style={[styles.meta, { color: c.textMuted }]}>path: {params.path}</Text> : null}
            {params.framework ? <Text style={[styles.meta, { color: c.textMuted }]}>framework: {params.framework}</Text> : null}
          </View>
        ) : null}

        <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.label, { color: c.textPrimary }]}>Manifest URL</Text>
          <TextInput
            value={manifestUrl}
            onChangeText={setManifestUrl}
            autoCapitalize="none"
            autoCorrect={false}
            placeholder="https://..."
            placeholderTextColor={c.textMuted}
            style={[
              styles.input,
              { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bg },
            ]}
          />
          <Pressable
            onPress={handleFetch}
            disabled={loading}
            style={[styles.button, { backgroundColor: c.accent, opacity: loading ? 0.7 : 1 }]}
          >
            {loading ? <ActivityIndicator color="#fff" /> : <Text style={styles.buttonText}>Fetch Manifest</Text>}
          </Pressable>
        </View>

        {manifest ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>{manifest.name}</Text>
            {manifest.description ? <Text style={[styles.meta, { color: c.textMuted }]}>{manifest.description}</Text> : null}
            <Text style={[styles.meta, { color: c.textMuted }]}>bundle: {manifest.bundleUrl}</Text>
            <Text style={[styles.meta, { color: c.textMuted }]}>module: {manifest.moduleName || "main"}</Text>
            {manifest.git?.repoUrl ? <Text style={[styles.meta, { color: c.textMuted }]}>git: {manifest.git.repoUrl}</Text> : null}
            {manifest.git?.commit ? <Text style={[styles.meta, { color: c.textMuted }]}>commit: {manifest.git.commit}</Text> : null}
            {manifest.feedback?.compileTimeInjected !== undefined ? (
              <Text style={[styles.meta, { color: c.textMuted }]}>
                yaver ci: {manifest.feedback.compileTimeInjected ? "compile-time injected" : "not declared"}
              </Text>
            ) : null}
            {manifest.sharing ? (
              <Text style={[styles.meta, { color: c.textMuted }]}>
                sharing: host {manifest.sharing.hostVisible ? "visible" : "private"} · guests {manifest.sharing.guestVisible ? "visible" : "private"}
              </Text>
            ) : null}
            <Pressable
              onPress={handleLaunch}
              disabled={launching}
              style={[styles.button, { backgroundColor: "#16a34a", opacity: launching ? 0.7 : 1 }]}
            >
              {launching ? <ActivityIndicator color="#fff" /> : <Text style={styles.buttonText}>Launch Inside Yaver</Text>}
            </Pressable>
          </View>
        ) : null}
      </ScrollView>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  content: { padding: 20, gap: 16 },
  title: { fontSize: 28, fontWeight: "800" },
  subtitle: { fontSize: 14, lineHeight: 20 },
  card: {
    borderWidth: 1,
    borderRadius: 16,
    padding: 16,
    gap: 10,
  },
  cardTitle: { fontSize: 18, fontWeight: "700" },
  label: { fontSize: 13, fontWeight: "700" },
  input: {
    borderWidth: 1,
    borderRadius: 12,
    paddingHorizontal: 12,
    paddingVertical: 12,
    fontSize: 15,
  },
  button: {
    minHeight: 46,
    borderRadius: 12,
    alignItems: "center",
    justifyContent: "center",
    paddingHorizontal: 14,
  },
  buttonText: {
    color: "#fff",
    fontSize: 15,
    fontWeight: "700",
  },
  meta: {
    fontSize: 13,
    lineHeight: 18,
  },
});
