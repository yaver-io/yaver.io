// CidrChips.tsx — add/remove editor for subnet-route CIDRs, replacing the raw
// comma-separated TextInput the old network.tsx used for a Gateway node's
// advertised routes. Each route is a removable chip; a small input appends.

import React, { useState } from "react";
import { Pressable, Text, TextInput, View } from "react-native";
import { useColors } from "../../context/ThemeContext";

export function CidrChips({
  routes,
  onChange,
  placeholder = "10.0.0.0/24",
}: {
  routes: string[];
  onChange: (next: string[]) => void;
  placeholder?: string;
}) {
  const c = useColors();
  const [draft, setDraft] = useState("");

  const add = () => {
    const v = draft.trim();
    if (!v || routes.includes(v)) {
      setDraft("");
      return;
    }
    onChange([...routes, v]);
    setDraft("");
  };

  return (
    <View style={{ gap: 8 }}>
      {routes.length > 0 ? (
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
          {routes.map((r) => (
            <View
              key={r}
              style={{
                flexDirection: "row",
                alignItems: "center",
                gap: 6,
                borderRadius: 999,
                paddingLeft: 10,
                paddingRight: 6,
                paddingVertical: 3,
                borderWidth: 1,
                borderColor: "#22d3ee55",
                backgroundColor: "#22d3ee1f",
              }}
            >
              <Text style={{ color: "#22d3ee", fontSize: 12, fontFamily: "Menlo" }}>{r}</Text>
              <Pressable hitSlop={8} onPress={() => onChange(routes.filter((x) => x !== r))}>
                <Text style={{ color: "#22d3ee", fontSize: 13 }}>✕</Text>
              </Pressable>
            </View>
          ))}
        </View>
      ) : null}
      <View style={{ flexDirection: "row", gap: 8 }}>
        <TextInput
          value={draft}
          onChangeText={setDraft}
          onSubmitEditing={add}
          autoCapitalize="none"
          autoCorrect={false}
          placeholder={placeholder}
          placeholderTextColor={c.textMuted}
          style={{
            flex: 1,
            color: c.textPrimary,
            borderWidth: 1,
            borderColor: c.border,
            borderRadius: 8,
            paddingHorizontal: 10,
            paddingVertical: 6,
            fontSize: 12,
            fontFamily: "Menlo",
          }}
        />
        <Pressable
          onPress={add}
          style={{
            borderRadius: 8,
            paddingHorizontal: 14,
            justifyContent: "center",
            borderWidth: 1,
            borderColor: "#22d3ee55",
            backgroundColor: "#22d3ee1f",
          }}
        >
          <Text style={{ color: "#22d3ee", fontSize: 13, fontWeight: "600" }}>Add</Text>
        </Pressable>
      </View>
    </View>
  );
}
