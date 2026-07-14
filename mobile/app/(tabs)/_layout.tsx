import { Redirect, Tabs, useRouter } from "expo-router";
import React, { useEffect, useRef, useState } from "react";
import { Alert, Platform, Pressable, StyleSheet, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import * as ExpoDevice from "expo-device";
import { Ionicons } from "@expo/vector-icons";
import { useColors, useTheme } from "../../src/context/ThemeContext";
import { YaverGlass } from "../../src/components/YaverGlass";
import { useAuth } from "../../src/context/AuthContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";
import { isBundleLoaderAvailable, loadApp } from "../../src/lib/bundleLoader";
import { openAppBus } from "../../src/lib/openAppBus";
import { typography } from "../../src/theme/tokens";
import { useResponsiveLayout } from "../../src/hooks/useResponsiveLayout";

// (DeviceAttentionBanner / HeaderWithBanner removed — see commit
// notes. Recovery is now silent: the agent and the per-tab UI hooks
// kick recoverDeviceAuth themselves when needed, and on hard auth
// failures we route the user through the normal Yaver web OAuth
// flow rather than surfacing a confusing global "Reclaim" CTA.)

function TabIcon({ label, focused, showGreenDot, rail }: { label: string; focused: boolean; showGreenDot?: boolean; rail?: boolean }) {
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
  // Expanded landscape rail: icon + label sit side-by-side in a wide
  // row with a subtle accent pill behind the active item — the desktop
  // navigator layout the `rail.expandedWidth` token was reserved for.
  // Bottom bar / portrait keep the compact stacked icon-over-label.
  return (
    <View
      style={[
        styles.tabIconWrap,
        rail && styles.tabIconWrapRail,
        rail && focused && { backgroundColor: c.accent + "1f" },
      ]}
    >
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
        style={[styles.tabLabel, rail && styles.tabLabelRail, { color: focused ? c.accent : c.tabInactive }]}
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
  // Auth invariant: the tab shell must NEVER stay mounted without a valid
  // token. `app/index.tsx` only gates on mount, so when a token is later
  // revoked/rotated/dropped (e.g. a 401 on /auth/refresh during app-resume
  // flips isAuthenticated=false while the user is already inside the tabs),
  // nothing re-routed them — they were stranded on a signed-in-looking shell
  // where every /devices/list poll hit `no token, skipping` and spun
  // "Reconnecting…" forever. Re-gating here sends any auth loss straight
  // back to sign-in so the session self-heals instead of dead-ending.
  const { isAuthenticated, isLoading: authLoading } = useAuth();
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
        // YaverBundleLoader module (iOS + Android). Guard on the capability so
        // a build / web preview without the module reports back cleanly
        // instead of swallowing the "native module not available" throw below.
        if (!isBundleLoaderAvailable()) {
          Alert.alert(
            "Bundle Loader Unavailable",
            "This build of Yaver can't mount the requested bundle. Update Yaver to the latest version and try again.",
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

      // A box crossed 85% / 95% full. On a headless machine this alert has
      // nowhere else to land — stderr reaches nobody. Surface it here with the
      // reclaimable figure attached, so it's an offer to act rather than just
      // bad news: tapping through opens the box's Storage panel.
      if (command === "storage_pressure") {
        const alerts: string[] = Array.isArray(data.alerts) ? data.alerts : [];
        if (!alerts.length) return;
        const reclaimable =
          typeof data.reclaimable === "string" && data.reclaimable !== "0 B"
            ? `\n\n${data.reclaimable} of build caches can be reclaimed.`
            : "";
        Alert.alert(
          `${data.hostname || "A box"} is running out of space`,
          `${alerts.join("\n")}${reclaimable}`,
          [
            { text: "Later", style: "cancel" },
            {
              text: "Review",
              onPress: () => {
                try {
                  router.push("/(tabs)/devices");
                } catch {
                  // older expo-router/no-navigator fallback — ignore.
                }
              },
            },
          ]
        );
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

  // Re-gate after every hook has run (keeps hook order stable). Any auth
  // loss while inside the tabs routes back to sign-in instead of stranding
  // the user on a tokenless shell. `authLoading` guards the boot window so
  // a slow token-restore doesn't flash the login screen.
  if (!authLoading && !isAuthenticated) {
    return <Redirect href="/login" />;
  }

  return (
    <Tabs
      screenOptions={{
        headerStyle: { backgroundColor: c.bg },
        headerTintColor: c.textPrimary,
        headerTitleStyle: { ...typography.navTitle, color: c.textPrimary },
        tabBarPosition: useLeftRail ? "left" : "bottom",
        // Tab bar background — a clean, SOLID flat bar that matches the
        // app background, restoring the pre-Liquid-Glass look.
        //
        // We deliberately DON'T use the frosted BlurView here. On a
        // pure-black dark UI, `systemChromeMaterialDark` renders as a
        // washed-out GRAY material, which made the bar read as a
        // distinct floating "box" against the black content — the ugly
        // boundary the old plain bar never had. forceSolid drops to a
        // solid fill (= tint = bgTabBar, which is the app bg in dark
        // mode), so the bar blends flush with the screen. borderRadius:0
        // keeps the corners square (YaverGlass defaults to a 12pt
        // card radius, which drew a rounded-rectangle frame).
        tabBarBackground: () => (
          <YaverGlass
            forceSolid
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
              width: layout.rail.expandedWidth,
              paddingTop: 16,
              paddingHorizontal: 10,
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
          ? { height: 56, marginBottom: 4 }
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
            <TabIcon label="Reload" focused={focused} showGreenDot={devServerRunning} rail={useLeftRail} />
          ),
        }}
      />
      <Tabs.Screen
        name="tasks"
        options={{
          title: "Tasks",
          tabBarIcon: ({ focused }) => <TabIcon label="Tasks" focused={focused} rail={useLeftRail} />,
        }}
      />
      <Tabs.Screen
        name="apps"
        options={{
          title: "Projects",
          tabBarIcon: ({ focused }) => (
            <TabIcon label="Projects" focused={focused} rail={useLeftRail} />
          ),
        }}
      />
      {/* Shortcuts removed from the tab bar (kept as a hidden route so any
          existing deep links / navigation don't 404). */}
      <Tabs.Screen name="shortcuts" options={{ href: null, headerShown: false }} />
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
          tabBarIcon: ({ focused }) => <TabIcon label="More" focused={focused} rail={useLeftRail} />,
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
  // Expanded landscape rail: horizontal icon+label row filling the
  // widened rail, with room for the active-item accent pill.
  tabIconWrapRail: {
    flexDirection: "row",
    justifyContent: "flex-start",
    alignItems: "center",
    alignSelf: "stretch",
    minWidth: 0,
    paddingTop: 0,
    paddingLeft: 14,
    paddingVertical: 8,
    gap: 12,
    borderRadius: 12,
  },
  tabLabelRail: { marginTop: 0, fontSize: 15, letterSpacing: 0 },
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
