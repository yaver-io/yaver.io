// BoxInitSection — the first-class "is this box ready to code?" settings panel.
//
// Until now the things that make a box codeable were scattered: agent health,
// runner auth (claude/codex), and git/CI creds each lived in their own screen
// with their own status shape. This panel collapses them into ONE checklist for
// the currently-connected box, driven by boxInit.ts (pure policy) +
// boxInitStore.ts (the quic I/O). Each row shows a status and, when something
// needs doing, an inline Fix button wired to the exact remediation — install +
// sign in a runner, or apply a git token. "Make ready" runs every fixable step
// in order.
//
// CREDENTIAL SAFETY: git tokens are held in transient state only long enough to
// POST them to the agent over the authed channel (machineOnboardingApply), then
// wiped. They are secureTextEntry, never logged, never rendered back — the
// status API is redacted server-side.

import React, { useCallback, useEffect, useState } from "react";
import { View, Text, Pressable, TextInput, ActivityIndicator } from "react-native";
import { useDevice } from "../context/DeviceContext";
import type { ThemeColors } from "../constants/colors";
import { readinessSummary, type BoxCheck, type BoxReadiness, type CheckStatus } from "../lib/boxInit";
import { loadBoxReadiness, runBoxAction, runBoxInit, type BoxActionParams } from "../lib/boxInitStore";

function statusGlyph(s: CheckStatus): string {
  switch (s) {
    case "ok":
      return "✓";
    case "warn":
      return "!";
    case "missing":
      return "✗";
    case "n-a":
      return "—";
  }
}

function statusColor(c: ThemeColors, s: CheckStatus): string {
  switch (s) {
    case "ok":
      return c.success;
    case "warn":
      return c.warn;
    case "missing":
      return c.error;
    case "n-a":
      return c.textMuted;
  }
}

function overallColor(c: ThemeColors, o: BoxReadiness["overall"]): string {
  return o === "ready" ? c.success : o === "partial" ? c.warn : c.error;
}

