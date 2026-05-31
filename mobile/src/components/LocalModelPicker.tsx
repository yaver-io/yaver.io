/**
 * LocalModelPicker — UI for choosing / downloading the on-device LLM.
 *
 * Lists every model in the registry (localAgent/models.ts): the bundled
 * router (always installed, runs offline), plus downloadable router/coder
 * GGUFs from GitHub Releases. Marks ONE as "Recommended for your device" from
 * the device's RAM, shows install/download state, and gates non-runnable
 * models with a clear reason. All models share the one llama.rn engine, so a
 * listed model is guaranteed compatible — this screen just picks which weights.
 *
 * Presentational: the host passes the device RAM + downloaded ids and the
 * download/activate callbacks (the native download/verify adapter lives
 * elsewhere). No model bytes flow through here.
 */
import React from "react";
import { ActivityIndicator, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../context/ThemeContext";
import { modelPicker, type ModelAvailability } from "../lib/localAgent/models";

interface Props {
  totalRamMb?: number;
  downloadedIds?: string[];
  /** Currently-active model id (the one inference uses now). */
  activeId?: string | null;
  /** id currently downloading → shows progress; null when idle. */
  downloadingId?: string | null;
  /** 0..1 progress for the downloading model. */
  downloadProgress?: number;
  /** Download (and verify) a model's weights. */
  onDownload: (m: ModelAvailability) => void;
  /** Make an installed model the active one. */
  onActivate: (m: ModelAvailability) => void;
  /** Remove a downloaded model to reclaim space (never the bundled one). */
  onDelete?: (m: ModelAvailability) => void;
}

function sizeLabel(mb: number): string {
  return mb >= 1000 ? `${(mb / 1000).toFixed(1)} GB` : `${mb} MB`;
}

export default function LocalModelPicker({
  totalRamMb,
  downloadedIds,
  activeId,
  downloadingId,
  downloadProgress,
  onDownload,
  onActivate,
  onDelete,
}: Props) {
  const c = useColors();
  const rows = modelPicker({ totalRamMb, downloadedIds });

  const routers = rows.filter((r) => r.tier === "router");
  const coders = rows.filter((r) => r.tier === "coder");

  const renderRow = (m: ModelAvailability) => {
    const isActive = activeId === m.id;
    const isDownloading = downloadingId === m.id;
    return (
      <View
        key={m.id}
        style={[
          styles.row,
          { backgroundColor: c.bgCard, borderColor: isActive ? c.accent : c.border },
        ]}
      >
        <View style={{ flex: 1 }}>
          <View style={styles.titleRow}>
            <Text style={[styles.label, { color: c.textPrimary }]}>{m.label}</Text>
            {m.recommended ? (
              <View style={[styles.badge, { backgroundColor: c.accent + "22", borderColor: c.accent }]}>
                <Text style={[styles.badgeText, { color: c.accent }]}>Recommended</Text>
              </View>
            ) : null}
            {m.bundled ? (
              <View style={[styles.badge, { backgroundColor: c.border, borderColor: c.border }]}>
                <Text style={[styles.badgeText, { color: c.textSecondary }]}>Included</Text>
              </View>
            ) : null}
          </View>
          <Text style={[styles.note, { color: c.textMuted }]}>{m.note}</Text>
          <Text style={[styles.meta, { color: c.textMuted }]}>
            {sizeLabel(m.approxSizeMb)} · {m.quant}
            {!m.runnable ? "  ·  needs more RAM than this device has" : ""}
          </Text>
          {isDownloading ? (
            <View style={[styles.progressTrack, { backgroundColor: c.border }]}>
              <View
                style={[
                  styles.progressFill,
                  { backgroundColor: c.accent, width: `${Math.round((downloadProgress ?? 0) * 100)}%` },
                ]}
              />
            </View>
          ) : null}
        </View>

        <View style={styles.actionCol}>
          {isActive ? (
            <View style={styles.activePill}>
              <Ionicons name="checkmark-circle" size={16} color={c.accent} />
              <Text style={[styles.activeText, { color: c.accent }]}>Active</Text>
            </View>
          ) : isDownloading ? (
            <ActivityIndicator size="small" color={c.accent} />
          ) : m.installed ? (
            <Pressable
              onPress={() => m.runnable && onActivate(m)}
              disabled={!m.runnable}
              style={({ pressed }) => [
                styles.btn,
                { borderColor: c.accent, opacity: m.runnable ? (pressed ? 0.7 : 1) : 0.4 },
              ]}
            >
              <Text style={[styles.btnText, { color: c.accent }]}>Use</Text>
            </Pressable>
          ) : (
            <Pressable
              onPress={() => m.runnable && onDownload(m)}
              disabled={!m.runnable}
              style={({ pressed }) => [
                styles.btn,
                { borderColor: c.border, opacity: m.runnable ? (pressed ? 0.7 : 1) : 0.4 },
              ]}
            >
              <Ionicons name="cloud-download-outline" size={15} color={c.textSecondary} />
              <Text style={[styles.btnText, { color: c.textSecondary }]}>Download</Text>
            </Pressable>
          )}
          {!m.bundled && m.installed && !isActive && onDelete ? (
            <Pressable onPress={() => onDelete(m)} hitSlop={8} style={{ marginTop: 8 }}>
              <Ionicons name="trash-outline" size={15} color={c.textMuted} />
            </Pressable>
          ) : null}
        </View>
      </View>
    );
  };

  return (
    <ScrollView contentContainerStyle={styles.wrap} showsVerticalScrollIndicator={false}>
      <Text style={[styles.section, { color: c.textMuted }]}>VOICE HELPER</Text>
      {routers.map(renderRow)}
      <Text style={[styles.section, { color: c.textMuted, marginTop: 20 }]}>ON-DEVICE CODER</Text>
      <Text style={[styles.sectionHint, { color: c.textMuted }]}>
        For the Mobile Sandbox. Larger models need a more powerful phone; you can still
        pair a computer for full-power coding.
      </Text>
      {coders.map(renderRow)}
      <Text style={[styles.footer, { color: c.textMuted }]}>
        All models run on the same on-device engine, fully offline. Downloads are
        verified before use.
      </Text>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  wrap: { padding: 16, gap: 10 },
  section: { fontSize: 11, fontWeight: "700", letterSpacing: 1 },
  sectionHint: { fontSize: 12, lineHeight: 17, marginTop: -2, marginBottom: 4 },
  row: { flexDirection: "row", alignItems: "flex-start", gap: 12, borderWidth: 1, borderRadius: 14, padding: 14 },
  titleRow: { flexDirection: "row", alignItems: "center", flexWrap: "wrap", gap: 8 },
  label: { fontSize: 15, fontWeight: "700" },
  badge: { paddingHorizontal: 8, paddingVertical: 2, borderRadius: 6, borderWidth: 1 },
  badgeText: { fontSize: 10, fontWeight: "700", letterSpacing: 0.3 },
  note: { fontSize: 12, lineHeight: 17, marginTop: 4 },
  meta: { fontSize: 11, marginTop: 6 },
  progressTrack: { height: 4, borderRadius: 2, marginTop: 10, overflow: "hidden" },
  progressFill: { height: 4, borderRadius: 2 },
  actionCol: { alignItems: "center", justifyContent: "center", minWidth: 96 },
  btn: { flexDirection: "row", alignItems: "center", gap: 6, borderWidth: 1, borderRadius: 10, paddingVertical: 9, paddingHorizontal: 14, minHeight: 38, justifyContent: "center" },
  btnText: { fontSize: 13, fontWeight: "700" },
  activePill: { flexDirection: "row", alignItems: "center", gap: 5 },
  activeText: { fontSize: 13, fontWeight: "700" },
  footer: { fontSize: 11, lineHeight: 16, marginTop: 16, textAlign: "center" },
});
