import React, { useEffect, useState } from "react";
import { Modal, View, Text, Pressable } from "react-native";
import { useAuth } from "../context/AuthContext";
import { useColors } from "../context/ThemeContext";
import { acceptBetaInvite, getManagedSubscription } from "../lib/subscription";

// BetaConsentHost — a global overlay that auto-shows the beta consent card the
// first time a pre-seeded (whitelisted) user opens the app after signing up with
// ANY provider (Google, email/password, …). It matches by the account email, so
// it fires regardless of which screen they land on. Approving consents to managed
// AI + the shared box and activates the grant; "Not now" dismisses for the session.
export function BetaConsentHost() {
  const { token } = useAuth();
  const c = useColors();
  const [invite, setInvite] = useState<{ inviterName: string; includedHours: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [dismissed, setDismissed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    if (!token) {
      setInvite(null);
      setDismissed(false);
      return;
    }
    void getManagedSubscription(token).then((s) => {
      if (cancelled) return;
      const inv = s?.beta?.betaInvite;
      if (inv?.pending && !s?.beta?.isBeta) {
        setInvite({ inviterName: inv.inviterName, includedHours: inv.includedHours });
      } else {
        setInvite(null);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [token]);

  if (!invite || dismissed) return null;

  const approve = async () => {
    if (!token || busy) return;
    setBusy(true);
    const ok = await acceptBetaInvite(token);
    setBusy(false);
    if (ok) setInvite(null); // grant active; other surfaces re-read on next load
  };

  return (
    <Modal transparent animationType="fade" visible onRequestClose={() => setDismissed(true)}>
      <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.65)", justifyContent: "center", padding: 24 }}>
        <View style={{ borderRadius: 16, padding: 24, backgroundColor: c.bgCard, borderWidth: 1, borderColor: c.accent }}>
          <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "700" }}>
            ✨ {invite.inviterName} invited you to Yaver Beta
          </Text>
          <Text style={{ color: c.textMuted, marginTop: 8, lineHeight: 20 }}>
            Approve to enable managed AI — no API key needed — and {invite.includedHours} hours on a
            shared Yaver box. Build a sandbox app on your phone and deploy it to Yaver Serverless.
          </Text>
          <Pressable
            onPress={approve}
            disabled={busy}
            style={{ marginTop: 18, backgroundColor: c.accent, borderRadius: 10, paddingVertical: 12, alignItems: "center", opacity: busy ? 0.6 : 1 }}
          >
            <Text style={{ color: c.bg, fontWeight: "700" }}>{busy ? "Activating…" : "Approve beta access"}</Text>
          </Pressable>
          <Pressable onPress={() => setDismissed(true)} style={{ marginTop: 10, paddingVertical: 8, alignItems: "center" }}>
            <Text style={{ color: c.textMuted }}>Not now</Text>
          </Pressable>
        </View>
      </View>
    </Modal>
  );
}
