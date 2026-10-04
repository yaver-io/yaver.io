import { Ionicons } from "@expo/vector-icons";
import React, { useMemo, useState } from "react";
import {
  KeyboardAvoidingView,
  Alert,
  Modal,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import {
  DogfoodEntryIcon,
  DogfoodControlMenu,
  type DogfoodRenderBehavior,
  type DogfoodSessionBehavior,
  type DogfoodUsageMode,
  type DogfoodFailure,
  type DogfoodLane,
  type DogfoodLogLine,
  type DogfoodPhase,
} from "../../../sdk/feedback/react-native/src";
import { connectionManager } from "../lib/connectionManager";
import { mobileSessionSettings } from "../lib/appVersion";
import { useDevice } from "../context/DeviceContext";
import { StudioChatPane } from "./studio/StudioChatPane";

type ReloadKind = "fast" | "full";

/**
 * Compatibility boundary for preview call sites.
 *
 * The Y is the persistent control for a running Dogfood session. It opens an
 * in-place sheet; it never exits or navigates away on its own. Message owns the
 * compact composer and Settings owns reload/navigation/exit, so destructive
 * session teardown is always an explicit labelled action.
 */
export function BrowserVibeBubble({
  onExitPreview,
  onGoHome,
  onOpenSettings,
  exitLabel = "Open Dogfood",
  endLabel = "Exit Dogfood",
  projectPath,
  projectName,
  deviceId,
  deviceName,
  runner,
  usageMode = "reload-and-chat",
  sessionBehavior = "resume-last",
  onReload,
  reloadBusy = false,
}: {
  projectPath?: string;
  projectName?: string;
  onExitPreview: () => void;
  onGoHome?: () => void;
  onOpenSettings?: () => void;
  deviceId?: string;
  deviceName?: string;
  runner?: string;
  exitLabel?: string;
  endLabel?: string;
  onReload: (kind: ReloadKind) => boolean | void | Promise<boolean | void>;
  reloadBusy?: boolean;
  onFixException?: () => void | Promise<void>;
  exceptionFixBusy?: boolean;
  usageMode?: DogfoodUsageMode;
  renderBehavior?: DogfoodRenderBehavior;
  sessionBehavior?: DogfoodSessionBehavior;
  reloadProgress?: {
    lane: DogfoodLane;
    phase: DogfoodPhase;
    message: string;
    logs: readonly DogfoodLogLine[];
    failure?: DogfoodFailure;
  };
}) {
  const insets = useSafeAreaInsets();
  const { connectedDeviceIds } = useDevice();
  const [menuOpen, setMenuOpen] = useState(false);
  const [chatOpen, setChatOpen] = useState(false);
  const taskClient = useMemo(
    () => deviceId ? connectionManager.clientFor(deviceId) : connectionManager.runnerClient(),
    [deviceId],
  );
  const connected = deviceId ? connectedDeviceIds.includes(deviceId) : taskClient.isConnected;
  const sessionSettings = useMemo(() => mobileSessionSettings({
    surface: "yaver-mobile-dogfood",
    lane: "browser",
    dogfood: true,
    usageMode,
  }), [usageMode]);

  return (
    <>
      <DogfoodEntryIcon
        accessibilityLabel={exitLabel}
        preferenceScope="io.yaver.mobile:native"
        onPress={() => setMenuOpen(true)}
      />
      <DogfoodControlMenu
        visible={menuOpen}
        usageMode={usageMode}
        busy={reloadBusy ? "reload" : null}
        onChat={() => { setMenuOpen(false); setChatOpen(true); }}
        onReload={() => { void onReload("fast"); }}
        onSettings={() => { setMenuOpen(false); (onOpenSettings || onGoHome)?.(); }}
        onExit={() => {
          Alert.alert(
            "Exit Dogfood?",
            "Return to the normal installed app. Your source changes and coding sessions stay untouched.",
            [
              { text: "Cancel", style: "cancel" },
              { text: endLabel, style: "destructive", onPress: () => { setMenuOpen(false); onExitPreview(); } },
            ],
          );
        }}
        onDismiss={() => setMenuOpen(false)}
      />
      <Modal visible={chatOpen} animationType="slide" presentationStyle="pageSheet" onRequestClose={() => setChatOpen(false)}>
        <KeyboardAvoidingView
          style={styles.sheet}
          behavior={Platform.OS === "ios" ? "padding" : undefined}
        >
          <View style={[styles.header, { paddingTop: Math.max(insets.top, 12) }]}>
            <View style={styles.headerCopy}>
              <Text style={styles.title}>Dogfood</Text>
              <Text style={styles.subtitle} numberOfLines={1}>{projectName || "Yaver"}{deviceName ? ` · ${deviceName}` : ""}</Text>
            </View>
            <Pressable accessibilityRole="button" accessibilityLabel="Close Dogfood message composer" onPress={() => setChatOpen(false)} style={styles.closeButton}>
              <Ionicons name="close" size={22} color="#666673" />
            </Pressable>
          </View>
          <StudioChatPane
            compact
            feedbackStyle
            projectPath={projectPath}
            projectName={projectName}
            runner={runner}
            client={taskClient}
            clientConnected={connected}
            codingMachineName={deviceName}
            initialSessionBehavior={sessionBehavior}
            sessionSettings={sessionSettings}
          />
        </KeyboardAvoidingView>
      </Modal>
    </>
  );
}

const styles = StyleSheet.create({
  sheet: { flex: 1, backgroundColor: "#f8f8fb" },
  header: { minHeight: 68, paddingHorizontal: 16, paddingBottom: 10, flexDirection: "row", alignItems: "center", borderBottomWidth: StyleSheet.hairlineWidth, borderBottomColor: "#dedee7" },
  headerCopy: { flex: 1, minWidth: 0 },
  title: { color: "#17171d", fontSize: 19, fontWeight: "800" },
  subtitle: { color: "#777782", fontSize: 11, marginTop: 2 },
  closeButton: { width: 42, height: 42, borderRadius: 21, alignItems: "center", justifyContent: "center", backgroundColor: "#ececf2" },
});
