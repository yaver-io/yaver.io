/**
 * Runner-auth approval screen — the first-class OAuth helper UI.
 *
 * Triggered by:
 *   - A blackbox `runner_auth_required` event (push notification +
 *     deep-link `/runner-auth/approve?runner=claude&target=cloud-1`)
 *   - A voice intent "authorize Claude Code on cloud-1" routed
 *     from /voice/stream
 *   - A direct tap from the Devices tab → "Mirror auth" CTA
 *
 * What it does:
 *   1. Shows "Mirror <runner> Code to <targetLabel>?"
 *   2. Calls `POST /runner/auth/mirror/request` on the agent
 *   3. If source has no local credential, falls through to a
 *      phone-relay device-auth path (`/runner-auth/browser`)
 *   4. Renders the result (✓ mirrored, ✗ no source creds, etc.)
 *
 * Constraints honored:
 *   - Subscription OAuth only — never an API-key fallback (the
 *     screen literally cannot accept an API-key paste)
 *   - Single-tap approval — biometric gate could be added in v2
 *   - Glass-friendly status — the blackbox `runner_auth_completed`
 *     event the agent emits after success drives the glass speech
 *     ("Claude ready") automatically; this screen just dispatches.
 */

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppBackButton } from "../../src/components/AppBackButton";
import { SafeAreaView } from "react-native-safe-area-context";
import { useColors } from "../../src/context/ThemeContext";
import { quicClient } from "../../src/lib/quic";
import { YaverGlass } from "../../src/components/YaverGlass";

type Status = "ready" | "running" | "success" | "no_source" | "error";

interface MirrorResponse {
  ok: boolean;
  runner?: string;
  tokenHash?: string;
  sourceHost?: string;
  writtenTo?: string;
  reason?: string;
  nextAction?: string;
  message?: string;
}

export default function RunnerAuthApproveScreen(): React.JSX.Element {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{
    runner?: string;
    target?: string;
    targetLabel?: string;
    source?: string;
    sourceLabel?: string;
  }>();

  const runner = (params.runner ?? "claude").toLowerCase();
  const targetLabel = params.targetLabel ?? params.target ?? "this device";
  const sourceLabel = params.sourceLabel ?? params.source ?? "your Mac";

  const [status, setStatus] = useState<Status>("ready");
  const [response, setResponse] = useState<MirrorResponse | null>(null);
  const [errorMsg, setErrorMsg] = useState("");

  const runnerLabel = runnerDisplayName(runner);

  const approve = useCallback(async () => {
    setStatus("running");
    setErrorMsg("");
    try {
      const res = await fetch(`${quicClient.baseUrl}/runner/auth/mirror/request`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...quicClient.getAuthHeaders(),
        },
        body: JSON.stringify({
          runner,
          sourceDeviceId: params.source ?? "",
          targetDeviceId: params.target ?? "",
        }),
      });
      if (!res.ok) {
        const t = await res.text().catch(() => "");
        throw new Error(`HTTP ${res.status}: ${t.slice(0, 200)}`);
      }
      const body: MirrorResponse = await res.json();
      setResponse(body);
      if (body.ok) {
        setStatus("success");
      } else if (body.reason === "no_local_credential") {
        setStatus("no_source");
      } else {
        setStatus("error");
        setErrorMsg(body.message ?? "mirror failed");
      }
    } catch (err: any) {
      setErrorMsg(err?.message ?? String(err));
      setStatus("error");
    }
  }, [runner, params.source, params.target]);

  const startPhoneRelay = useCallback(() => {
    router.push({
      pathname: "/runner-auth/browser" as any,
      params: { runner, target: params.target ?? "" },
    });
  }, [router, runner, params.target]);

  // Auto-close on success after 2s so glass-driven flows hand back
  // to whatever screen the user came from. Mobile-driven flows can
  // still navigate manually.
  useEffect(() => {
    if (status !== "success") return;
    const t = setTimeout(() => router.back(), 2000);
    return () => clearTimeout(t);
  }, [status, router]);

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
      <View style={styles.headerRow}>
        <AppBackButton variant="icon" color={c.textPrimary} onPress={() => router.back()} />
        <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "600" }}>Runner Auth</Text>
        <View style={{ width: 24 }} />
      </View>

      <View style={styles.body}>
        <YaverGlass tint={c.bgCard} style={styles.card}>
          <View style={styles.cardInner}>
            <Ionicons
              name={iconForStatus(status)}
              size={48}
              color={colorForStatus(status, c)}
              style={{ marginBottom: 12 }}
            />
            <Text style={[styles.title, { color: c.textPrimary }]}>
              {titleForStatus(status, runnerLabel, targetLabel, sourceLabel)}
            </Text>
            <Text style={[styles.body2, { color: c.textMuted }]}>
              {subtitleForStatus(status, runnerLabel, sourceLabel, errorMsg)}
            </Text>

            {status === "ready" && (
              <View style={styles.actions}>
                <Pressable onPress={approve} style={[styles.btnPrimary, { backgroundColor: c.accent }]}>
                  <Text style={[styles.btnText, { color: "#fff" }]}>Mirror {runnerLabel}</Text>
                </Pressable>
                <Pressable onPress={() => router.back()} style={styles.btnSecondary}>
                  <Text style={[styles.btnText, { color: c.textMuted }]}>Cancel</Text>
                </Pressable>
              </View>
            )}

            {status === "running" && (
              <View style={{ marginTop: 16 }}>
                <ActivityIndicator color={c.accent} />
              </View>
            )}

            {status === "success" && response?.writtenTo && (
              <Text style={[styles.mono, { color: c.textMuted }]}>{shortPath(response.writtenTo)}</Text>
            )}

            {status === "no_source" && (
              <View style={styles.actions}>
                <Pressable onPress={startPhoneRelay} style={[styles.btnPrimary, { backgroundColor: c.accent }]}>
                  <Text style={[styles.btnText, { color: "#fff" }]}>Sign in via phone browser</Text>
                </Pressable>
                <Pressable onPress={() => router.back()} style={styles.btnSecondary}>
                  <Text style={[styles.btnText, { color: c.textMuted }]}>Cancel</Text>
                </Pressable>
              </View>
            )}

            {status === "error" && (
              <View style={styles.actions}>
                <Pressable onPress={approve} style={[styles.btnPrimary, { backgroundColor: c.accent }]}>
                  <Text style={[styles.btnText, { color: "#fff" }]}>Try again</Text>
                </Pressable>
                <Pressable onPress={() => router.back()} style={styles.btnSecondary}>
                  <Text style={[styles.btnText, { color: c.textMuted }]}>Cancel</Text>
                </Pressable>
              </View>
            )}
          </View>
        </YaverGlass>

        <Text style={[styles.footnote, { color: c.textMuted }]}>
          Subscription OAuth only · never API keys
        </Text>
      </View>
    </SafeAreaView>
  );
}

