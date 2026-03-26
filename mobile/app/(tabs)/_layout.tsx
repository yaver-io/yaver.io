import { Tabs, useRouter } from "expo-router";
import React, { useEffect, useRef, useState } from "react";
import { StyleSheet, Text, View } from "react-native";
import { useColors } from "../../src/context/ThemeContext";
import { useDevice } from "../../src/context/DeviceContext";
import { quicClient } from "../../src/lib/quic";

function TabIcon({ label, focused, showGreenDot }: { label: string; focused: boolean; showGreenDot?: boolean }) {
  const c = useColors();
  const icons: Record<string, string> = {
    Tasks: "T",
    Todos: "\u2610",
    Apps: "\u25B6",
    Reload: "\uD83D\uDD25",
    Repos: "\u{1F4C2}",
    Builds: "B",
    Devices: "D",
    More: "\u22EF",
    Settings: "S",
  };
  return (
    <View>
      <Text style={[styles.icon, { color: focused ? c.tabActive : c.tabInactive }]}>
        {icons[label] ?? "?"}
      </Text>
      {showGreenDot && (
        <View style={styles.greenDot} />
      )}
    </View>
  );
}

export default function TabLayout() {
  const c = useColors();
  const router = useRouter();
  const { connectionStatus, activeDevice } = useDevice();
  const isConnected = connectionStatus === "connected" && !!activeDevice;
  const [devServerRunning, setDevServerRunning] = useState(false);
  const wasRunning = useRef(false);

  // Poll dev server status for green dot badge + auto-route
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
          // Auto-navigate to Apps tab when dev server first starts
          if (running && !wasRunning.current) {
            wasRunning.current = true;
            router.navigate("/(tabs)/hotreload");
          }
          if (!running) wasRunning.current = false;
        }
      } catch {
        if (mounted) setDevServerRunning(false);
      }
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => { mounted = false; clearInterval(interval); };
  }, [isConnected, router]);

  return (
    <Tabs
      screenOptions={{
        headerStyle: { backgroundColor: c.bg },
        headerTintColor: c.textPrimary,
        headerTitleStyle: { fontWeight: "700" },
        tabBarStyle: {
          backgroundColor: c.bgTabBar,
          borderTopColor: c.border,
          borderTopWidth: 1,
        },
        tabBarActiveTintColor: c.tabActive,
        tabBarInactiveTintColor: c.tabInactive,
      }}
    >
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
          title: "Apps",
          tabBarIcon: ({ focused }) => (
            <TabIcon label="Apps" focused={focused} showGreenDot={devServerRunning} />
          ),
        }}
      />
      <Tabs.Screen
        name="hotreload"
        options={{
          title: "Reload",
          tabBarIcon: ({ focused }) => (
            <TabIcon label="Reload" focused={focused} showGreenDot={devServerRunning} />
          ),
        }}
      />
      <Tabs.Screen name="builds" options={{ href: null }} />
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
      <Tabs.Screen name="todos" options={{ href: null }} />
      <Tabs.Screen name="settings" options={{ href: null }} />
    </Tabs>
  );
}

const styles = StyleSheet.create({
  icon: {
    fontSize: 18,
    fontWeight: "700",
  },
  greenDot: {
    position: "absolute",
    top: -2,
    right: -6,
    width: 8,
    height: 8,
    borderRadius: 4,
    backgroundColor: "#22c55e",
    borderWidth: 1.5,
    borderColor: "#0a0a0a",
  },
});
