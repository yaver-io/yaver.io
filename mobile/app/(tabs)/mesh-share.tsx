// mesh-share.tsx — Yaver Mesh sharing. The support-link flow + who-can-access
// lists lifted out of the old flat network.tsx into their own screen. See
// docs/mesh-support-link.md.

import { useState } from "react";
import { Alert, Pressable, ScrollView, Share, Text, View } from "react-native";
import * as Clipboard from "expo-clipboard";
import { useColors } from "../../src/context/ThemeContext";
import { useMesh } from "../../src/lib/useMesh";

export default function MeshShareScreen() {
  const c = useColors();
  const mesh = useMesh();
  const [supportLink, setSupportLink] = useState<string | null>(null);

  const create = async (offerTerminal: boolean, offerDesktopControl: boolean) => {
    const url = await mesh.createSupportLink(offerTerminal, offerDesktopControl);
    if (url) setSupportLink(url);
  };

  return (
    <ScrollView style={{ flex: 1, backgroundColor: c.bg }} contentContainerStyle={{ padding: 16, gap: 16 }}>
      {mesh.error ? (
        <View style={{ borderRadius: 14, borderWidth: 1, borderColor: "#ef444455", backgroundColor: "#ef444415", padding: 12 }}>
          <Text style={{ color: "#fca5a5", fontSize: 13 }}>{mesh.error}</Text>
        </View>
      ) : null}

      <View style={{ borderRadius: 16, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 14, gap: 10 }}>
        <Text style={{ fontSize: 15, fontWeight: "700", color: c.textPrimary }}>Support a friend</Text>
        <Text style={{ fontSize: 12, color: c.textMuted, lineHeight: 17 }}>
          Send a link. Your friend installs Yaver, approves access, and their computer joins your mesh so
          you can help them. Default = view + files; they opt into more on their own screen.
        </Text>
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Pressable
            onPress={() => void create(false, false)}
            style={{ flex: 1, borderRadius: 999, paddingVertical: 9, alignItems: "center", borderWidth: 1, borderColor: "#34d39955", backgroundColor: "#34d39915" }}
          >
            <Text style={{ color: "#34d399", fontSize: 13, fontWeight: "600" }}>View-only link</Text>
          </Pressable>
          <Pressable
            onPress={() => void create(true, true)}
            style={{ flex: 1, borderRadius: 999, paddingVertical: 9, alignItems: "center", borderWidth: 1, borderColor: "#fcd34d55", backgroundColor: "#fcd34d15" }}
          >
            <Text style={{ color: "#fcd34d", fontSize: 13, fontWeight: "600" }}>Full-support link</Text>
          </Pressable>
        </View>
        {supportLink ? (
          <View style={{ gap: 8 }}>
            <Text selectable style={{ color: "#34d399", fontSize: 12, fontFamily: "Menlo" }}>{supportLink}</Text>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable
                onPress={async () => {
                  await Clipboard.setStringAsync(supportLink);
                  Alert.alert("Copied", "Support link copied to clipboard.");
                }}
                style={{ borderRadius: 999, paddingHorizontal: 14, paddingVertical: 6, borderWidth: 1, borderColor: c.border }}
              >
                <Text style={{ color: c.textPrimary, fontSize: 12 }}>Copy</Text>
              </Pressable>
              <Pressable
                onPress={() => Share.share({ message: `Open this to let me help you on your computer: ${supportLink}` })}
                style={{ borderRadius: 999, paddingHorizontal: 14, paddingVertical: 6, borderWidth: 1, borderColor: c.border }}
              >
                <Text style={{ color: c.textPrimary, fontSize: 12 }}>Share…</Text>
              </Pressable>
            </View>
          </View>
        ) : null}
      </View>

      {mesh.supporting.length > 0 ? (
        <View style={{ borderRadius: 16, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, padding: 14, gap: 6 }}>
          <Text style={{ fontSize: 11, color: c.textMuted, letterSpacing: 1 }}>YOU CAN SUPPORT</Text>
          {mesh.supporting.map((s) => (
            <View key={s.grantId} style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <Text style={{ flex: 1, color: c.textPrimary, fontSize: 13 }}>
                {s.counterpartName}{s.allowDesktopControl ? "  · desktop" : ""}
                {s.expiresAt ? "  · time-boxed" : "  · until revoked"}
              </Text>
              <Pressable onPress={() => void mesh.revokeSupport(s.grantId)}>
                <Text style={{ color: c.textMuted, fontSize: 12 }}>end</Text>
              </Pressable>
            </View>
          ))}
        </View>
      ) : null}

      {mesh.supportedBy.length > 0 ? (
        <View style={{ borderRadius: 16, borderWidth: 1, borderColor: "#ef444433", backgroundColor: c.bgCard, padding: 14, gap: 6 }}>
          <Text style={{ fontSize: 11, color: "#fca5a5", letterSpacing: 1 }}>WHO CAN ACCESS YOUR MACHINES</Text>
          {mesh.supportedBy.map((s) => (
            <View key={s.grantId} style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
              <Text style={{ flex: 1, color: c.textPrimary, fontSize: 13 }}>{s.counterpartName}</Text>
              <Pressable onPress={() => void mesh.revokeSupport(s.grantId)} style={{ borderRadius: 6, borderWidth: 1, borderColor: "#ef444455", paddingHorizontal: 8, paddingVertical: 2 }}>
                <Text style={{ color: "#fca5a5", fontSize: 12 }}>revoke</Text>
              </Pressable>
            </View>
          ))}
        </View>
      ) : null}
    </ScrollView>
  );
}
