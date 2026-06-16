// RunnerAuthModal — mobile mirror of web's RunnerAuthModal in
// web/app/dashboard/page.tsx. Drives the /runner-auth/browser/* flow
// for Claude Code / Codex on a remote box, including the paste-back-
// code step Anthropic's OAuth needs.
//
// Use case: user is away from desktop, on phone, claude refresh token
// rotated on yaver-test-ephemeral. From devices tab they tap [auth →]
// on the device's runner row → this modal opens → expo-web-browser
// hands off to platform.claude.com → user OAuths with their Apple /
// Google identity → callback page shows the code → user copies and
// pastes it into the input below → token saved on the remote box and
// the modal auto-closes when the agent confirms.

import React, { useEffect, useRef, useState } from "react";
import {
  Modal,
  View,
  Text,
  StyleSheet,
  TouchableOpacity,
  TextInput,
  ActivityIndicator,
  ScrollView,
  KeyboardAvoidingView,
  Platform,
} from "react-native";
import * as WebBrowser from "expo-web-browser";
import * as Clipboard from "expo-clipboard";

import {
  quicClient,
  unwrapRunnerBrowserAuthEnvelope,
  type RunnerBrowserAuthSession,
} from "../lib/quic";

interface Props {
  visible: boolean;
  runner: string;          // "claude" | "codex"
  deviceName: string;      // e.g. "yaver-test-ephemeral"
  baseUrl?: string;
  headers?: Record<string, string>;
  /** When provided, runner-auth requests are routed via /peer/<id>
   *  to the named device instead of the connected agent. Use this
   *  when the device the user wants to re-auth is NOT the same
   *  device the mobile app is connected to. */
  target?: string;
  onClose: () => void;
  onCompleted?: () => void;
}

const runnerLabel = (runner: string) => {
  const id = (runner || "").toLowerCase();
  if (id === "claude" || id === "claude-code") return "Claude Code";
  if (id === "codex") return "Codex";
  if (id === "opencode") return "OpenCode";
  if (id === "glm") return "GLM (z.ai)";
  return runner || "agent";
};

