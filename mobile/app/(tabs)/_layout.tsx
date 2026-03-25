import { Tabs } from "expo-router";
import React from "react";
import { StyleSheet, Text } from "react-native";
import { useColors } from "../../src/context/ThemeContext";

function TabIcon({ label, focused }: { label: string; focused: boolean }) {
  const c = useColors();
  const icons: Record<string, string> = {
    Tasks: "T",
    Todos: "☐",
    Builds: "B",
    Devices: "D",
    Settings: "S",
  };
  return (
    <Text style={[styles.icon, { color: focused ? c.tabActive : c.tabInactive }]}>
      {icons[label] ?? "?"}
    </Text>
  );
}

export default function TabLayout() {
  const c = useColors();
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
        name="todos"
        options={{
          title: "Todos",
          tabBarIcon: ({ focused }) => <TabIcon label="Todos" focused={focused} />,
        }}
      />
      <Tabs.Screen
        name="builds"
        options={{
          href: null, // Hidden — builds are managed by the agent, not shown in UI
        }}
      />
      <Tabs.Screen
        name="devices"
        options={{
          title: "Devices",
          tabBarIcon: ({ focused }) => <TabIcon label="Devices" focused={focused} />,
        }}
      />
      <Tabs.Screen
        name="settings"
        options={{
          title: "Settings",
          tabBarIcon: ({ focused }) => <TabIcon label="Settings" focused={focused} />,
        }}
      />
    </Tabs>
  );
}

const styles = StyleSheet.create({
  icon: {
    fontSize: 18,
    fontWeight: "700",
  },
});
