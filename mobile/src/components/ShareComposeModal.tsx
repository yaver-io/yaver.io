// ShareComposeModal — the WhatsApp-style sheet that opens when the user
// shares a screenshot into Yaver from another app's share sheet.
//
// Flow: shareReceiver decodes the shared image(s) → shareIntentEmitter →
// this modal opens with thumbnails + a caption box + a multi-select list
// of the user's online machines. "Send" fans the bug out as one feedback
// task PER selected machine (quicClient.createFeedbackTaskOnDevice), each
// of which the remote agent auto-routes through the vibing pipeline and
// auto-starts the runner to fix it.
import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Image,
  Modal,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useTheme } from "../context/ThemeContext";
import { useDevice } from "../context/DeviceContext";
import { quicClient } from "../lib/quic";
import { shareIntentEmitter } from "../lib/shareIntent";
import type { ImageAttachment } from "../lib/quic";

type SendState = "idle" | "sending" | "sent" | "error";

export function ShareComposeModal() {
  const { colors } = useTheme();
  const { devices, activeDevice, connectedDeviceIds } = useDevice();

  const [visible, setVisible] = useState(false);
  const [images, setImages] = useState<ImageAttachment[]>([]);
  const [comment, setComment] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sendState, setSendState] = useState<Record<string, SendState>>({});
  const [busy, setBusy] = useState(false);

  // Machines the user can fan out to: anything the relay/heartbeat
  // currently reports reachable. Phones aren't in `devices` (they're the
  // client), so this is the user's dev boxes / servers.
  const candidates = useMemo(
    () => devices.filter((d) => d.online || connectedDeviceIds.includes(d.id)),
    [devices, connectedDeviceIds],
  );

  useEffect(() => {
    return shareIntentEmitter.on((imgs, text) => {
      setImages(imgs);
      setComment(text || "");
      setSendState({});
      setBusy(false);
      // Default target = the box you're already attached to; the user
      // can tick more. Fall back to the only candidate when there's no
      // active attachment yet.
      const def =
        activeDevice && (activeDevice.online || connectedDeviceIds.includes(activeDevice.id))
          ? activeDevice.id
          : candidates[0]?.id;
      setSelected(new Set(def ? [def] : []));
      setVisible(true);
    });
  }, [activeDevice, candidates, connectedDeviceIds]);

  const close = useCallback(() => {
    setVisible(false);
    setImages([]);
    setComment("");
    setSelected(new Set());
    setSendState({});
    setBusy(false);
  }, []);

  const toggle = useCallback((id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const canSend =
    !busy &&
    selected.size > 0 &&
    (comment.trim().length > 0 || images.length > 0);

  const handleSend = useCallback(async () => {
    if (!canSend) return;
    setBusy(true);
    const title =
      comment.trim() ||
      "Investigate and fix the issue shown in the attached screenshot.";
    const targets = Array.from(selected);
    setSendState(Object.fromEntries(targets.map((id) => [id, "sending"])));

    const results = await Promise.allSettled(
      targets.map((id) =>
        quicClient.createFeedbackTaskOnDevice(id, { title, images }),
      ),
    );

    const next: Record<string, SendState> = {};
    targets.forEach((id, i) => {
      next[id] = results[i].status === "fulfilled" ? "sent" : "error";
    });
    setSendState(next);
    setBusy(false);

    // All delivered → close after a short confirmation beat. Leave the
    // sheet up if anything failed so the user can retry just those.
    if (targets.every((id) => next[id] === "sent")) {
      setTimeout(close, 900);
    }
  }, [canSend, comment, images, selected, close]);

  if (!visible) return null;

  const allSent =
    selected.size > 0 &&
    Array.from(selected).every((id) => sendState[id] === "sent");

  return (
    <Modal visible transparent animationType="slide" onRequestClose={close}>
      <View style={styles.backdrop}>
        <View style={[styles.sheet, { backgroundColor: colors.surface }]}>
          <View style={styles.headerRow}>
            <Text style={[styles.title, { color: colors.textPrimary }]}>
              Send to Yaver
            </Text>
            <Pressable onPress={close} hitSlop={10}>
              <Text style={[styles.close, { color: colors.textTertiary }]}>✕</Text>
            </Pressable>
          </View>

          {images.length > 0 ? (
            <ScrollView
              horizontal
              showsHorizontalScrollIndicator={false}
              style={styles.thumbStrip}
              contentContainerStyle={{ gap: 8 }}
            >
              {images.map((img, i) => (
                <Image
                  key={i}
                  source={{ uri: `data:${img.mimeType};base64,${img.base64}` }}
                  style={[styles.thumb, { borderColor: colors.border }]}
                />
              ))}
            </ScrollView>
          ) : null}

          <TextInput
            style={[
              styles.input,
              {
                backgroundColor: colors.surfaceMuted,
                color: colors.textPrimary,
                borderColor: colors.border,
              },
            ]}
            placeholder="Add a comment — what's the bug?"
            placeholderTextColor={colors.textTertiary}
            value={comment}
            onChangeText={setComment}
            multiline
          />

          <Text style={[styles.sectionLabel, { color: colors.textSecondary }]}>
            Send to {selected.size > 0 ? `(${selected.size})` : ""}
          </Text>

          {candidates.length === 0 ? (
            <Text style={[styles.empty, { color: colors.textTertiary }]}>
              No machines online. Connect a device and try again.
            </Text>
          ) : (
            <ScrollView style={styles.deviceList} keyboardShouldPersistTaps="handled">
              {candidates.map((d) => {
                const on = selected.has(d.id);
                const st = sendState[d.id];
                return (
                  <Pressable
                    key={d.id}
                    onPress={() => !busy && toggle(d.id)}
                    style={[
                      styles.deviceRow,
                      {
                        borderColor: on ? colors.brandPrimary : colors.border,
                        backgroundColor: on
                          ? colors.brandPrimarySoft
                          : colors.surfaceMuted,
                      },
                    ]}
                  >
                    <View
                      style={[
                        styles.check,
                        {
                          borderColor: on ? colors.brandPrimary : colors.borderStrong,
                          backgroundColor: on ? colors.brandPrimary : "transparent",
                        },
                      ]}
                    >
                      {on ? <Text style={styles.checkMark}>✓</Text> : null}
                    </View>
                    <View style={{ flex: 1 }}>
                      <Text style={[styles.deviceName, { color: colors.textPrimary }]}>
                        {d.alias || d.name}
                      </Text>
                      <Text style={[styles.deviceMeta, { color: colors.textTertiary }]}>
                        {d.os || "device"}
                        {d.isGuest ? " · shared" : ""}
                      </Text>
                    </View>
                    {st === "sending" ? (
                      <ActivityIndicator size="small" color={colors.brandPrimary} />
                    ) : st === "sent" ? (
                      <Text style={{ color: colors.success, fontWeight: "700" }}>
                        sent
                      </Text>
                    ) : st === "error" ? (
                      <Text style={{ color: colors.error, fontWeight: "700" }}>
                        failed
                      </Text>
                    ) : null}
                  </Pressable>
                );
              })}
            </ScrollView>
          )}

          <Pressable
            onPress={handleSend}
            disabled={!canSend}
            style={[
              styles.sendBtn,
              {
                backgroundColor: canSend ? colors.brandPrimary : colors.surfaceMuted,
              },
            ]}
          >
            {busy ? (
              <ActivityIndicator color="#fff" />
            ) : (
              <Text
                style={[
                  styles.sendText,
                  { color: canSend ? "#fff" : colors.textTertiary },
                ]}
              >
                {allSent
                  ? "Sent"
                  : selected.size > 1
                    ? `Send to ${selected.size} machines`
                    : "Send"}
              </Text>
            )}
          </Pressable>
        </View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  backdrop: {
    flex: 1,
    backgroundColor: "rgba(0,0,0,0.55)",
    justifyContent: "flex-end",
  },
  sheet: {
    borderTopLeftRadius: 20,
    borderTopRightRadius: 20,
    paddingHorizontal: 18,
    paddingTop: 16,
    paddingBottom: 28,
    maxHeight: "88%",
  },
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    marginBottom: 12,
  },
  title: { fontSize: 18, fontWeight: "800" },
  close: { fontSize: 18, fontWeight: "600", padding: 4 },
  thumbStrip: { marginBottom: 12, flexGrow: 0 },
  thumb: { width: 84, height: 150, borderRadius: 10, borderWidth: 1 },
  input: {
    minHeight: 64,
    maxHeight: 140,
    borderRadius: 12,
    borderWidth: 1,
    paddingHorizontal: 12,
    paddingVertical: 10,
    fontSize: 15,
    textAlignVertical: "top",
    marginBottom: 14,
  },
  sectionLabel: {
    fontSize: 12,
    fontWeight: "700",
    textTransform: "uppercase",
    letterSpacing: 0.5,
    marginBottom: 8,
  },
  empty: { fontSize: 14, paddingVertical: 16, textAlign: "center" },
  deviceList: { maxHeight: 240, marginBottom: 14 },
  deviceRow: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    borderWidth: 1,
    borderRadius: 12,
    paddingHorizontal: 12,
    paddingVertical: 12,
    marginBottom: 8,
  },
  check: {
    width: 22,
    height: 22,
    borderRadius: 6,
    borderWidth: 2,
    alignItems: "center",
    justifyContent: "center",
  },
  checkMark: { color: "#fff", fontSize: 13, fontWeight: "800" },
  deviceName: { fontSize: 15, fontWeight: "700" },
  deviceMeta: { fontSize: 12, marginTop: 1 },
  sendBtn: {
    borderRadius: 14,
    paddingVertical: 15,
    alignItems: "center",
    justifyContent: "center",
  },
  sendText: { fontSize: 16, fontWeight: "800" },
});
