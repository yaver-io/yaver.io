// CloudProvidersSection — first-class "bring your own cloud" connect UI
// for Settings. The user pastes their OWN provider API token (Hetzner
// first-class, DigitalOcean too); it's stored ENCRYPTED on their agent
// (AES-256-GCM at ~/.yaver/secrets), never synced to Convex, and lets
// them provision boxes on their OWN account — they pay the provider
// directly, nothing to Yaver.
//
// CREDENTIAL-LEAK SAFETY (deliberate):
//  - the token input is secureTextEntry + autofill/autocorrect/spellcheck
//    OFF, so it's never shown, cached, or sent to a keyboard cloud;
//  - the token is held in local state ONLY long enough to POST it to the
//    agent over the authed channel, then cleared immediately;
//  - we NEVER log it and NEVER render it back — the list/status APIs are
//    redacted server-side (they return connected/label/lastUsed, never
//    the token), so there is nowhere the secret can echo out.

import React, { useCallback, useEffect, useState } from "react";
import { View, Text, Pressable, TextInput, Alert, ActivityIndicator, Linking } from "react-native";
import { quicClient } from "../lib/quic";

// Featured BYO compute providers (the VM providers the agent can
// provision on directly). Others connect via the web Accounts view.
const FEATURED = ["hetzner", "digitalocean"];

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

  const load = useCallback(async () => {
    if (!token || !quicClient.isConnected) return;
    try {
      const r = await quicClient.accountsList();
      const provs: ProviderMeta[] = (r.providers || [])
        .filter((p: any) => FEATURED.includes(p.id))
        .map((p: any) => ({
          id: p.id,
          label: p.label,
          fields: Array.isArray(p.fields) ? p.fields : ["token"],
          tokenURL: p.tokenURL,
          signupURL: p.signupURL,
          notes: p.notes,
        }));
      // Keep Hetzner first.
      provs.sort((a, b) => (a.id === "hetzner" ? -1 : b.id === "hetzner" ? 1 : 0));
      setProviders(provs);
      const byId: Record<string, AccountSummary> = {};
      for (const a of r.accounts || []) byId[a.provider] = a;
      setAccounts(byId);
      setLoaded(true);
    } catch {
      // agent unreachable — leave whatever we had; section still renders
      setLoaded(true);
    }
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
      await quicClient.accountConnect(activeProvider, label.trim(), { token: value });
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
      "Removes the stored API token from this machine's vault. Boxes already running on your account are not affected.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Disconnect",
          style: "destructive",
          onPress: async () => {
            setBusy(`disc:${providerId}`);
            try {
              await quicClient.accountDisconnect(providerId);
              setServers(null);
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
      const r = await quicClient.cloudListServers();
      setServers(Array.isArray(r.servers) ? r.servers : []);
    } catch (e: any) {
      Alert.alert("Couldn't list servers", e?.message || "Try again.");
    } finally {
      setBusy(null);
    }
  };

  const removeServer = (srv: any) => {
    const id = String(srv.id ?? srv.ID ?? "");
    const name = String(srv.name ?? srv.Name ?? id);
    if (!id) return;
    Alert.alert(
      `Delete ${name}?`,
      `Permanently deletes Hetzner server ${id} on YOUR account (no snapshot). This cannot be undone.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Delete",
          style: "destructive",
          onPress: async () => {
            setBusy(`rm:${id}`);
            try {
              await quicClient.cloudDestroyServer(id);
              await loadServers();
            } catch (e: any) {
              Alert.alert("Couldn't delete", e?.message || "Try again.");
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
          {!token || !quicClient.isConnected ? (
            <Text style={{ color: c.textMuted, fontSize: 12 }}>
              Connect to a machine first — the token is stored on that machine&apos;s encrypted vault, never on our servers.
            </Text>
          ) : !loaded ? (
            <ActivityIndicator color={c.textMuted} />
          ) : (
            <>
              <Text style={{ color: c.textMuted, fontSize: 11 }}>
                Your API token is encrypted on this machine ({"~/.yaver/secrets"}) and never leaves it — we never see or store it.
              </Text>

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

              {/* BYO server management (Hetzner connected). */}
              {hetznerConnected ? (
                <View style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 10, gap: 6 }}>
                  <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Your Hetzner servers</Text>
                    <Pressable disabled={busy !== null} onPress={loadServers} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}>
                      {busy === "servers" ? <ActivityIndicator size="small" color={c.textMuted} /> : (
                        <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700" }}>{servers === null ? "Load" : "Refresh"}</Text>
                      )}
                    </Pressable>
                  </View>
                  {servers === null ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>
                      Tap Load to list servers on your account. Provision new boxes with {"`yaver ops cloud_provision host=hetzner`"} (in-app spin-up coming soon).
                    </Text>
                  ) : servers.length === 0 ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>No servers on your Hetzner account.</Text>
                  ) : (
                    servers.map((s: any) => {
                      const id = String(s.id ?? s.ID ?? "");
                      return (
                        <View key={id} style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", paddingVertical: 4 }}>
                          <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "monospace", flex: 1 }}>
                            {String(s.name ?? s.Name ?? id)} · {String(s.status ?? s.Status ?? "?")} · {String(s.ip ?? s.IP ?? s.publicIp ?? "")}
                          </Text>
                          <Pressable disabled={busy !== null} onPress={() => removeServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}>
                            {busy === `rm:${id}` ? <ActivityIndicator size="small" color="#e11d48" /> : (
                              <Text style={{ color: "#e11d48", fontSize: 11, fontWeight: "700" }}>Delete</Text>
                            )}
                          </Pressable>
                        </View>
                      );
                    })
                  )}
                </View>
              ) : null}

              <Text style={{ color: c.textMuted, fontSize: 10, marginTop: 2 }}>
                Other providers (Vercel, Supabase, Cloudflare, …) connect from the web dashboard → Accounts.
              </Text>
            </>
          )}
        </View>
      ) : null}
    </View>
  );
}
