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
  getManagedCloudBalance,
  getManagedCloudUsage,
  getManagedSubscription,
  startManagedCloudMachine,
  stopManagedCloudMachine,
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
  snapshotting: "Saving a snapshot…",
  "powering-down": "Powering down…",
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
      `Decommissions the cloud resource (${resourceId ?? "—"}) and stops managed-infrastructure billing. Cannot be undone.`,
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

  return (
    <View style={[{ borderRadius: 12, borderWidth: 1, borderColor: c.border, padding: 14, gap: 8, backgroundColor: c.surface }]}>
      <Text style={{ color: c.textPrimary, fontSize: 16, fontWeight: "700" }}>
        ☁ Yaver Cloud
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>
        {sub
          ? `Managed cloud · ${sub.status}`
          : "No Yaver Cloud workspace is active on this account."}
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

      <View style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, gap: 4 }}>
        <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>
          Web-billed infrastructure
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 10, lineHeight: 15 }}>
          Yaver Cloud checkout, credits, and new workspace purchases live on
          the web. This app only controls cloud workspaces already active on
          your account: resume, pause, monitor, and finish setup.
        </Text>
      </View>

      <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
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
          const phaseInProgress = !!phase && phase !== "ready" && phase !== "error";
          const pct =
            typeof m.provisionProgress === "number"
              ? m.provisionProgress
              : m.status === "active"
                ? 90
                : 10;
          const initializing =
            m.status === "provisioning" ||
            m.status === "resuming" ||
            m.status === "stopping" ||
            (phaseInProgress &&
              m.status !== "error" &&
              m.status !== "stopped" &&
              m.status !== "paused" &&
              m.status !== "suspended");
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
                    {m.status === "stopping"
                      ? "Closing down your box"
                      : m.status === "resuming"
                        ? "Waking your box"
                        : "Setting up your box"}{" "}
                    — {phase ? PHASE_LABEL[phase] ?? phase : "initializing…"}
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
