// CloudProvidersSection — first-class "bring your own cloud" connect UI
// for Settings. The user pastes their OWN provider API token (Hetzner
// first-class, DigitalOcean too). Hetzner is phone-direct: its token lives
// only in native Keychain/Keystore and calls go straight to Hetzner. It never
// transits Convex, a Yaver agent, or Relay Free.
//
// CREDENTIAL-LEAK SAFETY (deliberate):
//  - the token input is secureTextEntry + autofill/autocorrect/spellcheck
//    OFF, so it's never shown, cached, or sent to a keyboard cloud;
//  - the token is held in local state only long enough to validate it, then
//    moved to hardware-backed native storage and cleared immediately;
//  - we NEVER log it and NEVER render it back — the list/status APIs are
//    redacted server-side (they return connected/label/lastUsed, never
//    the token), so there is nowhere the secret can echo out.

import React, { useCallback, useEffect, useState } from "react";
import { View, Text, Pressable, TextInput, Alert, ActivityIndicator, Linking, Share } from "react-native";
import * as Clipboard from "expo-clipboard";
import { quicClient } from "../lib/quic";
import { useAuth } from "../context/AuthContext";
import { shareLocalHetznerWithConnectedEndpoint } from "../lib/hetznerHandoff";
import { hetznerClientCloud } from "../lib/clientCloudProvider";
import type { HetznerRecoveryExport } from "../lib/hetznerRecovery";
import type { LocalHetznerManagedServer } from "../lib/hetznerDirect";
import type { HetznerActionLog } from "../lib/hetznerDirectCore";

// Featured BYO compute providers (the VM providers the agent can
// provision on directly). Others connect via the web Accounts view.
const FEATURED = ["hetzner"];

// Approx Hetzner allocated-server cost (EUR/mo, incl. IPv4) by server_type.
// Power state does not stop billing. Unknown type → null
// (we then just show the type, no fake number).
const TYPE_EUR_MO: Record<string, number> = {
  cx11: 4.15, cx21: 5.83, cx31: 10.59, cx41: 19.9, cx51: 35.79,
  cx22: 4.59, cx32: 7.59, cx42: 17.49, cx52: 33.69,
  cpx11: 4.79, cpx21: 8.49, cpx31: 15.49, cpx41: 29.99, cpx51: 65.99,
  cax11: 3.99, cax21: 7.49, cax31: 14.99, cax41: 29.99,
};
function monthlyEur(type?: string | null): number | null {
  if (!type) return null;
  return TYPE_EUR_MO[String(type).toLowerCase()] ?? null;
}
function uptimeLabel(created?: string | null): string {
  if (!created) return "";
  const t = Date.parse(String(created));
  if (Number.isNaN(t)) return "";
  const ms = Date.now() - t;
  const days = Math.floor(ms / 86400000);
  if (days >= 1) return `up ${days}d`;
  const hrs = Math.floor(ms / 3600000);
  return `up ${Math.max(1, hrs)}h`;
}

function displayPowerStatus(status: unknown): string {
  switch (String(status || "unknown").toLowerCase()) {
    case "running": return "Running";
    case "off": return "Off — allocated";
    case "starting": return "Powering on…";
    case "stopping": return "Shutting down…";
    default: return String(status || "Unknown");
  }
}

type ProviderMeta = {
  id: string;
  label: string;
  fields: string[];
  tokenURL?: string;
  signupURL?: string;
  notes?: string;
};
type AccountSummary = {
  provider: string;
  connected?: boolean;
  label?: string;
  connectedAt?: string;
  lastUsedAt?: string;
  hint?: string;
};

