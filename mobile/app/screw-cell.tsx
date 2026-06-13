// Screw Cell — Yaver as the management layer for the robotic screw cell's
// shop-floor analytics. Reads the SAME agent ops verbs the firmware pushes to
// (screw_cell_record via cell_runner.py --yaver) and the host coding agent
// reads via the screw_cell_analytics MCP tool. KPIs + daily fail-rate trend +
// flagged production orders + worst blocks + recent runs. Transport mirrors the
// circuit/printer/arm cells: LAN-first, relay fallback, your bearer. Runs stay
// on the box vault — never on Convex.
import React from "react";
import { View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { ScrewCellScreen } from "../src/components/ScrewCellScreen";

export default function ScrewCellRoute() {
  const c = useColors();
  const router = useRouter();
  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Screw Cell" onBack={() => router.back()} />
      <ScrewCellScreen />
    </View>
  );
}
