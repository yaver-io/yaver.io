// Deploy Status — a lean, glanceable board of what's shipping right now, read
// from the active box's autorun store over P2P (GET /autoruns/deploy-status).
//
// This is the mobile half of the deploy-status UI wiring (AUTORUN_STORE.md §8.5):
// the store on the box is the source of truth for "is TestFlight deploying? which
// build? which stage? how many uploads today vs the cap?", and this screen
// summarises it for the phone so you don't shell in. Auto-refreshes while open so
// a live archive/upload shows its stage advancing. Nothing here is Convex — it's
// pulled live from the paired box.

import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, RefreshControl, ScrollView, Text, View } from "react-native";
import { useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { useAuth } from "../src/context/AuthContext";
import { agentFetch } from "../src/lib/agentRequest";

interface DeployRow {
  target: string;
  deploying: boolean;
  holder?: string;
  build?: string;
  stage?: string;
  startedAt?: number;
  elapsedSecs?: number;
  uploadsToday: number;
  quota: number;
}

const TARGET_LABEL: Record<string, string> = {
  testflight: "TestFlight (iOS)",
  playstore: "Play Store (Android)",
  convex: "Convex backend",
  "cloudflare-web": "Cloudflare (web)",
};

// The store's deploy stages, in order, so we can show a progress hint.
const STAGES = ["archiving", "exporting", "uploading", "submitting"];

function elapsedLabel(secs?: number): string {
  if (!secs || secs < 0) return "";
  if (secs < 60) return `${secs}s`;
  const m = Math.floor(secs / 60);
  return m < 60 ? `${m}m` : `${Math.floor(m / 60)}h ${m % 60}m`;
}

export default function DeployStatusScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice } = useDevice() as any;
  const { token } = useAuth();
  const [rows, setRows] = useState<DeployRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    if (!activeDevice?.id) {
      setError("No box connected.");
      setLoading(false);
      return;
    }
    try {
      const res = await agentFetch(activeDevice, token, "/autoruns/deploy-status", {}, 8000);
      if (!res.ok) throw new Error(`agent ${res.status}`);
      const data = await res.json();
      setRows(Array.isArray(data?.targets) ? data.targets : []);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [activeDevice, token]);

  // Auto-refresh while open so a live deploy's stage advances on screen.
  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), 5000);
    return () => clearInterval(t);
  }, [load]);

  const card = {
    backgroundColor: c.bgCard,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 12,
    padding: 14,
    marginBottom: 12,
  } as const;

  const anyDeploying = rows.some((r) => r.deploying);

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Deploy Status" onBack={() => router.back()} />
      <ScrollView
        contentContainerStyle={{ padding: 16 }}
        refreshControl={<RefreshControl refreshing={loading && rows.length === 0} onRefresh={load} tintColor={c.accent} />}
      >
        {/* Lean summary line */}
        <Text style={{ color: c.textMuted, fontSize: 12, marginBottom: 12 }}>
          {activeDevice?.name || activeDevice?.id || "box"} ·{" "}
          {anyDeploying ? "shipping now" : "nothing deploying"}
        </Text>

        {loading && rows.length === 0 && (
          <View style={{ alignItems: "center", padding: 24 }}>
            <ActivityIndicator color={c.accent} />
          </View>
        )}

        {!!error && rows.length === 0 && (
          <View style={[card, { borderColor: c.error || "#ef4444" }]}>
            <Text style={{ color: c.error || "#ef4444", fontSize: 14 }}>{error}</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 6 }}>
              Needs the box's yaver agent (≥ the autorun-store build) reachable.
            </Text>
          </View>
        )}

        {rows.map((r) => {
          const stageIdx = r.stage ? STAGES.indexOf(r.stage) : -1;
          const nearQuota = r.quota > 0 && r.uploadsToday >= r.quota - 3;
          return (
            <View key={r.target} style={[card, r.deploying ? { borderColor: c.accent } : null]}>
              <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
                <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700" }}>
                  {TARGET_LABEL[r.target] || r.target}
                </Text>
                <Text
                  style={{
                    color: r.deploying ? c.accent : c.textMuted,
                    fontSize: 12,
                    fontWeight: "700",
                  }}
                >
                  {r.deploying ? `● ${r.stage || "deploying"}` : "idle"}
                </Text>
              </View>

              {r.deploying && (
                <>
                  {/* Stage progress dots — clear stages, glanceable */}
                  <View style={{ flexDirection: "row", gap: 6, marginTop: 10 }}>
                    {STAGES.map((st, i) => (
                      <View
                        key={st}
                        style={{
                          flex: 1,
                          height: 4,
                          borderRadius: 2,
                          backgroundColor: i <= stageIdx ? c.accent : c.border,
                        }}
                      />
                    ))}
                  </View>
                  <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 8 }} numberOfLines={1}>
                    build {r.build || "?"} · {elapsedLabel(r.elapsedSecs)} · {r.holder}
                  </Text>
                </>
              )}

              {/* Quota line (the whole reason the store gates deploys) */}
              <Text
                style={{
                  color: nearQuota ? "#f59e0b" : c.textMuted,
                  fontSize: 12,
                  marginTop: r.deploying ? 6 : 8,
                }}
              >
                {r.uploadsToday}/{r.quota > 0 ? r.quota : "∞"} uploads today
                {nearQuota ? " · near daily cap" : ""}
              </Text>
            </View>
          );
        })}

        <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center", marginTop: 4 }}>
          Live from the box's autorun store · one deploy per target at a time
        </Text>
      </ScrollView>
    </View>
  );
}
