// Slim pill pinned just above the tab bar that surfaces tasks
// running on the current agent while the user is on any other tab
// (Projects, Devices, More, …). Tap → routes to the Tasks tab and
// publishes an open-task intent so the chat-detail modal opens
// directly on the running task. The list view alone wasn't enough
// of a signal — once the user navigated away there was no
// indication anything was still going.
//
// Polling-based (5 s) instead of pushed: the QUIC streaming output
// listener is bound to the Tasks screen's effect lifecycle, so the
// pill needs its own cheap heartbeat. listTasks is small + cached
// agent-side; the cost is negligible.

import React, { useEffect, useState } from "react";
import { useRouter, usePathname } from "expo-router";
import { Animated, Easing, Pressable, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { quicClient, Task } from "../lib/quic";
import { useColors } from "../context/ThemeContext";
import { useDevice } from "../context/DeviceContext";
import { openTaskBus } from "../lib/runningTasksBus";

const TAB_BAR_HEIGHT = 68;

export function RunningTasksPill() {
  const c = useColors();
  const router = useRouter();
  const pathname = usePathname();
  const insets = useSafeAreaInsets();
  const { connectionStatus, activeDevice, connectedDeviceIds } = useDevice();
  // Pool-aware: pill polls if EITHER the focused device is connected,
  // OR any other pooled box is. Without this, switching focus to a
  // box mid-task would silently kill the cross-tab "running" indicator
  // even though the original task keeps streaming on the previous
  // (now-pooled) connection.
  const isConnected = (connectionStatus === "connected" && !!activeDevice) || connectedDeviceIds.length > 0;
  const [running, setRunning] = useState<Task[]>([]);
  const dot = React.useRef(new Animated.Value(0.35)).current;

  useEffect(() => {
    if (!isConnected) { setRunning([]); return; }
    let mounted = true;
    const poll = async () => {
      try {
        const list = await quicClient.listTasks();
        const open = list.filter((t) => t.status === "running" || t.status === "queued");
        if (mounted) setRunning(open);
      } catch {
        if (mounted) setRunning([]);
      }
    };
    poll();
    const t = setInterval(poll, 5000);
    return () => { mounted = false; clearInterval(t); };
  }, [isConnected]);

  useEffect(() => {
    if (running.length === 0) return;
    const loop = Animated.loop(
      Animated.sequence([
        Animated.timing(dot, { toValue: 1, duration: 700, easing: Easing.inOut(Easing.quad), useNativeDriver: true }),
        Animated.timing(dot, { toValue: 0.35, duration: 700, easing: Easing.inOut(Easing.quad), useNativeDriver: true }),
      ]),
    );
    loop.start();
    return () => loop.stop();
  }, [running.length, dot]);

  if (running.length === 0) return null;
  // Suppress while the chat-detail modal is plausibly already up:
  // the user is on the Tasks tab and not the list-only state. We
  // can't introspect the modal from here; treat any /tasks pathname
  // as "they're already in the right place" and hide.
  if (pathname?.startsWith("/tasks") || pathname === "/(tabs)/tasks") return null;

  const first = running[0];
  const runner = first.runnerId || "agent";
  const device = first.deviceName || activeDevice?.name || "remote";
  const title = (first.title || "running").trim();
  const summary = running.length > 1
    ? `${runner} · ${device} +${running.length - 1} more`
    : `${runner} · ${device}`;

  return (
    <View
      pointerEvents="box-none"
      style={{
        position: "absolute",
        left: 0,
        right: 0,
        bottom: TAB_BAR_HEIGHT + (insets.bottom ?? 0),
        paddingHorizontal: 12,
        paddingBottom: 6,
      }}
    >
      <Pressable
        onPress={() => {
          openTaskBus.publish(first.id);
          try { router.push("/(tabs)/tasks" as any); } catch {}
        }}
        style={({ pressed }) => ({
          backgroundColor: c.bgCard,
          borderColor: c.border,
          borderWidth: 1,
          borderRadius: 12,
          paddingHorizontal: 12,
          paddingVertical: 9,
          flexDirection: "row",
          alignItems: "center",
          opacity: pressed ? 0.85 : 1,
          shadowColor: "#000",
          shadowOpacity: 0.18,
          shadowRadius: 8,
          shadowOffset: { width: 0, height: 4 },
          elevation: 4,
        })}
      >
        <Animated.View style={{
          width: 8,
          height: 8,
          borderRadius: 4,
          backgroundColor: c.accent,
          opacity: dot,
          marginRight: 9,
        }} />
        <View style={{ flex: 1, minWidth: 0 }}>
          <Text numberOfLines={1} style={{ color: c.textPrimary, fontSize: 13, fontWeight: "600" }}>
            {title}
          </Text>
          <Text numberOfLines={1} style={{ color: c.textMuted, fontSize: 11, marginTop: 1 }}>
            {summary}
          </Text>
        </View>
        <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600", marginLeft: 8 }}>
          Open
        </Text>
      </Pressable>
    </View>
  );
}
