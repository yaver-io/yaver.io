import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, Linking, Platform, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
import { CameraView, useCameraPermissions, type BarcodeScanningResult } from "expo-camera";
import { useRouter } from "expo-router";
import QRCode from "react-native-qrcode-svg";
import { useSafeAreaInsets } from "react-native-safe-area-context";

import { AppBackButton } from "../src/components/AppBackButton";
import { useAuth } from "../src/context/AuthContext";
import { useColors } from "../src/context/ThemeContext";
import { LOCAL_KEYS } from "../src/lib/auth";
import {
  credentialAccountFingerprint,
  credentialHandoffVerificationCode,
  encodeCredentialHandoffQr,
  parseCredentialHandoffQr,
  sealCredentialForHandoff,
  type CredentialHandoffEnvelope,
  type CredentialHandoffRequest,
  type HandoffCredentialKind,
} from "../src/lib/credentialHandoff";
import {
  acceptCredentialHandoff,
  createLocalCredentialHandoffRequest,
  getCredentialHandoffDeviceId,
  getCredentialHandoffPublicIdentity,
} from "../src/lib/credentialHandoffStore";
import { registerCredentialHandoffDevice, verifyCredentialHandoffReceiver } from "../src/lib/credentialHandoffDirectory";
import { getSecret } from "../src/lib/secure-storage";
import {
  canReceiveCredentialHandoffOverBle,
  disconnectCredentialHandoffBle,
  findCredentialHandoffBleReceiver,
  sendCredentialHandoffEnvelopeOverBle,
  startCredentialHandoffBleReceiver,
} from "../src/lib/credentialHandoffBle";

const SECRET_SLOT: Record<HandoffCredentialKind, string> = {
  "deepseek-api-key": LOCAL_KEYS.deepseekApiKey,
  "openai-api-key": LOCAL_KEYS.openAiApiKey,
  "anthropic-api-key": LOCAL_KEYS.anthropicApiKey,
  "glm-api-key": LOCAL_KEYS.glmApiKey,
  "github-token": LOCAL_KEYS.githubToken,
  "gitlab-token": LOCAL_KEYS.gitlabToken,
  "bitbucket-token": LOCAL_KEYS.bitbucketToken,
  "hetzner-api-token": LOCAL_KEYS.hetznerToken,
};

const LABEL: Record<HandoffCredentialKind, string> = {
  "deepseek-api-key": "DeepSeek API key",
  "openai-api-key": "OpenAI API key",
  "anthropic-api-key": "Anthropic API key",
  "glm-api-key": "GLM API key",
  "github-token": "GitHub token",
  "gitlab-token": "GitLab token",
  "bitbucket-token": "Bitbucket token",
  "hetzner-api-token": "Hetzner API token",
};

type ScanFor = "request" | "envelope" | null;

