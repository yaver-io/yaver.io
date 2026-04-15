import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Alert, Pressable, ScrollView, Share, StyleSheet, Text, TextInput, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import * as Clipboard from "expo-clipboard";
import { useColors } from "../../src/context/ThemeContext";
import { useAuth } from "../../src/context/AuthContext";
import { useDevice } from "../../src/context/DeviceContext";
import {
  acceptGuestByCode,
  inviteGuest,
  listGuests,
  revokeGuest,
  type GuestInfo,
} from "../../src/lib/guests";

// Mobile-first guest access screen. Hosts invite someone to share their
// machine; guests paste a 6-char code to accept. Everything native — invite
// codes are copyable + shareable via the OS share sheet so the host can send
// them via iMessage/WhatsApp/email without retyping.

export default function GuestsScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { token } = useAuth();
  const { guestInvitations, acceptGuestInvitation, refreshDevices } = useDevice();

  const [mode, setMode] = useState<"my-guests" | "join">("my-guests");
  const [guests, setGuests] = useState<GuestInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviting, setInviting] = useState(false);
  const [lastCode, setLastCode] = useState<string | null>(null);
  const [lastEmail, setLastEmail] = useState<string | null>(null);
  const [joinCode, setJoinCode] = useState("");
  const [joining, setJoining] = useState(false);

  const loadGuests = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    try { setGuests(await listGuests(token)); } catch {}
    setLoading(false);
  }, [token]);

  useEffect(() => { loadGuests(); }, [loadGuests]);

  async function invite() {
    if (!token || !inviteEmail.trim()) return;
    setInviting(true);
    try {
      const r = await inviteGuest(token, inviteEmail.trim());
      setLastCode(r.inviteCode);
      setLastEmail(inviteEmail.trim());
      setInviteEmail("");
      loadGuests();
    } catch (e: any) {
      Alert.alert("Invite failed", e?.message || String(e));
    }
    setInviting(false);
  }

  async function revoke(email: string) {
    Alert.alert("Revoke access?", email, [
      { text: "Cancel", style: "cancel" },
      { text: "Revoke", style: "destructive", onPress: async () => {
        if (!token) return;
        try { await revokeGuest(token, email); loadGuests(); } catch (e: any) { Alert.alert("Failed", e?.message || String(e)); }
      }},
    ]);
  }

  async function copyCode(code: string) {
    await Clipboard.setStringAsync(code);
    Alert.alert("Copied", code);
  }

  async function shareInvite(code: string, email?: string) {
    const msg = email
      ? `Hey — here's your Yaver access code for ${email}: ${code}\n\nDownload: https://yaver.io/download · expires in 2 days`
      : `Your Yaver invite code: ${code}`;
    try { await Share.share({ message: msg }); } catch {}
  }

  async function acceptByCode() {
    if (!token || joinCode.trim().length < 4) return;
    setJoining(true);
    try {
      const code = joinCode.trim().toUpperCase();
      await acceptGuestByCode(token, code);
      setJoinCode("");
      Alert.alert("Joined", "Host machine should now appear in your device list.");
      refreshDevices();
    } catch (e: any) {
      Alert.alert("Failed", e?.message || String(e));
    }
    setJoining(false);
  }

  async function acceptPending(id: string) {
    try {
      await acceptGuestInvitation(id);
      Alert.alert("Joined", "Host machine added.");
      refreshDevices();
    } catch (e: any) { Alert.alert("Failed", e?.message || String(e)); }
  }

  return (
    <View style={[styles.container, { backgroundColor: c.bg }]}>
      <View style={[styles.header, { borderBottomColor: c.border, paddingTop: insets.top + 12 }]}>
        <Pressable onPress={() => router.navigate("/(tabs)/more" as any)} style={{ paddingVertical: 8 }}>
          <Text style={{ color: c.accent, fontSize: 15, fontWeight: "600" }}>{"\u2039"} Back</Text>
        </Pressable>
        <Text style={{ fontSize: 17, fontWeight: "700", color: c.textPrimary }}>Guest Access</Text>
        <View style={{ width: 50 }} />
      </View>

      <View style={{ flexDirection: "row", padding: 12, gap: 8 }}>
        <ModeBtn c={c} label="My guests" active={mode === "my-guests"} onPress={() => setMode("my-guests")} />
        <ModeBtn c={c} label="Join as guest" active={mode === "join"} onPress={() => setMode("join")} />
      </View>

      <ScrollView contentContainerStyle={{ padding: 16, paddingBottom: 40, gap: 12 }}>
        {mode === "my-guests" ? (
          <>
            <View style={[card(c), { gap: 8 }]}>
              <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>Invite a guest</Text>
              <TextInput value={inviteEmail} onChangeText={setInviteEmail}
                placeholder="email@example.com" placeholderTextColor={c.textMuted}
                autoCapitalize="none" keyboardType="email-address" autoCorrect={false}
                style={[inputStyle(c), { fontFamily: "Menlo" }]} />
              <Pressable onPress={invite} disabled={inviting || !inviteEmail.trim()}
                style={[actionBtn(c), { backgroundColor: c.accent, opacity: inviting || !inviteEmail.trim() ? 0.5 : 1 }]}>
                {inviting ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontWeight: "700" }}>Send invite</Text>}
              </Pressable>
              <Text style={{ color: c.textMuted, fontSize: 10 }}>
                Max 5 guests. Codes expire in 2 days. Guests can create tasks + use Data/Ops but can't touch vault, session, or exec.
              </Text>
            </View>

            {lastCode && (
              <View style={[card(c), { backgroundColor: c.accent + "15", borderColor: c.accent, borderWidth: 1, gap: 10 }]}>
                <Text style={{ color: c.accent, fontSize: 11, fontWeight: "700", textTransform: "uppercase" }}>New invite for {lastEmail}</Text>
                <Text selectable style={{ color: c.textPrimary, fontFamily: "Menlo", fontSize: 26, letterSpacing: 4, fontWeight: "700", textAlign: "center" }}>{lastCode}</Text>
                <View style={{ flexDirection: "row", gap: 8 }}>
                  <Pressable onPress={() => copyCode(lastCode)} style={[actionBtn(c), { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, flex: 1 }]}>
                    <Text style={{ color: c.textPrimary, fontSize: 13 }}>Copy code</Text>
                  </Pressable>
                  <Pressable onPress={() => shareInvite(lastCode, lastEmail || undefined)} style={[actionBtn(c), { backgroundColor: c.accent, flex: 1 }]}>
                    <Text style={{ color: "#fff", fontWeight: "700" }}>Share…</Text>
                  </Pressable>
                </View>
              </View>
            )}

            <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700", marginTop: 4 }}>
              Active guests {guests.length > 0 && `(${guests.length}/5)`}
            </Text>
            {loading && <ActivityIndicator color={c.accent} />}
            {!loading && guests.length === 0 && <Text style={{ color: c.textMuted, fontSize: 12 }}>No active guests.</Text>}
            {guests.map((g) => (
              <View key={g.email} style={[card(c), { flexDirection: "row", alignItems: "center" }]}>
                <View style={{ flex: 1 }}>
                  <Text style={{ color: c.textPrimary, fontSize: 13 }}>{g.email}</Text>
                  {g.status && <Text style={{ color: c.textMuted, fontSize: 10 }}>{g.status}{g.grantedAt ? ` · granted ${new Date(g.grantedAt).toLocaleDateString()}` : ""}</Text>}
                </View>
                {g.status === "pending" && g.inviteCode && (
                  <Pressable onPress={() => shareInvite(g.inviteCode!, g.email)}>
                    <Text style={{ color: c.accent, fontSize: 12, marginRight: 10 }}>Share</Text>
                  </Pressable>
                )}
                <Pressable onPress={() => revoke(g.email)}>
                  <Text style={{ color: "#ef4444", fontSize: 12 }}>Revoke</Text>
                </Pressable>
              </View>
            ))}
          </>
        ) : (
          <>
            {guestInvitations && guestInvitations.length > 0 && (
              <View style={[card(c), { gap: 8 }]}>
                <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>Pending invites</Text>
                {guestInvitations.map((inv) => (
                  <View key={inv._id} style={{ padding: 10, backgroundColor: c.accent + "15", borderRadius: 8, gap: 6 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13 }}>From <Text style={{ fontFamily: "Menlo" }}>{inv.hostEmail || inv.hostName}</Text></Text>
                    <Pressable onPress={() => inv._id && acceptPending(inv._id)} style={[actionBtn(c), { backgroundColor: c.accent, paddingVertical: 8 }]}>
                      <Text style={{ color: "#fff", fontWeight: "700", fontSize: 13 }}>Accept</Text>
                    </Pressable>
                  </View>
                ))}
              </View>
            )}

            <View style={[card(c), { gap: 8 }]}>
              <Text style={{ color: c.textMuted, fontSize: 10, textTransform: "uppercase", fontWeight: "700" }}>Enter invite code</Text>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                If you signed in with a different email than the host invited, or just got the code by text/message, paste it here.
              </Text>
              <TextInput value={joinCode} onChangeText={setJoinCode}
                placeholder="ABC123" placeholderTextColor={c.textMuted}
                autoCapitalize="characters" autoCorrect={false} maxLength={10}
                style={[inputStyle(c), { fontFamily: "Menlo", fontSize: 22, letterSpacing: 4, textAlign: "center" }]} />
              <Pressable onPress={acceptByCode} disabled={joining || joinCode.trim().length < 4}
                style={[actionBtn(c), { backgroundColor: c.accent, opacity: joining || joinCode.trim().length < 4 ? 0.5 : 1 }]}>
                {joining ? <ActivityIndicator color="#fff" /> : <Text style={{ color: "#fff", fontWeight: "700" }}>Join</Text>}
              </Pressable>
            </View>
          </>
        )}
      </ScrollView>
    </View>
  );
}

function ModeBtn({ c, label, active, onPress }: { c: any; label: string; active: boolean; onPress: () => void }) {
  return (
    <Pressable onPress={onPress} style={{ flex: 1, paddingVertical: 8, borderRadius: 8, backgroundColor: active ? c.accent + "20" : c.bgCard, borderWidth: 1, borderColor: active ? c.accent : c.border, alignItems: "center" }}>
      <Text style={{ color: active ? c.accent : c.textMuted, fontSize: 13, fontWeight: "700" }}>{label}</Text>
    </Pressable>
  );
}

function card(c: any) { return { backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 10, padding: 12 } as const; }
function actionBtn(c: any) { return { paddingVertical: 10, borderRadius: 8, alignItems: "center", justifyContent: "center" } as const; }
function inputStyle(c: any) { return { backgroundColor: c.bg, borderColor: c.border, borderWidth: 1, borderRadius: 8, padding: 10, color: c.textPrimary } as const; }

const styles = StyleSheet.create({
  container: { flex: 1 },
  header: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingHorizontal: 16, paddingBottom: 12, borderBottomWidth: 1 },
});
