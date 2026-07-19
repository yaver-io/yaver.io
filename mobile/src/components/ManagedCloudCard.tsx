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
  getManagedSubscription,
  setManagedCloudAutoPark,
  startManagedCloudMachine,
  stopManagedCloudMachine,
  type ManagedCloudBalanceSummary,
  type ManagedSubscriptionSummary,
} from "../lib/subscription";
import { describeRest } from "../lib/wakeMachineCore";

const PHASE_LABEL: Record<string, string> = {
  creating: "Reserving your box…",
  booting: "Booting & installing Docker…",
  "installing-docker": "Installing Docker…",
  "pulling-image": "Pulling the Yaver image…",
  "starting-agent": "Starting the Yaver agent…",
  registering: "Registering your device…",
  "authorizing-runners": "Almost there — finishing setup…",
  snapshotting: "Saving recovery snapshot…",
  "powering-down": "Powering down…",
  ready: "Ready",
  error: "Setup failed",
  // Wake-only steps. Without entries these fell through the `?? phase`
  // fallback below and printed the raw control-plane slug at the user.
  "checking-snapshot": "Finding legacy snapshot…",
  "preparing-volume": "Preparing saved workspace state…",
  "restoring-snapshot": "Starting workspace…",
  // Not progress — the box is up and waiting on the user.
  "awaiting-yaver-auth": "Waiting for you to sign this box in",
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
  const [access, setAccess] = useState<boolean | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    const r = await getManagedSubscription(token);
    if (r) {
      setData(r);
      // Render for the owner OR when the launch flag opens cloud access.
      setAccess(r.cloudAccess === true || r.cloudPreviewOwner === true);
      const b = await getManagedCloudBalance(token);
      setBalance(b ?? r.balance ?? null);
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
  const remainingCredits = balance?.allowance?.remainingStandardCredits;
  const includedCredits = balance?.allowance?.includedStandardCredits;

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
        {typeof remainingCredits === "number" && typeof includedCredits === "number" ? (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>
            {remainingCredits.toFixed(1)} standard credits left of {includedCredits}
          </Text>
        ) : (
          <Text style={{ color: c.textMuted, fontSize: 11 }}>
            Workspace allowance appears here after web subscription setup.
          </Text>
        )}
      </View>

      <View style={{ borderWidth: 1, borderColor: c.border, borderRadius: 8, padding: 10, gap: 4 }}>
        <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700" }}>
          Web-billed infrastructure
        </Text>
        <Text style={{ color: c.textMuted, fontSize: 10, lineHeight: 15 }}>
          Yaver Cloud subscription, cancellation, and new workspace purchase
          flows live on the web. This app only controls cloud workspaces already
          active on your account: resume, pause, monitor, and finish setup.
        </Text>
      </View>

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
                        "This preserves workspace state, deletes active compute, and stops compute spend. Resume recreates it when you need it.",
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
                    {/* Never print a raw slug: an unmapped phase is our bug,
                        and "pulling-image" spelled with a hyphen is not
                        something the user can act on. */}
                    — {phase ? PHASE_LABEL[phase] ?? "working on it…" : "initializing…"}
                  </Text>
                  {m.bootImageSource === "golden" ? (
                    <Text style={{ color: "#059669", fontSize: 10 }}>⚡ Fast boot from a prebuilt image</Text>
                  ) : m.bootImageSource === "vanilla" ? (
                    <Text style={{ color: c.textMuted, fontSize: 10 }}>First boot — preparing workspace image</Text>
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
                // A parked box used to say only "data kept, meter stopped" — the
                // same line whether it had slept peacefully all week or woken,
                // sat signed-out for ten minutes and re-parked itself. The
                // second case is exactly the one the user needs explained, since
                // they just watched that wake apparently do nothing.
                (() => {
                  const rest = describeRest(m, Date.now());
                  return (
                    <View style={{ gap: 3 }}>
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        ⏸ Paused — data kept, active compute stopped.
                      </Text>
                      {rest.warning ? (
                        <Text style={{ color: c.warn, fontSize: 11 }}>{rest.warning}</Text>
                      ) : null}
                      <Text style={{ color: c.textMuted, fontSize: 11 }}>
                        {rest.storage ? `${rest.storage} ` : ""}Wakes in about {rest.eta}.
                      </Text>
                    </View>
                  );
                })()
              ) : null}

              {/* Auto-close (auto-park). Required so a forgotten box always
                  stops its own meter. Legacy OFF rows can only be turned ON. */}
              <Pressable
                disabled={busy !== null || m.autoParkEnabled !== false}
                onPress={() => {
                  const go = async () => {
                    if (!token) return;
                    setBusy(`autopark:${m.id}`);
                    try {
                      await setManagedCloudAutoPark(token, m.id, true);
                      await load();
                    } catch (e: any) {
                      Alert.alert("Couldn't change auto-close", e?.message ?? "Try again.");
                    } finally {
                      setBusy(null);
                    }
                  };
                  void go();
                }}
                style={{
                  marginTop: 6,
                  flexDirection: "row",
                  alignItems: "center",
                  gap: 6,
                  opacity: busy || m.autoParkEnabled !== false ? 0.65 : 1,
                }}
              >
                {busy === `autopark:${m.id}` ? (
                  <ActivityIndicator size="small" color={c.textMuted} />
                ) : (
                  <Text style={{ fontSize: 13 }}>{m.autoParkEnabled === false ? "☐" : "☑"}</Text>
                )}
                <Text style={{ color: c.textMuted, fontSize: 11, flex: 1 }}>
                  {m.autoParkEnabled === false
                    ? "Auto-close OFF on this legacy row — tap to turn cost protection back on."
                    : `Auto-close ON — parks itself after ${m.autoParkMinutes ?? 45} min idle so it stops billing.`}
                </Text>
              </Pressable>

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
