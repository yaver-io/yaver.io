// HetznerSection — phone-DIRECT Hetzner management in Settings. Wire your
// Hetzner token once (stored in the device keychain, never synced) and the app
// lists / stops / deletes boxes on your account by calling api.hetzner.cloud
// itself — NO paired agent required. This is what lets a fresh install manage
// (and, next, provision) boxes with nothing else set up.
//
// CREDENTIAL SAFETY: the token is secureTextEntry + autofill/correct/spellcheck
// OFF; held in component state only until it's written to SecureStore, then the
// field is cleared. It's never rendered back or logged. Because every call is
// phone→Hetzner direct, the token never touches Convex or a relay.

import React, { useCallback, useEffect, useState } from "react";
import { View, Text, Pressable, TextInput, Alert, ActivityIndicator, Linking } from "react-native";
import { LOCAL_KEYS, getLocalSecret, saveLocalSecret, deleteLocalSecret } from "../lib/auth";
import type { ThemeColors } from "../constants/colors";
import { HetznerClient, monthlyEur, uptimeLabel, looksLikeToken, serverTypeFor, type HetznerServer, type Plan, type Region } from "../lib/hcloud";
import { provisionByoBox } from "../lib/byoProvision";

const TOKEN_URL = "https://console.hetzner.cloud/"; // Project → Security → API tokens (Read & Write)

// Spin-up is gated until the Convex /byo/provision-* routes are deployed AND a
// real-box validation run has happened (provisioning costs money). Flip to true
// after `cd backend && npx convex deploy` + one successful test spin-up.
const PROVISION_ENABLED = false;

