/**
 * Runner-auth browser screen — the phone-relay device-auth fallback
 * for when the user has no signed-in Mac to mirror from.
 *
 * Flow (Claude Code path — Anthropic OAuth):
 *   1. POST /runner-auth/browser/start { runner: "claude" }
 *      → agent spawns `claude auth login --claudeai` and captures
 *        the OpenURL from stdout
 *   2. Open OpenURL in an in-app WebView. User signs in at
 *      claude.ai/code.
 *   3. Claude.ai/code shows a long secret string after sign-in.
 *      User taps "Copy", switches to Yaver (we render a paste field
 *      below the WebView for this), pastes.
 *   4. POST /runner-auth/browser/submit-code { id, code }
 *      → agent forwards the paste to the running CLI's stdin
 *   5. Agent writes ~/.claude/.credentials.json; ledger updates;
 *      blackbox bus fires runner_auth_completed → glass speaks
 *
 * Flow (Codex path — OpenAI device-auth):
 *   1. Same as Claude through step 2, but the OpenURL is
 *      https://chatgpt.com/connect?code=ABCD-EFGH and the user
 *      completes OAuth there.
 *   2. The codex CLI itself detects completion via polling —
 *      no paste step needed. We poll /runner-auth/browser/status
 *      for `status: "completed"`.
 *
 * Per CLAUDE.md privacy contract: the pasted code travels once
 * over HTTPS/QUIC, is forwarded to stdin, never persisted in
 * Yaver storage.
 *
 * Per feedback_no_api_keys_subscription_only: this screen ONLY
 * accepts subscription-OAuth tokens. There is no API-key
 * paste-back path.
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
import { WebView, type WebViewNavigation } from "react-native-webview";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { SafeAreaView, useSafeAreaInsets } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";
import { YaverGlass } from "../../src/components/YaverGlass";

type SessionStatus = "idle" | "awaiting_url" | "ready_to_sign_in" | "awaiting_code" | "submitting" | "completed" | "cancelled" | "error";

interface Snapshot {
  id?: string;
  runner?: string;
  status?: string;
  openURL?: string;
  code?: string;
  message?: string;
  error?: string;
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
      if (sess.openURL) setPhase("ready_to_sign_in");
      else setPhase("awaiting_url"); // openURL may arrive on a subsequent poll
    } catch (err: any) {
      setErrorMsg(err?.message ?? String(err));
      setPhase("error");
    }
  }, [runner]);

  // Auto-start on mount
  useEffect(() => { void start(); }, [start]);

  // Step 2-4: poll the agent for status until URL appears or session
  // completes. Codex completes server-side without a paste; Claude
  // requires a paste.
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
        if (sess.openURL && phase === "awaiting_url") setPhase("ready_to_sign_in");
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
  }, [sessionId, phase]);

  // Auto-back on completion
  useEffect(() => {
    if (phase !== "completed") return;
    const t = setTimeout(() => router.back(), 1800);
    return () => clearTimeout(t);
  }, [phase, router]);

  const submitCode = useCallback(async () => {
    const code = pasted.trim();
    if (!code) {
      Alert.alert("Paste your code", "Copy the long string from the Claude page and paste it here.");
      return;
    }
    setPhase("submitting");
    setErrorMsg("");
    try {
      const r = await fetch(`${quicClient.baseUrl}/runner-auth/browser/submit-code`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...quicClient.getAuthHeaders() },
        body: JSON.stringify({ id: sessionId, code }),
      });
      if (!r.ok) throw new Error(`submit: HTTP ${r.status}: ${(await r.text()).slice(0, 200)}`);
      // Don't snap to "completed" here — let polling confirm so we
      // know the agent actually wrote the credentials file.
      setPasted("");
      setPhase("awaiting_code"); // brief loop until status flips to completed
    } catch (err: any) {
      setErrorMsg(err?.message ?? String(err));
      setPhase("error");
    }
  }, [pasted, sessionId]);

  const cancel = useCallback(async () => {
    if (sessionId) {
      try {
        await fetch(`${quicClient.baseUrl}/runner-auth/browser/cancel`, {
          method: "POST",
          headers: { "Content-Type": "application/json", ...quicClient.getAuthHeaders() },
          body: JSON.stringify({ id: sessionId }),
        });
      } catch { /* best effort */ }
    }
    router.back();
  }, [sessionId, router]);

  const onWebNav = useCallback((nav: WebViewNavigation) => {
    // Both Claude and Codex flows finish on the platform's "auth
    // complete" page. For Claude, the next step is the user copying
    // the visible token string + pasting below. For Codex, the agent
    // CLI handles the callback itself — no paste field rendered.
    if (runner === "claude" && phase === "ready_to_sign_in") {
      // Detect that we landed on a code-shown page so we can flip
      // into "awaiting_code" mode + reveal the paste field.
      const url = nav.url ?? "";
      if (/claude\.ai.*\/(?:code|authorize|success)/i.test(url)) {
        setPhase("awaiting_code");
      }
    }
  }, [runner, phase]);

  const wantsPaste = runner === "claude" && (phase === "ready_to_sign_in" || phase === "awaiting_code" || phase === "submitting");
  const runnerLabel = runner === "claude" ? "Claude Code" : runner === "codex" ? "Codex" : runner;

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
          {(phase === "awaiting_url" || phase === "submitting" || phase === "awaiting_code") && (
            <ActivityIndicator color={c.accent} />
          )}
        </View>
      </YaverGlass>

      {/* WebView — only show once we have an OpenURL */}
      {snapshot?.openURL ? (
        <View style={{ flex: 1, marginHorizontal: 12, borderRadius: 10, overflow: "hidden", backgroundColor: "#000", marginTop: 8 }}>
          <WebView
            source={{ uri: snapshot.openURL }}
            onNavigationStateChange={onWebNav}
            javaScriptEnabled
            domStorageEnabled
            sharedCookiesEnabled
            thirdPartyCookiesEnabled
            originWhitelist={["*"]}
            applicationNameForUserAgent="Yaver-RN-WebView"
          />
        </View>
      ) : (
        <View style={[styles.center, { flex: 1 }]}>
          <ActivityIndicator color={c.accent} />
          <Text style={[styles.dim, { color: c.textMuted, marginTop: 8 }]}>
            Waiting for the agent to spawn the auth flow…
          </Text>
        </View>
      )}

      {/* Paste-back field (Claude only) */}
      {wantsPaste && (
        <YaverGlass tint={c.bgCard} style={{ ...styles.pasteCard, marginBottom: Math.max(insets.bottom, 12) }}>
          <View style={styles.pasteInner}>
            <Text style={[styles.pasteLabel, { color: c.textMuted }]}>
              After signing in, copy the long string from {runnerLabel} and paste here.
            </Text>
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
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Pressable
                onPress={submitCode}
                style={[styles.btnPrimary, { backgroundColor: c.accent, flex: 1 }]}
                disabled={!pasted.trim() || phase === "submitting"}
              >
                <Text style={[styles.btnText, { color: "#fff" }]}>
                  {phase === "submitting" ? "Submitting…" : "Submit"}
                </Text>
              </Pressable>
              {snapshot?.openURL && (
                <Pressable
                  onPress={() => Linking.openURL(snapshot.openURL!)}
                  style={styles.btnSecondaryWrap}
                >
                  <Ionicons name="open-outline" size={20} color={c.textMuted} />
                </Pressable>
              )}
            </View>
            <Text style={[styles.footnote, { color: c.textMuted }]}>
              Subscription OAuth only · never API keys
            </Text>
          </View>
        </YaverGlass>
      )}
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
    case "awaiting_url": return "Spawning auth flow on remote…";
    case "ready_to_sign_in": return `Sign in to ${runner}`;
    case "awaiting_code": return runner === "Claude Code" ? "Paste the code shown above" : "Completing on remote…";
    case "submitting": return "Submitting code…";
    case "completed": return `${runner} ready`;
    case "cancelled": return "Cancelled";
    case "error": return "Auth failed";
  }
}

function subFor(p: SessionStatus, runner: string): string {
  switch (p) {
    case "idle":
    case "awaiting_url": return "Once we have the sign-in URL we'll open it below.";
    case "ready_to_sign_in":
      return runner === "claude"
        ? "Sign in with your Max Pro subscription. You'll see a long code string at the end — copy + paste it."
        : "Sign in with your ChatGPT Plus subscription. Codex will detect completion automatically.";
    case "awaiting_code": return "Looking for the pasted code…";
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
  pasteCard: { marginHorizontal: 12, marginTop: 8, borderRadius: 12, overflow: "hidden" },
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
  btnPrimary: { paddingVertical: 12, borderRadius: 8, alignItems: "center" },
  btnSecondaryWrap: { paddingHorizontal: 14, alignItems: "center", justifyContent: "center" },
  btnText: { fontSize: 14, fontWeight: "600" },
  footnote: { fontSize: 10, textAlign: "center" },
});
