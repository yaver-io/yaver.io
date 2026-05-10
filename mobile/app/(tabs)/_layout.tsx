import { Tabs, useRouter } from "expo-router";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { Pressable, StyleSheet, Text, View } from "react-native";
import * as ExpoDevice from "expo-device";
import { Ionicons } from "@expo/vector-icons";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import { loadApp } from "../../src/lib/bundleLoader";
import { openAppBus } from "../../src/lib/openAppBus";
import { AppBackButton } from "../../src/components/AppBackButton";
import { typography } from "../../src/theme/tokens";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";

// (DeviceAttentionBanner / HeaderWithBanner removed — see commit
// notes. Recovery is now silent: the agent and the per-tab UI hooks
// kick recoverDeviceAuth themselves when needed, and on hard auth
// failures we route the user through the normal Yaver web OAuth
// flow rather than surfacing a confusing global "Reclaim" CTA.)

function TabIcon({ label, focused, showGreenDot }: { label: string; focused: boolean; showGreenDot?: boolean }) {
  const c = useColors();
  const icons: Record<string, { on: keyof typeof Ionicons.glyphMap; off: keyof typeof Ionicons.glyphMap }> = {
    Reload: { on: "refresh-circle", off: "refresh-circle-outline" },
    Tasks: { on: "checkmark-circle", off: "checkmark-circle-outline" },
    Todos: { on: "checkbox", off: "square-outline" },
    Projects: { on: "play-circle", off: "play-circle-outline" },
    Repos: { on: "folder", off: "folder-outline" },
    Builds: { on: "hammer", off: "hammer-outline" },
    Devices: { on: "desktop", off: "desktop-outline" },
    More: { on: "ellipsis-horizontal-circle", off: "ellipsis-horizontal-circle-outline" },
    Settings: { on: "settings", off: "settings-outline" },
  };
  const glyph = icons[label] ?? { on: "ellipse", off: "ellipse-outline" };
  // Accent-tinted pill bg behind the focused icon glyph (Material 3 /
  // Linear pattern). Replaces the prior 24x2 indicator bar floating
  // above the icon — that read as a stuck artifact on iOS where no
  // first-party app uses that affordance. Inactive: bare wrapper.
  return (
    <View style={styles.tabIconWrap}>
      <View
        style={[
          styles.iconPill,
          focused
            ? { backgroundColor: c.accent + "1A" }
            : null,
        ]}
      >
        <Ionicons
          name={focused ? glyph.on : glyph.off}
          size={20}
          color={focused ? c.accent : c.tabInactive}
        />
        {showGreenDot && (
          <View style={[styles.greenDot, { borderColor: c.bgTabBar }]} />
        )}
      </View>
      <Text style={[styles.tabLabel, { color: focused ? c.accent : c.tabInactive, fontWeight: focused ? "600" : "400" }]}>
        {label}
      </Text>
    </View>
  );
}