export default function HetznerSection({ c, token }: { c: ThemeColors; token?: string | null }) {
  const [open, setOpen] = useState(false);
  const [connected, setConnected] = useState<boolean | null>(null); // null = loading
  const [secret, setSecret] = useState("");
  const [servers, setServers] = useState<HetznerServer[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  // Spin-up form.
  const [plan, setPlan] = useState<Plan>("starter");
  const [region, setRegion] = useState<Region>("eu");
  const [provMsg, setProvMsg] = useState<string | null>(null);

  // Detect a previously-wired token (presence only — never read into the form).
  useEffect(() => {
    void getLocalSecret(LOCAL_KEYS.hetznerToken).then((t) => setConnected(!!t));
  }, []);

  const client = useCallback(async () => {
    const t = await getLocalSecret(LOCAL_KEYS.hetznerToken);
    if (!t) throw new Error("not wired");
    return new HetznerClient(t);
  }, []);

  const loadServers = useCallback(async () => {
    setBusy("load");
    setErr(null);
    try {
      const cl = await client();
      setServers(await cl.listServers());
    } catch (e: any) {
      setErr(e?.message || "couldn't list servers");
      setServers(null);
    } finally {
      setBusy(null);
    }
  }, [client]);

  useEffect(() => {
    if (open && connected) void loadServers();
  }, [open, connected, loadServers]);

  const connect = useCallback(async () => {
    const tok = secret.trim();
    if (!looksLikeToken(tok)) {
      setErr("that doesn't look like a Hetzner API token (40-80 chars).");
      return;
    }
    setBusy("connect");
    setErr(null);
    try {
      // Validate by listing before persisting — a bad token never gets stored.
      const servers = await new HetznerClient(tok).listServers();
      await saveLocalSecret(LOCAL_KEYS.hetznerToken, tok);
      setSecret("");
      setConnected(true);
      setServers(servers);
    } catch (e: any) {
      setErr(e?.message || "token rejected by Hetzner");
    } finally {
      setBusy(null);
    }
  }, [secret]);

  const disconnect = useCallback(() => {
    Alert.alert("Disconnect Hetzner?", "Removes the API token from this phone. Your boxes keep running; you just can't manage them here until you re-wire.", [
      { text: "Cancel", style: "cancel" },
      {
        text: "Disconnect",
        style: "destructive",
        onPress: async () => {
          await deleteLocalSecret(LOCAL_KEYS.hetznerToken);
          setConnected(false);
          setServers(null);
        },
      },
    ]);
  }, []);

  const stopServer = useCallback(
    (s: HetznerServer) => {
      Alert.alert(
        `Stop ${s.name}?`,
        "Snapshots the box (recover-safe) then deletes the server so Hetzner billing fully stops — a powered-off server still bills. You can recreate it from the snapshot later.",
        [
          { text: "Cancel", style: "cancel" },
          {
            text: "Stop",
            onPress: async () => {
              setBusy(`stop:${s.id}`);
              setErr(null);
              try {
                const cl = await client();
                await cl.stop(s.id, `yaver-stop-${s.name}`);
                await loadServers();
              } catch (e: any) {
                setErr(e?.message || "stop failed");
              } finally {
                setBusy(null);
              }
            },
          },
        ],
      );
    },
    [client, loadServers],
  );

  const deleteServer = useCallback(
    (s: HetznerServer) => {
      Alert.alert(`Delete ${s.name}?`, `Permanently deletes server ${s.id} (${s.ip}) — NO snapshot. This cannot be undone.`, [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: async () => {
            setBusy(`rm:${s.id}`);
            setErr(null);
            try {
              const cl = await client();
              await cl.deleteServer(s.id);
              await loadServers();
            } catch (e: any) {
              setErr(e?.message || "delete failed");
            } finally {
              setBusy(null);
            }
          },
        },
      ]);
    },
    [client, loadServers],
  );

  const spinUp = useCallback(() => {
    if (!PROVISION_ENABLED) return;
    Alert.alert(
      "Spin up a box?",
      `Creates a ${plan}/${region} box on YOUR Hetzner account (~€/mo on your Hetzner bill, billed by Hetzner — not us). It self-installs Yaver, signs in as you, and is ready to vibe code in a few minutes.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Spin up",
          onPress: async () => {
            const hetznerToken = await getLocalSecret(LOCAL_KEYS.hetznerToken);
            if (!hetznerToken || !token) {
              setErr("wire Hetzner + sign in first");
              return;
            }
            setBusy("provision");
            setErr(null);
            setProvMsg("starting…");
            try {
              await provisionByoBox({
                token,
                hetznerToken,
                machineType: "cpu",
                region,
                plan,
                onProgress: (p) => setProvMsg(p.message),
              });
              await loadServers();
            } catch (e: any) {
              setErr(e?.message || "spin-up failed");
              setProvMsg(null);
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  }, [plan, region, token, loadServers]);

  // ── render ──
  const burn = (() => {
    if (!servers) return null;
    const known = servers.map((s) => monthlyEur(s.type)).filter((x): x is number => x !== null);
    const total = known.reduce((a, b) => a + b, 0);
    if (!total) return null;
    return { total, approx: known.length < servers.length };
  })();

  return (
    <View style={{ marginTop: 16 }}>
      <Pressable
        onPress={() => setOpen((v) => !v)}
        style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 8 }}
      >
        <Text style={{ fontSize: 16, fontWeight: "700", color: c.textPrimary }}>🟥 Hetzner (direct from phone)</Text>
        <Text style={{ fontSize: 13, color: connected ? c.success : c.textMuted }}>
          {connected === null ? "" : connected ? "connected" : "not wired"} {open ? "▾" : "▸"}
        </Text>
      </Pressable>

      {open && (
        <View style={{ backgroundColor: c.bgCard, borderColor: c.border, borderWidth: 1, borderRadius: 12, padding: 14, gap: 8 }}>
          <Text style={{ color: c.textMuted, fontSize: 12 }}>
            Manage boxes on your own Hetzner account straight from this phone — no paired machine needed. The token stays in
            this phone's keychain and talks to Hetzner directly (never our servers).
          </Text>

          {!connected ? (
            <View style={{ gap: 8 }}>
              <TextInput
                value={secret}
                onChangeText={setSecret}
                placeholder="Hetzner API token (Read & Write)"
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
                  paddingVertical: 9,
                  fontSize: 13,
                }}
              />
              <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                <Pressable
                  disabled={busy !== null}
                  onPress={() => void connect()}
                  style={{ backgroundColor: c.accent, borderRadius: 8, paddingHorizontal: 16, paddingVertical: 8, opacity: busy ? 0.6 : 1 }}
                >
                  {busy === "connect" ? (
                    <ActivityIndicator size="small" color={c.textInverse} />
                  ) : (
                    <Text style={{ color: c.textInverse, fontWeight: "700", fontSize: 13 }}>Connect</Text>
                  )}
                </Pressable>
                <Pressable onPress={() => void Linking.openURL(TOKEN_URL)}>
                  <Text style={{ color: c.accent, fontSize: 12 }}>Get a token ↗</Text>
                </Pressable>
              </View>
            </View>
          ) : (
            <>
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Your servers</Text>
                <View style={{ flexDirection: "row", gap: 12 }}>
                  <Pressable disabled={busy !== null} onPress={() => void loadServers()}>
                    {busy === "load" ? <ActivityIndicator size="small" color={c.accent} /> : <Text style={{ color: c.accent, fontSize: 12 }}>Refresh</Text>}
                  </Pressable>
                  <Pressable onPress={disconnect}>
                    <Text style={{ color: c.error, fontSize: 12 }}>Disconnect</Text>
                  </Pressable>
                </View>
              </View>

              {burn ? (
                <Text style={{ color: burn.total > 20 ? c.warn : c.textMuted, fontSize: 11, fontWeight: "700" }}>
                  ≈ €{burn.total.toFixed(2)}
                  {burn.approx ? "+" : ""}/mo across {servers!.length} box{servers!.length === 1 ? "" : "es"} — you pay Hetzner directly. Stop idle ones to save.
                </Text>
              ) : null}

              {servers === null ? null : servers.length === 0 ? (
                <Text style={{ color: c.textMuted, fontSize: 12 }}>No servers on this account.</Text>
              ) : (
                servers.map((s) => {
                  const eur = monthlyEur(s.type);
                  const sub = [s.type || null, eur !== null ? `~€${eur.toFixed(2)}/mo` : null, uptimeLabel(s.created) || null, s.location || null]
                    .filter(Boolean)
                    .join(" · ");
                  return (
                    <View key={s.id} style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 8 }}>
                      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                        <Text style={{ color: c.textPrimary, fontSize: 12, fontFamily: "monospace", flex: 1 }}>
                          {s.name} · <Text style={{ color: s.status === "running" ? c.success : c.textMuted }}>{s.status}</Text> · {s.ip}
                        </Text>
                        <Pressable disabled={busy !== null} onPress={() => stopServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}>
                          {busy === `stop:${s.id}` ? <ActivityIndicator size="small" color={c.warn} /> : <Text style={{ color: c.warn, fontSize: 11, fontWeight: "700" }}>Stop</Text>}
                        </Pressable>
                        <Pressable disabled={busy !== null} onPress={() => deleteServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}>
                          {busy === `rm:${s.id}` ? <ActivityIndicator size="small" color={c.error} /> : <Text style={{ color: c.error, fontSize: 11, fontWeight: "700" }}>Delete</Text>}
                        </Pressable>
                      </View>
                      {sub ? <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "monospace", marginTop: 2 }}>{sub}</Text> : null}
                    </View>
                  );
                })
              )}

              {/* Spin up a vibe-ready box — gated until the provision routes
                  are deployed + validated (PROVISION_ENABLED). */}
              <View style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 10, gap: 6 }}>
                <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Spin up a box</Text>
                <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
                  {(["starter", "pro", "scale"] as Plan[]).map((p) => (
                    <Pressable key={p} onPress={() => setPlan(p)} style={{ borderWidth: 1, borderColor: plan === p ? c.accent : c.border, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 5 }}>
                      <Text style={{ color: plan === p ? c.accent : c.textMuted, fontSize: 11, fontWeight: "700" }}>{p}</Text>
                    </Pressable>
                  ))}
                  {(["eu", "us"] as Region[]).map((r) => (
                    <Pressable key={r} onPress={() => setRegion(r)} style={{ borderWidth: 1, borderColor: region === r ? c.accent : c.border, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 5 }}>
                      <Text style={{ color: region === r ? c.accent : c.textMuted, fontSize: 11, fontWeight: "700" }}>{r.toUpperCase()}</Text>
                    </Pressable>
                  ))}
                </View>
                <Text style={{ color: c.textMuted, fontSize: 10 }}>
                  {serverTypeFor(plan, region)}{monthlyEur(serverTypeFor(plan, region)) !== null ? ` · ~€${monthlyEur(serverTypeFor(plan, region))!.toFixed(2)}/mo on your Hetzner bill` : ""}
                </Text>
                <Pressable
                  disabled={busy !== null || !PROVISION_ENABLED}
                  onPress={spinUp}
                  style={{ backgroundColor: PROVISION_ENABLED ? c.accent : c.neutralBg, borderRadius: 8, paddingVertical: 10, alignItems: "center", opacity: busy ? 0.6 : 1 }}
                >
                  {busy === "provision" ? (
                    <ActivityIndicator size="small" color={c.textInverse} />
                  ) : (
                    <Text style={{ color: PROVISION_ENABLED ? c.textInverse : c.textMuted, fontWeight: "700", fontSize: 13 }}>
                      {PROVISION_ENABLED ? `Spin up ${plan} box` : "Spin up — available after deploy"}
                    </Text>
                  )}
                </Pressable>
                {provMsg ? <Text style={{ color: c.textMuted, fontSize: 11 }}>{provMsg}</Text> : null}
              </View>
            </>
          )}

          {err ? (
            <View style={{ backgroundColor: c.errorBg, borderColor: c.errorBorder, borderWidth: 1, borderRadius: 8, padding: 8 }}>
              <Text style={{ color: c.error, fontSize: 11 }}>{err}</Text>
            </View>
          ) : null}
        </View>
      )}
    </View>
  );
}