export default function SecureHandoffScreen() {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { user, token } = useAuth();
  const [mode, setMode] = useState<"receive" | "send">("receive");
  const [request, setRequest] = useState<CredentialHandoffRequest | null>(null);
  const [scannedRequest, setScannedRequest] = useState<CredentialHandoffRequest | null>(null);
  const [outgoing, setOutgoing] = useState<CredentialHandoffEnvelope | null>(null);
  const [pendingIncoming, setPendingIncoming] = useState<CredentialHandoffEnvelope | null>(null);
  const [availableKinds, setAvailableKinds] = useState<HandoffCredentialKind[]>([]);
  const [scanFor, setScanFor] = useState<ScanFor>(null);
  const [busy, setBusy] = useState(false);
  const [bleReceiving, setBleReceiving] = useState(false);
  const [bleSenderConnected, setBleSenderConnected] = useState(false);
  const stopBleReceiver = useRef<(() => void) | null>(null);
  const accountFingerprint = useMemo(
    () => user?.id ? credentialAccountFingerprint(user.id) : "",
    [user?.id],
  );

  const makeRequest = useCallback(async () => {
    if (!accountFingerprint || !token) return;
    stopBleReceiver.current?.();
    stopBleReceiver.current = null;
    setBleReceiving(false);
    setBusy(true);
    try {
      const deviceId = await getCredentialHandoffDeviceId();
      const identity = await getCredentialHandoffPublicIdentity(accountFingerprint);
      await registerCredentialHandoffDevice({ token, deviceId, publicKey: identity.publicKey, platform: Platform.OS });
      setRequest(await createLocalCredentialHandoffRequest({ deviceId, accountFingerprint }));
      setPendingIncoming(null);
    } catch (error) {
      Alert.alert("Secure handoff unavailable", error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }, [accountFingerprint, token]);

  useEffect(() => { void makeRequest(); }, [makeRequest]);
  useEffect(() => () => {
    stopBleReceiver.current?.();
    void disconnectCredentialHandoffBle();
  }, []);

  const loadAvailable = useCallback(async () => {
    const rows = await Promise.all(
      (Object.keys(SECRET_SLOT) as HandoffCredentialKind[]).map(async (kind) => ({
        kind,
        ready: !!(await getSecret(SECRET_SLOT[kind]))?.trim(),
      })),
    );
    setAvailableKinds(rows.filter((row) => row.ready).map((row) => row.kind));
  }, []);

  useEffect(() => {
    if (mode === "send") void loadAvailable().catch((error) => {
      Alert.alert("Secure storage unavailable", error instanceof Error ? error.message : String(error));
    });
  }, [loadAvailable, mode]);

  const handleScanned = useCallback((raw: string) => {
    const parsed = parseCredentialHandoffQr(raw);
    if (!parsed || !scanFor) return;
    if (scanFor === "request" && parsed.type === "yaver-credential-request") {
      setScanFor(null);
      if (parsed.accountFingerprint !== accountFingerprint) {
        Alert.alert("Different account", "The receiving device is signed in to a different Yaver account.");
        return;
      }
      setBusy(true);
      void verifyCredentialHandoffReceiver(token || "", parsed).then(() => {
        setScannedRequest(parsed);
        setOutgoing(null);
      }).catch((error) => {
        Alert.alert("Unverified receiver", error instanceof Error ? error.message : String(error));
      }).finally(() => setBusy(false));
      return;
    }
    if (scanFor === "envelope" && parsed.type === "yaver-credential-envelope") {
      setScanFor(null);
      if (!request || parsed.handoffId !== request.handoffId) {
        Alert.alert("Wrong handoff", "That response belongs to a different or expired request.");
        return;
      }
      setPendingIncoming(parsed);
    }
  }, [accountFingerprint, request, scanFor, token]);

  const approveKind = useCallback(async (kind: HandoffCredentialKind) => {
    if (!scannedRequest) return;
    setBusy(true);
    try {
      const value = (await getSecret(SECRET_SLOT[kind]))?.trim();
      if (!value) throw new Error(`${LABEL[kind]} is no longer available on this device.`);
      setOutgoing(sealCredentialForHandoff({
        request: scannedRequest,
        expectedAccountFingerprint: accountFingerprint,
        kind,
        value,
      }));
    } catch (error) {
      Alert.alert("Could not approve handoff", error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }, [accountFingerprint, scannedRequest]);

  const receiveNearby = useCallback(async () => {
    if (!request) return;
    setBusy(true);
    try {
      stopBleReceiver.current?.();
      stopBleReceiver.current = await startCredentialHandoffBleReceiver(request, (envelope) => {
        if (envelope.handoffId !== request.handoffId) return;
        setPendingIncoming(envelope);
        setBleReceiving(false);
        stopBleReceiver.current?.();
        stopBleReceiver.current = null;
      });
      setBleReceiving(true);
    } catch (error) {
      Alert.alert("Nearby receive unavailable", error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }, [request]);

  const findNearby = useCallback(async () => {
    setBusy(true);
    try {
      const found = await findCredentialHandoffBleReceiver();
      if (found.accountFingerprint !== accountFingerprint) {
        await disconnectCredentialHandoffBle();
        throw new Error("The nearby receiving device is signed in to a different Yaver account.");
      }
      await verifyCredentialHandoffReceiver(token || "", found);
      setScannedRequest(found);
      setOutgoing(null);
      setBleSenderConnected(true);
    } catch (error) {
      Alert.alert("Nearby handoff unavailable", error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }, [accountFingerprint, token]);

  const deliverNearby = useCallback(async () => {
    if (!outgoing) return;
    setBusy(true);
    try {
      await sendCredentialHandoffEnvelopeOverBle(outgoing);
      setBleSenderConnected(false);
      Alert.alert("Sent nearby", "The receiving device must still verify the matching code before saving the credential.");
    } catch (error) {
      Alert.alert("Nearby delivery failed", error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }, [outgoing]);

  const acceptIncoming = useCallback(async () => {
    if (!request || !pendingIncoming) return;
    setBusy(true);
    try {
      const accepted = await acceptCredentialHandoff({
        envelope: pendingIncoming,
        deviceId: request.targetDeviceId,
        accountFingerprint,
      });
      Alert.alert("Credential saved", `${LABEL[accepted.kind]} is now stored only in this device's secure storage.`);
      await makeRequest();
    } catch (error) {
      Alert.alert("Credential not accepted", error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }, [accountFingerprint, makeRequest, pendingIncoming, request]);

  if (!user) {
    return <View style={[styles.fill, styles.center, { backgroundColor: c.bg }]}><Text style={{ color: c.textPrimary }}>Sign in to use same-account secure handoff.</Text></View>;
  }
  if (scanFor) {
    return <HandoffScanner expected={scanFor} onScanned={handleScanned} onClose={() => setScanFor(null)} />;
  }

  const recipientCode = request && pendingIncoming
    ? credentialHandoffVerificationCode(request, pendingIncoming)
    : null;
  const senderCode = scannedRequest && outgoing
    ? credentialHandoffVerificationCode(scannedRequest, outgoing)
    : null;

  return (
    <View style={[styles.fill, { backgroundColor: c.bg, paddingTop: insets.top }]}>
      <View style={styles.header}>
        <AppBackButton onPress={() => router.back()} />
        <View style={{ flex: 1 }}>
          <Text style={[styles.title, { color: c.textPrimary }]}>Secure handoff</Text>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>Keychain ↔ Keystore · end-to-end encrypted</Text>
        </View>
      </View>
      <ScrollView contentContainerStyle={[styles.content, { paddingBottom: insets.bottom + 28 }]}>
        <View style={[styles.segment, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          {(["receive", "send"] as const).map((item) => (
            <Pressable key={item} onPress={() => setMode(item)} style={[styles.segmentButton, mode === item && { backgroundColor: c.accent }]}>
              <Text style={{ color: mode === item ? "#fff" : c.textPrimary, fontWeight: "700" }}>{item === "receive" ? "Receive" : "Send"}</Text>
            </Pressable>
          ))}
        </View>

        {mode === "receive" ? (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            <Text style={[styles.cardTitle, { color: c.textPrimary }]}>1. Let the sending device scan this</Text>
            <Text style={[styles.body, { color: c.textMuted }]}>This QR has only a device public key, account fingerprint, and short expiry. It contains no credential.</Text>
            {request ? <View style={styles.qr}><QRCode value={encodeCredentialHandoffQr(request)} size={236} backgroundColor="#fff" color="#000" /></View> : null}
            <Pressable disabled={busy} onPress={() => void makeRequest()} style={[styles.secondary, { borderColor: c.border }]}>
              <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Generate new request</Text>
            </Pressable>
            {canReceiveCredentialHandoffOverBle() ? (
              <Pressable disabled={!request || busy || bleReceiving} onPress={() => void receiveNearby()} style={[styles.secondary, { borderColor: c.border }]}>
                <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{bleReceiving ? "Nearby receiver active…" : "Receive nearby over BLE"}</Text>
              </Pressable>
            ) : null}

            <Text style={[styles.cardTitle, { color: c.textPrimary, marginTop: 24 }]}>2. Scan the encrypted reply</Text>
            {Boolean((Platform as typeof Platform & { isTV?: boolean }).isTV) ? (
              <Text style={[styles.body, { color: c.textMuted }]}>This TV has no camera. Use nearby delivery or the encrypted same-account mailbox when available.</Text>
            ) : (
              <Pressable disabled={!request || busy} onPress={() => setScanFor("envelope")} style={[styles.primary, { backgroundColor: c.accent }]}>
                <Text style={styles.primaryText}>Scan encrypted reply</Text>
              </Pressable>
            )}

            {request && pendingIncoming ? (
              <View style={[styles.confirm, { borderColor: c.border }]}>
                <Text style={[styles.body, { color: c.textMuted }]}>Confirm this code matches the sending device:</Text>
                <Text style={[styles.code, { color: c.textPrimary }]}>{recipientCode}</Text>
                <Pressable disabled={busy} onPress={() => void acceptIncoming()} style={[styles.primary, { backgroundColor: c.accent }]}>
                  <Text style={styles.primaryText}>Codes match · save securely</Text>
                </Pressable>
              </View>
            ) : null}
          </View>
        ) : (
          <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
            {!scannedRequest ? (
              <>
                <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Scan the receiving device</Text>
                <Text style={[styles.body, { color: c.textMuted }]}>Yaver verifies the same account before any credential can be approved.</Text>
                <Pressable onPress={() => setScanFor("request")} style={[styles.primary, { backgroundColor: c.accent }]}>
                  <Text style={styles.primaryText}>Scan request QR</Text>
                </Pressable>
                <Pressable disabled={busy} onPress={() => void findNearby()} style={[styles.secondary, { borderColor: c.border }]}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Find nearby receiver</Text>
                </Pressable>
              </>
            ) : !outgoing ? (
              <>
                <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Choose one credential</Text>
                <Text style={[styles.body, { color: c.textMuted }]}>Only the selected value is read from secure storage, encrypted to device {scannedRequest.targetDeviceId.slice(0, 16)}…, and never copied to the clipboard.</Text>
                {availableKinds.length ? availableKinds.map((kind) => (
                  <Pressable key={kind} disabled={busy} onPress={() => void approveKind(kind)} style={[styles.credential, { borderColor: c.border }]}>
                    <Text style={{ color: c.textPrimary, fontWeight: "700" }}>{LABEL[kind]}</Text>
                    <Text style={{ color: c.accent }}>Approve ›</Text>
                  </Pressable>
                )) : <Text style={[styles.body, { color: c.textMuted }]}>No supported credentials are saved on this device.</Text>}
              </>
            ) : (
              <>
                <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Let the receiving device scan this reply</Text>
                <Text style={[styles.body, { color: c.textMuted }]}>The QR contains authenticated ciphertext encrypted only for that device.</Text>
                <View style={styles.qr}><QRCode value={encodeCredentialHandoffQr(outgoing)} size={236} backgroundColor="#fff" color="#000" /></View>
                <Text style={[styles.body, { color: c.textMuted, textAlign: "center" }]}>Both devices must show:</Text>
                <Text style={[styles.code, { color: c.textPrimary }]}>{senderCode}</Text>
                {bleSenderConnected ? (
                  <Pressable disabled={busy} onPress={() => void deliverNearby()} style={[styles.primary, { backgroundColor: c.accent }]}>
                    <Text style={styles.primaryText}>Send encrypted reply nearby</Text>
                  </Pressable>
                ) : null}
                <Pressable onPress={() => { setScannedRequest(null); setOutgoing(null); }} style={[styles.secondary, { borderColor: c.border }]}>
                  <Text style={{ color: c.textPrimary, fontWeight: "700" }}>Done</Text>
                </Pressable>
              </>
            )}
          </View>
        )}

        <Text style={[styles.footnote, { color: c.textMuted }]}>QR is a transport, not the security boundary. Requests expire, responses are device-targeted and one-time, and credentials are saved only to platform secure storage.</Text>
      </ScrollView>
    </View>
  );
}

function HandoffScanner({ expected, onScanned, onClose }: { expected: Exclude<ScanFor, null>; onScanned: (raw: string) => void; onClose: () => void }) {
  const c = useColors();
  const [permission, requestPermission] = useCameraPermissions();
  const handled = useRef(false);
  const onBarcode = useCallback((result: BarcodeScanningResult) => {
    if (handled.current || !parseCredentialHandoffQr(result.data ?? "")) return;
    handled.current = true;
    onScanned(result.data);
  }, [onScanned]);
  if (!permission?.granted) {
    return (
      <View style={[styles.fill, styles.center, { backgroundColor: c.bg, padding: 28 }]}>
        <Text style={[styles.cardTitle, { color: c.textPrimary }]}>Camera access is required</Text>
        <Text style={[styles.body, { color: c.textMuted, textAlign: "center" }]}>Yaver only accepts a structured secure-handoff QR on this screen.</Text>
        <Pressable onPress={() => void (permission?.canAskAgain === false ? Linking.openSettings() : requestPermission())} style={[styles.primary, { backgroundColor: c.accent }]}>
          <Text style={styles.primaryText}>{permission?.canAskAgain === false ? "Open settings" : "Allow camera"}</Text>
        </Pressable>
        <Pressable onPress={onClose} style={{ padding: 16 }}><Text style={{ color: c.textPrimary }}>Cancel</Text></Pressable>
      </View>
    );
  }
  return (
    <View style={[styles.fill, { backgroundColor: "#000" }]}>
      <CameraView style={StyleSheet.absoluteFill} facing="back" barcodeScannerSettings={{ barcodeTypes: ["qr"] }} onBarcodeScanned={onBarcode} />
      <View style={styles.scanOverlay} pointerEvents="box-none">
        <Text style={styles.scanText}>{expected === "request" ? "Scan the receiving device's request" : "Scan the sending device's encrypted reply"}</Text>
        <View style={styles.reticle} />
        <Pressable onPress={onClose} style={styles.cancel}><Text style={{ color: "#fff", fontWeight: "700" }}>Cancel</Text></Pressable>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  fill: { flex: 1 },
  center: { alignItems: "center", justifyContent: "center" },
  header: { flexDirection: "row", alignItems: "center", gap: 10, paddingHorizontal: 16, paddingVertical: 12 },
  title: { fontSize: 22, fontWeight: "800" },
  content: { width: "100%", maxWidth: 620, alignSelf: "center", padding: 16, gap: 16 },
  segment: { flexDirection: "row", borderWidth: 1, borderRadius: 12, padding: 4 },
  segmentButton: { flex: 1, alignItems: "center", paddingVertical: 10, borderRadius: 9 },
  card: { borderWidth: 1, borderRadius: 16, padding: 18 },
  cardTitle: { fontSize: 17, fontWeight: "800", marginBottom: 6 },
  body: { fontSize: 13, lineHeight: 19, marginBottom: 14 },
  qr: { backgroundColor: "#fff", padding: 14, borderRadius: 12, alignSelf: "center", marginVertical: 12 },
  primary: { minHeight: 46, borderRadius: 11, alignItems: "center", justifyContent: "center", paddingHorizontal: 16 },
  primaryText: { color: "#fff", fontWeight: "800" },
  secondary: { minHeight: 44, borderRadius: 11, borderWidth: 1, alignItems: "center", justifyContent: "center", marginTop: 8 },
  credential: { minHeight: 50, borderWidth: 1, borderRadius: 10, paddingHorizontal: 14, marginTop: 8, flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  confirm: { borderTopWidth: 1, marginTop: 18, paddingTop: 18 },
  code: { fontSize: 34, fontWeight: "900", letterSpacing: 7, textAlign: "center", marginBottom: 16 },
  footnote: { fontSize: 11, lineHeight: 17, textAlign: "center", paddingHorizontal: 12 },
  scanOverlay: { ...StyleSheet.absoluteFillObject, alignItems: "center", justifyContent: "center" },
  scanText: { position: "absolute", top: 80, color: "#fff", fontWeight: "700", fontSize: 15 },
  reticle: { width: 240, height: 240, borderWidth: 2, borderColor: "#fff", borderRadius: 22 },
  cancel: { position: "absolute", bottom: 60, backgroundColor: "rgba(0,0,0,0.6)", paddingHorizontal: 24, paddingVertical: 13, borderRadius: 24 },
});
