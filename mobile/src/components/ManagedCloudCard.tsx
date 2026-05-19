// ManagedCloudCard — #10 mobile parity for the web ManagedCloudPanel /
// BillingView. Owner-gated (server cloudPreviewOwner — never a
// hardcoded name; hide is cosmetic, server 403s every action). Shows
// each managed box with a real "Setting up your box — <phase>"
// progress bar, an Unauthorized state, and a Decommission action.
// project_managed_cloud_onboarding_gap.

import React, { useCallback, useEffect, useState } from "react";
import { View, Text, Pressable, Alert, ActivityIndicator } from "react-native";
import { getConvexSiteUrl } from "../lib/auth";
import {
  devTopUpManagedCloud,
  getManagedCloudBalance,
  getManagedSubscription,
  startManagedCloudMachine,
  stopManagedCloudMachine,
  type ManagedCloudBalanceSummary,
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
  const [owner, setOwner] = useState<boolean | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    const r = await getManagedSubscription(token);
    if (r) {
      setData(r);
      setOwner(r.cloudPreviewOwner === true);
      const b = await getManagedCloudBalance(token);
      setBalance(b ?? r.balance ?? null);
    } else {
      setOwner(false);
    }
  }, [token]);

  useEffect(() => {
    void load();
    const iv = setInterval(() => void load(), 8000);
    return () => clearInterval(iv);
  }, [load]);

  // Owner-only private preview: render nothing for non-owners or while
  // ownership is still unknown (never flash to a non-owner).
  if (!token || owner !== true) return null;

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
      Alert.alert(action === "start" ? "Start failed" : "Stop failed", e?.message || "Managed-cloud lifecycle route is not available yet.");
    } finally {
      setBusy(null);
    }
  };

  const topUpDev = async () => {
    setBusy("topup-dev");
    try {
      await devTopUpManagedCloud(token, 1000);
      await load();
    } catch (e: any) {
      Alert.alert("Top-up failed", e?.message || "Managed-cloud top-up route is not available yet.");
    } finally {
      setBusy(null);
    }
  };

  return (
    <View style={[{ borderRadius: 12, borderWidth: 1, borderColor: c.border, padding: 14, gap: 8, backgroundColor: c.surface }]}>
      <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>
        ☁ Managed Cloud
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>
        {sub
          ? `Plan ${sub.plan} · ${sub.status}`
          : "No active subscription — buy from the web dashboard (Devices → Managed cloud)."}
      </Text>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
        <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>
          Balance {money(balanceCents)}
        </Text>
        {typeof balance?.estimatedHourlyCents === "number" ? (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>
            ~{money(balance.estimatedHourlyCents)}/hr running
          </Text>
        ) : null}
        <Pressable
          disabled={busy !== null}
          onPress={topUpDev}
          style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: c.border, borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}
        >
          <Text style={{ color: c.textPrimary, fontSize: 11, fontWeight: "700" }}>
            Dev top-up
          </Text>
        </Pressable>
      </View>

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
                {m.status === "stopped" ? (
                  <Pressable
                    disabled={busy !== null}
                    onPress={() => lifecycle(m.id, "start")}
                    style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#059669", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}
                  >
                    {busy === `start:${m.id}` ? (
                      <ActivityIndicator size="small" color="#059669" />
                    ) : (
                      <Text style={{ color: "#059669", fontSize: 11, fontWeight: "700" }}>
                        Start
                      </Text>
                    )}
                  </Pressable>
                ) : m.status !== "stopping" ? (
                  <Pressable
                    disabled={busy !== null}
                    onPress={() => lifecycle(m.id, "stop")}
                    style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#b45309", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}
                  >
                    {busy === `stop:${m.id}` ? (
                      <ActivityIndicator size="small" color="#b45309" />
                    ) : (
                      <Text style={{ color: "#b45309", fontSize: 11, fontWeight: "700" }}>
                        Stop
                      </Text>
                    )}
                  </Pressable>
                ) : (
                  <Text style={{ color: c.textMuted, fontSize: 10 }}>{m.status}</Text>
                )}
              </View>

              {m.status !== "stopping" ? (
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
