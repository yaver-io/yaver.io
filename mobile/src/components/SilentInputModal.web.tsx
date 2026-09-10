/**
 * Browser capability boundary for Silent Input.
 *
 * react-native-vision-camera throws while its module is imported on web, so
 * this sibling must never import the native camera implementation. Metro
 * selects it for RN-web, keeping Tasks, Dogfood, auth, and unrelated browser
 * automation usable. Camera capture remains a native iOS capability.
 */
import React from "react";
import { Modal, Pressable, StyleSheet, Text, View } from "react-native";
import type { SilentInputModalProps } from "./SilentInputModal.types";

export function SilentInputModal({ visible, colors: c, onCancel, onConfigure }: SilentInputModalProps) {
  return (
    <Modal visible={visible} animationType="fade" transparent onRequestClose={onCancel}>
      <View style={styles.backdrop}>
        <View style={[styles.panel, { backgroundColor: c.bgCard, borderColor: c.border }]} accessibilityLabel="Silent Input browser limitation">
          <Text style={[styles.title, { color: c.textPrimary }]}>Camera capture needs the native Yaver app</Text>
          <Text accessibilityRole="alert" style={[styles.body, { color: c.textSecondary }]}>Silent Input cannot use a camera in RN-web or browser automation. Tasks, Dogfood, and every non-camera feature remain available here. Test lip reading on a physical iPhone.</Text>
          <View style={styles.actions}>
            {onConfigure ? (
              <Pressable accessibilityRole="button" accessibilityLabel="Configure Silent Input" onPress={onConfigure} style={styles.action}>
                <Text style={{ color: c.accent, fontWeight: "700" }}>Configure</Text>
              </Pressable>
            ) : null}
            <Pressable accessibilityRole="button" accessibilityLabel="Close Silent Input browser limitation" onPress={onCancel} style={styles.action}>
              <Text style={{ color: c.accent, fontWeight: "700" }}>Close</Text>
            </Pressable>
          </View>
        </View>
      </View>
    </Modal>
  );
}

const styles = StyleSheet.create({
  backdrop: { flex: 1, alignItems: "center", justifyContent: "center", padding: 24, backgroundColor: "rgba(0,0,0,0.55)" },
  panel: { width: "100%", maxWidth: 440, borderWidth: 1, borderRadius: 16, padding: 20 },
  title: { fontSize: 18, lineHeight: 24, fontWeight: "800" },
  body: { fontSize: 14, lineHeight: 21, marginTop: 10 },
  actions: { flexDirection: "row", justifyContent: "flex-end", gap: 8, marginTop: 16 },
  action: { minHeight: 44, justifyContent: "center", paddingHorizontal: 12 },
});
