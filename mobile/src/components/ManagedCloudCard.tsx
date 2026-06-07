// ManagedCloudCard — #10 mobile parity for the web ManagedCloudPanel /
// BillingView. Owner-gated (server cloudPreviewOwner — never a
// hardcoded name; hide is cosmetic, server 403s every action). Shows
// each managed box with a real "Setting up your box — <phase>"
// progress bar, an Unauthorized state, and a Decommission action.
// project_managed_cloud_onboarding_gap.

import React, { useCallback, useEffect, useState } from "react";
import { View, Text, Pressable, Alert, ActivityIndicator, Linking } from "react-native";
import { getConvexSiteUrl } from "../lib/auth";
import {
  createCreditPackCheckout,
  getCreditPacks,
  getManagedCloudBalance,
  getManagedCloudUsage,
  getManagedSubscription,
  provisionManagedCloud,
  startManagedCloudMachine,
  stopManagedCloudMachine,
  type CreditPack,
  type ManagedCloudBalanceSummary,
  type ManagedCloudUsageSummary,
  type ManagedSubscriptionSummary,
} from "../lib/subscription";

const PHASE_LABEL: Record<string, string> = {
  creating: "Reserving your box…",
  booting: "Booting & installing Docker…",
  "installing-docker": "Installing Docker…",
  "pulling-image": "Pulling the Yaver image…",
  "starting-agent": "Starting the Yaver agent…",
  registering: "Registering your device…",
  "authorizing-runners": "Almost there — finishing setup…",
  ready: "Ready",
  error: "Setup failed",
};

