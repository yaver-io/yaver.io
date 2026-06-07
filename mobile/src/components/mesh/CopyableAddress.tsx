// CopyableAddress.tsx — a mono address (overlay IP / MagicDNS name) with a
// one-tap copy affordance. Used on node detail and the this-device card.

import React, { useState } from "react";
import { Pressable, Text, View } from "react-native";
import * as Clipboard from "expo-clipboard";
import { useColors } from "../../context/ThemeContext";
import { CopyIcon, CheckIcon } from "./MeshIcons";

export function CopyableAddress({
  label,
  value,
  tint = "#34d399",
}: {
  label?: string;
  value: string;
  tint?: string;
}) {
  const c = useColors();
  const [copied, setCopied] = useState(false);
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
      {label ? <Text style={{ color: c.textMuted, fontSize: 12, width: 64 }}>{label}</Text> : null}
      <Text selectable style={{ flex: 1, color: tint, fontSize: 13, fontFamily: "Menlo" }}>
        {value}
      </Text>
      <Pressable
        hitSlop={10}
        onPress={async () => {
          await Clipboard.setStringAsync(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1200);
        }}
        style={{ padding: 4 }}
      >
        {copied ? <CheckIcon color={tint} size={16} /> : <CopyIcon color={c.textMuted} size={16} />}
      </Pressable>
    </View>
  );
}
