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
import { getByoMachines, type ByoMachine } from "../lib/subscription";

// Featured BYO compute providers (the VM providers the agent can
// provision on directly). Others connect via the web Accounts view.
const FEATURED = ["hetzner", "digitalocean"];

// Approx Hetzner running cost (EUR/mo, incl. IPv4) by server_type. Rough —
// for at-a-glance "what am I burning right now", NOT billing. A running box
// bills full price even powered-off; only DELETE halts it. Unknown type → null
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
  // Stopped boxes (snapshot images) available to restart.
  const [snapshots, setSnapshots] = useState<any[] | null>(null);
  // Convex-synced lifecycle state (alive/sleeping/deleted across devices).
  const [byoState, setByoState] = useState<ByoMachine[] | null>(null);
  // Spin-up form.
  const [showSpinUp, setShowSpinUp] = useState(false);
  const [plan, setPlan] = useState<"starter" | "pro" | "scale">("starter");
  const [region, setRegion] = useState<"eu" | "us">("eu");
  const [repoUrl, setRepoUrl] = useState("");

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
      void loadLifecycle();
    } catch (e: any) {
      Alert.alert("Couldn't list servers", e?.message || "Try again.");
    } finally {
      setBusy(null);
    }
  };

  // Refresh the Convex-synced lifecycle (reconcile live servers → active,
  // then read the cross-device state). Best-effort.
  const loadLifecycle = async () => {
    if (!token) return;
    try {
      await quicClient.cloudReconcile().catch(() => {});
      setByoState(await getByoMachines(token));
    } catch {
      /* non-fatal */
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

  // Approx Hetzner hourly price by plan (cx21/31/41), shown so the user
  // sees the per-hour cost of a box on their own account.
  const HOURLY_EUR: Record<string, string> = { starter: "€0.007/hr", pro: "€0.013/hr", scale: "€0.026/hr" };

  const spinUp = async () => {
    setBusy("spinup");
    try {
      const r = await quicClient.cloudProvision({ plan, region, repoUrl: repoUrl.trim() || undefined });
      if (r?.dryRun) {
        Alert.alert("Dry run — nothing created", String(r.plan || "") + "\n\nTo create real boxes, set YAVER_CLOUD_STOPSTART_LIVE=1 on the connected machine. This keeps spin-up / stop / start consistently enabled together.");
      } else {
        Alert.alert(
          "Box spinning up",
          `${r?.name || "Box"} is booting on your Hetzner account (${r?.ip || "ip pending"}). It self-installs Yaver and appears as a device to claim. Stop it anytime to halt billing.`,
        );
        setShowSpinUp(false);
        setRepoUrl("");
        await loadServers();
      }
    } catch (e: any) {
      Alert.alert("Couldn't spin up", e?.message || "Try again.");
    } finally {
      setBusy(null);
    }
  };

  const stopServer = (srv: any) => {
    const id = String(srv.id ?? srv.ID ?? "");
    const name = String(srv.name ?? srv.Name ?? id);
    if (!id) return;
    Alert.alert(
      `Stop ${name}?`,
      "Snapshots the box (recover-safe) then deletes the server so Hetzner billing stops — a powered-off server still bills full price; only delete halts it. Bring it back anytime from the snapshot.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Stop",
          onPress: async () => {
            setBusy(`stop:${id}`);
            try {
              const r = await quicClient.cloudStopServer(id);
              if (r?.dryRun) {
                Alert.alert("Dry run", String(r.plan || "") + "\n\nReal stop needs YAVER_CLOUD_STOPSTART_LIVE=1 on the machine.");
              } else {
                await loadServers();
                await loadSnapshots();
              }
            } catch (e: any) {
              Alert.alert("Couldn't stop", e?.message || "Try again.");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  const bakeServer = (srv: any) => {
    const id = String(srv.id ?? srv.ID ?? "");
    const name = String(srv.name ?? srv.Name ?? id);
    if (!id) return;
    Alert.alert(
      `Bake ${name} into a fast-boot image?`,
      "Snapshots this ready box into a reusable golden image on YOUR account (the box keeps running). Future spin-ups boot from it in seconds. Re-bake after upgrading the box.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Bake",
          onPress: async () => {
            setBusy(`bake:${id}`);
            try {
              const r = await quicClient.cloudBake(id);
              if (r?.dryRun) {
                Alert.alert("Dry run", String(r.plan || "") + "\n\nReal bake needs YAVER_CLOUD_STOPSTART_LIVE=1 on the machine.");
              } else {
                Alert.alert("Baked", `Golden image ${r?.baked || ""} cached — new boxes will spin up fast.`);
                await loadSnapshots();
              }
            } catch (e: any) {
              Alert.alert("Couldn't bake", e?.message || "Try again.");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  const loadSnapshots = async () => {
    try {
      const r = await quicClient.cloudSnapshots();
      setSnapshots(r.snapshots);
    } catch {
      // non-fatal
    }
  };

  const startFromSnapshot = (snap: any) => {
    const imageId = String(snap.id ?? snap.ID ?? "");
    const desc = String(snap.description ?? snap.Description ?? imageId);
    if (!imageId) return;
    // Recreate under a fresh name derived from the snapshot label.
    const name = (desc.replace(/^yaver-stop-/, "yaver-") || `yaver-${imageId}`).slice(0, 40);
    Alert.alert(
      "Start this box?",
      `Recreates a ${plan}/${region} server from snapshot ${imageId} on your account (new IP). Hourly billing resumes.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Start",
          onPress: async () => {
            setBusy(`start:${imageId}`);
            try {
              const r = await quicClient.cloudStartServer(imageId, name, plan, region);
              if (r?.dryRun) {
                Alert.alert("Dry run", String(r.plan || "") + "\n\nReal start needs YAVER_CLOUD_STOPSTART_LIVE=1 on the machine.");
              } else {
                Alert.alert("Box starting", `${name} is booting (${r?.ip || "ip pending"}).`);
                await loadServers();
                await loadSnapshots();
              }
            } catch (e: any) {
              Alert.alert("Couldn't start", e?.message || "Try again.");
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
                <View style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 10, gap: 8 }}>
                  {/* Spin up a box on the user's own account. */}
                  <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Run a box on your Hetzner</Text>
                    <Pressable disabled={busy !== null} onPress={() => setShowSpinUp((s) => !s)} style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#059669", borderRadius: 6, paddingHorizontal: 10, paddingVertical: 4 }}>
                      <Text style={{ color: "#059669", fontSize: 12, fontWeight: "700" }}>{showSpinUp ? "Close" : "＋ Spin up"}</Text>
                    </Pressable>
                  </View>

                  {showSpinUp ? (
                    <View style={{ gap: 6, padding: 8, borderRadius: 8, borderWidth: 1, borderColor: c.border }}>
                      <View style={{ flexDirection: "row", gap: 6 }}>
                        {(["starter", "pro", "scale"] as const).map((pl) => (
                          <Pressable key={pl} onPress={() => setPlan(pl)} style={{ borderWidth: 1, borderColor: plan === pl ? "#0ea5e9" : c.border, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 5 }}>
                            <Text style={{ color: plan === pl ? "#0ea5e9" : c.textMuted, fontSize: 11, fontWeight: "700" }}>{pl}</Text>
                          </Pressable>
                        ))}
                        {(["eu", "us"] as const).map((rg) => (
                          <Pressable key={rg} onPress={() => setRegion(rg)} style={{ borderWidth: 1, borderColor: region === rg ? "#0ea5e9" : c.border, borderRadius: 6, paddingHorizontal: 10, paddingVertical: 5 }}>
                            <Text style={{ color: region === rg ? "#0ea5e9" : c.textMuted, fontSize: 11, fontWeight: "700" }}>{rg}</Text>
                          </Pressable>
                        ))}
                      </View>
                      <Text style={{ color: c.textMuted, fontSize: 10 }}>~{HOURLY_EUR[plan]} on your Hetzner bill (you pay Hetzner directly).</Text>
                      <TextInput
                        value={repoUrl}
                        onChangeText={setRepoUrl}
                        placeholder="Git repo to clone (optional, https:// or git@)"
                        placeholderTextColor={c.textMuted}
                        autoCapitalize="none"
                        autoCorrect={false}
                        spellCheck={false}
                        style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, color: c.textPrimary, backgroundColor: c.bgCardElevated ?? c.bgCard, fontFamily: "monospace", fontSize: 12 }}
                      />
                      <Pressable disabled={busy !== null} onPress={spinUp} style={{ opacity: busy ? 0.5 : 1, borderRadius: 8, paddingVertical: 9, alignItems: "center", backgroundColor: "#059669" }}>
                        {busy === "spinup" ? <ActivityIndicator size="small" color="#fff" /> : (
                          <Text style={{ color: "#fff", fontSize: 13, fontWeight: "700" }}>Spin up {plan} box</Text>
                        )}
                      </Pressable>
                    </View>
                  ) : null}

                  {/* Running servers. */}
                  <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", marginTop: 2 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 13, fontWeight: "700" }}>Your servers</Text>
                    <Pressable disabled={busy !== null} onPress={() => { void loadServers(); void loadSnapshots(); }} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 8, paddingVertical: 4 }}>
                      {busy === "servers" ? <ActivityIndicator size="small" color={c.textMuted} /> : (
                        <Text style={{ color: "#0ea5e9", fontSize: 12, fontWeight: "700" }}>{servers === null ? "Load" : "Refresh"}</Text>
                      )}
                    </Pressable>
                  </View>
                  {servers === null ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>Tap Load to list servers on your account.</Text>
                  ) : servers.length === 0 ? (
                    <Text style={{ color: c.textMuted, fontSize: 11 }}>No running servers.</Text>
                  ) : (
                    <>
                      {/* Headline monthly burn — the at-a-glance "am I leaking
                          money on idle boxes" number. A running box bills even
                          when idle; Stop (snapshot+delete) is what halts it. */}
                      {(() => {
                        const known = servers.map((s: any) => monthlyEur(s.type ?? s.Type)).filter((x): x is number => x !== null);
                        const total = known.reduce((a, b) => a + b, 0);
                        if (!total) return null;
                        const approx = known.length < servers.length ? "+" : "";
                        return (
                          <Text style={{ color: total > 20 ? "#b45309" : c.textMuted, fontSize: 11, fontWeight: "700", marginTop: 2 }}>
                            ≈ €{total.toFixed(2)}{approx}/mo across {servers.length} running box{servers.length === 1 ? "" : "es"} — you pay Hetzner directly. Stop idle ones to save.
                          </Text>
                        );
                      })()}
                      {servers.map((s: any) => {
                        const id = String(s.id ?? s.ID ?? "");
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
                            {String(s.name ?? s.Name ?? id)} · {String(s.status ?? s.Status ?? "?")} · {String(s.ip ?? s.IP ?? "")}
                          </Text>
                          <Pressable disabled={busy !== null} onPress={() => bakeServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 6, paddingVertical: 4 }}>
                            {busy === `bake:${id}` ? <ActivityIndicator size="small" color="#0ea5e9" /> : (
                              <Text style={{ color: "#0ea5e9", fontSize: 11, fontWeight: "700" }}>Bake</Text>
                            )}
                          </Pressable>
                          <Pressable disabled={busy !== null} onPress={() => stopServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 6, paddingVertical: 4 }}>
                            {busy === `stop:${id}` ? <ActivityIndicator size="small" color="#b45309" /> : (
                              <Text style={{ color: "#b45309", fontSize: 11, fontWeight: "700" }}>Stop</Text>
                            )}
                          </Pressable>
                          <Pressable disabled={busy !== null} onPress={() => removeServer(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 6, paddingVertical: 4 }}>
                            {busy === `rm:${id}` ? <ActivityIndicator size="small" color="#e11d48" /> : (
                              <Text style={{ color: "#e11d48", fontSize: 11, fontWeight: "700" }}>Delete</Text>
                            )}
                          </Pressable>
                        </View>
                        {costLine ? (
                          <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "monospace", marginLeft: 2 }}>{costLine}</Text>
                        ) : null}
                        </View>
                      );
                    })}
                    </>
                  )}

                  {/* Stopped boxes (snapshots) — restart to resume. */}
                  {snapshots && snapshots.length > 0 ? (
                    <View style={{ gap: 4, marginTop: 2 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>Stopped boxes (snapshots)</Text>
                      {snapshots.map((s: any) => {
                        const id = String(s.id ?? s.ID ?? "");
                        return (
                          <View key={id} style={{ flexDirection: "row", alignItems: "center", gap: 6, paddingVertical: 3 }}>
                            <Text style={{ color: c.textMuted, fontSize: 11, fontFamily: "monospace", flex: 1 }}>
                              {String(s.description ?? s.Description ?? id)} · €{Number(s.estMonthlyEur ?? s.EstMonthlyEUR ?? 0).toFixed(2)}/mo
                            </Text>
                            <Pressable disabled={busy !== null} onPress={() => startFromSnapshot(s)} style={{ opacity: busy ? 0.5 : 1, paddingHorizontal: 6, paddingVertical: 4 }}>
                              {busy === `start:${id}` ? <ActivityIndicator size="small" color="#059669" /> : (
                                <Text style={{ color: "#059669", fontSize: 11, fontWeight: "700" }}>Start</Text>
                              )}
                            </Pressable>
                          </View>
                        );
                      })}
                    </View>
                  ) : null}

                  {/* Convex-synced lifecycle — visible across all your
                      devices (alive / sleeping / deleted + timestamps).
                      Convex holds only id/state/time — never the token. */}
                  {byoState && byoState.length > 0 ? (
                    <View style={{ gap: 3, marginTop: 2, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 6 }}>
                      <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>Lifecycle (all devices)</Text>
                      {byoState.slice(0, 8).map((b) => {
                        const color = b.state === "active" ? "#059669" : b.state === "stopped" ? "#b45309" : c.textMuted;
                        const label = b.state === "active" ? "alive" : b.state === "stopped" ? "sleeping" : "deleted";
                        const ts = b.state === "deleted" ? b.deletedAt : b.state === "stopped" ? b.stoppedAt : b.lastUpAt;
                        const when = ts ? new Date(ts).toISOString().slice(0, 16).replace("T", " ") : "";
                        return (
                          <View key={b.id} style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
                            <Text style={{ color, fontSize: 10, fontWeight: "700", width: 56 }}>{label}</Text>
                            <Text style={{ color: c.textMuted, fontSize: 10, fontFamily: "monospace", flex: 1 }} numberOfLines={1}>
                              {b.name}{b.serverIp ? ` · ${b.serverIp}` : ""}
                            </Text>
                            <Text style={{ color: c.textMuted, fontSize: 9 }}>{when}</Text>
                          </View>
                        );
                      })}
                    </View>
                  ) : null}
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