export default function RunnerAuthModal({
  visible,
  runner,
  deviceName,
  baseUrl,
  headers,
  target,
  onClose,
  onCompleted,
}: Props) {
  const [session, setSession] = useState<RunnerBrowserAuthSession | null>(null);
  const [startError, setStartError] = useState<string | null>(null);
  const [pasteCode, setPasteCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  // Transient "Copied" feedback per copy target. Cleared after 1.6s so the
  // button reverts to its default label without a stale tick lingering.
  const [copied, setCopied] = useState<"url" | "code" | null>(null);
  const startedRef = useRef(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const normalizedRunner = (runner || "").toLowerCase();
  const needsPasteCode = normalizedRunner === "claude" || normalizedRunner === "claude-code";

  const callRunnerAuth = async (
    path: "start" | "status" | "cancel" | "submit-code",
    init?: RequestInit,
    sessionId?: string,
  ): Promise<any> => {
    const trimmedBaseUrl = String(baseUrl || "").trim();
    const requestHeaders = { ...(headers || {}) };
    if (trimmedBaseUrl) {
      const url = new URL(`${trimmedBaseUrl}/runner-auth/browser/${path}`);
      if (sessionId) url.searchParams.set("id", sessionId);
      const res = await fetch(url.toString(), {
        ...init,
        headers: {
          ...requestHeaders,
          ...(init?.headers || {}),
        },
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data?.error || `runner auth ${path} ${res.status}`);
      // Cancel returns nothing useful; everything else is the standard
      // {ok, session} envelope and the modal expects the flat session.
      if (path === "cancel") return data;
      return unwrapRunnerBrowserAuthEnvelope(data);
    }
    if (path === "start") return quicClient.startRunnerBrowserAuth(runner, target);
    if (path === "status") return quicClient.getRunnerBrowserAuthStatus(sessionId || "", target);
    if (path === "submit-code") {
      return quicClient.submitRunnerBrowserAuthCode(
        sessionId || "",
        JSON.parse(String(init?.body || "{}")).code || "",
        target,
      );
    }
    await quicClient.cancelRunnerBrowserAuth(sessionId || "", target);
    return null;
  };

  // Reset on close so re-opening always starts fresh.
  useEffect(() => {
    if (!visible) {
      startedRef.current = false;
      setSession(null);
      setStartError(null);
      setPasteCode("");
      setSubmitError(null);
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
    }
  }, [visible]);

  // Kick off the remote OAuth flow once when the modal opens.
  useEffect(() => {
    if (!visible || startedRef.current) return;
    startedRef.current = true;
    (async () => {
      try {
        const started = await callRunnerAuth("start", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ runner }),
        });
        setSession(started);
      } catch (err) {
        setStartError(err instanceof Error ? err.message : String(err));
      }
    })();
  }, [visible, runner, target, baseUrl, JSON.stringify(headers || {})]);

  // Poll for completion. Mirrors the web modal's 1.5s cadence. Surface
  // polling failures after a few consecutive misses so a wedged session
  // (agent restarted, transport flapped) doesn't spin forever — silent
  // catch is exactly how this modal stayed stuck on "Waiting for the
  // verification URL…" before.
  useEffect(() => {
    if (!session) return;
    if (
      session.status === "completed" ||
      session.status === "failed" ||
      session.status === "cancelled"
    ) {
      if (session.status === "completed") onCompleted?.();
      return;
    }
    let consecutiveFailures = 0;
    pollRef.current = setInterval(async () => {
      try {
        const next = await callRunnerAuth("status", undefined, session.id);
        consecutiveFailures = 0;
        setSession(next);
        if (next?.status === "completed") {
          onCompleted?.();
          onClose();
        }
      } catch (err) {
        consecutiveFailures += 1;
        if (consecutiveFailures >= 4) {
          setStartError(
            err instanceof Error
              ? `Lost contact with the remote machine: ${err.message}`
              : "Lost contact with the remote machine.",
          );
          if (pollRef.current) {
            clearInterval(pollRef.current);
            pollRef.current = null;
          }
        }
      }
    }, 1500);
    return () => {
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, [session, target, onCompleted, onClose, baseUrl, JSON.stringify(headers || {})]);

  const terminal = session && ["completed", "failed", "cancelled"].includes(session.status);

  const copyToClipboard = async (target: "url" | "code", value: string) => {
    if (!value) return;
    try {
      await Clipboard.setStringAsync(value);
      setCopied(target);
      // Reset after the user has had time to register the confirmation.
      // 1.6s is the same window the dashboard "Copied!" pill uses.
      setTimeout(() => {
        setCopied((c) => (c === target ? null : c));
      }, 1600);
    } catch {
      // expo-clipboard rejects on web; fall through silently. Long-press
      // → Copy on the selectable Text still works as a backup.
    }
  };

  const openAuthUrl = async () => {
    if (!session?.openUrl) return;
    try {
      await WebBrowser.openBrowserAsync(session.openUrl, {
        // Persist phone session cookies so existing google.com /
        // apple.com sign-ins don't have to re-prompt.
        showTitle: false,
        controlsColor: "#7c3aed",
      });
    } catch {
      // Fallback: copy via clipboard message; user pastes manually.
    }
    // openBrowserAsync resolves when the user dismisses the sheet (iOS
    // suspends our JS while the sheet is fullscreen). Once we're back,
    // immediately re-poll status so a "completed" flip we missed during
    // suspension surfaces in the next tick instead of the user staring
    // at the URL+code box on a session the agent already closed.
    if (session?.id) {
      try {
        const next = await callRunnerAuth("status", undefined, session.id);
        setSession(next);
      } catch {
        // The interval poll will catch up — non-fatal.
      }
    }
  };

  const submitCode = async () => {
    if (!session || !pasteCode.trim()) return;
    setSubmitting(true);
    setSubmitError(null);
    try {
      // Agent expects `id` in the URL query string and `code` in the body.
      // Pass session.id as the third arg so callRunnerAuth's direct-fetch
      // branch sets ?id=…; the body only carries the code.
      const next = await callRunnerAuth(
        "submit-code",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ code: pasteCode.trim() }),
        },
        session.id,
      );
      setSession(next);
      setPasteCode("");
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  };

  const cancel = async () => {
    if (session && !terminal) {
      await callRunnerAuth("cancel", { method: "POST" }, session.id).catch(() => {});
    }
    onClose();
  };

  return (
    <Modal visible={visible} transparent animationType="fade" onRequestClose={cancel}>
      <KeyboardAvoidingView
        behavior={Platform.OS === "ios" ? "padding" : "height"}
        style={styles.backdrop}
      >
        <View style={styles.card}>
          <View style={styles.headerRow}>
            <View style={{ flex: 1 }}>
              <Text style={styles.title}>Sign in to {runnerLabel(runner)}</Text>
              <Text style={styles.subtitle}>on <Text style={styles.deviceName}>{deviceName}</Text></Text>
            </View>
            <TouchableOpacity onPress={cancel} hitSlop={10}>
              <Text style={styles.closeBtn}>×</Text>
            </TouchableOpacity>
          </View>

          <ScrollView contentContainerStyle={{ paddingBottom: 8 }} keyboardShouldPersistTaps="handled">
            {startError ? (
              <View style={styles.errorBox}>
                <Text style={styles.errorTitle}>Couldn&apos;t start sign-in</Text>
                <Text style={styles.errorBody}>{startError}</Text>
              </View>
            ) : !session ? (
              <View style={styles.infoBox}>
                <ActivityIndicator size="small" color="#a78bfa" />
                <Text style={styles.infoText}>Starting the sign-in flow on the remote machine…</Text>
              </View>
            ) : session.status === "completed" ? (
              <View style={styles.successBox}>
                <Text style={styles.successTitle}>Signed in</Text>
                <Text style={styles.successBody}>
                  {session.detail || "Auth stored on the remote machine."}
                </Text>
              </View>
            ) : session.status === "failed" || session.status === "cancelled" ? (
              <View style={styles.errorBox}>
                <Text style={styles.errorTitle}>
                  {session.status === "cancelled" ? "Cancelled" : "Failed"}
                </Text>
                <Text style={styles.errorBody}>
                  {session.error || session.detail || "The CLI exited before sign-in completed."}
                </Text>
              </View>
            ) : session.status === "verifying" ? (
              // Set after the user submits the paste-back code (Claude
              // Code) or after the codex CLI is confirming with OpenAI.
              // Show a spinner — re-rendering the URL + paste box at this
              // point is confusing because the URL has already been used.
              <View style={styles.infoBox}>
                <ActivityIndicator size="small" color="#a78bfa" />
                <Text style={styles.infoText}>
                  {session.detail || `Confirming sign-in on ${runnerLabel(runner)}…`}
                </Text>
              </View>
            ) : (
              <View>
                <Text style={styles.help}>
                  {needsPasteCode
                    ? `Yaver started the remote ${runnerLabel(runner)} login flow on this machine. Tap the link below to authorize, then paste the resulting code back here.`
                    : `Yaver started the remote ${runnerLabel(runner)} device-auth flow on this machine. Tap the link below, enter the code if prompted, and this dialog will turn green automatically.`}
                </Text>
                {session.openUrl ? (
                  <View style={styles.urlButton}>
                    <TouchableOpacity onPress={openAuthUrl} style={styles.urlOpenRow}>
                      <Text style={styles.urlLabel}>↗ Open authorize page</Text>
                    </TouchableOpacity>
                    {/* selectable + an explicit Copy chip — selectable
                        alone hides the action behind a long-press
                        gesture most users never discover. */}
                    <Text style={styles.urlValue} numberOfLines={1} selectable>
                      {session.openUrl}
                    </Text>
                    <TouchableOpacity
                      onPress={() => copyToClipboard("url", session.openUrl ?? "")}
                      style={styles.copyChip}
                    >
                      <Text style={styles.copyChipText}>
                        {copied === "url" ? "✓ Copied" : "Copy URL"}
                      </Text>
                    </TouchableOpacity>
                  </View>
                ) : (
                  <View style={styles.urlPending}>
                    <ActivityIndicator size="small" color="#94a3b8" />
                    <Text style={styles.urlPendingText}>Waiting for the verification URL from the remote CLI…</Text>
                  </View>
                )}

                {session.code ? (
                  <View style={styles.codeBox}>
                    <View style={styles.codeHeaderRow}>
                      <Text style={styles.codeLabel}>Enter this code</Text>
                      <TouchableOpacity
                        onPress={() => copyToClipboard("code", session.code ?? "")}
                        style={styles.copyChip}
                      >
                        <Text style={styles.copyChipText}>
                          {copied === "code" ? "✓ Copied" : "Copy"}
                        </Text>
                      </TouchableOpacity>
                    </View>
                    <Text style={styles.codeValue} selectable>
                      {session.code}
                    </Text>
                  </View>
                ) : null}

                {needsPasteCode ? (
                  <View style={styles.pasteBox}>
                    <Text style={styles.pasteLabel}>Paste the code from {runnerLabel(runner)}</Text>
                    <Text style={styles.pasteHint}>
                      After clicking Authorize on platform.claude.com, copy the code from the callback page and paste it here.
                    </Text>
                    <View style={styles.pasteRow}>
                      <TextInput
                        value={pasteCode}
                        onChangeText={(t) => { setPasteCode(t); setSubmitError(null); }}
                        placeholder="paste code here"
                        placeholderTextColor="#64748b"
                        autoCapitalize="none"
                        autoCorrect={false}
                        spellCheck={false}
                        style={styles.pasteInput}
                      />
                      <TouchableOpacity
                        disabled={!pasteCode.trim() || submitting}
                        onPress={submitCode}
                        style={[styles.submitBtn, (!pasteCode.trim() || submitting) && styles.submitBtnDisabled]}
                      >
                        <Text style={styles.submitBtnText}>{submitting ? "…" : "Submit"}</Text>
                      </TouchableOpacity>
                    </View>
                    {submitError ? (
                      <Text style={styles.submitError}>{submitError}</Text>
                    ) : null}
                  </View>
                ) : null}

                <Text style={styles.footer}>
                  {needsPasteCode
                    ? "This dialog auto-completes once the remote CLI confirms the token."
                    : "This dialog auto-completes once the remote CLI finishes the device-auth flow."}
                </Text>
              </View>
            )}
          </ScrollView>
        </View>
      </KeyboardAvoidingView>
    </Modal>
  );
}

