import React, { useEffect, useRef, useState } from "react";
import { Platform, Pressable, Text, View } from "react-native";
import * as WebBrowser from "expo-web-browser";
import { useAuth } from "../context/AuthContext";
import { useColors } from "../context/ThemeContext";
import { getWebBaseUrlSync } from "../lib/backendConfig";
import { formatUserCode, startDeviceCodeSignIn, waitForDeviceCodeToken } from "../lib/deviceCodeSignIn";

/** Optional Yaver-owned identity card. No navigation, SSH credentials, runner
 * OAuth interception or remote-box enrollment. Closing it leaves SSH alone. */
export default function YaverAccountCard({ onClose }: { onClose: () => void }) {
  const c = useColors();
  const { isAuthenticated, login } = useAuth();
  const [code, setCode] = useState("");
  const [status, setStatus] = useState("");
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  useEffect(() => () => { generation.current++; }, []);
  const cancel = () => { generation.current++; onClose(); };
  const start = async () => {
    const attempt = ++generation.current;
    setBusy(true); setStatus("Preparing Yaver sign-in…");
    try {
      const challenge = await startDeviceCodeSignIn({machineName:"Yaver Plain SSH", platform:Platform.OS});
      if (attempt !== generation.current) return;
      setCode(formatUserCode(challenge.userCode)); setStatus("Approve this code in Yaver. SSH remains independent.");
      const url = `${getWebBaseUrlSync().replace(/\/+$/, "")}/auth/device?code=${encodeURIComponent(challenge.userCode)}`;
      // Public short code only; the secret poll credential stays in this closure.
      void WebBrowser.openBrowserAsync(url).catch(() => {
        if (attempt === generation.current) setStatus("Open Yaver on a signed-in device and approve the code shown here.");
      });
      const result = await waitForDeviceCodeToken(challenge.deviceCode, {
        timeoutMs: Math.max(0, challenge.expiresAt - Date.now()),
        isCancelled: () => generation.current !== attempt,
        onTick: ({unreachableReason}) => { if (generation.current === attempt && unreachableReason) setStatus(unreachableReason); },
      });
      if (attempt !== generation.current) return;
      if (result.kind === "token") { await login(result.token); setCode(""); setStatus("Yaver is connected. Your SSH pane is unchanged."); }
      else setStatus("Sign-in ended. You can retry whenever you need Yaver services.");
    } catch (cause) {
      if (attempt === generation.current) setStatus(cause instanceof Error ? cause.message : "Yaver sign-in failed. SSH is still available.");
    } finally { if (attempt === generation.current) setBusy(false); }
  };
  return <View style={{marginHorizontal:12,marginBottom:8,padding:12,borderWidth:1,borderColor:c.border,borderRadius:12,gap:8}}>
    <View style={{flexDirection:"row",justifyContent:"space-between",alignItems:"center"}}>
      <Text style={{color:c.textPrimary,fontWeight:"700"}}>{isAuthenticated ? "Yaver connected" : "Connect Yaver"}</Text>
      <Pressable accessibilityRole="button" accessibilityLabel="Close Yaver sign-in card" onPress={cancel} hitSlop={10}><Text style={{color:c.textMuted}}>Close</Text></Pressable>
    </View>
    <Text style={{color:c.textMuted,fontSize:12}}>Optional account features. Your SSH hosts and runner logins stay separate.</Text>
    {!!code && <Text selectable style={{color:c.textPrimary,fontSize:22,letterSpacing:3}}>{code}</Text>}
    {!!status && <Text accessibilityLiveRegion="polite" style={{color:c.textMuted,fontSize:12}}>{status}</Text>}
    {!isAuthenticated && <Pressable accessibilityRole="button" disabled={busy} onPress={() => {void start();}} style={{paddingVertical:8}}>
      <Text style={{color:c.accent,fontWeight:"600"}}>{busy ? "Waiting for Yaver approval…" : "Continue with Yaver"}</Text>
    </Pressable>}
  </View>;
}
