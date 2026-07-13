/**
 * Runner-auth browser screen — sign a remote box's coding runner
 * (Claude Code / Codex) into its subscription, driven from the phone.
 *
 * Flow (Claude Code path — Anthropic OAuth):
 *   1. POST /runner-auth/browser/start { runner: "claude" }
 *      → agent spawns `claude auth login --claudeai` and captures the
 *        OpenURL from stdout.
 *   2. We open that URL in the SYSTEM in-app browser sheet
 *      (WebBrowser.openBrowserAsync, PAGE_SHEET) — NOT an embedded
 *      react-native-webview. This matters:
 *        - Google/Apple BLOCK OAuth inside embedded WebViews
 *          (`disallowed_useragent`), so an embedded view simply can't
 *          complete a Google sign-in — which is why users were forced to
 *          copy the link out to an external browser.
 *        - The system sheet is a real browser (shared cookies) AND on iOS
 *          it presents OVER the app, so the QUIC/relay connection stays
 *          alive. Leaving to an external browser backgrounds the app,
 *          drops the relay, and the pasted-code POST then fails with a
 *          relay 401 (the bug this screen exists to avoid).
 *   3. claude.com shows a long code after sign-in. The user taps Copy,
 *      then Done to dismiss the sheet. On return we auto-read the
 *      clipboard and pre-fill the paste field (one-tap confirm).
 *   4. POST /runner-auth/browser/submit-code { id, code } → agent
 *      forwards the code to the running CLI's stdin → writes
 *      ~/.claude/.credentials.json.
 *
 * Flow (Codex path — OpenAI device-auth):
 *   The codex CLI detects completion server-side — no paste step. We just
 *   open the sheet and poll /runner-auth/browser/status for "completed".
 *
 * Why not auto-capture the code from the browser? claude's redirect is the
 * HOSTED platform.claude.com/oauth/code/callback page (manual code
 * display); it never redirects to a Yaver app scheme, so there is no
 * deep-link to intercept. Clipboard pre-fill is the closest we can get.
 *
 * Per CLAUDE.md privacy contract: the code travels once over HTTPS/QUIC,
 * is forwarded to stdin, and is never persisted in Yaver storage.
 */

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import * as WebBrowser from "expo-web-browser";
import * as Clipboard from "expo-clipboard";
import { Ionicons } from "@expo/vector-icons";
import { useRouter } from "expo-router";
import { useLocalSearchParams } from "expo-router";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";
import { YaverGlass } from "../../src/components/YaverGlass";

type SessionStatus = "idle" | "awaiting_url" | "ready_to_sign_in" | "signing_in" | "awaiting_code" | "submitting" | "completed" | "cancelled" | "error";

interface Snapshot {
  id?: string;
  runner?: string;
  status?: string;
  openUrl?: string;
  code?: string;
  message?: string;
  error?: string;
}

// A plausible Claude OAuth code is a long token, often `<code>#<state>`,
// with no whitespace. We only use this to decide whether to PRE-FILL the
// field from the clipboard — the user always confirms before submit, so a
// loose check is safe and just saves a paste.
function looksLikeAuthCode(s: string): boolean {
  const t = s.trim();
  return t.length >= 16 && !/\s/.test(t);
}

