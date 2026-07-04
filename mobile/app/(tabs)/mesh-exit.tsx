// mesh-exit.tsx — first-class exit-node picker (Tailscale style) for one node's
// egress. `deviceId` param = the node whose internet traffic we're routing
// (the phone itself from the home hero, or any owned node from node detail).
// Writes the consumer-axis field wantUseExitNode.

import { useMemo } from "react";
import { ActivityIndicator, Pressable, ScrollView, Text, View } from "react-native";
import { router, useLocalSearchParams } from "expo-router";
import { useColors } from "../../src/context/ThemeContext";
import { useTabletContentStyle } from "../../src/hooks/useTabletContentStyle";
import { useMesh } from "../../src/lib/useMesh";
import { isSelectableExit, nodeLabel } from "../../src/lib/meshTypes";
import { AppScreenHeader } from "../../src/components/AppScreenHeader";
import { CheckIcon } from "../../src/components/mesh/MeshIcons";

export default function MeshExitScreen() {
  const c = useColors();
  const tabletContent = useTabletContentStyle("regular");
  const { deviceId } = useLocalSearchParams<{ deviceId?: string }>();
  const mesh = useMesh();

  const target = useMemo(() => mesh.peers.find((p) => p.deviceId === deviceId), [mesh.peers, deviceId]);
  const exits = useMemo(
    () => mesh.peers.filter((p) => p.deviceId !== deviceId && isSelectableExit(p)),
    [mesh.peers, deviceId]
  );
  const current = target?.wantUseExitNode || "";

  const choose = (exitId: string) => {
    if (!deviceId) return;
    void mesh.saveNodeConfig(deviceId, { wantUseExitNode: exitId });
    router.back();
  };

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Exit node" onBack={() => router.navigate("/(tabs)/more" as any)} />
      <ScrollView style={{ flex: 1, backgroundColor: c.bg }} contentContainerStyle={[{ padding: 16, gap: 8 }, tabletContent]}>
      <Text style={{ fontSize: 13, color: c.textMuted, lineHeight: 18, marginBottom: 4 }}>
        {target ? `Send ${nodeLabel(target)}'s internet traffic through another node.` : "Choose an exit node."}
        {" "}Only nodes advertising themselves as exit nodes appear here.
      </Text>

      <Option label="None" sub="Direct internet" selected={current === ""} onPress={() => choose("")} c={c} />

      {mesh.loading && exits.length === 0 ? (
        <ActivityIndicator color={c.textMuted} style={{ marginTop: 12 }} />
      ) : exits.length === 0 ? (
        <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 8 }}>
          No exit nodes available. Turn on “Exit node” for a device on its detail screen first.
        </Text>
      ) : (
        exits.map((p) => (
          <Option
            key={p.deviceId}
            label={nodeLabel(p)}
            sub={`${p.meshIPv4 ?? "—"} · ${p.online ? "online" : "offline"}`}
            selected={current === p.deviceId}
            disabled={!p.online}
            onPress={() => choose(p.deviceId)}
            c={c}
          />
        ))
      )}
      </ScrollView>
    </View>
  );
}

function Option({
  label,
  sub,
  selected,
  disabled,
  onPress,
  c,
}: {
  label: string;
  sub: string;
  selected: boolean;
  disabled?: boolean;
  onPress: () => void;
  c: ReturnType<typeof useColors>;
}) {
  return (
    <Pressable
      onPress={disabled ? undefined : onPress}
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: 12,
        borderRadius: 14,
        borderWidth: 1,
        borderColor: selected ? "#fcd34d55" : c.border,
        backgroundColor: selected ? "#fcd34d14" : c.bgCard,
        padding: 14,
        opacity: disabled ? 0.45 : 1,
      }}
    >
      <View style={{ flex: 1 }}>
        <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }}>{label}</Text>
        <Text style={{ color: c.textMuted, fontSize: 12, fontFamily: "Menlo", marginTop: 2 }}>{sub}</Text>
      </View>
      {selected ? <CheckIcon size={18} color="#fcd34d" /> : null}
    </Pressable>
  );
}
