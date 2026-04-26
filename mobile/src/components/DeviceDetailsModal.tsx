// Mobile mirror of web/components/dashboard/DevicesView.tsx →
// DeviceDetailsPanel + ConnectionSection. Surfaces the same
// transport classification, relay version, LAN/Tailscale/Cloudflare
// breakdown, and runtime info on the phone.

import React, { useEffect, useState } from "react";
import { Modal, Pressable, ScrollView, Text, View } from "react-native";
import type { Device } from "../context/DeviceContext";
import { useColors } from "../context/ThemeContext";
import { quicClient } from "../lib/quic";
import {
  classifyTransport,
  fetchRelayHealth,
  transportToneRGB,
  type TransportInfo,
} from "../lib/transport";

function transportFor(device: Device): TransportInfo {
  return classifyTransport({
    host: device.host,
    port: device.port,
    localIps: device.lanIps,
    publicEndpoints: device.publicEndpoints,
    tunnelUrl: device.tunnelUrl,
    activeRelayUrl: quicClient.activeRelayBaseUrl ?? null,
    activeTunnelUrl: quicClient.activeTunnelBaseUrl ?? null,
    platform: device.os,
    name: device.name,
  });
}

function timeAgo(epochMs: number | undefined): string {
  if (!epochMs || epochMs <= 0) return "—";
  const diff = Math.max(0, Date.now() - epochMs);
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return sec <= 5 ? "just now" : `${sec}s ago`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

export interface DeviceDetailsModalProps {
  device: Device | null;
  visible: boolean;
  onClose: () => void;
}

export default function DeviceDetailsModal({ device, visible, onClose }: DeviceDetailsModalProps) {
  const c = useColors();
  const t = device ? transportFor(device) : null;
  const [relayHealth, setRelayHealth] = useState<{ version?: string; tunnels?: number; activeDevices?: number } | null>(null);

  useEffect(() => {
    if (!visible || !t || !t.url) { setRelayHealth(null); return; }
    if (t.primary !== "yaver-public-relay" && t.primary !== "self-hosted-relay") {
      setRelayHealth(null);
      return;
    }
    let cancelled = false;
    const ac = new AbortController();
    void fetchRelayHealth(t.url, ac.signal).then((h) => {
      if (!cancelled) setRelayHealth(h);
    });
    return () => { cancelled = true; ac.abort(); };
  }, [visible, t?.url, t?.primary]);

  if (!device || !t) return null;

  const palette = transportToneRGB(t.tone);

  const lanIps = device.lanIps || [];
  const tailscaleIp = lanIps.find((ip) => /^100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\./.test(ip));
  const wslIp = lanIps.find((ip) => /^172\.(1[6-9]|2\d|3[0-1])\./.test(ip));
  const privateLanIps = lanIps.filter((ip) => /^(10\.|192\.168\.)/.test(ip) && ip !== tailscaleIp);

  const Row = ({ label, value, mono }: { label: string; value: React.ReactNode; mono?: boolean }) => (
    <View style={{ flexDirection: "row", justifyContent: "space-between", paddingVertical: 4, gap: 12 }}>
      <Text style={{ color: c.textMuted, fontSize: 12 }}>{label}</Text>
      <Text style={{
        color: c.textPrimary,
        fontSize: 12,
        fontFamily: mono ? "Menlo" : undefined,
        flexShrink: 1,
        textAlign: "right",
      }}>
        {value || "—"}
      </Text>
    </View>
  );

  return (
    <Modal visible={visible} animationType="slide" presentationStyle="pageSheet" onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <View style={{
          flexDirection: "row", justifyContent: "space-between", alignItems: "center",
          paddingHorizontal: 16, paddingTop: 16, paddingBottom: 12,
          borderBottomWidth: 1, borderBottomColor: c.border,
        }}>
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textPrimary, fontSize: 18, fontWeight: "700" }}>{device.name}</Text>
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 2 }}>{device.os} · {device.host}:{device.port}</Text>
          </View>
          <Pressable onPress={onClose} style={{ paddingHorizontal: 12, paddingVertical: 6 }}>
            <Text style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}>Done</Text>
          </Pressable>
        </View>

        <ScrollView style={{ flex: 1 }} contentContainerStyle={{ padding: 16 }}>
          {/* Connection */}
          <View style={{
            borderWidth: 1, borderColor: c.border, borderRadius: 8,
            backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
          }}>
            <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center", marginBottom: 8 }}>
              <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1 }}>CONNECTION</Text>
              <View style={{
                paddingHorizontal: 6, paddingVertical: 2, borderRadius: 6,
                backgroundColor: palette.bg, borderWidth: 1, borderColor: palette.border,
              }}>
                <Text style={{ color: palette.text, fontSize: 10, fontWeight: "700", letterSpacing: 0.6 }}>
                  {t.label.toUpperCase()}
                </Text>
              </View>
            </View>
            <Row label="Active path" value={t.detail} />
            {t.url ? <Row label="URL" value={t.url} mono /> : null}
            {(t.primary === "yaver-public-relay" || t.primary === "self-hosted-relay") ? (
              <Row
                label="Relay version"
                value={
                  relayHealth?.version
                    ? `v${relayHealth.version}${typeof relayHealth.tunnels === "number" ? ` · ${relayHealth.tunnels} tunnel${relayHealth.tunnels === 1 ? "" : "s"}` : ""}`
                    : "probing…"
                }
              />
            ) : null}
            {device.tunnelUrl ? <Row label="Tunnel URL" value={device.tunnelUrl} mono /> : null}
            {tailscaleIp ? <Row label="Tailscale IP" value={`${tailscaleIp}:${device.port ?? 18080}`} mono /> : null}
            {wslIp ? <Row label="WSL2 NAT IP" value={`${wslIp}:${device.port ?? 18080}`} mono /> : null}
            {privateLanIps.length ? <Row label="LAN IPs" value={privateLanIps.join(", ")} mono /> : null}
            {(device.publicEndpoints || []).length ? <Row label="Public endpoints" value={(device.publicEndpoints || []).join(", ")} mono /> : null}
            <Row label="Reported host" value={`${device.host}:${device.port ?? 18080}`} mono />
          </View>

          {/* Identity */}
          <View style={{
            borderWidth: 1, borderColor: c.border, borderRadius: 8,
            backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
          }}>
            <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
              IDENTITY
            </Text>
            <Row label="Device ID" value={device.id} mono />
            {device.hwid ? <Row label="Hardware ID" value={device.hwid.slice(0, 16) + "…"} mono /> : null}
            {device.publicKey ? <Row label="Primary key" value={device.publicKey.slice(0, 16) + "…"} mono /> : null}
            {device.accessScope ? <Row label="Access scope" value={device.accessScope} /> : null}
            {device.priorityMode ? <Row label="Priority mode" value={device.priorityMode} /> : null}
            {device.isGuest && device.hostName ? <Row label="Shared from" value={device.hostName} /> : null}
          </View>

          {/* Runtime */}
          <View style={{
            borderWidth: 1, borderColor: c.border, borderRadius: 8,
            backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
          }}>
            <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
              RUNTIME
            </Text>
            <Row label="Status" value={device.online ? "Online" : "Offline"} />
            {device.needsAuth ? <Row label="Auth" value="Needs auth" /> : null}
            <Row label="Last agent signal" value={timeAgo(device.lastSeen)} />
            {device.lastTunnelEvent?.at ? (
              <Row
                label="Live signal"
                value={`${device.lastTunnelEvent.online ? "relay-online" : "relay-offline"} (${timeAgo(device.lastTunnelEvent.at)})`}
              />
            ) : null}
            {device.peerState ? (
              <Row
                label="Peer bus"
                value={`${device.peerState}${device.peerLastSeen ? ` (${timeAgo(device.peerLastSeen)})` : ""}`}
              />
            ) : null}
            {device.edgeProfile ? (
              <>
                <Row label="Local inference" value={device.edgeProfile.supportsLocalInference ? "yes" : "no"} />
                <Row label="Max model" value={device.edgeProfile.maxModelClass} />
                {typeof device.edgeProfile.batteryPct === "number" ? (
                  <Row label="Battery" value={`${device.edgeProfile.batteryPct}%${device.edgeProfile.isCharging ? " (charging)" : ""}`} />
                ) : null}
                {device.edgeProfile.thermalState ? <Row label="Thermal" value={device.edgeProfile.thermalState} /> : null}
              </>
            ) : null}
          </View>

          {/* Runners */}
          {(device.runners || []).length > 0 ? (
            <View style={{
              borderWidth: 1, borderColor: c.border, borderRadius: 8,
              backgroundColor: c.bgCard, padding: 12, marginBottom: 12,
            }}>
              <Text style={{ color: c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 1, marginBottom: 8 }}>
                RUNNERS
              </Text>
              {(device.runners || []).map((r, i) => (
                <Row key={`r-${i}`} label={r.runnerId || "runner"} value={`${r.title || ""}${r.pid ? ` · pid ${r.pid}` : ""}`} />
              ))}
            </View>
          ) : null}
        </ScrollView>
      </View>
    </Modal>
  );
}