export default function CloudProvidersSection({
  c,
  token,
}: {
  c: any;
  token: string | null | undefined;
}) {
  const { user } = useAuth();
  const [providers, setProviders] = useState<ProviderMeta[]>([]);
  const [accounts, setAccounts] = useState<Record<string, AccountSummary>>({});
  const [open, setOpen] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);

  // Connect form. `secret` holds the pasted token transiently and is
  // wiped the instant the connect call returns (success OR failure).
  const [activeProvider, setActiveProvider] = useState<string | null>(null);
  const [label, setLabel] = useState("");
  const [secret, setSecret] = useState("");

  // BYO server list (for a connected Hetzner account).
  const [servers, setServers] = useState<any[] | null>(null);
  const [managedServer, setManagedServer] = useState<LocalHetznerManagedServer | null>(null);
  const [renameServerId, setRenameServerId] = useState<number | null>(null);
  const [renameDraft, setRenameDraft] = useState("");
  const [actions, setActions] = useState<HetznerActionLog[] | null>(null);
  const [recovery, setRecovery] = useState<HetznerRecoveryExport | null>(null);
  const [showRecoveryImport, setShowRecoveryImport] = useState(false);
  const [recoveryBackupDraft, setRecoveryBackupDraft] = useState("");
  const [recoveryKeyDraft, setRecoveryKeyDraft] = useState("");

  const load = useCallback(async () => {
    const localHetzner = await hetznerClientCloud.isConnected().catch(() => false);
    const localManagedServer = localHetzner ? await hetznerClientCloud.getManagedServer().catch(() => null) : null;
    let provs: ProviderMeta[] = [{
      id: "hetzner",
      label: "Hetzner Cloud",
      fields: ["token"],
      tokenURL: "https://console.hetzner.cloud/projects",
      notes: "Stored only in this phone's Keychain/Keystore.",
    }];
    const byId: Record<string, AccountSummary> = {
      hetzner: {
        provider: "hetzner",
        connected: localHetzner,
        label: localHetzner ? "This phone" : undefined,
      },
    };
    try {
      if (!token || !quicClient.isConnected) throw new Error("agent unavailable");
      const r = await quicClient.accountsList();
      const agentProviders: ProviderMeta[] = (r.providers || [])
        .filter((p: any) => FEATURED.includes(p.id) && p.id !== "hetzner")
        .map((p: any) => ({
          id: p.id,
          label: p.label,
          fields: Array.isArray(p.fields) ? p.fields : ["token"],
          tokenURL: p.tokenURL,
          signupURL: p.signupURL,
          notes: p.notes,
        }));
      provs = [...provs, ...agentProviders];
      for (const a of r.accounts || []) {
        if (a.provider !== "hetzner") byId[a.provider] = a;
      }
    } catch {
      // Phone-direct Hetzner remains available without an online Yaver agent.
    }
    setProviders(provs);
    setAccounts(byId);
    setManagedServer(localManagedServer);
    setLoaded(true);
  }, [token]);

  useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const beginConnect = (providerId: string) => {
    setActiveProvider(providerId);
    setLabel("");
    setSecret("");
  };

  const cancelConnect = () => {
    setActiveProvider(null);
    setSecret(""); // never keep the token around
    setLabel("");
  };

  const submitConnect = async () => {
    if (!activeProvider || !secret.trim()) return;
    setBusy(`connect:${activeProvider}`);
    // Snapshot + immediately clear the secret from state; we only need
    // the local copy to make the request.
    const value = secret.trim();
    setSecret("");
    try {
      if (activeProvider === "hetzner") await hetznerClientCloud.connect(value);
      else await quicClient.accountConnect(activeProvider, label.trim(), { token: value });
      setActiveProvider(null);
      setLabel("");
      await load();
    } catch (e: any) {
      // Error messages from the agent never include the token; still,
      // surface a generic message and never echo `value`.
      Alert.alert("Couldn't connect", e?.message || "Check the token and try again.");
    } finally {
      setBusy(null);
    }
  };

  const disconnect = (providerId: string, providerLabel: string) => {
    Alert.alert(
      `Disconnect ${providerLabel}?`,
      providerId === "hetzner"
        ? "Removes the API token from this phone's secure storage. Servers on your account are not affected."
        : "Removes the stored API token from this machine's vault. Resources already running are not affected.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Disconnect",
          style: "destructive",
          onPress: async () => {
            setBusy(`disc:${providerId}`);
            try {
              if (providerId === "hetzner") await hetznerClientCloud.disconnect();
              else await quicClient.accountDisconnect(providerId);
              setServers(null);
              if (providerId === "hetzner") setManagedServer(null);
              await load();
            } catch (e: any) {
              Alert.alert("Couldn't disconnect", e?.message || "Try again.");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  const loadServers = async () => {
    setBusy("servers");
    try {
      setServers(await hetznerClientCloud.listServers());
    } catch (e: any) {
      Alert.alert("Couldn't list servers", e?.message || "Try again.");
    } finally {
      setBusy(null);
    }
  };

  const loadActions = async () => {
    if (!managedServer) return;
    setBusy("actions");
    try {
      setActions(await hetznerClientCloud.listActions(managedServer.id));
    } catch (e: any) {
      Alert.alert("Couldn't load Hetzner activity", e?.message || "Try again.");
    } finally {
      setBusy(null);
    }
  };

  const replaceServer = (next: any) => {
    setServers((current) => current?.map((server) => Number(server.id ?? server.ID) === Number(next.id ?? next.ID) ? next : server) ?? current);
  };

  const markServerTransition = (id: number, status: "starting" | "stopping") => {
    setServers((current) => current?.map((server) => Number(server.id ?? server.ID) === id ? { ...server, status } : server) ?? current);
  };

  const chooseManagedServer = (srv: any) => {
    const name = String(srv.name ?? srv.Name ?? "server");
    Alert.alert(
      `Manage ${name}?`,
      "This phone will allow Hetzner power controls only for this exact server. The selection stays in this device's secure storage.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Use this server",
          onPress: async () => {
            setBusy(`manage:${String(srv.id ?? srv.ID ?? "")}`);
            try {
              await hetznerClientCloud.setManagedServer(srv);
              setManagedServer(await hetznerClientCloud.getManagedServer());
            } catch (e: any) {
              Alert.alert("Couldn't save configuration", e?.message || "Try again.");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  const renameManagedServer = async () => {
    if (!managedServer || renameServerId !== managedServer.id || !renameDraft.trim()) return;
    setBusy(`rename:${managedServer.id}`);
    try {
      const renamed = await hetznerClientCloud.renameServer(managedServer.id, renameDraft);
      replaceServer(renamed);
      setManagedServer(await hetznerClientCloud.getManagedServer());
      setRenameServerId(null);
      setRenameDraft("");
    } catch (e: any) {
      Alert.alert("Couldn't rename server", e?.message || "Check the name and try again.");
    } finally {
      setBusy(null);
    }
  };

  const stopServer = (srv: any) => {
    const id = String(srv.id ?? srv.ID ?? "");
    const name = String(srv.name ?? srv.Name ?? id);
    if (!id) return;
    Alert.alert(
      `Shut down ${name}?`,
      "Requests a graceful shutdown directly from this phone. Hetzner continues billing while the server remains allocated.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Shut down",
          onPress: async () => {
            setBusy(`stop:${id}`);
            markServerTransition(Number(id), "stopping");
            try {
              replaceServer(await hetznerClientCloud.setPower(Number(id), "shutdown"));
            } catch (e: any) {
              await loadServers().catch(() => {});
              Alert.alert("Couldn't stop", e?.message || "Try again.");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  const powerOnServer = async (srv: any) => {
    const id = Number(srv.id ?? srv.ID);
    if (!Number.isFinite(id)) return;
    setBusy(`poweron:${id}`);
    markServerTransition(id, "starting");
    try {
      replaceServer(await hetznerClientCloud.setPower(id, "power_on"));
    } catch (e: any) {
      await loadServers().catch(() => {});
      Alert.alert("Couldn't power on", e?.message || "Try again.");
    } finally {
      setBusy(null);
    }
  };

  const createRecovery = async () => {
    setBusy("recovery-export");
    try {
      setRecovery(await hetznerClientCloud.exportRecovery());
    } catch (e: any) {
      Alert.alert("Couldn't create recovery", e?.message || "Try again.");
    } finally {
      setBusy(null);
    }
  };

  const restoreRecovery = async () => {
    if (!recoveryBackupDraft.trim() || !recoveryKeyDraft.trim()) return;
    setBusy("recovery-import");
    try {
      await hetznerClientCloud.importRecovery(recoveryBackupDraft, recoveryKeyDraft);
      setRecoveryBackupDraft("");
      setRecoveryKeyDraft("");
      setShowRecoveryImport(false);
      await load();
      Alert.alert("Hetzner restored", "The token is back in this phone's secure credential store.");
    } catch (e: any) {
      Alert.alert("Couldn't restore", e?.message || "Check both recovery values.");
    } finally {
      setBusy(null);
    }
  };

  const copyRecoveryKeyTemporarily = async () => {
    if (!recovery) return;
    const copied = recovery.recoveryKey;
    await Clipboard.setStringAsync(copied);
    setTimeout(() => {
      void Clipboard.getStringAsync().then((current) => {
        if (current === copied) return Clipboard.setStringAsync("");
      }).catch(() => {});
    }, 60_000);
  };

  const shareWithEndpoint = () => {
    if (!user?.id) {
      Alert.alert("Sign in required", "Sign in before sharing configuration with another trusted device.");
      return;
    }
    Alert.alert(
      "Share with connected endpoint?",
      "The token will be encrypted directly for the currently connected same-account endpoint and saved in that endpoint's local OS vault. This requires LAN or Yaver Mesh; the inspectable relay path is refused.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Share securely",
          onPress: async () => {
            setBusy("hetzner-handoff");
            try {
              await shareLocalHetznerWithConnectedEndpoint(user.id);
              Alert.alert("Shared securely", "The connected endpoint stored the token in its local encrypted credential vault.");
            } catch (e: any) {
              Alert.alert("Secure handoff failed", e?.message || "Use the same account over LAN or Yaver Mesh and try again.");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  const hetznerConnected = accounts["hetzner"]?.connected === true;

  return (
    <View style={{ marginBottom: 12 }}>
      <Pressable
        onPress={() => setOpen((v) => !v)}
        style={{
          flexDirection: "row", alignItems: "center", justifyContent: "space-between",
          padding: 16, borderRadius: 12, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard,
        }}
      >
        <View style={{ flex: 1 }}>
          <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 15 }}>
            ☁ Bring your own cloud
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>
            Connect your own Hetzner — run boxes on your account, pay the provider directly.
          </Text>
        </View>
        <Text style={{ color: c.textMuted }}>{open ? "▲" : "▼"}</Text>
      </Pressable>

      {open ? (
        <View style={{ marginTop: 4, padding: 16, borderRadius: 12, borderWidth: 1, borderColor: c.border, backgroundColor: c.bgCard, gap: 10 }}>
          {!loaded ? (
            <ActivityIndicator color={c.textMuted} />
          ) : (
            <>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                Hetzner credentials stay in this phone&apos;s native Keychain/Keystore across normal app and OS updates. This phone calls Hetzner directly; Yaver Cloud and Relay Free never receive the token.
              </Text>

              {hetznerConnected && quicClient.isConnected ? (
                <Pressable
                  disabled={busy !== null}
                  onPress={shareWithEndpoint}
                  style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#0ea5e9", borderRadius: 8, paddingHorizontal: 12, paddingVertical: 8 }}
                >
                  {busy === "hetzner-handoff" ? <ActivityIndicator size="small" color="#0ea5e9" /> : (
                    <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700", textAlign: "center" }}>Share securely with connected endpoint</Text>
                  )}
                </Pressable>
              ) : null}

              {providers.map((p) => {
                const acct = accounts[p.id];
                const connected = acct?.connected === true;
                return (
                  <View key={p.id} style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 10, gap: 6 }}>
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                      <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: connected ? "#059669" : c.textMuted }} />
                      <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "700", flex: 1 }}>{p.label}</Text>
                      {connected ? (
                        <Pressable
                          disabled={busy !== null}
                          onPress={() => disconnect(p.id, p.label)}
                          style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}
                        >
                          {busy === `disc:${p.id}` ? (
                            <ActivityIndicator size="small" color="#e11d48" />
                          ) : (
                            <Text style={{ color: "#e11d48", fontSize: 12, fontWeight: "700" }}>Disconnect</Text>
                          )}
                        </Pressable>
                      ) : (
                        <Pressable
                          disabled={busy !== null}
                          onPress={() => beginConnect(p.id)}
                          style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#0ea5e9", borderRadius: 6, paddingHorizontal: 10, paddingVertical: 4 }}
                        >
                          <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700" }}>Connect</Text>
                        </Pressable>
                      )}
                    </View>

                    {connected ? (
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        Connected{acct?.connectedAt ? ` ${acct.connectedAt.slice(0, 10)}` : ""}
                        {acct?.label ? ` · ${acct.label}` : ""}
                      </Text>
                    ) : p.tokenURL ? (
                      <Pressable onPress={() => { if (p.tokenURL?.startsWith("http")) void Linking.openURL(p.tokenURL); }}>
                        <Text style={{ color: c.textMuted, fontSize: 11 }}>
                          {p.tokenURL.startsWith("http") ? "Get an API token →" : p.tokenURL}
                        </Text>
                      </Pressable>
                    ) : null}

                    {/* Inline connect form (leak-safe token entry). */}
                    {activeProvider === p.id ? (
                      <View style={{ gap: 6, marginTop: 4 }}>
                        <TextInput
                          value={label}
                          onChangeText={setLabel}
                          placeholder="Label (optional, e.g. 'personal')"
                          placeholderTextColor={c.textMuted}
                          autoCapitalize="none"
                          style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, color: c.textPrimary, backgroundColor: c.bgCardElevated ?? c.bgCard }}
                        />
                        <TextInput
                          value={secret}
                          onChangeText={setSecret}
                          placeholder={`${p.label} API token`}
                          placeholderTextColor={c.textMuted}
                          secureTextEntry
                          autoCapitalize="none"
                          autoCorrect={false}
                          spellCheck={false}
                          autoComplete="off"
                          textContentType="password"
                          importantForAutofill="no"
                          style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, color: c.textPrimary, backgroundColor: c.bgCardElevated ?? c.bgCard, fontFamily: "monospace" }}
                        />
                        <View style={{ flexDirection: "row", gap: 8 }}>
                          <Pressable
                            disabled={busy !== null || !secret.trim()}
                            onPress={submitConnect}
                            style={{ opacity: busy || !secret.trim() ? 0.5 : 1, borderRadius: 8, paddingHorizontal: 14, paddingVertical: 8, backgroundColor: "#0ea5e9" }}
                          >
                            {busy === `connect:${p.id}` ? (
                              <ActivityIndicator size="small" color="#fff" />
                            ) : (
                              <Text style={{ color: "#fff", fontSize: 13, fontWeight: "700" }}>Save</Text>
                            )}
                          </Pressable>
                          <Pressable onPress={cancelConnect} style={{ borderRadius: 8, paddingHorizontal: 14, paddingVertical: 8, borderWidth: 1, borderColor: c.border }}>
                            <Text style={{ color: c.textSecondary ?? c.textMuted, fontSize: 13 }}>Cancel</Text>
                          </Pressable>
                        </View>
                      </View>
                    ) : null}
                  </View>
                );
              })}

              <View style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 10, gap: 8 }}>
                <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Local credential recovery</Text>
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  iOS Keychain normally survives reinstall with the same app identity. Android uninstall/reset removes Keystore data. Create an encrypted backup and keep its recovery key separately; Yaver cannot recover either for you.
                </Text>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                  {hetznerConnected ? (
                    <Pressable disabled={busy !== null} onPress={() => void createRecovery()} style={{ borderWidth: 1, borderColor: c.border, borderRadius: 7, paddingHorizontal: 10, paddingVertical: 6, opacity: busy ? 0.5 : 1 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>Create encrypted backup</Text>
                    </Pressable>
                  ) : null}
                  <Pressable disabled={busy !== null} onPress={() => setShowRecoveryImport((v) => !v)} style={{ borderWidth: 1, borderColor: c.border, borderRadius: 7, paddingHorizontal: 10, paddingVertical: 6, opacity: busy ? 0.5 : 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>Restore backup</Text>
                  </Pressable>
                </View>

                {recovery ? (
                  <View style={{ gap: 7, padding: 10, borderRadius: 8, backgroundColor: c.bgCardElevated ?? c.bgCard }}>
                    <Text style={{ color: "#b45309", fontSize: 11, fontWeight: "700" }}>
                      Save these in different places. Anyone with both can control your Hetzner account.
                    </Text>
                    <Pressable onPress={() => void Share.share({ title: "Encrypted Hetzner recovery backup", message: recovery.encryptedBackup })}>
                      <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700" }}>Share encrypted backup…</Text>
                    </Pressable>
                    <Text selectable style={{ color: c.textPrimary, fontFamily: "monospace", fontSize: 10 }}>
                      {recovery.recoveryKey}
                    </Text>
                    <Pressable onPress={() => void copyRecoveryKeyTemporarily()}>
                      <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700" }}>Copy recovery key (clears in 60s)</Text>
                    </Pressable>
                    <Pressable onPress={() => setRecovery(null)}>
                      <Text style={{ color: c.textMuted, fontSize: 12 }}>I've saved both separately</Text>
                    </Pressable>
                  </View>
                ) : null}

                {showRecoveryImport ? (
                  <View style={{ gap: 7 }}>
                    <TextInput
                      value={recoveryBackupDraft}
                      onChangeText={setRecoveryBackupDraft}
                      placeholder="Encrypted backup"
                      placeholderTextColor={c.textMuted}
                      autoCapitalize="none"
                      autoCorrect={false}
                      spellCheck={false}
                      style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, color: c.textPrimary, fontFamily: "monospace" }}
                    />
                    <TextInput
                      value={recoveryKeyDraft}
                      onChangeText={setRecoveryKeyDraft}
                      placeholder="Recovery key"
                      placeholderTextColor={c.textMuted}
                      secureTextEntry
                      autoCapitalize="none"
                      autoCorrect={false}
                      spellCheck={false}
                      autoComplete="off"
                      importantForAutofill="no"
                      style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, color: c.textPrimary, fontFamily: "monospace" }}
                    />
                    <Pressable disabled={busy !== null || !recoveryBackupDraft.trim() || !recoveryKeyDraft.trim()} onPress={() => void restoreRecovery()} style={{ alignSelf: "flex-start", backgroundColor: "#0ea5e9", borderRadius: 7, paddingHorizontal: 12, paddingVertical: 7, opacity: busy || !recoveryBackupDraft.trim() || !recoveryKeyDraft.trim() ? 0.5 : 1 }}>
                      <Text style={{ color: "#fff", fontSize: 12, fontWeight: "700" }}>Validate and restore</Text>
                    </Pressable>
                  </View>
                ) : null}
              </View>

              {/* BYO server management (Hetzner connected). */}
              {hetznerConnected ? (
                <View style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 10, gap: 8 }}>
                  <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Hetzner power control</Text>
                  </View>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    {managedServer
                      ? `Managed server: ${managedServer.name} · ${managedServer.ip || "no public IPv4"}`
                      : "Choose exactly one server below. Yaver will refuse power actions for every other server."}
                  </Text>

                  {/* Running servers. */}
                  <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: 2 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Your servers</Text>
                    <Pressable disabled={busy !== null} onPress={() => { void loadServers(); }} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}>
                      {busy === "servers" ? <ActivityIndicator size="small" color={c.textMuted} /> : (
                        <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700" }}>{servers === null ? "Load" : "Refresh"}</Text>
                      )}
                    </Pressable>
                  </View>
                  {servers === null ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>Tap Load to list servers on your account.</Text>
                  ) : servers.length === 0 ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>No servers on this Hetzner account.</Text>
                  ) : (
                    <>
                      {/* Approximate allocated cost. Powering a server off does
                          not halt Hetzner billing, so include every server. */}
                      {(() => {
                        const known = servers.map((s: any) => monthlyEur(s.type ?? s.Type)).filter((x): x is number => x !== null);
                        const total = known.reduce((a, b) => a + b, 0);
                        if (!total) return null;
                        const approx = known.length < servers.length ? "+" : "";
                        return (
                          <Text style={{ color: total > 20 ? "#b45309" : c.textMuted, fontSize: 11, fontWeight: "700", marginTop: 2 }}>
                            ≈ €{total.toFixed(2)}{approx}/mo across {servers.length} allocated box{servers.length === 1 ? "" : "es"} — paid directly to Hetzner; shutdown does not stop billing.
                          </Text>
                        );
                      })()}
                      {servers.map((s: any) => {
                        const id = String(s.id ?? s.ID ?? "");
                        const isManaged = managedServer?.id === Number(s.id ?? s.ID);
                        const type = s.type ?? s.Type ?? null;
                        const eur = monthlyEur(type);
                        const up = uptimeLabel(s.created ?? s.Created);
                        const costLine = [
                          type ? String(type) : null,
                          eur !== null ? `~€${eur.toFixed(2)}/mo` : null,
                          up || null,
                        ].filter(Boolean).join(" · ");
                        return (
                        <View key={id} style={{ paddingVertical: 4 }}>
                        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                          <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "monospace", flex: 1 }}>
                            {String(s.name ?? s.Name ?? id)} · {displayPowerStatus(s.status ?? s.Status)} · {String(s.ip ?? s.IP ?? "")}
                          </Text>
                          {!isManaged ? (
                            <Pressable disabled={busy !== null} onPress={() => chooseManagedServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 6, paddingVertical: 4 }}>
                              {busy === `manage:${id}` ? <ActivityIndicator size="small" color="#0ea5e9" /> : (
                                <Text style={{ color: "#0ea5e9", fontSize: 11, fontWeight: "700" }}>Manage this server</Text>
                              )}
                            </Pressable>
                          ) : String(s.status ?? s.Status ?? "").toLowerCase() === "off" ? (
                            <Pressable disabled={busy !== null} onPress={() => void powerOnServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 6, paddingVertical: 4 }}>
                              {busy === `poweron:${id}` ? <ActivityIndicator size="small" color="#059669" /> : (
                                <Text style={{ color: "#059669", fontSize: 11, fontWeight: "700" }}>Power on</Text>
                              )}
                            </Pressable>
                          ) : String(s.status ?? s.Status ?? "").toLowerCase() === "running" ? (
                            <Pressable disabled={busy !== null} onPress={() => stopServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 6, paddingVertical: 4 }}>
                              {busy === `stop:${id}` ? <ActivityIndicator size="small" color="#b45309" /> : (
                                <Text style={{ color: "#b45309", fontSize: 11, fontWeight: "700" }}>Shut down</Text>
                              )}
                            </Pressable>
                          ) : null}
                        </View>
                        {costLine ? (
                          <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "monospace", marginLeft: 2 }}>{costLine}</Text>
                        ) : null}
                        {isManaged ? (
                          <View style={{ gap: 6, marginLeft: 2, marginTop: 4 }}>
                            <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                              <Text style={{ color: "#059669", fontSize: 10, fontWeight: "700" }}>Managed on this phone</Text>
                              <Pressable
                                disabled={busy !== null}
                                onPress={() => { setRenameServerId(Number(id)); setRenameDraft(String(s.name ?? s.Name ?? "")); }}
                              >
                                <Text style={{ color: "#0ea5e9", fontSize: 10, fontWeight: "700" }}>Rename</Text>
                              </Pressable>
                            </View>
                            {renameServerId === Number(id) ? (
                              <View style={{ gap: 6 }}>
                                <TextInput
                                  value={renameDraft}
                                  onChangeText={setRenameDraft}
                                  placeholder="Hetzner server name"
                                  placeholderTextColor={c.textMuted}
                                  autoCapitalize="none"
                                  autoCorrect={false}
                                  spellCheck={false}
                                  maxLength={64}
                                  style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 9, color: c.textPrimary, fontFamily: "monospace" }}
                                />
                                <View style={{ flexDirection: "row", gap: 8 }}>
                                  <Pressable disabled={busy !== null || !renameDraft.trim()} onPress={() => void renameManagedServer()} style={{ opacity: busy || !renameDraft.trim() ? 0.5 : 1 }}>
                                    <Text style={{ color: "#0ea5e9", fontSize: 11, fontWeight: "700" }}>Save name</Text>
                                  </Pressable>
                                  <Pressable onPress={() => { setRenameServerId(null); setRenameDraft(""); }}>
                                    <Text style={{ color: c.textMuted, fontSize: 11 }}>Cancel</Text>
                                  </Pressable>
                                </View>
                              </View>
                            ) : null}
                          </View>
                        ) : null}
                        </View>
                      );
                    })}
                    </>
                  )}

                  {managedServer ? (
                    <View style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 8, gap: 5 }}>
                      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                        <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>Hetzner activity</Text>
                        <Pressable disabled={busy !== null} onPress={() => { void loadActions(); }} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}>
                          {busy === "actions" ? <ActivityIndicator size="small" color={c.textMuted} /> : (
                            <Text style={{ color: "#0ea5e9", fontSize: 11, fontWeight: "700" }}>{actions === null ? "Load" : "Refresh"}</Text>
                          )}
                        </Pressable>
                      </View>
                      {actions === null ? (
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>Recent native Hetzner actions stay on this phone and are never copied to Yaver Cloud.</Text>
                      ) : actions.length === 0 ? (
                        <Text style={{ color: c.textMuted, fontSize: 10 }}>No recent actions returned by Hetzner.</Text>
                      ) : actions.slice(0, 8).map((action) => (
                        <Text key={action.id} style={{ color: action.status === "error" ? "#e11d48" : c.textMuted, fontSize: 10, fontFamily: "monospace" }}>
                          {action.command} · {action.status}{action.progress !== null ? ` · ${action.progress}%` : ""}{action.started ? ` · ${new Date(action.started).toLocaleString()}` : ""}{action.errorCode ? ` · ${action.errorCode}` : ""}
                        </Text>
                      ))}
                    </View>
                  ) : null}

                </View>
              ) : null}

              <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>
                Power on and graceful shutdown keep the same server and Primary IP. They never delete the server, and an off server remains allocated and billable.
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 10 }}>
                Create the VPS in Hetzner Console, SSH into it, then install and authenticate Yaver CLI manually. This screen supports only list, rename, power on, and graceful shutdown; use Hetzner Console for everything else.
              </Text>
            </>
          )}
        </View>
      ) : null}
    </View>
  );
}