export default function BoxInitSection({ c }: { c: ThemeColors; token?: string | null }) {
  const { activeDevice, connectionStatus } = useDevice();
  const connected = connectionStatus === "connected" && !!activeDevice && !activeDevice.isGuest;
  const deviceId = activeDevice?.id;

  const [open, setOpen] = useState(false);
  const [readiness, setReadiness] = useState<BoxReadiness | null>(null);
  const [loading, setLoading] = useState(false);
  const [busyAction, setBusyAction] = useState<string | null>(null);
  const [makeReadyMsg, setMakeReadyMsg] = useState<string | null>(null);

  // Transient git-token entry, keyed by check (github/gitlab). Wiped after apply.
  const [gitField, setGitField] = useState<{ key: string; token: string; host: string } | null>(null);

  const refresh = useCallback(async () => {
    if (!deviceId || !connected) {
      setReadiness(null);
      return;
    }
    setLoading(true);
    try {
      setReadiness(await loadBoxReadiness(deviceId));
    } finally {
      setLoading(false);
    }
  }, [deviceId, connected]);

  useEffect(() => {
    if (open && connected) void refresh();
  }, [open, connected, refresh]);

  const fixCheck = useCallback(
    async (check: BoxCheck, params: BoxActionParams = {}) => {
      if (!deviceId) return;
      setBusyAction(check.key);
      try {
        const res = await runBoxAction(deviceId, check.action, params);
        setMakeReadyMsg(res.ok ? `${check.label}: ${res.detail ?? "done"}` : `${check.label}: ${res.error ?? "failed"}`);
        await refresh();
      } finally {
        setBusyAction(null);
        setGitField(null);
      }
    },
    [deviceId, refresh],
  );

  const makeReady = useCallback(async () => {
    if (!deviceId) return;
    setBusyAction("__all__");
    setMakeReadyMsg("starting…");
    try {
      await runBoxInit({
        deviceId,
        onProgress: (p) => {
          setMakeReadyMsg(p.message);
          if (p.readiness) setReadiness(p.readiness);
        },
      });
    } catch (e) {
      setMakeReadyMsg(e instanceof Error ? e.message : String(e));
    } finally {
      setBusyAction(null);
    }
  }, [deviceId]);

  // ---- render ----
  const summary = readiness ? readinessSummary(readiness) : connected ? "tap to check" : "no box connected";

  return (
    <View style={{ marginTop: 16 }}>
      <Pressable
        onPress={() => setOpen((v) => !v)}
        style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 8 }}
      >
        <Text style={{ fontSize: 16, fontWeight: "700", color: c.textPrimary }}>🧰 Box initialization</Text>
        <Text style={{ fontSize: 13, color: readiness ? overallColor(c, readiness.overall) : c.textMuted }}>
          {summary} {open ? "▾" : "▸"}
        </Text>
      </Pressable>

      {open && (
        <View
          style={{
            backgroundColor: c.bgCard,
            borderColor: c.border,
            borderWidth: 1,
            borderRadius: 12,
            padding: 14,
          }}
        >
          {!connected ? (
            <Text style={{ color: c.textMuted, fontSize: 13 }}>
              Connect to a box first — its readiness checklist (agent, Claude Code, Codex, git) shows up here.
            </Text>
          ) : (
            <>
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginBottom: 10 }}>
                <Text style={{ color: c.textSecondary, fontSize: 13 }} numberOfLines={1}>
                  {activeDevice?.name ?? deviceId}
                </Text>
                {loading ? (
                  <ActivityIndicator size="small" color={c.accent} />
                ) : (
                  <Pressable onPress={() => void refresh()}>
                    <Text style={{ color: c.accent, fontSize: 13 }}>Recheck</Text>
                  </Pressable>
                )}
              </View>

              {readiness?.checks.map((check) => (
                <View key={check.key} style={{ marginBottom: 8 }}>
                  <View style={{ flexDirection: "row", alignItems: "center" }}>
                    <Text style={{ width: 20, fontWeight: "700", color: statusColor(c, check.status) }}>
                      {statusGlyph(check.status)}
                    </Text>
                    <View style={{ flex: 1 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 14 }}>{check.label}</Text>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>{check.detail}</Text>
                    </View>
                    {check.action !== "none" && check.status !== "ok" && check.status !== "n-a" && (
                      <Pressable
                        disabled={busyAction !== null}
                        onPress={() => {
                          if (check.key === "git_github" || check.key === "git_gitlab") {
                            setGitField({ key: check.key, token: "", host: "" });
                          } else {
                            void fixCheck(check);
                          }
                        }}
                        style={{
                          paddingHorizontal: 12,
                          paddingVertical: 6,
                          borderRadius: 8,
                          backgroundColor: c.accentSoft,
                          opacity: busyAction !== null ? 0.5 : 1,
                        }}
                      >
                        {busyAction === check.key ? (
                          <ActivityIndicator size="small" color={c.accent} />
                        ) : (
                          <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>Fix</Text>
                        )}
                      </Pressable>
                    )}
                  </View>

                  {/* Inline git-token entry */}
                  {gitField?.key === check.key && (
                    <View style={{ marginTop: 8, marginLeft: 20, gap: 6 }}>
                      <TextInput
                        value={gitField.token}
                        onChangeText={(t) => setGitField((g) => (g ? { ...g, token: t } : g))}
                        placeholder={check.key === "git_github" ? "ghp_… token" : "glpat_… token"}
                        placeholderTextColor={c.textMuted}
                        secureTextEntry
                        autoCapitalize="none"
                        autoCorrect={false}
                        spellCheck={false}
                        style={{
                          backgroundColor: c.bgInput,
                          color: c.textPrimary,
                          borderColor: c.border,
                          borderWidth: 1,
                          borderRadius: 8,
                          paddingHorizontal: 10,
                          paddingVertical: 8,
                          fontSize: 13,
                        }}
                      />
                      {check.key === "git_gitlab" && (
                        <TextInput
                          value={gitField.host}
                          onChangeText={(h) => setGitField((g) => (g ? { ...g, host: h } : g))}
                          placeholder="gitlab host (optional, e.g. gitlab.com)"
                          placeholderTextColor={c.textMuted}
                          autoCapitalize="none"
                          autoCorrect={false}
                          style={{
                            backgroundColor: c.bgInput,
                            color: c.textPrimary,
                            borderColor: c.border,
                            borderWidth: 1,
                            borderRadius: 8,
                            paddingHorizontal: 10,
                            paddingVertical: 8,
                            fontSize: 13,
                          }}
                        />
                      )}
                      <View style={{ flexDirection: "row", gap: 8 }}>
                        <Pressable
                          disabled={!gitField.token || busyAction !== null}
                          onPress={() =>
                            void fixCheck(
                              check,
                              check.key === "git_github"
                                ? { githubToken: gitField.token }
                                : { gitlabToken: gitField.token, gitlabHost: gitField.host || undefined },
                            )
                          }
                          style={{
                            paddingHorizontal: 14,
                            paddingVertical: 7,
                            borderRadius: 8,
                            backgroundColor: c.accent,
                            opacity: !gitField.token || busyAction !== null ? 0.5 : 1,
                          }}
                        >
                          <Text style={{ color: c.textInverse, fontSize: 12, fontWeight: "700" }}>Apply</Text>
                        </Pressable>
                        <Pressable onPress={() => setGitField(null)} style={{ paddingHorizontal: 14, paddingVertical: 7 }}>
                          <Text style={{ color: c.textMuted, fontSize: 12 }}>Cancel</Text>
                        </Pressable>
                      </View>
                    </View>
                  )}
                </View>
              ))}

              {readiness && readiness.overall !== "ready" && (
                <Pressable
                  disabled={busyAction !== null}
                  onPress={() => void makeReady()}
                  style={{
                    marginTop: 6,
                    paddingVertical: 11,
                    borderRadius: 10,
                    backgroundColor: c.accent,
                    alignItems: "center",
                    opacity: busyAction !== null ? 0.6 : 1,
                  }}
                >
                  {busyAction === "__all__" ? (
                    <ActivityIndicator size="small" color={c.textInverse} />
                  ) : (
                    <Text style={{ color: c.textInverse, fontWeight: "700", fontSize: 14 }}>Make ready</Text>
                  )}
                </Pressable>
              )}

              {makeReadyMsg && (
                <Text style={{ marginTop: 8, color: c.textMuted, fontSize: 12 }}>{makeReadyMsg}</Text>
              )}
            </>
          )}
        </View>
      )}
    </View>
  );
}
