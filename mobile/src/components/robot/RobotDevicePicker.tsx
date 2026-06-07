// RobotDevicePicker — honest device list for the Robot Cell screen.
//
// The old picker listed every device identically (name + os), so a powered-
// off machine looked just as pickable as a live one — "false hope". This
// shows each device's real presence from recent Convex data: a green dot +
// "online" when the heartbeat is fresh, otherwise a grey dot + "last seen
// <time>". Down machines get a Ping button that actively probes the mesh
// (relay → direct) so the user can confirm reachability before committing —
// and a successful probe overrides a stale "offline" from Convex.
import React, { useCallback, useState } from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";

import { useColors } from "../../context/ThemeContext";
import type { Device } from "../../context/DeviceContext";
import { lastSeenLabel, probeMobileDeviceStatus } from "../../lib/deviceStatus";

const OK = "#22c55e";
const DOWN = "#71717a";

type ProbeResult = { ok: boolean; line: string };

export function RobotDevicePicker({
  devices,
  currentId,
  token,
  onPick,
}: {
  devices: Device[];
  currentId: string;
  token: string | null;
  onPick: (id: string) => void;
}) {
  const c = useColors();
  const [pinging, setPinging] = useState<Record<string, boolean>>({});
  const [probe, setProbe] = useState<Record<string, ProbeResult>>({});

  const ping = useCallback(
    async (d: Device) => {
      setPinging((p) => ({ ...p, [d.id]: true }));
      try {
        const r = await probeMobileDeviceStatus(
          { id: d.id, host: (d as any).host, port: (d as any).port, lanIps: (d as any).lanIps },
          token,
          8000,
        );
        setProbe((p) => ({
          ...p,
          [d.id]: r.reachable
            ? { ok: true, line: `reachable · ${r.path === "relay" ? "relay" : "direct"}` }
            : { ok: false, line: "unreachable" },
        }));
      } catch {
        setProbe((p) => ({ ...p, [d.id]: { ok: false, line: "ping failed" } }));
      } finally {
        setPinging((p) => ({ ...p, [d.id]: false }));
      }
    },
    [token],
  );

  if (devices.length === 0) {
    return <Text style={{ color: c.tabInactive }}>No devices. Pair a machine first.</Text>;
  }

  return (
    <>
      {devices.map((d) => {
        const pr = probe[d.id];
        // A successful active probe wins over a stale Convex "offline".
        const up = !!d.online || !!pr?.ok;
        const lastSeen = (d as any).lastSeen as number | undefined;
        return (
          <View key={d.id} style={{ borderBottomColor: c.borderSubtle, borderBottomWidth: 1, paddingVertical: 10 }}>
            <Pressable
              onPress={() => onPick(d.id)}
              style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}
            >
              <View style={{ flexDirection: "row", alignItems: "center", gap: 10, flex: 1, paddingRight: 10 }}>
                <View style={{ width: 9, height: 9, borderRadius: 5, backgroundColor: up ? OK : DOWN }} />
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontWeight: "600" }} numberOfLines={1}>
                    {d.name}
                  </Text>
                  <Text style={{ color: up ? OK : c.tabInactive, fontSize: 12 }} numberOfLines={1}>
                    {up ? "online" : lastSeenLabel(lastSeen)}
                    {pr ? ` · ${pr.line}` : ""}
                  </Text>
                </View>
              </View>
              {d.id === currentId && <Ionicons name="checkmark-circle" size={20} color={OK} />}
            </Pressable>
            {!up && (
              <Pressable
                onPress={() => void ping(d)}
                disabled={pinging[d.id]}
                hitSlop={6}
                style={{
                  alignSelf: "flex-start",
                  marginTop: 8,
                  marginLeft: 19,
                  paddingHorizontal: 12,
                  paddingVertical: 6,
                  borderRadius: 8,
                  borderWidth: 1,
                  borderColor: c.accent,
                  opacity: pinging[d.id] ? 0.5 : 1,
                  flexDirection: "row",
                  alignItems: "center",
                  gap: 6,
                }}
              >
                {pinging[d.id] ? <ActivityIndicator size="small" color={c.accent} /> : null}
                <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>
                  {pinging[d.id] ? "Pinging…" : "Ping"}
                </Text>
              </Pressable>
            )}
          </View>
        );
      })}
    </>
  );
}
