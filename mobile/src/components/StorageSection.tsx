// StorageSection — "the box is full and I'm not at my desk".
//
// Lives INSIDE a device's details (same shape as ScreenlogSection: one import
// + one render line in DeviceDetailsModal, so nothing on the main tabs moves).
// Peer-aware: for a non-active device it routes through /peer/<id>, which is
// what lets you free space on a remote box from the phone.
//
// The flow it exists for: a build dies on ENOSPC, you open the box's details,
// tap Storage, see "10.2 GB reclaimable — 4.6 GB Gradle, 5.4 GB Go cache",
// tick what you don't need, approve, and the build runs.
//
// Deliberately collapsed and lazy: the scan shells out to `du` across the
// whole home dir, so it only runs when someone actually opens this.

import React, { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Alert, Pressable, StyleSheet, Text, View } from "react-native";
import { useColors } from "../context/ThemeContext";
import { useDevice, type Device } from "../context/DeviceContext";
import { quicClient } from "../lib/quic";

interface Filesystem {
  mount: string;
  totalGb: number;
  usedGb: number;
  freeGb: number;
  usedPct: number;
}
interface Target {
  id: string;
  kind: string;
  label: string;
  path?: string;
  project?: string;
  sizeBytes: number;
  lastUsedMs?: number;
  action: string;
  rebuild: string;
}
interface Group {
  project: string;
  sizeBytes: number;
  targets: Target[];
}
interface Scan {
  hostname: string;
  os: string;
  scannedAt: string;
  filesystems: Filesystem[];
  groups: Group[];
  totalReclaimableBytes: number;
  partial?: boolean;
}

function fmtBytes(b: number): string {
  if (!b || b < 0) return "0 B";
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`;
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(0)} MB`;
  if (b >= 1 << 10) return `${(b / (1 << 10)).toFixed(0)} KB`;
  return `${b} B`;
}

// "untouched since March" is what makes an approval decision easy — a size
// alone doesn't tell you whether you still need it.
function fmtAge(ms?: number): string {
  if (!ms) return "";
  const days = Math.floor((Date.now() - ms) / 86_400_000);
  if (days < 1) return "today";
  if (days === 1) return "yesterday";
  if (days < 30) return `${days}d ago`;
  if (days < 365) return `${Math.floor(days / 30)}mo ago`;
  return `${Math.floor(days / 365)}y ago`;
}

function pctColor(pct: number): string {
  if (pct >= 95) return "#f43f5e";
  if (pct >= 85) return "#fbbf24";
  return "#34d399";
}