function runnerDisplayName(r: string): string {
  switch (r.toLowerCase()) {
    case "claude": return "Claude Code";
    case "codex": return "Codex";
    case "opencode": return "OpenCode";
    default: return r;
  }
}

function iconForStatus(s: Status): React.ComponentProps<typeof Ionicons>["name"] {
  switch (s) {
    case "ready": return "key-outline";
    case "running": return "sync-outline";
    case "success": return "checkmark-circle";
    case "no_source": return "phone-portrait-outline";
    case "error": return "alert-circle";
  }
}

function colorForStatus(s: Status, c: ReturnType<typeof useColors>): string {
  switch (s) {
    case "success": return "#10b981";
    case "error": return "#ef4444";
    case "no_source": return "#f59e0b";
    default: return c.accent;
  }
}

function titleForStatus(s: Status, runner: string, target: string, source: string): string {
  switch (s) {
    case "ready": return `Mirror ${runner}?`;
    case "running": return `Mirroring ${runner}…`;
    case "success": return `${runner} ready on ${target}`;
    case "no_source": return `${source} isn't signed into ${runner}`;
    case "error": return "Mirror failed";
  }
}

function subtitleForStatus(s: Status, runner: string, source: string, errorMsg: string): string {
  switch (s) {
    case "ready": return `Copy ${runner} subscription OAuth from ${source} to this device. No API keys, no codes to retype.`;
    case "running": return "Forwarding subscription credentials over your private QUIC peer link.";
    case "success": return "Credentials in place. Glass surfaces will hear the confirmation automatically.";
    case "no_source": return `Sign in once via your phone browser. ${runner} OAuth completes there, then mirrors here.`;
    case "error": return errorMsg || "Something went wrong.";
  }
}

function shortPath(p: string): string {
  return p.replace(/^.*?\/\.([^/]+)\//, ".$1/");
}

const styles = StyleSheet.create({
  headerRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingHorizontal: 16,
    paddingVertical: 10,
  },
  body: { flex: 1, paddingHorizontal: 20, paddingTop: 24, gap: 12 },
  card: { borderRadius: 16, overflow: "hidden" },
  cardInner: { padding: 24, alignItems: "center" },
  title: { fontSize: 19, fontWeight: "700", textAlign: "center", marginBottom: 6 },
  body2: { fontSize: 13, textAlign: "center", lineHeight: 19 },
  actions: { width: "100%", marginTop: 22, gap: 8 },
  btnPrimary: { paddingVertical: 13, borderRadius: 10, alignItems: "center" },
  btnSecondary: { paddingVertical: 13, alignItems: "center" },
  btnText: { fontSize: 15, fontWeight: "600" },
  mono: { fontSize: 11, fontFamily: "Menlo", marginTop: 10, textAlign: "center" },
  footnote: { fontSize: 10, textAlign: "center", marginTop: 6 },
});