export default function ManagedCloudCard({
  c,
  token,
}: {
  c: any;
  token: string | null | undefined;
}) {
  const [data, setData] = useState<ManagedSubscriptionSummary | null>(null);
  const [balance, setBalance] = useState<ManagedCloudBalanceSummary | null>(null);
  const [usage, setUsage] = useState<ManagedCloudUsageSummary | null>(null);
  const [packs, setPacks] = useState<CreditPack[]>([]);
  const [access, setAccess] = useState<boolean | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [showLedger, setShowLedger] = useState(false);

  const load = useCallback(async () => {
    if (!token) return;
    const r = await getManagedSubscription(token);
    if (r) {
      setData(r);
      // Render for the owner OR when the launch flag opens cloud access.
      setAccess(r.cloudAccess === true || r.cloudPreviewOwner === true);
      const [b, u] = await Promise.all([
        getManagedCloudBalance(token),
        getManagedCloudUsage(token),
      ]);
      setBalance(b ?? r.balance ?? null);
      setUsage(u);
    } else {
      setAccess(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
    const iv = setInterval(() => void load(), 8000);
    return () => clearInterval(iv);
  }, [load]);

  // Lazily fetch the credit-pack catalog once access is confirmed.
  useEffect(() => {
    if (!token || access !== true || packs.length) return;
    void getCreditPacks(token).then(setPacks);
  }, [token, access, packs.length]);

  // Private preview / launch-gated: render nothing for non-access users
  // or while access is still unknown (never flash to a non-access user).
  if (!token || access !== true) return null;

  const machines = data?.machines ?? [];
  const sub = data?.subscription ?? null;
  const balanceCents =
    balance?.balanceCents ??
    balance?.prepaidBalanceCents ??
    data?.prepaidBalanceCents ??
    null;
  const currency = balance?.currency ?? data?.currency ?? "EUR";

  const money = (cents: number | null | undefined) => {
    if (typeof cents !== "number") return "—";
    return `${currency.toUpperCase()} ${(cents / 100).toFixed(2)}`;
  };

  const decommission = (id: string, resourceId?: string) => {
    Alert.alert(
      "Decommission box?",
      `Decommissions the cloud resource (${resourceId ?? "—"}), stops billing, and cancels the subscription. Cannot be undone.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Decommission",
          style: "destructive",
          onPress: async () => {
            setBusy(id);
            try {
              await fetch(
                `${getConvexSiteUrl()}/billing/yaver-cloud/dev-deprovision`,
                {
                  method: "POST",
                  headers: {
                    Authorization: `Bearer ${token}`,
                    "Content-Type": "application/json",
                  },
                  body: JSON.stringify({ machineId: id }),
                },
              ).then(async (res) => {
                if (!res.ok) {
                  const data = await res.json().catch(() => ({}));
                  throw new Error(data?.error || `HTTP ${res.status}`);
                }
              });
              await load();
            } catch (e: any) {
              Alert.alert("Decommission failed", e?.message || "Could not decommission this managed box.");
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  const lifecycle = async (id: string, action: "start" | "stop") => {
    setBusy(`${action}:${id}`);
    try {
      if (action === "start") {
        await startManagedCloudMachine(token, id);
      } else {
        await stopManagedCloudMachine(token, id);
      }
      await load();
    } catch (e: any) {
      Alert.alert(
        action === "start" ? "Couldn't Resume" : "Couldn't Pause",
        e?.message || `Yaver couldn't ${action === "start" ? "resume" : "pause"} this managed-cloud machine right now. Update Yaver if a newer build is available, and check your connection — then try again.`,
      );
    } finally {
      setBusy(null);
    }
  };

  // Add credit, OpenAI-style: open a web checkout in the system browser
  // (NEVER an in-app purchase — keeps Apple/Google billing policy out of
  // it; compute is remote IaaS, sold on the web). On payment the
  // order_created webhook credits the wallet; balance refreshes on the
  // next poll.
  const addCredit = async (packId: string) => {
    setBusy(`pack:${packId}`);
    try {
      const r = await createCreditPackCheckout(token, packId);
      if (r?.url) {
        await Linking.openURL(r.url);
        Alert.alert(
          "Finish in your browser",
          "Complete the payment in the browser that just opened. Your balance updates here automatically once the payment clears.",
        );
      } else {
        throw new Error("No checkout URL returned");
      }
    } catch (e: any) {
      Alert.alert("Couldn't start top-up", e?.message || "Try again in a moment.");
    } finally {
      setBusy(null);
    }
  };

  const spinUp = (machineType: "cpu" | "gpu") => {
    // The running estimate the wallet reports tracks a CPU box; don't show it
    // for GPU (GPU bills at a higher rate). Keep GPU copy rate-agnostic.
    const rateLine =
      machineType === "cpu"
        ? `billed from your prepaid balance (~${money(balance?.estimatedHourlyCents)}/hr running)`
        : "billed from your prepaid balance at the GPU rate (higher than CPU)";
    Alert.alert(
      "Spin up a box?",
      `Provisions a new ${machineType.toUpperCase()} cloud box, ${rateLine}. You can pause it anytime — paused costs ~€0.50/mo.`,
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Spin up",
          onPress: async () => {
            setBusy(`spinup:${machineType}`);
            try {
              await provisionManagedCloud(token, { machineType });
              await load();
            } catch (e: any) {
              const msg = e?.message || "";
              Alert.alert(
                msg.toLowerCase().includes("balance") ? "Add credit first" : "Couldn't spin up",
                msg.toLowerCase().includes("balance")
                  ? "Your prepaid balance is too low to safely run a box. Add credit, then try again."
                  : msg || "Yaver couldn't provision a box right now. Try again.",
              );
            } finally {
              setBusy(null);
            }
          },
        },
      ],
    );
  };

  return (
    <View style={[{ borderRadius: 12, borderWidth: 1, borderColor: c.border, padding: 14, gap: 8, backgroundColor: c.surface }]}>
      <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>
        ☁ Yaver Cloud
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>
        {sub
          ? `Managed cloud · ${sub.status}`
          : "No managed cloud machine is active on this account."}
      </Text>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <Text style={{ color: c.textPrimary, fontSize: 14, fontWeight: "800" }}>
          {money(balanceCents)}
        </Text>
        {typeof balance?.estimatedHourlyCents === "number" ? (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>
            ~{money(balance.estimatedHourlyCents)}/hr running
          </Text>
        ) : null}
        {balance?.lowBalance ? (
          <Text style={{ color: "#b45309", fontSize: 11, fontWeight: "700" }}>
            Low balance
          </Text>
        ) : null}
      </View>

      {/* Add credit — OpenAI-style web top-up (no in-app purchase). */}
      <View style={{ gap: 4 }}>
        <Text style={{ color: c.textMuted, fontSize: 11 }}>Add credit</Text>
        <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
          {(packs.length
            ? packs
            : [
                { id: "p10", cents: 1000, label: "$10" },
                { id: "p25", cents: 2500, label: "$25" },
                { id: "p50", cents: 5000, label: "$50" },
              ]
          ).map((p) => (
            <Pressable
              key={p.id}
              disabled={busy !== null}
              onPress={() => addCredit(p.id)}
              style={{
                opacity: busy ? 0.5 : 1,
                borderWidth: 1,
                borderColor: "#0ea5e9",
                borderRadius: 8,
                paddingHorizontal: 12,
                paddingVertical: 6,
              }}
            >
              {busy === `pack:${p.id}` ? (
                <ActivityIndicator size="small" color="#0ea5e9" />
              ) : (
                <Text style={{ color: "#0ea5e9", fontSize: 13, fontWeight: "700" }}>
                  + {p.label}
                </Text>
              )}
            </Pressable>
          ))}
        </View>
        <Text style={{ color: c.textMuted, fontSize: 10 }}>
          Opens a secure checkout in your browser. Credit is added when payment clears.
        </Text>
      </View>

      {/* Spin up a new box from prepaid balance. */}
      <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
        <Pressable
          disabled={busy !== null}
          onPress={() => spinUp("cpu")}
          style={{
            opacity: busy ? 0.5 : 1,
            borderWidth: 1,
            borderColor: "#059669",
            borderRadius: 8,
            paddingHorizontal: 12,
            paddingVertical: 6,
          }}
        >
          {busy === "spinup:cpu" ? (
            <ActivityIndicator size="small" color="#059669" />
          ) : (
            <Text style={{ color: "#059669", fontSize: 13, fontWeight: "700" }}>
              ＋ Spin up CPU box
            </Text>
          )}
        </Pressable>
        <Pressable
          disabled={busy !== null}
          onPress={() => spinUp("gpu")}
          style={{
            opacity: busy ? 0.5 : 1,
            borderWidth: 1,
            borderColor: "#7c3aed",
            borderRadius: 8,
            paddingHorizontal: 12,
            paddingVertical: 6,
          }}
        >
          {busy === "spinup:gpu" ? (
            <ActivityIndicator size="small" color="#7c3aed" />
          ) : (
            <Text style={{ color: "#7c3aed", fontSize: 13, fontWeight: "700" }}>
              ＋ Spin up GPU box
            </Text>
          )}
        </Pressable>
        {usage && (usage.usage.length || usage.topups.length) ? (
          <Pressable onPress={() => setShowLedger((s) => !s)} style={{ paddingVertical: 6, paddingHorizontal: 6 }}>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>
              {showLedger ? "Hide activity" : "Activity"}
            </Text>
          </Pressable>
        ) : null}
      </View>

      {/* Recent wallet activity ledger. */}
      {showLedger && usage ? (
        <View style={{ gap: 3, borderTopWidth: 1, borderTopColor: c.border, paddingTop: 6 }}>
          {usage.topups.slice(0, 5).map((t) => (
            <View key={`t-${t.orderId}`} style={{ flexDirection: "row", justifyContent: "space-between" }}>
              <Text style={{ color: c.textMuted, fontSize: 10 }}>
                Top-up{t.packId ? ` (${t.packId})` : ""}
              </Text>
              <Text style={{ color: "#059669", fontSize: 10, fontWeight: "700" }}>
                + {money(t.amountCents)}
              </Text>
            </View>
          ))}
          {usage.usage.slice(0, 6).map((u, i) => (
            <View key={`u-${u.createdAt}-${i}`} style={{ flexDirection: "row", justifyContent: "space-between" }}>
              <Text style={{ color: c.textMuted, fontSize: 10 }}>
                {u.date} · {u.state}{u.dryRun ? " · sim" : ""}
              </Text>
              <Text style={{ color: c.textMuted, fontSize: 10 }}>
                − {money(u.chargedCents)}
              </Text>
            </View>
          ))}
        </View>
      ) : null}

      {machines.length === 0 ? (
        <Text style={{ color: c.textMuted, fontSize: 12 }}>No managed boxes.</Text>
      ) : (
        machines.map((m) => {
          const phase = m.provisionPhase ?? null;
          const pct =
            typeof m.provisionProgress === "number"
              ? m.provisionProgress
              : m.status === "active"
                ? 90
                : 10;
          const initializing =
            m.status === "provisioning" ||
            (!!phase &&
              phase !== "ready" &&
              m.status !== "error" &&
              m.status !== "stopped" &&
              m.status !== "paused" &&
              m.status !== "suspended" &&
              m.status !== "resuming" &&
              m.status !== "active");
          return (
            <View
              key={m.id}
              style={{ borderTopWidth: 1, borderTopColor: c.border, paddingTop: 8, gap: 6 }}
            >
              <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
                <Text style={{ color: c.textPrimary, fontSize: 12, fontFamily: "monospace" }}>
                  {m.machineType ?? "cpu"} · {m.region ?? "eu"} ·{" "}
                  <Text
                    style={{
                      color:
                        m.status === "error"
                          ? "#e11d48"
                          : m.status === "active"
                            ? "#059669"
                            : c.textMuted,
                      fontWeight: "700",
                    }}
                  >
                    {m.status ?? "?"}
                  </Text>
                </Text>
                {m.status === "active" ? (
                  <Pressable
                    disabled={busy !== null}
                    onPress={() =>
                      Alert.alert(
                        "Pause box?",
                        "Snapshots the disk, then deletes the cloud server so it stops billing — ~€0.50/mo paused vs ~€30/mo running. Resume recreates it from the snapshot in ~2-3 min (new IP).",
                        [
                          { text: "Cancel", style: "cancel" },
                          { text: "Pause", onPress: () => void lifecycle(m.id, "stop") },
                        ],
                      )
                    }
                    style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#b45309", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}
                  >
                    {busy === `stop:${m.id}` ? (
                      <ActivityIndicator size="small" color="#b45309" />
                    ) : (
                      <Text style={{ color: "#b45309", fontSize: 11, fontWeight: "700" }}>
                        ⏸ Pause
                      </Text>
                    )}
                  </Pressable>
                ) : m.status === "paused" || m.status === "suspended" ? (
                  <Pressable
                    disabled={busy !== null}
                    onPress={() => lifecycle(m.id, "start")}
                    style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#059669", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}
                  >
                    {busy === `start:${m.id}` ? (
                      <ActivityIndicator size="small" color="#059669" />
                    ) : (
                      <Text style={{ color: "#059669", fontSize: 11, fontWeight: "700" }}>
                        ▶ Resume
                      </Text>
                    )}
                  </Pressable>
                ) : (
                  <Text style={{ color: c.textMuted, fontSize: 10 }}>{m.status}</Text>
                )}
              </View>

              {m.status !== "stopping" && m.status !== "resuming" ? (
                <Pressable
                  disabled={busy !== null}
                  onPress={() => decommission(m.id, m.hetznerServerId)}
                  style={{ alignSelf: "flex-start", opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#e11d48", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}
                >
                  {busy === m.id ? (
                    <ActivityIndicator size="small" color="#e11d48" />
                  ) : (
                    <Text style={{ color: "#e11d48", fontSize: 11, fontWeight: "700" }}>
                      Decommission
                    </Text>
                  )}
                </Pressable>
              ) : null}

              {initializing ? (
                <View style={{ gap: 4 }}>
                  <Text style={{ color: c.textMuted, fontSize: 11 }}>
                    Setting up your box —{" "}
                    {phase ? PHASE_LABEL[phase] ?? phase : "initializing…"}
                  </Text>
                  {m.bootImageSource === "golden" ? (
                    <Text style={{ color: "#059669", fontSize: 10 }}>⚡ Fast boot from a prebuilt image</Text>
                  ) : m.bootImageSource === "vanilla" ? (
                    <Text style={{ color: c.textMuted, fontSize: 10 }}>First boot — building the image (~3-5 min)</Text>
                  ) : null}
                  <View style={{ height: 6, borderRadius: 3, backgroundColor: c.border, overflow: "hidden" }}>
                    <View
                      style={{
                        height: 6,
                        borderRadius: 3,
                        backgroundColor: "#0ea5e9",
                        width: `${Math.max(5, Math.min(100, pct))}%`,
                      }}
                    />
                  </View>
                </View>
              ) : m.status === "active" && m.runnersAuthorized === false ? (
                <Text style={{ color: "#b45309", fontSize: 11, fontWeight: "600" }}>
                  ⚠ Unauthorized — sign your coding agents in from the web dashboard.
                </Text>
              ) : m.status === "paused" || m.status === "suspended" ? (
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  ⏸ Paused — snapshot kept, ~€0.50/mo (vs ~€30/mo running). Resume recreates it in ~2-3 min.
                </Text>
              ) : m.status === "resuming" ? (
                <Text style={{ color: "#0ea5e9", fontSize: 11 }}>
                  Resuming from snapshot…
                </Text>
              ) : null}

              {m.errorMessage ? (
                <Text style={{ color: "#e11d48", fontSize: 11 }}>{m.errorMessage}</Text>
              ) : null}
            </View>
          );
        })
      )}
    </View>
  );
}