export default function RunnerAuthBrowserScreen(): React.JSX.Element {
  const c = useColors();
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const params = useLocalSearchParams<{ runner?: string; target?: string }>();
  const runner = (params.runner ?? "claude").toLowerCase();

  const [sessionId, setSessionId] = useState("");
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [phase, setPhase] = useState<SessionStatus>("idle");
  const [pasted, setPasted] = useState("");
  const [errorMsg, setErrorMsg] = useState("");
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  // Step 1: kick off the agent-side browser-auth session.
  const start = useCallback(async () => {
    setPhase("awaiting_url");
    setErrorMsg("");
    try {
      const res = await fetch(`${quicClient.baseUrl}/runner-auth/browser/start`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...quicClient.getAuthHeaders() },
        body: JSON.stringify({ runner }),
      });
      if (!res.ok) throw new Error(`start: HTTP ${res.status}: ${(await res.text()).slice(0, 200)}`);
      const body = await res.json();
      const sess: Snapshot = body.session ?? {};
      setSnapshot(sess);
      setSessionId(sess.id ?? "");
      if (sess.openUrl) setPhase("ready_to_sign_in");
      else setPhase("awaiting_url"); // openURL may arrive on a subsequent poll
    } catch (err: any) {
      setErrorMsg(err?.message ?? String(err));
      setPhase("error");
    }
  }, [runner]);

  // Auto-start on mount
  useEffect(() => { void start(); }, [start]);

  // Poll the agent for status until the URL appears or the session
  // completes. Codex completes server-side; Claude requires a paste.
  useEffect(() => {
    if (!sessionId) return;
    const poll = async () => {
      try {
        const r = await fetch(`${quicClient.baseUrl}/runner-auth/browser/status?id=${encodeURIComponent(sessionId)}`, {
          headers: quicClient.getAuthHeaders(),
        });
        if (!r.ok) return;
        const body = await r.json();
        const sess: Snapshot = body.session ?? {};
        setSnapshot(sess);
        setPhase((prev) => (sess.openUrl && prev === "awaiting_url" ? "ready_to_sign_in" : prev));
        const s = (sess.status ?? "").toLowerCase();
        if (s === "completed" || s === "success") setPhase("completed");
        if (s === "cancelled" || s === "canceled") setPhase("cancelled");
        if (s === "error" || s === "failed") {
          setPhase("error");
          setErrorMsg(sess.error ?? sess.message ?? "auth session failed");
        }
      } catch { /* swallow polling errors */ }
    };
    void poll();
    pollRef.current = setInterval(poll, 1500);
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [sessionId]);

  // Auto-back on completion
  useEffect(() => {
    if (phase !== "completed") return;
    const t = setTimeout(() => router.back(), 1800);
    return () => clearTimeout(t);
  }, [phase, router]);

  // Step 2: open the sign-in URL in the SYSTEM in-app browser sheet. On
  // iOS this presents over the app (connection survives); on Android it's
  // a Custom Tab. When the sheet is dismissed we auto-read the clipboard
  // so the copied code lands in the field with no manual paste.
  const openSignIn = useCallback(async () => {
    const url = snapshot?.openUrl;
    if (!url) return;
    setErrorMsg("");
    try {
      setPhase("signing_in");
      await WebBrowser.openBrowserAsync(url, {
        dismissButtonStyle: "done",
        controlsColor: c.accent,
        presentationStyle: WebBrowser.WebBrowserPresentationStyle.PAGE_SHEET,
        toolbarColor: c.bgCard,
      });
      // Sheet dismissed → try to lift the copied code off the clipboard.
      if (runner === "claude") {
        try {
          const clip = (await Clipboard.getStringAsync())?.trim() ?? "";
          if (clip && looksLikeAuthCode(clip)) setPasted(clip);
        } catch { /* clipboard unavailable — user can paste manually */ }
        setPhase("awaiting_code");
      }
      // Codex needs no paste; polling will flip to completed.
    } catch (err: any) {
      // openBrowserAsync rejects on old Android / web — fall back to the
      // external browser so the user can still complete OAuth. The
      // submit-code path reconnects the relay before posting, so the
      // background/foreground round-trip no longer 401s.
      try {
        await Linking.openURL(url);
        if (runner === "claude") setPhase("awaiting_code");
      } catch {
        setErrorMsg(err?.message ?? "Could not open the sign-in page.");
        setPhase("error");
      }
    }
  }, [snapshot?.openUrl, runner, c.accent, c.bgCard]);

  const pasteFromClipboard = useCallback(async () => {
    try {
      const clip = (await Clipboard.getStringAsync())?.trim() ?? "";
      if (clip) setPasted(clip);
      else Alert.alert("Clipboard empty", "Copy the code from the Claude page first.");
    } catch {
      Alert.alert("Clipboard unavailable", "Paste the code into the field manually.");
    }
  }, []);

  // Wait until the client has a usable transport before POSTing the code.
  // Completing OAuth can briefly background the app (Android Custom Tab, or
  // an external-browser fallback) and drop the relay; on return,
  // `authHeaders` omits `X-Relay-Password` until the relay re-attaches, so
  // a too-eager POST is rejected BY THE RELAY with 401 and never reaches
  // the agent. We kick a reconnect and poll until connected (and, on a
  // relay route, until the relay password is back).
  const waitForLiveConnection = useCallback(async (timeoutMs = 9000): Promise<boolean> => {
    const relayReady = () => !quicClient.activeRelayHttpUrl || !!quicClient.activeRelayPasswordValue;
    if (quicClient.isConnected && relayReady()) return true;
    try { quicClient.fullReconnect(); } catch { /* best effort */ }
    const start = Date.now();
    while (Date.now() - start < timeoutMs) {
      if (quicClient.isConnected && relayReady()) return true;
      await new Promise((res) => setTimeout(res, 250));
    }
    return quicClient.isConnected;
  }, []);

  const submitCode = useCallback(async () => {
    const code = pasted.trim();
    if (!code) {
      Alert.alert("Paste your code", "Copy the long string from the Claude page and paste it here.");
      return;
    }
    setPhase("submitting");
    setErrorMsg("");
    const post = () =>
      fetch(`${quicClient.baseUrl}/runner-auth/browser/submit-code?id=${encodeURIComponent(sessionId)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...quicClient.getAuthHeaders() },
        body: JSON.stringify({ id: sessionId, code }),
      });
    try {
      await waitForLiveConnection();
      let r = await post();
      // A 401/403 here is almost always the relay rejecting an
      // unauthenticated forward because the connection hadn't fully
      // re-attached yet. Give the transport a moment and retry once.
      if (r.status === 401 || r.status === 403) {
        await waitForLiveConnection();
        r = await post();
      }
      if (!r.ok) throw new Error(`submit: HTTP ${r.status}: ${(await r.text()).slice(0, 200)}`);
      setPasted("");
      setPhase("awaiting_code"); // brief loop until status flips to completed
    } catch (err: any) {
      setErrorMsg(err?.message ?? String(err));
      setPhase("error");
    }
  }, [pasted, sessionId, waitForLiveConnection]);

  const cancel = useCallback(async () => {
    if (sessionId) {
      try {
        await fetch(`${quicClient.baseUrl}/runner-auth/browser/cancel?id=${encodeURIComponent(sessionId)}`, {
          method: "POST",
          headers: { "Content-Type": "application/json", ...quicClient.getAuthHeaders() },
          body: JSON.stringify({ id: sessionId }),
        });
      } catch { /* best effort */ }
    }
    router.back();
  }, [sessionId, router]);

  const runnerLabel = runner === "claude" ? "Claude Code" : runner === "codex" ? "Codex" : runner;
  const hasUrl = !!snapshot?.openUrl;
  const wantsPaste = runner === "claude" && (phase === "awaiting_code" || phase === "submitting");
  const canSignIn = hasUrl && (phase === "ready_to_sign_in" || phase === "signing_in" || phase === "awaiting_code" || phase === "error");

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
      <View style={[styles.headerRow, { paddingTop: 8 }]}>
        <Pressable onPress={cancel} hitSlop={12}>
          <Ionicons name="close" size={24} color={c.textPrimary} />
        </Pressable>
        <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "600" }}>
          {runnerLabel} sign-in
        </Text>
        <View style={{ width: 24 }} />
      </View>

      {/* Status banner */}
      <YaverGlass tint={c.bgCard} style={styles.banner}>
        <View style={styles.bannerInner}>
          <Ionicons name={iconFor(phase)} size={20} color={tintFor(phase, c)} />
          <View style={{ flex: 1 }}>
            <Text style={[styles.bannerTitle, { color: c.textPrimary }]}>
              {labelFor(phase, runnerLabel)}
            </Text>
            <Text style={[styles.bannerSub, { color: c.textMuted }]}>
              {errorMsg || subFor(phase, runner)}
            </Text>
          </View>
          {(phase === "awaiting_url" || phase === "submitting" || phase === "signing_in") && (
            <ActivityIndicator color={c.accent} />
          )}
        </View>
      </YaverGlass>

      <View style={{ flex: 1, paddingHorizontal: 16, paddingTop: 16, gap: 14 }}>
        {/* Sign-in launcher */}
        {hasUrl ? (
          <>
            <Pressable
              onPress={openSignIn}
              style={[styles.btnPrimary, { backgroundColor: c.accent }]}
            >
              <Ionicons name="globe-outline" size={18} color="#fff" />
              <Text style={[styles.btnText, { color: "#fff" }]}>
                {phase === "awaiting_code" ? "Reopen sign-in" : `Sign in to ${runnerLabel}`}
              </Text>
            </Pressable>
            <Text style={[styles.help, { color: c.textMuted }]}>
              Opens a secure in-app browser. Sign in with your{" "}
              {runner === "claude" ? "Max / Pro subscription" : "ChatGPT Plus subscription"}.
              {runner === "claude"
                ? " At the end, tap Copy on the code, then Done — we'll fill it in for you."
                : " Codex finishes automatically — just close the sheet when done."}
            </Text>
            <Pressable onPress={() => snapshot?.openUrl && Linking.openURL(snapshot.openUrl)} hitSlop={8}>
              <Text style={[styles.linkFallback, { color: c.textMuted }]}>
                Trouble? Open in your default browser instead
              </Text>
            </Pressable>
          </>
        ) : (
          <View style={[styles.center, { flex: 1 }]}>
            <ActivityIndicator color={c.accent} />
            <Text style={[styles.dim, { color: c.textMuted, marginTop: 8 }]}>
              Waiting for the agent to spawn the sign-in flow…
            </Text>
          </View>
        )}

        {/* Paste-back field (Claude only) — pre-filled from clipboard on return */}
        {wantsPaste && (
          <YaverGlass tint={c.bgCard} style={styles.pasteCard}>
            <View style={styles.pasteInner}>
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                <Text style={[styles.pasteLabel, { color: c.textMuted, flex: 1 }]}>
                  Paste the code from {runnerLabel} (we try to fill it automatically).
                </Text>
                <Pressable onPress={pasteFromClipboard} hitSlop={8} style={{ paddingLeft: 8 }}>
                  <Ionicons name="clipboard-outline" size={20} color={c.accent} />
                </Pressable>
              </View>
              <TextInput
                value={pasted}
                onChangeText={setPasted}
                placeholder="Paste auth code"
                placeholderTextColor={c.textMuted}
                autoCapitalize="none"
                autoCorrect={false}
                multiline
                style={[styles.pasteField, { color: c.textPrimary, backgroundColor: c.bg, borderColor: c.border }]}
              />
              <Pressable
                onPress={submitCode}
                style={[styles.btnPrimary, { backgroundColor: c.accent }]}
                disabled={!pasted.trim() || phase === "submitting"}
              >
                <Text style={[styles.btnText, { color: "#fff" }]}>
                  {phase === "submitting" ? "Submitting…" : "Submit"}
                </Text>
              </Pressable>
              <Text style={[styles.footnote, { color: c.textMuted }]}>
                Subscription OAuth only · never API keys
              </Text>
            </View>
          </YaverGlass>
        )}
      </View>
    </SafeAreaView>
  );
}

function iconFor(p: SessionStatus): React.ComponentProps<typeof Ionicons>["name"] {
  switch (p) {
    case "completed": return "checkmark-circle";
    case "cancelled": return "close-circle";
    case "error": return "alert-circle";
    case "submitting":
    case "awaiting_url":
    case "signing_in":
    case "awaiting_code": return "time-outline";
    default: return "globe-outline";
  }
}

function tintFor(p: SessionStatus, c: ReturnType<typeof useColors>): string {
  switch (p) {
    case "completed": return "#10b981";
    case "cancelled": return c.textMuted;
    case "error": return "#ef4444";
    default: return c.accent;
  }
}

function labelFor(p: SessionStatus, runner: string): string {
  switch (p) {
    case "idle": return "Starting…";
    case "awaiting_url": return "Spawning sign-in on remote…";
    case "ready_to_sign_in": return `Ready — sign in to ${runner}`;
    case "signing_in": return "Signing in…";
    case "awaiting_code": return runner === "Codex" ? "Completing on remote…" : "Paste the code to finish";
    case "submitting": return "Submitting code…";
    case "completed": return `${runner} ready`;
    case "cancelled": return "Cancelled";
    case "error": return "Auth failed";
  }
}

function subFor(p: SessionStatus, runner: string): string {
  switch (p) {
    case "idle":
    case "awaiting_url": return "Once we have the sign-in URL we'll open it in a secure in-app browser.";
    case "ready_to_sign_in":
      return runner === "claude"
        ? "Tap below — Google/Apple sign-in works in the in-app browser."
        : "Tap below to sign in with your ChatGPT Plus subscription.";
    case "signing_in": return "Complete sign-in, copy the code, then tap Done.";
    case "awaiting_code": return runner === "codex" ? "Looking for completion…" : "We filled the code if it was on your clipboard — confirm and submit.";
    case "submitting": return "Forwarding to the runner CLI…";
    case "completed": return "Credentials saved on the remote machine.";
    case "cancelled": return "You cancelled before completion.";
    case "error": return "Something went wrong — try again or close.";
  }
}

const styles = StyleSheet.create({
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 8,
  },
  banner: { marginHorizontal: 12, marginTop: 4, borderRadius: 10, overflow: "hidden" },
  bannerInner: { flexDirection: "row", alignItems: "center", gap: 10, padding: 12 },
  bannerTitle: { fontSize: 14, fontWeight: "600" },
  bannerSub: { fontSize: 11, lineHeight: 16 },
  center: { alignItems: "center", justifyContent: "center" },
  dim: { fontSize: 12 },
  help: { fontSize: 12, lineHeight: 18 },
  linkFallback: { fontSize: 12, textDecorationLine: "underline" },
  pasteCard: { borderRadius: 12, overflow: "hidden", marginTop: 4 },
  pasteInner: { padding: 14, gap: 10 },
  pasteLabel: { fontSize: 12, lineHeight: 16 },
  pasteField: {
    minHeight: 64,
    maxHeight: 120,
    borderWidth: 1,
    borderRadius: 8,
    padding: 10,
    fontFamily: "Menlo",
    fontSize: 12,
  },
  btnPrimary: { flexDirection: "row", gap: 8, paddingVertical: 12, borderRadius: 8, alignItems: "center", justifyContent: "center" },
  btnText: { fontSize: 14, fontWeight: "600" },
  footnote: { fontSize: 10, textAlign: "center" },
});
