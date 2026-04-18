import React, { useCallback, useEffect, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import { Image } from "expo-image";
import * as ImagePicker from "expo-image-picker";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { getLocalSecret, LOCAL_KEYS, saveLocalSecret } from "../../src/lib/auth";
import { quicClient } from "../../src/lib/quic";
import {
  buildRemoteDesignPrompt,
  generateDesignModeBrief,
  importFigmaReference,
  importReferenceLink,
  importScreenshotReference,
  type DesignImportResult,
} from "../../src/lib/designMode";

type Surface = "mobile-ui" | "web-ui" | "full-stack";
type InputMode = "figma" | "screenshot" | "reference";

export default function DesignModeScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [inputMode, setInputMode] = useState<InputMode>("figma");
  const [figmaUrl, setFigmaUrl] = useState("");
  const [figmaToken, setFigmaToken] = useState("");
  const [openAiKey, setOpenAiKey] = useState("");
  const [referenceUrl, setReferenceUrl] = useState("");
  const [referenceLabel, setReferenceLabel] = useState("");
  const [referenceNotes, setReferenceNotes] = useState("");
  const [projectQuery, setProjectQuery] = useState("");
  const [goal, setGoal] = useState("Build the first shippable version of this design with strong mobile ergonomics.");
  const [surface, setSurface] = useState<Surface>("mobile-ui");
  const [imported, setImported] = useState<DesignImportResult | null>(null);
  const [brief, setBrief] = useState("");
  const [loadingImport, setLoadingImport] = useState(false);
  const [loadingBrief, setLoadingBrief] = useState(false);
  const [sendingRemote, setSendingRemote] = useState(false);

  useEffect(() => {
    void (async () => {
      const [storedFigma, storedOpenAi] = await Promise.all([
        getLocalSecret(LOCAL_KEYS.figmaAccessToken),
        getLocalSecret(LOCAL_KEYS.openAiApiKey),
      ]);
      if (storedFigma) setFigmaToken(storedFigma);
      if (storedOpenAi) setOpenAiKey(storedOpenAi);
    })();
  }, []);

  const runImport = useCallback(async () => {
    setLoadingImport(true);
    try {
      const result = await importFigmaReference(figmaUrl, figmaToken);
      setImported(result);
      setBrief("");
      await saveLocalSecret(LOCAL_KEYS.figmaAccessToken, figmaToken.trim());
    } catch (e: any) {
      Alert.alert("Figma import failed", e?.message || "Unknown error");
    } finally {
      setLoadingImport(false);
    }
  }, [figmaToken, figmaUrl]);

  const importScreenshot = useCallback(async () => {
    setLoadingImport(true);
    try {
      const permission = await ImagePicker.requestMediaLibraryPermissionsAsync();
      if (!permission.granted) {
        throw new Error("Photo library permission is required");
      }
      const picked = await ImagePicker.launchImageLibraryAsync({
        mediaTypes: ["images"],
        quality: 1,
      });
      if (picked.canceled || !picked.assets?.[0]?.uri) {
        setLoadingImport(false);
        return;
      }
      const result = await importScreenshotReference(picked.assets[0].uri);
      setImported(result);
      setBrief("");
    } catch (e: any) {
      Alert.alert("Screenshot import failed", e?.message || "Unknown error");
    } finally {
      setLoadingImport(false);
    }
  }, []);

  const importReference = useCallback(async () => {
    setLoadingImport(true);
    try {
      const result = importReferenceLink({
        url: referenceUrl,
        label: referenceLabel,
        notes: referenceNotes,
      });
      setImported(result);
      setBrief("");
    } catch (e: any) {
      Alert.alert("Reference import failed", e?.message || "Unknown error");
    } finally {
      setLoadingImport(false);
    }
  }, [referenceLabel, referenceNotes, referenceUrl]);

  const runBrief = useCallback(async () => {
    if (!imported) return;
    setLoadingBrief(true);
    try {
      const nextBrief = await generateDesignModeBrief({
        apiKey: openAiKey,
        imported,
        productGoal: goal,
        targetSurface: surface,
      });
      setBrief(nextBrief);
    } catch (e: any) {
      Alert.alert("Brief generation failed", e?.message || "Unknown error");
    } finally {
      setLoadingBrief(false);
    }
  }, [goal, imported, openAiKey, surface]);

  const sendToRemote = useCallback(async () => {
    if (!connected) {
      Alert.alert("Not connected", "Connect to an agent first.");
      return;
    }
    if (!imported) {
      Alert.alert("Import first", "Import a Figma frame or file before sending.");
      return;
    }
    if (!projectQuery.trim()) {
      Alert.alert("Project required", "Enter a project name or path query to target.");
      return;
    }
    setSendingRemote(true);
    try {
      const project = await quicClient.getVibingState(projectQuery.trim());
      const prompt = buildRemoteDesignPrompt({
        imported,
        brief: brief.trim() || undefined,
        targetSurface: surface,
        productGoal: goal,
      });
      const task = await quicClient.sendTask(
        `Design Mode: ${imported.nodeName}`,
        prompt,
        undefined,
        undefined,
        undefined,
        undefined,
        undefined,
        project.path,
      );
      router.navigate("/(tabs)/tasks" as any);
      Alert.alert("Task created", `Sent to ${project.project} as task ${task.id}.`);
    } catch (e: any) {
      Alert.alert("Remote handoff failed", e?.message || "Unknown error");
    } finally {
      setSendingRemote(false);
    }
  }, [brief, connected, goal, imported, projectQuery, router, surface]);

  const openSource = useCallback(async () => {
    if (!imported?.sourceUrl) return;
    try {
      await Linking.openURL(imported.sourceUrl);
    } catch {
      Alert.alert("Can't open link", imported.sourceUrl);
    }
  }, [imported?.sourceUrl]);

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Design Mode</Text>
        <View style={{ width: 50 }} />
      </View>

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 40 }} keyboardShouldPersistTaps="handled">
        <View style={[styles.hero, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.eyebrow, { color: c.accent }]}>Figma + vibing</Text>
          <Text style={[styles.heroTitle, { color: c.textPrimary }]}>Import a frame, write a brief, send it to code</Text>
          <Text style={[styles.heroBody, { color: c.textMuted }]}>
            This is the first real Design Mode path: pull a live Figma reference into Yaver mobile, optionally generate an implementation brief with your local OpenAI key, then hand it to the paired dev machine as a coding task.
          </Text>
        </View>

        <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Import from Figma</Text>
          <Text style={[styles.panelBody, { color: c.textMuted }]}>
            Use a Figma file/frame URL plus a personal access token. Recent Figma API policy changes mean PATs expire, so treat this like a refreshable local secret.
          </Text>

          <View style={styles.surfaceRow}>
            {(["figma", "screenshot", "reference"] as InputMode[]).map((mode) => {
              const active = inputMode === mode;
              return (
                <Pressable
                  key={mode}
                  onPress={() => setInputMode(mode)}
                  style={[
                    styles.surfaceChip,
                    {
                      borderColor: active ? c.accent : c.border,
                      backgroundColor: active ? c.accent + "18" : c.bg,
                    },
                  ]}
                >
                  <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 12, fontWeight: "700" }}>
                    {mode === "figma" ? "Figma" : mode === "screenshot" ? "Screenshot" : "Canva / Link"}
                  </Text>
                </Pressable>
              );
            })}
          </View>

          {inputMode === "figma" ? (
            <>
              <TextInput
                value={figmaUrl}
                onChangeText={setFigmaUrl}
                placeholder="https://www.figma.com/design/... ?node-id=..."
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]}
              />
              <TextInput
                value={figmaToken}
                onChangeText={setFigmaToken}
                placeholder="Figma personal access token"
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                secureTextEntry
                style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]}
              />

              <Pressable
                onPress={() => void runImport()}
                disabled={loadingImport}
                style={[styles.primaryButton, { backgroundColor: c.accent, opacity: loadingImport ? 0.65 : 1 }]}
              >
                <Text style={styles.primaryButtonText}>{loadingImport ? "Importing…" : "Import Figma reference"}</Text>
              </Pressable>
            </>
          ) : null}

          {inputMode === "screenshot" ? (
            <>
              <Text style={[styles.panelBody, { color: c.textMuted, marginBottom: 12 }]}>
                Pick a screenshot, mockup export, or Canva-exported image from the phone. The brief generator will use the image directly as visual context.
              </Text>
              <Pressable
                onPress={() => void importScreenshot()}
                disabled={loadingImport}
                style={[styles.primaryButton, { backgroundColor: c.accent, opacity: loadingImport ? 0.65 : 1 }]}
              >
                <Text style={styles.primaryButtonText}>{loadingImport ? "Importing…" : "Pick screenshot or mockup"}</Text>
              </Pressable>
            </>
          ) : null}

          {inputMode === "reference" ? (
            <>
              <Text style={[styles.panelBody, { color: c.textMuted, marginBottom: 12 }]}>
                Use this for Canva share links, moodboards, or any other design reference URL when direct API import is not available yet.
              </Text>
              <TextInput
                value={referenceUrl}
                onChangeText={setReferenceUrl}
                placeholder="https://www.canva.com/design/... or other reference URL"
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]}
              />
              <TextInput
                value={referenceLabel}
                onChangeText={setReferenceLabel}
                placeholder="Label, e.g. Canva board v2"
                placeholderTextColor={c.textMuted}
                style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]}
              />
              <TextInput
                value={referenceNotes}
                onChangeText={setReferenceNotes}
                placeholder="Notes about what to copy from this board"
                placeholderTextColor={c.textMuted}
                multiline
                style={[
                  styles.input,
                  { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, minHeight: 110, textAlignVertical: "top" },
                ]}
              />
              <Pressable
                onPress={() => void importReference()}
                disabled={loadingImport}
                style={[styles.primaryButton, { backgroundColor: c.accent, opacity: loadingImport ? 0.65 : 1 }]}
              >
                <Text style={styles.primaryButtonText}>{loadingImport ? "Importing…" : "Save reference link"}</Text>
              </Pressable>
            </>
          ) : null}
        </View>

        <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Design intent</Text>
          <View style={styles.surfaceRow}>
            {(["mobile-ui", "web-ui", "full-stack"] as Surface[]).map((item) => {
              const active = surface === item;
              return (
                <Pressable
                  key={item}
                  onPress={() => setSurface(item)}
                  style={[
                    styles.surfaceChip,
                    {
                      borderColor: active ? c.accent : c.border,
                      backgroundColor: active ? c.accent + "18" : c.bg,
                    },
                  ]}
                >
                  <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 12, fontWeight: "700" }}>
                    {item === "full-stack" ? "Full-stack" : item === "mobile-ui" ? "Mobile UI" : "Web UI"}
                  </Text>
                </Pressable>
              );
            })}
          </View>
          <TextInput
            value={goal}
            onChangeText={setGoal}
            placeholder="What should the imported design become?"
            placeholderTextColor={c.textMuted}
            multiline
            style={[
              styles.input,
              { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, minHeight: 110, textAlignVertical: "top" },
            ]}
          />
        </View>

        {imported ? (
          <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", gap: 10 }}>
              <View style={{ flex: 1 }}>
            <Text style={[styles.panelTitle, { color: c.textPrimary }]}>{imported.nodeName}</Text>
            <Text style={[styles.panelBody, { color: c.textMuted }]}>
                  {imported.fileName}
                  {imported.pageName ? ` · ${imported.pageName}` : ""}
                  {imported.sourceType ? ` · ${imported.sourceType}` : ""}
                </Text>
              </View>
              <Pressable onPress={() => void openSource()}>
                <Text style={{ color: c.accent, fontWeight: "700" }}>Open</Text>
              </Pressable>
            </View>

            {imported.previewUrl ? (
              <Image source={{ uri: imported.previewUrl }} style={styles.preview} contentFit="cover" />
            ) : null}

            <Text style={[styles.smallLabel, { color: c.textMuted }]}>Summary</Text>
            <Text style={[styles.summaryText, { color: c.textPrimary }]}>{imported.summary}</Text>

            {imported.topLevelLayers.length ? (
              <>
                <Text style={[styles.smallLabel, { color: c.textMuted }]}>Top layers</Text>
                <View style={styles.wrapRow}>
                  {imported.topLevelLayers.map((layer) => (
                    <View key={layer} style={[styles.pill, { backgroundColor: c.bgInput }]}>
                      <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>{layer}</Text>
                    </View>
                  ))}
                </View>
              </>
            ) : null}

            {imported.colors.length ? (
              <>
                <Text style={[styles.smallLabel, { color: c.textMuted }]}>Palette clues</Text>
                <View style={styles.wrapRow}>
                  {imported.colors.map((color) => (
                    <View key={color} style={[styles.colorPill, { backgroundColor: c.bgInput, borderColor: c.border }]}>
                      <View style={[styles.swatch, { backgroundColor: color }]} />
                      <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>{color}</Text>
                    </View>
                  ))}
                </View>
              </>
            ) : null}
          </View>
        ) : null}

        <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.panelTitle, { color: c.textPrimary }]}>AI brief</Text>
          <Text style={[styles.panelBody, { color: c.textMuted }]}>
            Optional. This uses the OpenAI-compatible key already stored on the phone to turn the imported design into a clearer implementation brief before handing it off.
          </Text>
          <TextInput
            value={openAiKey}
            onChangeText={setOpenAiKey}
            placeholder="OpenAI / Codex-compatible API key"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            secureTextEntry
            style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]}
          />
          <Pressable
            onPress={() => void runBrief()}
            disabled={!imported || loadingBrief}
            style={[styles.secondaryButton, { borderColor: c.border, backgroundColor: c.bgInput, opacity: !imported || loadingBrief ? 0.55 : 1 }]}
          >
            <Text style={{ color: c.textPrimary, fontWeight: "700" }}>
              {loadingBrief ? "Generating brief…" : "Generate implementation brief"}
            </Text>
          </Pressable>
          {brief ? (
            <TextInput
              value={brief}
              onChangeText={setBrief}
              multiline
              style={[
                styles.input,
                { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput, minHeight: 220, textAlignVertical: "top" },
              ]}
            />
          ) : null}
        </View>

        <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]}>
          <Text style={[styles.panelTitle, { color: c.textPrimary }]}>Remote dev handoff</Text>
          <Text style={[styles.panelBody, { color: c.textMuted }]}>
            Send the imported design and optional AI brief to the paired machine as a real task. The task runs in the resolved project path, so it can inspect the codebase before implementing.
          </Text>
          <TextInput
            value={projectQuery}
            onChangeText={setProjectQuery}
            placeholder="Project query, e.g. bento or /Users/.../repo"
            placeholderTextColor={c.textMuted}
            autoCapitalize="none"
            autoCorrect={false}
            style={[styles.input, { color: c.textPrimary, borderColor: c.border, backgroundColor: c.bgInput }]}
          />
          <Pressable
            onPress={() => void sendToRemote()}
            disabled={!connected || !imported || sendingRemote}
            style={[styles.primaryButton, { backgroundColor: c.accent, opacity: !connected || !imported || sendingRemote ? 0.55 : 1 }]}
          >
            <Text style={styles.primaryButtonText}>
              {sendingRemote ? "Sending…" : connected ? "Send to remote dev" : "Connect an agent first"}
            </Text>
          </Pressable>
        </View>

        {!imported && !loadingImport ? (
          <View style={[styles.footerNote, { borderColor: c.border }]}>
            <Text style={{ color: c.textMuted, lineHeight: 20 }}>
              Current scope: live Figma import, optional AI briefing, and remote-dev handoff. Canva and screenshot ingestion can layer onto this same screen next.
            </Text>
          </View>
        ) : null}
        {loadingImport || loadingBrief || sendingRemote ? <ActivityIndicator style={{ marginTop: 10 }} /> : null}
      </ScrollView>
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingBottom: 12,
    borderBottomWidth: 1,
  },
  hero: {
    borderWidth: 1,
    borderRadius: 24,
    padding: 18,
    marginBottom: 16,
  },
  eyebrow: { fontSize: 12, fontWeight: "800", letterSpacing: 0.7, textTransform: "uppercase" },
  heroTitle: { fontSize: 24, fontWeight: "800", marginTop: 10 },
  heroBody: { fontSize: 14, lineHeight: 21, marginTop: 8 },
  panel: {
    borderWidth: 1,
    borderRadius: 24,
    padding: 18,
    marginBottom: 16,
  },
  panelTitle: { fontSize: 19, fontWeight: "800" },
  panelBody: { fontSize: 14, lineHeight: 21, marginTop: 8, marginBottom: 14 },
  input: {
    borderWidth: 1,
    borderRadius: 16,
    paddingHorizontal: 14,
    paddingVertical: 14,
    fontSize: 15,
    marginBottom: 12,
  },
  primaryButton: {
    paddingVertical: 14,
    borderRadius: 16,
    alignItems: "center",
    justifyContent: "center",
  },
  primaryButtonText: { color: "#fff", fontWeight: "800", fontSize: 15 },
  secondaryButton: {
    paddingVertical: 14,
    borderRadius: 16,
    alignItems: "center",
    justifyContent: "center",
    borderWidth: 1,
  },
  preview: {
    width: "100%",
    height: 220,
    borderRadius: 18,
    marginTop: 14,
    marginBottom: 14,
    backgroundColor: "#111827",
  },
  smallLabel: { fontSize: 12, fontWeight: "800", textTransform: "uppercase", marginTop: 8, marginBottom: 8 },
  summaryText: { fontSize: 14, lineHeight: 21 },
  wrapRow: { flexDirection: "row", flexWrap: "wrap", gap: 8 },
  pill: { borderRadius: 999, paddingHorizontal: 12, paddingVertical: 7 },
  colorPill: {
    borderRadius: 999,
    paddingHorizontal: 10,
    paddingVertical: 7,
    borderWidth: 1,
    flexDirection: "row",
    alignItems: "center",
    gap: 8,
  },
  swatch: { width: 16, height: 16, borderRadius: 8 },
  surfaceRow: { flexDirection: "row", flexWrap: "wrap", gap: 8, marginBottom: 12 },
  surfaceChip: { borderWidth: 1, borderRadius: 999, paddingHorizontal: 12, paddingVertical: 8 },
  footerNote: { borderWidth: 1, borderRadius: 16, padding: 14, marginBottom: 10 },
});
