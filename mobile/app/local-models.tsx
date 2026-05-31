// local-models.tsx — Settings → "On-device AI models" screen.
//
// Lets the user download / switch / remove the on-device LLMs at any time,
// not just during onboarding. Wraps the reusable LocalModelPicker with the
// background-download state machine (downloadState.ts) so downloads here are
// non-blocking with progress, exactly like the onboarding prompt.
//
// The actual file fetch + sha256 verify is the native adapter's job; this
// screen drives the pure state transitions and renders status. Device RAM is
// read from expo-device when available so the picker can flag runnable models
// and recommend the right one.

import React, { useCallback, useMemo, useState } from "react";
import { Platform, StyleSheet, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Stack } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import LocalModelPicker from "../src/components/LocalModelPicker";
import type { ModelAvailability } from "../src/lib/localAgent/models";
import {
  acceptDownload,
  promptDownload,
  cancelDownload,
  readyIds,
  type DownloadMap,
} from "../src/lib/localAgent/downloadState";

// Device RAM (MB) drives runnable-gating + the "Recommended" pick. We don't
// add a native dep just for this: when RAM is unknown the picker keeps every
// model runnable (a safe default), and the app's own capability signal
// (schema edgeProfile.memoryMb, reported at device registration) can be
// threaded in here later. undefined → "unknown, allow all".
function useDeviceRamMb(): number | undefined {
  // Placeholder until edgeProfile is wired through: leave undefined so the
  // picker shows everything as runnable rather than mis-gating on a guess.
  return Platform.OS ? undefined : undefined;
}

export default function LocalModelsScreen() {
  const c = useColors();
  const totalRamMb = useDeviceRamMb();

  const [downloads, setDownloads] = useState<DownloadMap>({});
  const [activeId, setActiveId] = useState<string | null>(null);

  const downloadedIds = useMemo(() => readyIds(downloads), [downloads]);
  const downloadingId = useMemo(
    () => Object.values(downloads).find((d) => d.phase === "downloading" || d.phase === "queued")?.modelId ?? null,
    [downloads],
  );
  const downloadProgress = downloadingId ? downloads[downloadingId]?.progress ?? 0 : 0;

  // Kick off a (non-blocking) background download. The native adapter would
  // observe `queued` and drive downloading→verifying→ready; here we just move
  // the state to queued so the UI shows progress immediately. Wiring the real
  // resumable fetch is the native adapter's job (see downloadState.ts).
  const onDownload = useCallback((m: ModelAvailability) => {
    setDownloads((prev) => acceptDownload(promptDownload(prev, m.id), m.id));
    // TODO(native): startModelDownload(m, setDownloads) — expo-file-system
    // resumable download + sha256 verify against models.ts, then markReady.
  }, []);

  const onActivate = useCallback((m: ModelAvailability) => {
    setActiveId(m.id);
    // TODO(native): load the GGUF into llama.rn and set it as the active model.
  }, []);

  const onDelete = useCallback((m: ModelAvailability) => {
    setDownloads((prev) => cancelDownload(prev, m.id));
    // TODO(native): remove the cached GGUF file to reclaim space.
  }, []);

  return (
    <SafeAreaView style={[styles.safe, { backgroundColor: c.bg }]} edges={["bottom"]}>
      <Stack.Screen options={{ title: "On-device AI" }} />
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
});
