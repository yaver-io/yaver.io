// AirDropMacSetupButton — drop-in "Set up my Mac via AirDrop" CTA.
//
// Self-contained so it can be added to the onboarding pairing screen
// (mobile/app/onboarding-pair.tsx) or the Devices tab without those
// files needing to know how the share works. Tapping it generates a
// double-clickable yaver-setup.command and opens the share sheet
// (AirDrop). See mobile/src/lib/macSetupAirDrop.ts for why AirDrop is
// the only transport that reaches a cold Mac, and how the phone then
// adopts the agent silently over the existing beacon flow.
//
// Falls back to copy-to-clipboard when sharing isn't available (e.g.
// simulator, or AirDrop disabled) so the user is never stuck.

import React from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import * as Clipboard from "expo-clipboard";

import { useColors } from "../context/ThemeContext";
import { MAC_SETUP_ONELINER, shareMacSetupScript } from "../lib/macSetupAirDrop";

interface Props {
  /** Optional callback once the share sheet was presented — the
   *  onboarding screen can use it to nudge "waiting for your Mac…". */
  onShared?: () => void;
}

export default function AirDropMacSetupButton({ onShared }: Props) {
  const c = useColors();
  const [busy, setBusy] = React.useState(false);
  const [copied, setCopied] = React.useState(false);
  const [note, setNote] = React.useState<string | null>(null);

  const copyFallback = React.useCallback(async () => {
    await Clipboard.setStringAsync(MAC_SETUP_ONELINER);
    setCopied(true);
    setNote("AirDrop unavailable — command copied. Paste it in your Mac's Terminal.");
    setTimeout(() => setCopied(false), 1800);
  }, []);

  const onPress = React.useCallback(async () => {
    if (busy) return;
    setBusy(true);
    setNote(null);
    try {
      const res = await shareMacSetupScript();
      if (res.ok) {
        onShared?.();
      } else if (res.unsupported) {
        await copyFallback();
      } else {
        setNote(res.error || "Couldn't open the share sheet.");
      }
    } finally {
      setBusy(false);
    }
  }, [busy, onShared, copyFallback]);

  return (
    <View>
      <Pressable
        onPress={() => void onPress()}
        disabled={busy}
        style={({ pressed }) => ({
          flexDirection: "row",
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: c.accent,
          paddingVertical: 14,
          borderRadius: 10,
          opacity: pressed || busy ? 0.85 : 1,
        })}
      >
        {busy ? (
          <ActivityIndicator color="#000" size="small" />
        ) : (
          <Text style={{ color: "#000", fontWeight: "700", fontSize: 15 }}>
            {copied ? "Copied ✓" : "Set up my Mac with AirDrop"}
          </Text>
        )}
      </Pressable>
      <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 8, textAlign: "center" }}>
        AirDrop the setup file to your Mac, double-click it, and this phone pairs it
        automatically — no typing, no sign-in on the Mac.
      </Text>
      {note ? (
        <Text style={{ color: c.warn, fontSize: 11, marginTop: 6, textAlign: "center" }}>
          {note}
        </Text>
      ) : null}
    </View>
  );
}