export default function TabLayout() {
  const c = useColors();
  const router = useRouter();
  const layout = useResponsiveLayout();
  const { connectionStatus, activeDevice, devices } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;
  const [devServerRunning, setDevServerRunning] = useState(false);
  const wasRunning = useRef(false);

  // Tablet landscape gets a left navigation rail; everywhere else
  // keeps the bottom tab bar. expo-router's <Tabs> exposes
  // tabBarPosition='left' in v3+; we set it via screenOptions.
  const useLeftRail = layout.layoutClass === "tablet-landscape";

  const backToMore = useCallback(
    () => (
      <AppBackButton onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingLeft: 14 }} />
    ),
    [router],
  );

  // Poll dev server status — drives the green-dot badge on the Hot
  // Reload + Projects tabs. We INTENTIONALLY do not auto-navigate to
  // any tab when the dev server starts. The previous behaviour ripped
  // the user out of Hot Reload (where they had just tapped "Open in
  // Yaver" and were watching progress) the moment Metro flipped to
  // running, dumping them on Projects with a stale state and a "no
  // device selected" appearance — even though the loading was healthy
  // in the tab they came from. Loading state belongs to the tab the
  // user is on; navigation is the user's call.
  useEffect(() => {
    if (!isConnected) {
      setDevServerRunning(false);
      wasRunning.current = false;
      return;
    }
    let mounted = true;
    const poll = async () => {
      try {
        const status = await quicClient.getDevServerStatus();
        const running = status?.running === true;
        if (mounted) {
          setDevServerRunning(running);
          if (running) wasRunning.current = true;
          if (!running) wasRunning.current = false;
        }
      } catch {
        if (mounted) setDevServerRunning(false);
      }
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => { mounted = false; clearInterval(interval); };
  }, [isConnected]);

  useEffect(() => {
    if (!isConnected) return;

    const platform = ExpoDevice.osName?.toLowerCase().includes("ios") ? "ios" : "android";
    const nameCandidates = [
      ExpoDevice.deviceName,
      ExpoDevice.modelName,
    ]
      .map((value) => (value || "").trim().toLowerCase())
      .filter(Boolean);

    const edgeMobiles = devices.filter((device) => device.deviceClass === "edge-mobile");
    const samePlatform = edgeMobiles.filter((device) => (device.os || "").toLowerCase().includes(platform));
    const resolved =
      samePlatform.find((device) => nameCandidates.some((name) => device.name.toLowerCase() === name)) ||
      samePlatform.find((device) => nameCandidates.some((name) => device.name.toLowerCase().includes(name) || name.includes(device.name.toLowerCase()))) ||
      (samePlatform.length === 1 ? samePlatform[0] : null);

    if (!resolved?.id) return;

    void quicClient.pushBlackBoxEvents(resolved.id, [{
      type: "state",
      level: "info",
      message: "preview_worker_command_channel_connected",
      timestamp: Date.now(),
      metadata: { app: "yaver-mobile", platform },
    }]);

    const unsubscribe = quicClient.streamBlackBoxCommands(resolved.id, async (event) => {
      const command = event.command?.command;
      const data = event.command?.data || {};
      if (!command) return;

      if (command === "reload_bundle" || command === "reload") {
        const bundlePath = typeof data.bundleUrl === "string"
          ? data.bundleUrl
          : "/dev/native-bundle";
        const moduleName = typeof data.moduleName === "string" ? data.moduleName : "main";
        try {
          await loadApp(`${quicClient.baseUrl}${bundlePath}`, moduleName, quicClient.getAuthHeaders());
          await quicClient.pushBlackBoxEvents(resolved.id, [{
            type: "state",
            level: "info",
            message: "preview_worker_bundle_loaded",
            timestamp: Date.now(),
            metadata: { bundleUrl: bundlePath, moduleName },
          }]);
        } catch (error: any) {
          await quicClient.pushBlackBoxEvents(resolved.id, [{
            type: "error",
            level: "error",
            message: `preview_worker_bundle_load_failed: ${error?.message || "unknown error"}`,
            timestamp: Date.now(),
            metadata: { bundleUrl: bundlePath, moduleName },
          }]);
        }
        return;
      }

      if (command === "open_app") {
        const app = typeof data.app === "string" ? data.app.trim() : "";
        if (!app) return;
        try {
          router.push("/(tabs)/apps");
        } catch {
          // older expo-router/no-navigator fallback — ignore.
        }
        openAppBus.publish(app);
        await quicClient.pushBlackBoxEvents(resolved.id, [{
          type: "state",
          level: "info",
          message: "preview_worker_open_app_received",
          timestamp: Date.now(),
          metadata: { app },
        }]);
        return;
      }

      if (command === "capture_screenshot") {
        await quicClient.pushBlackBoxEvents(resolved.id, [{
          type: "state",
          level: "info",
          message: "preview_worker_capture_requested",
          timestamp: Date.now(),
          metadata: { supported: false, reason: "screenshot-capture-not-wired-yet" },
        }]);
      }
    });

    return unsubscribe;
  }, [isConnected, devices]);

  return (
    <Tabs
      screenOptions={{
        headerStyle: { backgroundColor: c.bg },
        headerTintColor: c.textPrimary,
        headerTitleStyle: { ...typography.navTitle, color: c.textPrimary },
        tabBarPosition: useLeftRail ? "left" : "bottom",
        tabBarStyle: useLeftRail
          ? {
              backgroundColor: c.bgTabBar,
              borderRightColor: c.borderSubtle,
              borderRightWidth: 1,
              borderTopWidth: 0,
              width: 104,
              paddingTop: 12,
            }
          : {
              backgroundColor: c.bgTabBar,
              borderTopColor: c.borderSubtle,
              borderTopWidth: StyleSheet.hairlineWidth,
              height: 64,
              paddingTop: 0,
            },
        tabBarLabel: () => null,
        tabBarActiveTintColor: c.tabActive,
        tabBarInactiveTintColor: c.tabInactive,
        tabBarItemStyle: useLeftRail ? { height: 64 } : undefined,
      }}
    >
      <Tabs.Screen
        name="hotreload"
        options={{
          // One-word label — "Hot Reload" wraps to two lines on every
          // tab on a phone, while every other tab is single-word, so it
          // sticks out. Route + screen file name stay `hotreload` to
          // avoid breaking deeplinks (yaver://hotreload, sentry / convex
          // event slugs, autodev session naming, etc.).
          title: "Reload",
          tabBarIcon: ({ focused }) => (
            <TabIcon label="Reload" focused={focused} showGreenDot={devServerRunning} />
          ),
        }}
      />
      <Tabs.Screen
        name="tasks"
        options={{
          title: "Tasks",
          tabBarIcon: ({ focused }) => <TabIcon label="Tasks" focused={focused} />,
        }}
      />
      <Tabs.Screen
        name="apps"
        options={{
          title: "Projects",
          tabBarIcon: ({ focused }) => (
            <TabIcon label="Projects" focused={focused} showGreenDot={devServerRunning} />
          ),
        }}
      />
      <Tabs.Screen name="builds" options={{ href: null, headerShown: false }} />
      <Tabs.Screen
        name="devices"
        options={{
          title: "Devices",
          tabBarIcon: ({ focused }) => <TabIcon label="Devices" focused={focused} />,
        }}
      />
      <Tabs.Screen
        name="more"
        options={{
          title: "More",
          tabBarIcon: ({ focused }) => <TabIcon label="More" focused={focused} />,
        }}
      />
      <Tabs.Screen name="todos" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="healthmon" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="files" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="newproject" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="designmode" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="gitproviders" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="guests" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="solostack" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="mail" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="studio" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="qualitygates" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="tutorials" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="settings" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="ops" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="infra" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="data" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="console" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="home" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="terminal" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="project" options={{ href: null, headerShown: false }} />
      <Tabs.Screen
        name="runs"
        options={{ href: null, title: "Local CI", headerShown: true, headerLeft: backToMore }}
      />
      <Tabs.Screen
        name="autodev"
        options={{ href: null, title: "Auto Dev", headerShown: true, headerLeft: backToMore }}
      />
      <Tabs.Screen
        name="monitor"
        options={{ href: null, title: "Monitor", headerShown: true, headerLeft: backToMore }}
      />
      <Tabs.Screen
        name="agent"
        options={{ href: null, title: "Agent Mode", headerShown: true, headerLeft: backToMore }}
      />
    </Tabs>
  );
}

const styles = StyleSheet.create({
  tabIconWrap: { alignItems: "center", justifyContent: "center", minWidth: 56, paddingTop: 4 },
  // Pill behind the icon glyph; only painted on focus (background set
  // inline). 48x28 / radius 14 mirrors the Material 3 active-tab
  // indicator and works in both themes via accent + low alpha.
  iconPill: {
    width: 48,
    height: 28,
    borderRadius: 14,
    alignItems: "center",
    justifyContent: "center",
  },
  tabLabel: { marginTop: 3, fontSize: 12 },
  greenDot: {
    position: "absolute",
    top: -2,
    right: 4,
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: "#22c55e",
    borderWidth: 1.5,
  },
});
