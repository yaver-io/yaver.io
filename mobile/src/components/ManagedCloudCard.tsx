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
  getManagedSubscription,
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
  const [owner, setOwner] = useState<boolean | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!token) return;
    const r = await getManagedSubscription(token);
    if (r) {
      setData(r);
      setOwner(r.cloudPreviewOwner === true);
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

  // Hide removed/decommissioned (stopped) boxes — gone, not billing.
  const machines = (data?.machines ?? []).filter((m) => m.status !== "stopped");
  const sub = data?.subscription ?? null;

  const decommission = (id: string, srv?: string) => {
    Alert.alert(
      "Decommission box?",
      `Destroys the server (srv ${srv ?? "—"}), stops billing, and cancels the subscription. Cannot be undone.`,
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
              );
              await load();
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
        ☁ Managed Cloud
      </Text>
      <Text style={{ color: c.textMuted, fontSize: 11 }}>
        {sub
          ? `Plan ${sub.plan} · ${sub.status}`
          : "No active subscription — buy from the web dashboard (Devices → Managed cloud)."}
      </Text>

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
                {m.status !== "stopped" && m.status !== "stopping" ? (
                  <Pressable
                    disabled={busy !== null}
                    onPress={() => decommission(m.id, m.hetznerServerId)}
                    style={{ opacity: busy ? 0.5 : 1, borderWidth: 1, borderColor: "#e11d48", borderRadius: 6, paddingHorizontal: 8, paddingVertical: 3 }}
                  >
                    {busy === m.id ? (
                      <ActivityIndicator size="small" color="#e11d48" />
                    ) : (
                      <Text style={{ color: "#e11d48", fontSize: 11, fontWeight: "700" }}>
                        ♻ Decommission
                      </Text>
                    )}
                  </Pressable>
                ) : (
                  <Text style={{ color: c.textMuted, fontSize: 10 }}>{m.status}</Text>
                )}
              </View>

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
