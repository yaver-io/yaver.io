// local-models.tsx — Settings → "On-device AI models" screen.
//
// Lets the user download / switch / remove the on-device LLMs at any time.
// Platform-aware: device RAM (expo-device) drives which models are runnable and
// which is recommended, so we never offer a model the phone can't load. The
// real resumable download + GGUF validation lives in localModelDownload.ts and
// writes to the same cache the coding backend loads from.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack, useRouter } from "expo-router";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../src/context/ThemeContext";
import LocalModelPicker from "../src/components/LocalModelPicker";
import { getModel, type ModelAvailability } from "../src/lib/localAgent/models";
import {
  markFailed,
  markReady,
  setProgress,
  startDownloading,
  startVerifying,
  type DownloadMap,
} from "../src/lib/localAgent/downloadState";
import { useDeviceCapability } from "../src/lib/deviceCapability";
import { capabilityClassLabel } from "../src/lib/deviceCapabilityCore";
import {
  deleteModelFile,
  installedModelIds,
  startModelDownload,
} from "../src/lib/localModelDownload";
import { releaseLocalModel } from "../src/lib/codingBackendStore";

export default function LocalModelsScreen() {
  const c = useColors();
  const router = useRouter();
  const { tier, totalRamMb } = useDeviceCapability();

  const [downloads, setDownloads] = useState<DownloadMap>({});
  const [installedIds, setInstalledIds] = useState<string[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);

  const refreshInstalled = useCallback(async () => {
    setInstalledIds(await installedModelIds());
  }, []);

  useEffect(() => {
    void refreshInstalled();
  }, [refreshInstalled]);

  // Everything present on disk (bundled + downloaded) counts as installed.
  const downloadedIds = useMemo(() => installedIds, [installedIds]);

  const downloadingId = useMemo(
    () =>
      Object.values(downloads).find((d) => d.phase === "downloading" || d.phase === "verifying")
        ?.modelId ?? null,
    [downloads],
  );
  const downloadProgress = downloadingId ? downloads[downloadingId]?.progress ?? 0 : 0;

  const onDownload = useCallback(
    (m: ModelAvailability) => {
      const entry = getModel(m.id);
      if (!entry) return;
      setDownloads((prev) => startDownloading(prev, m.id));
      void startModelDownload(entry, {
        onProgress: (id, received, total) =>
          setDownloads((prev) => setProgress(prev, id, received, total)),
        onVerifying: (id) => setDownloads((prev) => startVerifying(prev, id)),
        onReady: (id) => {
          setDownloads((prev) => markReady(prev, id));
          void refreshInstalled();
        },
        onFailed: (id, error) => setDownloads((prev) => markFailed(prev, id, error)),
      });
    },
    [refreshInstalled],
  );

  const onActivate = useCallback((m: ModelAvailability) => {
    // The coding backend auto-selects the largest installed runnable coder, so
    // activation is mostly a visual confirmation here. Release any cached model
    // so the next inference reloads the freshly-chosen one.
    setActiveId(m.id);
    void releaseLocalModel();
  }, []);

  const onDelete = useCallback(
    async (m: ModelAvailability) => {
      await deleteModelFile(m.id);
      if (activeId === m.id) {
        setActiveId(null);
        await releaseLocalModel();
      }
      await refreshInstalled();
    },
    [activeId, refreshInstalled],
  );

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <Stack.Screen options={{ title: "On-device AI" }} />
      <Pressable
        onPress={() => (router.canGoBack() ? router.back() : router.replace("/(tabs)"))}
        hitSlop={12}
        style={{ flexDirection: "row", alignItems: "center", marginTop: 12, marginHorizontal: 16, alignSelf: "flex-start" }}
        accessibilityRole="button"
        accessibilityLabel="Back"
      >
        <Ionicons name="chevron-back" size={22} color={c.textSecondary} />
        <Text style={{ color: c.textSecondary, fontSize: 16, marginLeft: 2, fontWeight: "600" }}>Back</Text>
      </Pressable>
      <View style={[styles.banner, { backgroundColor: c.bgCard, borderColor: c.border }]}>
        <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 13 }}>
          This device{totalRamMb ? ` · ${(totalRamMb / 1024).toFixed(0)} GB RAM` : ""}
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
          {capabilityClassLabel(tier)}
        </Text>
      </View>
      <View style={{ flex: 1 }}>
        <LocalModelPicker
          totalRamMb={totalRamMb}
          downloadedIds={downloadedIds}
          activeId={activeId}
          downloadingId={downloadingId}
          downloadProgress={downloadProgress}
          onDownload={onDownload}
          onActivate={onActivate}
          onDelete={onDelete}
        />
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1 },
  banner: { marginHorizontal: 16, marginTop: 12, padding: 12, borderWidth: 1, borderRadius: 12 },
});