export function StorageSection({ device }: { device: Device }) {
  const c = useColors();
  const { activeDevice, connectionStatus } = useDevice();
  const isActive = Boolean(
    activeDevice && activeDevice.id === device.id && connectionStatus === "connected"
  );
  const target = isActive ? undefined : device.id;
  const base = `${quicClient.baseUrl}${target ? `/peer/${target}` : ""}`;

  const [open, setOpen] = useState(false);
  const [scan, setScan] = useState<Scan | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const [unsupported, setUnsupported] = useState(false);

  const refresh = useCallback(
    async (force: boolean) => {
      setLoading(true);
      setError(null);
      try {
        const res = await fetch(`${base}/storage/scan${force ? "?refresh=1" : ""}`, {
          headers: quicClient.getAuthHeaders(),
        });
        // An older agent simply doesn't have this route. Degrade to a clear
        // "update the agent" line rather than a scary red error.
        if (res.status === 404) {
          setUnsupported(true);
          return;
        }
        if (!res.ok) throw new Error(`scan → ${res.status}`);
        const json = await res.json();
        setScan(json.scan);
        setSelected(new Set());
        setMsg(null);
      } catch (e: any) {
        setError(e?.message || "scan failed");
      } finally {
        setLoading(false);
      }
    },
    [base]
  );

  useEffect(() => {
    if (open && !scan && !unsupported) refresh(false);
  }, [open, scan, unsupported, refresh]);

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const toggleGroup = (g: Group) =>
    setSelected((prev) => {
      const next = new Set(prev);
      const allOn = g.targets.every((t) => next.has(t.id));
      for (const t of g.targets) {
        if (allOn) next.delete(t.id);
        else next.add(t.id);
      }
      return next;
    });

  const selectedTargets: Target[] =
    scan?.groups.flatMap((g) => g.targets.filter((t) => selected.has(t.id))) ?? [];
  const selectedBytes = selectedTargets.reduce((sum, t) => sum + t.sizeBytes, 0);

  const doReclaim = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const res = await fetch(`${base}/storage/reclaim`, {
        method: "POST",
        headers: { ...quicClient.getAuthHeaders(), "Content-Type": "application/json" },
        body: JSON.stringify({ ids: Array.from(selected), confirm: true }),
      });
      if (!res.ok) throw new Error(`reclaim → ${res.status}`);
      const json = await res.json();
      const r = json.result;
      const failed = (r.outcomes || []).filter((o: any) => !o.ok);
      // Report what actually happened, including partial failures — a
      // permission-denied on one cache dir must not read as total success.
      setMsg(
        `✓ Freed ${r.freed} · ${r.rootFreeGbAfter?.toFixed(1)} GB free` +
          (failed.length ? ` · ${failed.length} target(s) failed` : "")
      );
      if (failed.length) setError(failed.map((o: any) => `${o.label}: ${o.error}`).join("\n"));
      await refresh(true);
    } catch (e: any) {
      setError(e?.message || "reclaim failed");
    } finally {
      setBusy(false);
    }
  }, [base, selected, refresh]);

  // The approval must name what it will delete and what it costs to get back.
  // Consent to a number the user was never shown isn't consent.
  const confirmReclaim = () => {
    if (!selectedTargets.length) return;
    const lines = selectedTargets
      .slice(0, 8)
      .map((t) => `• ${t.project ? `${t.project} — ` : ""}${t.label} (${fmtBytes(t.sizeBytes)})`)
      .join("\n");
    const more =
      selectedTargets.length > 8 ? `\n…and ${selectedTargets.length - 8} more` : "";
    Alert.alert(
      `Free ${fmtBytes(selectedBytes)}?`,
      `${lines}${more}\n\nEvery one of these is a rebuildable cache — the toolchain regenerates it. Worst case is a slower next build.`,
      [
        { text: "Cancel", style: "cancel" },
        { text: `Free ${fmtBytes(selectedBytes)}`, style: "destructive", onPress: doReclaim },
      ]
    );
  };

  const primary = scan?.filesystems?.[0];

  return (
    <View style={{ marginTop: 14 }}>
      <Pressable onPress={() => setOpen((v) => !v)} style={styles.header}>
        <Text style={[styles.title, { color: c.textPrimary }]}>💾 Storage</Text>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          {primary && (
            <Text style={{ color: pctColor(primary.usedPct), fontSize: 11, fontWeight: "700" }}>
              {primary.usedPct.toFixed(0)}% full
            </Text>
          )}
          <Text style={{ color: c.textMuted }}>{open ? "▾" : "▸"}</Text>
        </View>
      </Pressable>

      {open && (
        <View style={[styles.body, { borderColor: c.border, backgroundColor: c.bgCard }]}>
          {unsupported ? (
            <Text style={{ color: c.textMuted, fontSize: 11 }}>
              This agent is too old for storage reclaim — update it to enable this panel.
            </Text>
          ) : loading && !scan ? (
            <ActivityIndicator size="small" />
          ) : (
            <>
              {scan?.filesystems.map((fs) => (
                <View key={fs.mount} style={{ marginBottom: 10 }}>
                  <View style={styles.row}>
                    <Text style={{ color: c.textSecondary, fontSize: 11 }} numberOfLines={1}>
                      {fs.mount}
                    </Text>
                    <Text style={{ color: c.textSecondary, fontSize: 11 }}>
                      {fs.freeGb.toFixed(1)} / {fs.totalGb.toFixed(0)} GB free
                    </Text>
                  </View>
                  <View style={[styles.barTrack, { backgroundColor: c.border }]}>
                    <View
                      style={{
                        width: `${Math.min(100, fs.usedPct)}%`,
                        height: 6,
                        borderRadius: 3,
                        backgroundColor: pctColor(fs.usedPct),
                      }}
                    />
                  </View>
                </View>
              ))}

              {scan && (
                <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "700", marginBottom: 6 }}>
                  {fmtBytes(scan.totalReclaimableBytes)} reclaimable
                  {scan.partial ? " (partial scan)" : ""}
                </Text>
              )}

              {scan?.groups.length === 0 && (
                <Text style={{ color: c.textMuted, fontSize: 11 }}>
                  Nothing worth reclaiming — the caches on this box are already small.
                </Text>
              )}

              {scan?.groups.map((g) => {
                const allOn = g.targets.every((t) => selected.has(t.id));
                return (
                  <View key={g.project || "__shared"} style={{ marginBottom: 8 }}>
                    <Pressable onPress={() => toggleGroup(g)} style={styles.row}>
                      <Text style={{ color: c.textPrimary, fontSize: 12, fontWeight: "600" }}>
                        {allOn ? "☑" : "☐"} {g.project || "Shared caches"}
                      </Text>
                      <Text style={{ color: c.textSecondary, fontSize: 11 }}>
                        {fmtBytes(g.sizeBytes)}
                      </Text>
                    </Pressable>
                    {g.targets.map((t) => (
                      <Pressable key={t.id} onPress={() => toggle(t.id)} style={styles.targetRow}>
                        <Text
                          style={{ color: selected.has(t.id) ? "#60a5fa" : c.textMuted, fontSize: 11, flex: 1 }}
                          numberOfLines={1}
                        >
                          {selected.has(t.id) ? "☑" : "☐"} {t.label}
                          {t.lastUsedMs ? ` · ${fmtAge(t.lastUsedMs)}` : ""}
                        </Text>
                        <Text style={{ color: c.textSecondary, fontSize: 11 }}>
                          {fmtBytes(t.sizeBytes)}
                        </Text>
                      </Pressable>
                    ))}
                  </View>
                );
              })}

              {selected.size > 0 && (
                <Pressable
                  onPress={confirmReclaim}
                  disabled={busy}
                  style={[
                    styles.btn,
                    { backgroundColor: "#f43f5e22", borderColor: "#f43f5e55", opacity: busy ? 0.5 : 1 },
                  ]}
                >
                  {busy ? (
                    <ActivityIndicator size="small" />
                  ) : (
                    <Text style={{ color: "#fb7185", fontWeight: "600", fontSize: 12 }}>
                      Free {fmtBytes(selectedBytes)} · {selected.size} target(s)
                    </Text>
                  )}
                </Pressable>
              )}

              <Pressable
                onPress={() => refresh(true)}
                disabled={loading || busy}
                style={[styles.btn, { marginTop: 6, backgroundColor: "#3b82f622", borderColor: "#3b82f655" }]}
              >
                <Text style={{ color: "#60a5fa", fontWeight: "600", fontSize: 12 }}>
                  {loading ? "Scanning…" : "Rescan"}
                </Text>
              </Pressable>

              {msg && <Text style={{ color: "#34d399", fontSize: 11, marginTop: 6 }}>{msg}</Text>}
              {error && <Text style={{ color: "#fbbf24", fontSize: 11, marginTop: 6 }}>{error}</Text>}
            </>
          )}
        </View>
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingVertical: 6,
  },
  title: { fontSize: 13, fontWeight: "700" },
  body: { borderWidth: 1, borderRadius: 8, padding: 12, marginTop: 4 },
  row: { flexDirection: "row", alignItems: "center", justifyContent: "space-between" },
  targetRow: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    paddingLeft: 12,
    paddingVertical: 3,
  },
  barTrack: { height: 6, borderRadius: 3, marginTop: 4, overflow: "hidden" },
  btn: {
    borderWidth: 1,
    borderRadius: 6,
    paddingVertical: 8,
    alignItems: "center",
    marginTop: 8,
  },
});
