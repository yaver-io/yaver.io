// mesh-access.tsx — Yaver Mesh access rules (ACLs). Kept in-app (unlike
// Tailscale, which is web-only) for the solo-founder audience, but demoted
// behind a tap from the mesh home. Same wire format the agent compiles into the
// port-level packet filter.

import { ScrollView, Text, TextInput, View, Pressable } from "react-native";
import { useColors } from "../../src/context/ThemeContext";
import { useMesh } from "../../src/lib/useMesh";
import type { ACLRule } from "../../src/lib/meshTypes";

function describeEndpoint(type: ACLRule["srcType"], val: string) {
  if (type === "any") return "any";
  if (type === "tag") return val.startsWith("tag:") ? val : `tag:${val}`;
  if (type === "device") return val.slice(0, 8);
  return val;
}

export default function MeshAccessScreen() {
  const c = useColors();
  const mesh = useMesh();
  const { rules, saveRules } = mesh;

  return (
    <ScrollView style={{ flex: 1, backgroundColor: c.bg }} contentContainerStyle={{ padding: 16, gap: 12 }}>
      <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 18 }}>
        No rules = open mesh (every node reaches every node). Add a rule and everything not explicitly
        allowed is denied. Rules apply on every device, live.
      </Text>

      {mesh.error ? (
        <View style={{ borderRadius: 14, borderWidth: 1, borderColor: "#ef444455", backgroundColor: "#ef444415", padding: 12 }}>
          <Text style={{ color: "#fca5a5", fontSize: 13 }}>{mesh.error}</Text>
        </View>
      ) : null}

      {rules.length === 0 ? (
        <Text style={{ color: c.textMuted, fontSize: 13 }}>No rules — open mesh (default allow).</Text>
      ) : (
        rules.map((r, i) => (
          <View key={i} style={{ borderRadius: 14, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 12, gap: 8 }}>
            <Text style={{ color: c.textPrimary, fontSize: 13 }}>
              {describeEndpoint(r.srcType, r.src)} → {describeEndpoint(r.dstType, r.dst)}
            </Text>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <Text style={{ color: c.textMuted, fontSize: 12 }}>ports</Text>
              <TextInput
                defaultValue={r.ports.join(",")}
                onEndEditing={(e) => {
                  const ports = e.nativeEvent.text.split(",").map((s) => s.trim()).filter(Boolean);
                  void saveRules(rules.map((x, idx) => (idx === i ? { ...x, ports } : x)));
                }}
                placeholder="22,80-90,*"
                placeholderTextColor={c.textMuted}
                style={{ flex: 1, color: c.textPrimary, borderWidth: 1, borderColor: c.border, borderRadius: 8, paddingHorizontal: 8, paddingVertical: 4, fontSize: 12 }}
              />
              <Pressable
                onPress={() =>
                  void saveRules(rules.map((x, idx) => (idx === i ? { ...x, action: x.action === "accept" ? "drop" : "accept" } : x)))
                }
                style={{ borderRadius: 8, paddingHorizontal: 10, paddingVertical: 4, backgroundColor: r.action === "accept" ? "#34d39922" : "#ef444422" }}
              >
                <Text style={{ color: r.action === "accept" ? "#34d399" : "#fca5a5", fontSize: 12 }}>{r.action}</Text>
              </Pressable>
              <Pressable onPress={() => void saveRules(rules.filter((_, idx) => idx !== i))}>
                <Text style={{ color: c.textMuted, fontSize: 16 }}>✕</Text>
              </Pressable>
            </View>
          </View>
        ))
      )}

      <Pressable
        onPress={() => void saveRules([...rules, { srcType: "any", src: "*", dstType: "any", dst: "*", ports: ["*"], action: "accept" }])}
        style={{ borderRadius: 999, borderWidth: 1, borderColor: "#34d39955", backgroundColor: "#34d39915", paddingVertical: 10, alignItems: "center" }}
      >
        <Text style={{ color: "#34d399", fontSize: 13, fontWeight: "600" }}>+ Add rule</Text>
      </Pressable>
    </ScrollView>
  );
}
