import { Tabs, useRouter } from "expo-router";
import React, { useEffect, useRef, useState } from "react";
import { Alert, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import * as ExpoDevice from "expo-device";
import { Ionicons } from "@expo/vector-icons";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import { YaverGlass } from "../../src/components/YaverGlass";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import { loadApp } from "../../src/lib/bundleLoader";
import { openAppBus } from "../../src/lib/openAppBus";
import { typography } from "../../src/theme/tokens";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";

// (DeviceAttentionBanner / HeaderWithBanner removed — see commit
// notes. Recovery is now silent: the agent and the per-tab UI hooks
// kick recoverDeviceAuth themselves when needed, and on hard auth
// failures we route the user through the normal Yaver web OAuth
// flow rather than surfacing a confusing global "Reclaim" CTA.)

function TabIcon({ label, focused, showGreenDot }: { label: string; focused: boolean; showGreenDot?: boolean }) {
  const c = useColors();
  // One consistent line-icon family across the bar — outline when
  // inactive, solid when active. Avoids the old mismatch where some tabs
  // used heavy "-circle" glyphs and others a bare bolt, which read as
  // ugly/unrhythmic. Active state is a clean accent tint (icon + label),
  // iOS-native style — no boxy pill behind the glyph.
  const icons: Record<string, { on: keyof typeof Ionicons.glyphMap; off: keyof typeof Ionicons.glyphMap }> = {
    Reload: { on: "refresh", off: "refresh-outline" },
    Tasks: { on: "list", off: "list-outline" },
    Todos: { on: "checkbox", off: "square-outline" },
    Projects: { on: "apps", off: "apps-outline" },
    Shortcuts: { on: "flash", off: "flash-outline" },
    Repos: { on: "folder", off: "folder-outline" },
    Builds: { on: "hammer", off: "hammer-outline" },
    Devices: { on: "desktop", off: "desktop-outline" },
    More: { on: "ellipsis-horizontal", off: "ellipsis-horizontal" },
    Settings: { on: "settings", off: "settings-outline" },
  };
  const glyph = icons[label] ?? { on: "ellipse", off: "ellipse-outline" };
  return (
    <View style={styles.tabIconWrap}>
      <View style={styles.iconSlot}>
        <Ionicons
          name={focused ? glyph.on : glyph.off}
          size={24}
          color={focused ? c.accent : c.tabInactive}
        />
        {showGreenDot && (
          <View style={[styles.greenDot, { borderColor: c.bgTabBar }]} />
        )}
      </View>
      <Text
        numberOfLines={1}
        allowFontScaling={false}
        // Active state is conveyed by the accent COLOR only — NOT a
        // heavier font weight. Bumping "Shortcuts" to 600 when selected
        // widens the glyphs just enough to overflow the tab slot, which
        // is what forced the label onto a second line / ellipsized the
        // trailing "s". Keep the weight constant so the longest label
        // fits identically whether or not it's the active tab.
        style={[styles.tabLabel, { color: focused ? c.accent : c.tabInactive }]}
      >
        {label}
      </Text>
    </View>
  );
}

export default function TabLayout() {
  const c = useColors();
  const { isDark } = useTheme();
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
  const insets = useSafeAreaInsets();
  // Tablet portrait still uses the bottom bar, but at a phone-sized
  // 64pt with paddingTop:0 the icons jam against the top edge —
  // there's plenty of vertical space on a tablet, so size the bar
  // for the form factor instead. iOS pads via the safe-area inset
  // automatically; on Android we add it ourselves.
  const isTabletPortrait = layout.layoutClass === "tablet-portrait";
  const bottomBarHeight = isTabletPortrait ? 76 : 64;
  const bottomBarPaddingBottom = Math.max(insets.bottom, isTabletPortrait ? 4 : 0);
  const bottomBarPaddingTop = isTabletPortrait ? 8 : 6;


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
        // Loading a guest bundle into the container needs the native
        // YaverBundleLoader module, which only ships on iOS today. On Android
        // surface a brief message (and report it back) instead of silently
        // swallowing the "native module not available" throw below.
        if (Platform.OS === "android") {
          Alert.alert(
            "iOS-Only For Now",
            "Loading apps inside Yaver is iOS-only today. This Android device can't mount the requested bundle. Open it on an iPhone or iPad instead.",
          );
          await quicClient.pushBlackBoxEvents(resolved.id, [{
            type: "state",
            level: "warn",
            message: "preview_worker_bundle_load_unsupported_platform",
            timestamp: Date.now(),
            metadata: { platform },
          }]);
          return;
        }
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
        // Liquid Glass tab bar — iOS 26+ gets real glass, iOS 18-25
        // BlurView, Android Material 3 surface (per spatial_constraints
        // memory: don't port Liquid Glass to Android). Tab bar BG is
        // transparent so the YaverGlass underlay shows through.
        //
        // borderRadius:0 is REQUIRED here. YaverGlass defaults to a
        // 12pt corner radius (it's normally a floating card/sheet), but
        // as a full-width tab-bar underlay that rounds the blur's
        // corners against the black screen behind it — rendering an ugly
        // floating rounded-rectangle "frame" instead of a clean
        // edge-to-edge bar. Flatten the corners so it sits flush like a
        // native iOS tab bar.
        tabBarBackground: () => (
          <YaverGlass
            style={[StyleSheet.absoluteFillObject, { borderRadius: 0 }] as any}
            tint={c.bgTabBar}
          />
        ),
        tabBarStyle: useLeftRail
          ? {
              backgroundColor: "transparent",
              borderRightColor: c.borderSubtle,
              borderRightWidth: 1,
              borderTopWidth: 0,
              width: 104,
              paddingTop: 16,
            }
          : {
              backgroundColor: "transparent",
              borderTopColor: c.borderSubtle,
              borderTopWidth: isDark ? StyleSheet.hairlineWidth : 0,
              height: bottomBarHeight + bottomBarPaddingBottom,
              paddingTop: bottomBarPaddingTop,
              paddingBottom: bottomBarPaddingBottom,
              shadowColor: !isDark ? c.shadowSm : "transparent",
              shadowOffset: { width: 0, height: -6 },
              shadowOpacity: 0.14,
              shadowRadius: 14,
              elevation: 8,
            },
        // TabIcon renders its own label as a child Text — telling
        // react-navigation to hide its label slot frees the vertical
        // space that was forcing icons to the top of the bar.
        tabBarShowLabel: false,
        tabBarActiveTintColor: c.tabActive,
        tabBarInactiveTintColor: c.tabInactive,
        tabBarItemStyle: useLeftRail
          ? { height: 64 }
          : isTabletPortrait
          ? { paddingVertical: 0 }
          : undefined,
      }}
    >
      <Tabs.Screen
        name="hotreload"
        options={{
          // One-word label — "Hot Reload" wraps to two lines on every
          // tab on a phone, while every other tab is single-word, so it
          // sticks out. Route + screen file name stay `hotreload` to
          // avoid breaking deeplinks (yaver://hotreload, sentry / convex
          // event slugs, etc.).
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
            <TabIcon label="Projects" focused={focused} />
          ),
        }}
      />
      <Tabs.Screen
        name="shortcuts"
        options={{
          title: "Shortcuts",
          tabBarIcon: ({ focused }) => (
            <TabIcon label="Shortcuts" focused={focused} />
          ),
        }}
      />
      <Tabs.Screen name="builds" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="publish" options={{ href: null, headerShown: false }} />
      <Tabs.Screen name="shots" options={{ href: null, headerShown: false }} />
      {/* Devices moved out of the bottom bar into More (more.tsx links to
          it) — the 6-tab bar was crowded enough that "Shortcuts" wrapped
          to two lines. Device *management* is occasional; the per-session
          device target is already pickable from the header status strip on
          Tasks. Reached from More, so it gets a back button like runs/monitor. */}
      <Tabs.Screen
        name="devices"
        options={{ href: null, title: "Devices", headerShown: false }}
      />
      <Tabs.Screen
        name="screenlog"
        options={{ href: null, title: "Screen Monitor", headerShown: false }}
      />
      <Tabs.Screen
        name="mesh"
        options={{ href: null, title: "Yaver Mesh", headerShown: false }}
      />
      <Tabs.Screen
        name="mesh-node"
        options={{ href: null, title: "Node", headerShown: false }}
      />
      <Tabs.Screen
        name="mesh-exit"
        options={{ href: null, title: "Exit node", headerShown: false }}
      />
      <Tabs.Screen
        name="mesh-access"
        options={{ href: null, title: "Access rules", headerShown: false }}
      />
      <Tabs.Screen
        name="mesh-share"
        options={{ href: null, title: "Sharing", headerShown: false }}
      />
      <Tabs.Screen
        name="robot"
        options={{ href: null, title: "Robot Cell", headerShown: false }}
      />
      <Tabs.Screen
        name="more"
        options={{
          title: "More",
          tabBarIcon: ({ focused }) => <TabIcon label="More" focused={focused} />,
        }}
      />
      <Tabs.Screen name="dogfood" options={{ href: null, headerShown: false }} />
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
        options={{ href: null, title: "Local CI", headerShown: false }}
      />
      <Tabs.Screen
        name="monitor"
        options={{ href: null, title: "Monitor", headerShown: false }}
      />
      <Tabs.Screen
        name="agent"
        options={{ href: null, title: "Agent Mode", headerShown: false }}
      />
    </Tabs>
  );
}

const styles = StyleSheet.create({
  tabIconWrap: { alignItems: "center", justifyContent: "center", minWidth: 56, paddingTop: 2, gap: 3 },
  // Plain centered slot for the glyph — no pill. Active state is conveyed
  // by the accent tint + solid icon, matching iOS-native tab bars.
  iconSlot: {
    width: 44,
    height: 28,
    alignItems: "center",
    justifyContent: "center",
  },
  // numberOfLines={1} on the <Text> keeps the longest label ("Shortcuts")
  // on one line; 11pt + tight tracking guarantees it fits the ~78pt tab
  // slot on a 390pt iPhone (14 / 13 / 12) without ellipsizing or wrapping.
  tabLabel: { marginTop: 1, fontSize: 11, letterSpacing: -0.2 },
  greenDot: {
    position: "absolute",
    top: 1,
    right: 6,
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: "#22c55e",
    borderWidth: 1.5,
  },
});