const styles = StyleSheet.create({
  backdrop: {
    flex: 1,
    backgroundColor: "rgba(0,0,0,0.65)",
    alignItems: "center",
    justifyContent: "center",
    padding: 16,
  },
  card: {
    width: "100%",
    // 460 matches layoutTokens.dialog.form — keeping the literal here
    // because importing tokens for one Stylesheet entry blows up the
    // file. If form-dialog width changes globally, update tokens too.
    maxWidth: 460,
    backgroundColor: "#0f172a",
    borderColor: "#1e293b",
    borderWidth: 1,
    borderRadius: 14,
    padding: 18,
    maxHeight: "90%",
  },
  headerRow: { flexDirection: "row", alignItems: "flex-start", gap: 12, marginBottom: 12 },
  title: { color: "#f1f5f9", fontSize: 16, fontWeight: "600" },
  subtitle: { color: "#94a3b8", fontSize: 12, marginTop: 2 },
  deviceName: { color: "#cbd5e1", fontFamily: "Menlo" },
  closeBtn: { color: "#94a3b8", fontSize: 26, lineHeight: 26 },

  errorBox: {
    backgroundColor: "rgba(239,68,68,0.08)",
    borderColor: "rgba(239,68,68,0.3)",
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
  },
  errorTitle: { color: "#fca5a5", fontWeight: "600", marginBottom: 4 },
  errorBody: { color: "#fda4af", fontSize: 12 },

  infoBox: {
    backgroundColor: "rgba(30,41,59,0.4)",
    borderColor: "#1e293b",
    borderWidth: 1,
    borderRadius: 10,
    padding: 14,
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
  },
  infoText: { color: "#cbd5e1", fontSize: 12 },

  successBox: {
    backgroundColor: "rgba(16,185,129,0.07)",
    borderColor: "rgba(16,185,129,0.3)",
    borderWidth: 1,
    borderRadius: 10,
    padding: 14,
  },
  successTitle: { color: "#6ee7b7", fontWeight: "600", marginBottom: 4 },
  successBody: { color: "#a7f3d0", fontSize: 12 },

  help: { color: "#94a3b8", fontSize: 12, marginBottom: 12, lineHeight: 18 },

  urlButton: {
    backgroundColor: "rgba(99,102,241,0.1)",
    borderColor: "rgba(99,102,241,0.4)",
    borderWidth: 1,
    borderRadius: 10,
    paddingVertical: 12,
    paddingHorizontal: 14,
    marginBottom: 12,
  },
  urlOpenRow: { paddingVertical: 0 },
  urlLabel: { color: "#a5b4fc", fontWeight: "600", fontSize: 13 },
  urlValue: { color: "#818cf8", fontSize: 11, marginTop: 4, fontFamily: "Menlo" },
  copyChip: {
    alignSelf: "flex-start",
    marginTop: 8,
    paddingHorizontal: 10,
    paddingVertical: 5,
    borderRadius: 6,
    backgroundColor: "rgba(167,139,250,0.16)",
    borderWidth: 1,
    borderColor: "rgba(167,139,250,0.45)",
  },
  copyChipText: { color: "#c4b5fd", fontSize: 11, fontWeight: "600" },
  urlPending: {
    backgroundColor: "rgba(30,41,59,0.4)",
    borderColor: "#1e293b",
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    flexDirection: "row",
    alignItems: "center",
    gap: 10,
    marginBottom: 12,
  },
  urlPendingText: { color: "#94a3b8", fontSize: 11 },

  codeBox: {
    backgroundColor: "rgba(30,41,59,0.5)",
    borderColor: "#334155",
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    marginBottom: 12,
  },
  codeHeaderRow: { flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 6 },
  codeLabel: { color: "#94a3b8", fontSize: 10, textTransform: "uppercase", letterSpacing: 1.5 },
  codeValue: { color: "#f1f5f9", fontSize: 22, letterSpacing: 4, fontFamily: "Menlo", fontWeight: "600" },

  pasteBox: {
    backgroundColor: "rgba(15,23,42,0.4)",
    borderColor: "#1e293b",
    borderWidth: 1,
    borderRadius: 10,
    padding: 12,
    marginBottom: 12,
  },
  pasteLabel: { color: "#cbd5e1", fontSize: 11, fontWeight: "600", textTransform: "uppercase", letterSpacing: 1.5, marginBottom: 6 },
  pasteHint: { color: "#94a3b8", fontSize: 11, marginBottom: 10, lineHeight: 16 },
  pasteRow: { flexDirection: "row", gap: 8 },
  pasteInput: {
    flex: 1,
    backgroundColor: "#020617",
    borderColor: "#334155",
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 8,
    color: "#f1f5f9",
    fontFamily: "Menlo",
    fontSize: 12,
  },
  submitBtn: {
    backgroundColor: "rgba(99,102,241,0.12)",
    borderColor: "rgba(99,102,241,0.4)",
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 14,
    justifyContent: "center",
  },
  submitBtnDisabled: { opacity: 0.4 },
  submitBtnText: { color: "#a5b4fc", fontWeight: "600", fontSize: 13 },
  submitError: { color: "#fda4af", fontSize: 11, marginTop: 6 },

  footer: { color: "#64748b", fontSize: 10, lineHeight: 14 },
});
