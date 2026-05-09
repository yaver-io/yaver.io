import React, { useEffect, useMemo, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  KeyboardAvoidingView,
  Linking,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { Stack, useLocalSearchParams, useRouter } from "expo-router";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { AppBackButton } from "../src/components/AppBackButton";
import { quicClient } from "../src/lib/quic";

// deploy-tokens.tsx — first-class onboarding screen for the secrets a
// user needs in their Yaver vault to export / deploy a mobile sandbox
// project. Pairs with desktop/agent/deploy_tokens.go's catalogue +
// /deploy/tokens/* endpoints. Reachable from the phone-project detail
// screen ("Set up deploy tokens") and from the wizard's post-create
// confirmation alert.

type CatalogueField = {
  name: string;
  label: string;
  hint: string;
  generateUrl: string;
  kind: "secret" | "json" | "file";
  canVerify: boolean;
  pairs?: string[];
};

type CatalogueTarget = {
  id: string;
  label: string;
  description: string;
  fields: CatalogueField[];
};

type StatusTarget = {
  id: string;
  label: string;
  ready: boolean;
  total: number;
  filled: number;
  fields: Array<{ name: string; set: boolean; updatedAt?: number }>;
};

// Per-target capability of the connected agent — populated from
// /deploy/capabilities. Lets us grey out a target row with a precise
// reason ("macOS only", "missing xcodebuild", etc.) instead of letting
// the user wire all four secrets into a Linux vault and discover the
// gap at deploy time.
type CapabilityTarget = {
  target: string;
  canDeploy: boolean;
  platformLock?: string;
  tools?: Array<{
    name: string;
    required: boolean;
    found: boolean;
    path?: string;
    version?: string;
    installHint?: string;
    deepValid?: boolean;
    deepError?: string;
    platformSkipped?: boolean;
    skipReason?: string;
  }>;
  secrets?: Array<{
    name: string;
    found: boolean;
    source?: string;
    project?: string;
    pathValid?: boolean;
    pathError?: string;
  }>;
  missingTools?: string[];
  missingSecrets?: string[];
  warnings?: string[];
  reason?: string;
  ciAlternative?: string;
  vaultProject?: string;
};

export default function DeployTokensScreen() {
  const c = useColors();
  const insets = useSafeAreaInsets();
  const params = useLocalSearchParams<{ project?: string }>();
  const project = String(params.project || "").trim();
  const router = useRouter();
  const { connectionStatus } = useDevice();
  const connected = connectionStatus === "connected";

  const [loading, setLoading] = useState(true);
  const [catalogue, setCatalogue] = useState<CatalogueTarget[]>([]);
  const [status, setStatus] = useState<Record<string, StatusTarget>>({});
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [busyTarget, setBusyTarget] = useState<string | null>(null);
  const [verifyResults, setVerifyResults] = useState<Record<string, string>>({});
  // Capability map keyed by target id. Computed from /deploy/capabilities
  // on the connected agent — failure to fetch is non-fatal (we fall
  // back to the old "every target is clickable" behaviour) so an older
  // agent without the endpoint doesn't break the screen.
  const [capabilities, setCapabilities] = useState<Record<string, CapabilityTarget>>({});
  const [hostLabel, setHostLabel] = useState<string>("");

  const refresh = async () => {
    if (!connected) {
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      // Capabilities call is best-effort: an older agent (pre-/deploy/
      // capabilities) returns 404, which we want to treat as "no
      // gating data" rather than blocking the whole screen.
      const [cat, st, caps] = await Promise.all([
        quicClient.deployTokensCatalogue(),
        quicClient.deployTokensStatus(project),
        quicClient.deployCapabilities({ project }).catch(() => null),
      ]);
      setCatalogue(cat.targets);
      const map: Record<string, StatusTarget> = {};
      st.targets.forEach((t) => { map[t.id] = t; });
      setStatus(map);
      if (caps) {
        const cmap: Record<string, CapabilityTarget> = {};
        caps.targets.forEach((c) => { cmap[c.target] = c; });
        setCapabilities(cmap);
        setHostLabel(`${caps.platform}${caps.isWsl ? " (WSL)" : ""} ${caps.arch}`);
      } else {
        setCapabilities({});
        setHostLabel("");
      }
    } catch (err: any) {
      Alert.alert("Deploy tokens", err?.message ?? "Failed to load.");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project, connected]);

  // Trigger an outbound P2P vault sync against every other device the
  // user owns. The agent walks the user's device list, opens a sync
  // handshake to each online peer, and pulls anything newer. After
  // the call we re-fetch capabilities so any newly-pulled secrets
  // flip the affected target's row from "MISSING" to "READY".
  const trySyncFromPeers = async () => {
    if (!connected) return;
    setBusyTarget("__peer-sync__");
    try {
      const result = await quicClient.vaultPeerSync();
      const totals = result.totals;
      const peerLines = result.results
        .map((r) =>
          r.error
            ? `  ${r.peer.slice(0, 8)}: ${r.error}`
            : `  ${r.peer.slice(0, 8)}: pulled ${r.pulled}, pushed ${r.pushed}`,
        )
        .join("\n");
      Alert.alert(
        "P2P vault sync",
        result.peers.length === 0
          ? result.note ?? "No peer devices found."
          : `Across ${result.peers.length} peers:\n  pulled ${totals.pulled}, pushed ${totals.pushed}\n${peerLines}`,
      );
      // Re-fetch the catalogue/status/caps so any pulled secret
      // immediately reflects in the deploy-target rows below.
      await refresh();
    } catch (err: any) {
      Alert.alert("P2P vault sync", err?.message ?? "Failed.");
    } finally {
      setBusyTarget(null);
    }
  };

  const headerTitle = useMemo(
    () => (project ? `Deploy tokens · ${project}` : "Deploy tokens"),
    [project],
  );

  const saveTarget = async (target: CatalogueTarget) => {
    const tokens: Record<string, string> = {};
    const verifyAs: Record<string, string> = {};
    for (const f of target.fields) {
      const v = (draft[f.name] || "").trim();
      if (v) {
        tokens[f.name] = v;
        if (f.canVerify) verifyAs[f.name] = target.id;
      }
    }
    if (Object.keys(tokens).length === 0) {
      Alert.alert("Nothing to save", "Paste at least one value first.");
      return;
    }
    setBusyTarget(target.id);
    try {
      const res = await quicClient.deployTokensSave({ project, tokens, verifyAs });
      const verifyMap: Record<string, string> = { ...verifyResults };
      let savedCount = 0;
      let verifyFailed: string | null = null;
      Object.entries(res.results).forEach(([name, row]) => {
        if (row.saved) savedCount++;
        if (row.verify === "passed" && row.verifyDetail) {
          verifyMap[name] = `✓ ${row.verifyDetail}`;
        } else if (row.verify === "failed") {
          verifyFailed = `${name}: ${row.verifyReason}`;
          verifyMap[name] = `✗ ${row.verifyReason || "verify failed"}`;
        }
      });
      setVerifyResults(verifyMap);
      // Wipe the local draft for fields that were saved so the
      // textboxes blank out — paste fields with the actual secret
      // value lying around in component state is poor hygiene.
      setDraft((prev) => {
        const next = { ...prev };
        for (const name of Object.keys(tokens)) delete next[name];
        return next;
      });
      void refresh();
      if (verifyFailed) {
        Alert.alert("Saved with warning", `${savedCount} saved.\n${verifyFailed}`);
      } else {
        Alert.alert("Saved", `${savedCount} ${target.label} secret${savedCount === 1 ? "" : "s"} written to vault${project ? ` (project: ${project})` : ""}.`);
      }
    } catch (err: any) {
      Alert.alert("Save failed", err?.message ?? "Could not save.");
    } finally {
      setBusyTarget(null);
    }
  };

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <Stack.Screen
        options={{
          title: headerTitle,
          headerLeft: () => <AppBackButton onPress={() => router.back()} />,
          headerStyle: { backgroundColor: c.bg },
          headerTintColor: c.textPrimary,
        }}
      />
      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          style={{ flex: 1 }}
          contentContainerStyle={{ padding: 16, paddingBottom: 32 + insets.bottom }}
        >
          {!connected ? (
            <View style={[styles.card, { backgroundColor: c.bgCard, borderColor: c.border }]}>
              <Text style={[styles.title, { color: c.textPrimary }]}>Yaver agent not connected</Text>
              <Text style={[styles.muted, { color: c.textMuted, marginTop: 6 }]}>
                Connect a Yaver machine first. Deploy tokens live in that machine's local vault — they never sync to Convex or anywhere else.
              </Text>
            </View>
          ) : loading ? (
            <View style={{ paddingVertical: 32, alignItems: "center" }}>
              <ActivityIndicator color={c.accent} />
            </View>
          ) : (
            <>
              <Text style={[styles.muted, { color: c.textMuted, marginBottom: 12 }]}>
                Generate each token at the provider, paste it here, and we save it straight to the agent's vault scoped to {project ? `"${project}"` : "this account"}. Deploy scripts source the vault, so once a row turns green the deploy just works — locally or from CI.
              </Text>
              {hostLabel ? (
                <Text style={[styles.muted, { color: c.textMuted, marginBottom: 12, fontSize: 11 }]}>
                  Connected agent: {hostLabel}
                </Text>
              ) : null}
              {catalogue.map((target) => {
                const st = status[target.id];
                const filled = st?.filled ?? 0;
                const total = target.fields.length;
                const ready = filled === total && total > 0;
                // capability is populated when /deploy/capabilities
                // succeeded; absence (older agent) leaves the row in
                // its prior, ungated behaviour.
                const cap = capabilities[target.id];
                const blocked = cap !== undefined && !cap.canDeploy;
                const platformBlocked = blocked && !!cap?.platformLock;
                return (
                  <View
                    key={target.id}
                    style={[
                      styles.card,
                      {
                        backgroundColor: c.bgCard,
                        borderColor: blocked ? "#64748b" : ready ? c.accent : c.border,
                        marginBottom: 14,
                        opacity: blocked ? 0.65 : 1,
                      },
                    ]}
                  >
                    <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                      <Text style={[styles.title, { color: c.textPrimary }]}>{target.label}</Text>
                      <View
                        style={{
                          paddingHorizontal: 8,
                          paddingVertical: 3,
                          borderRadius: 12,
                          backgroundColor: blocked
                            ? "#64748b33"
                            : ready
                              ? "#16a34a22"
                              : filled > 0
                                ? "#f59e0b22"
                                : "#64748b22",
                        }}
                      >
                        <Text
                          style={{
                            color: blocked
                              ? c.textMuted
                              : ready
                                ? "#22c55e"
                                : filled > 0
                                  ? "#f59e0b"
                                  : c.textMuted,
                            fontSize: 11,
                            fontWeight: "700",
                          }}
                        >
                          {blocked
                            ? platformBlocked
                              ? "WRONG OS"
                              : "MISSING TOOLS"
                            : ready
                              ? "READY"
                              : `${filled}/${total}`}
                        </Text>
                      </View>
                    </View>
                    <Text style={[styles.muted, { color: c.textMuted, marginTop: 4 }]}>
                      {target.description}
                    </Text>
                    {blocked ? (
                      <View
                        style={{
                          marginTop: 8,
                          padding: 10,
                          borderRadius: 8,
                          backgroundColor: "#64748b1a",
                        }}
                      >
                        <Text style={{ color: "#ef4444", fontSize: 12, fontWeight: "700", marginBottom: 4 }}>
                          Can't deploy from this agent
                        </Text>
                        <Text style={{ color: c.textMuted, fontSize: 12 }}>
                          {cap?.reason ?? "Capability check failed."}
                        </Text>
                        {/*
                         * Per-tool detail rows. xcodebuild missing? Java
                         * 17 not found? Render row-per-tool so the user
                         * can fix the right thing instead of guessing
                         * from a single comma-separated reason string.
                         */}
                        {(cap?.tools ?? [])
                          .filter((tool) => tool.required && !tool.found && !tool.platformSkipped)
                          .map((tool) => (
                            <Text key={`tool-${tool.name}`} style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                              ✗ {tool.name}: not found
                              {tool.installHint ? ` — ${tool.installHint}` : ""}
                            </Text>
                          ))}
                        {(cap?.tools ?? [])
                          .filter((tool) => tool.deepValid === false && tool.deepError)
                          .map((tool) => (
                            <Text key={`tool-deep-${tool.name}`} style={{ color: "#f59e0b", fontSize: 11, marginTop: 6 }}>
                              ⚠ {tool.name}: {tool.deepError}
                            </Text>
                          ))}
                        {/* Per-secret detail. Show "missing in vault"
                         * + invalid-path-for-this-host warnings; the
                         * latter is the case where APP_STORE_KEY_PATH
                         * resolves to a /Users/<other-user> directory
                         * because the value was synced from another
                         * machine and didn't use ~/ portable form. */}
                        {(cap?.secrets ?? [])
                          .filter((sec) => !sec.found || sec.pathValid === false)
                          .map((sec) => (
                            <Text
                              key={`sec-${sec.name}`}
                              style={{
                                color: !sec.found ? c.textMuted : "#f59e0b",
                                fontSize: 11,
                                marginTop: 6,
                              }}
                            >
                              {!sec.found
                                ? `✗ ${sec.name}: not in vault`
                                : `⚠ ${sec.name}: ${sec.pathError ?? "path invalid"}`}
                            </Text>
                          ))}
                        {/* Try syncing missing secrets from another
                         * device. Only show when the failure is
                         * fixable via vault sync (missing entries) —
                         * platform-locked / missing-tools failures
                         * can't be fixed by syncing more secrets. */}
                        {!platformBlocked && (cap?.missingSecrets?.length ?? 0) > 0 ? (
                          <Pressable
                            onPress={trySyncFromPeers}
                            disabled={busyTarget === "__peer-sync__"}
                            style={{
                              marginTop: 10,
                              paddingVertical: 8,
                              paddingHorizontal: 12,
                              borderRadius: 8,
                              borderWidth: 1,
                              borderColor: c.accent,
                              alignItems: "center",
                              opacity: busyTarget === "__peer-sync__" ? 0.6 : 1,
                            }}
                          >
                            <Text style={{ color: c.accent, fontSize: 12, fontWeight: "700" }}>
                              {busyTarget === "__peer-sync__" ? "Syncing…" : "Try syncing from another device"}
                            </Text>
                          </Pressable>
                        ) : null}
                        {cap?.ciAlternative ? (
                          <Text style={{ color: c.textMuted, fontSize: 11, marginTop: 6 }}>
                            CI fallback: {cap.ciAlternative}
                          </Text>
                        ) : null}
                      </View>
                    ) : null}
                    {target.fields.map((f) => {
                      const fieldStatus = st?.fields.find((x) => x.name === f.name);
                      const isSet = !!fieldStatus?.set;
                      const verifyMsg = verifyResults[f.name];
                      return (
                        <View key={f.name} style={{ marginTop: 12 }}>
                          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                            <Text style={[styles.label, { color: c.textPrimary }]}>{f.label}</Text>
                            {isSet ? (
                              <Text style={{ color: "#22c55e", fontSize: 11, fontWeight: "600" }}>
                                ✓ in vault
                              </Text>
                            ) : null}
                          </View>
                          <Text style={[styles.muted, { color: c.textMuted, marginTop: 2, marginBottom: 6 }]}>
                            {f.hint}
                          </Text>
                          <TextInput
                            value={draft[f.name] || ""}
                            onChangeText={(t) => setDraft((prev) => ({ ...prev, [f.name]: t }))}
                            placeholder={isSet ? "(stored — paste a new value to replace)" : `Paste ${f.name}`}
                            placeholderTextColor={c.textMuted}
                            secureTextEntry={f.kind === "secret"}
                            multiline={f.kind === "json"}
                            autoCapitalize="none"
                            autoCorrect={false}
                            spellCheck={false}
                            style={[
                              styles.input,
                              {
                                color: c.textPrimary,
                                borderColor: c.border,
                                minHeight: f.kind === "json" ? 120 : 44,
                              },
                            ]}
                          />
                          {verifyMsg ? (
                            <Text
                              style={{
                                color: verifyMsg.startsWith("✓") ? "#22c55e" : "#ef4444",
                                fontSize: 11,
                                marginTop: 4,
                              }}
                            >
                              {verifyMsg}
                            </Text>
                          ) : null}
                          <Pressable
                            onPress={() => Linking.openURL(f.generateUrl).catch(() => {})}
                            style={{ marginTop: 4 }}
                          >
                            <Text style={{ color: c.accent, fontSize: 12, fontWeight: "600" }}>
                              Generate at {new URL(f.generateUrl).host} →
                            </Text>
                          </Pressable>
                        </View>
                      );
                    })}
                    <View style={{ flexDirection: "row", marginTop: 14, gap: 8 }}>
                      <Pressable
                        onPress={() => void saveTarget(target)}
                        disabled={busyTarget === target.id}
                        style={[
                          styles.btn,
                          { backgroundColor: c.accent, flex: 1, opacity: busyTarget === target.id ? 0.6 : 1 },
                        ]}
                      >
                        {busyTarget === target.id ? (
                          <ActivityIndicator color={c.bg} />
                        ) : (
                          <Text style={[styles.btnText, { color: c.bg }]}>
                            Save to vault {target.fields.some((f) => f.canVerify) ? "+ verify" : ""}
                          </Text>
                        )}
                      </Pressable>
                    </View>
                  </View>
                );
              })}
            </>
          )}
        </ScrollView>
      </KeyboardAvoidingView>
    </View>
  );
}

const styles = StyleSheet.create({
  card: { borderRadius: 12, borderWidth: 1, padding: 14 },
  title: { fontSize: 15, fontWeight: "700" },
  label: { fontSize: 13, fontWeight: "600" },
  muted: { fontSize: 12, lineHeight: 17 },
  input: {
    borderWidth: 1,
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 9,
    fontSize: 13,
  },
  btn: {
    paddingVertical: 11,
    paddingHorizontal: 14,
    borderRadius: 10,
    alignItems: "center",
  },
  btnText: { fontSize: 13, fontWeight: "700" },
});
