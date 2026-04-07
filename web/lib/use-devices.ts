"use client";

import { useEffect, useState, useCallback } from "react";
import { CONVEX_URL } from "@/lib/constants";

export interface Device {
  id: string;
  name: string;
  platform: string;
  host: string;
  port: number;
  lastSeen: string;
  online: boolean;
}

interface DevicesState {
  devices: Device[];
  refreshDevices: () => Promise<void>;
}

export function useDevices(token: string | null): DevicesState {
  const [devices, setDevices] = useState<Device[]>([]);

  const refreshDevices = useCallback(async () => {
    if (!token) return;
    try {
      const res = await fetch(`${CONVEX_URL}/devices/list`, {
        method: "GET",
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) return;
      const raw = await res.json();
      const arr = Array.isArray(raw) ? raw : (raw.devices ?? []);

      // Map API fields to Device interface
      const mapped: Device[] = arr.map((d: any) => ({
        id: d.deviceId || d.id || "",
        name: d.name || d.hostname || "",
        platform: d.platform || "",
        host: d.quicHost || d.host || "",
        port: d.quicPort || d.port || 18080,
        lastSeen: d.lastHeartbeat ? new Date(d.lastHeartbeat).toISOString() : "",
        online: d.isOnline ?? d.online ?? false,
      }));

      // Deduplicate by hostname — keep the entry with latest heartbeat
      const seen = new Map<string, Device>();
      for (const d of mapped) {
        const key = d.name.toLowerCase().replace(/\.local$/, "");
        // Skip IP-only names if we already have a hostname for the same machine
        if (/^\d+\.\d+\.\d+\.\d+$/.test(d.name)) {
          // Only keep IP entry if no hostname entry exists
          if (!seen.has(key)) seen.set(key, d);
          continue;
        }
        const existing = seen.get(key);
        if (!existing || d.lastSeen > existing.lastSeen) {
          seen.set(key, d);
        }
      }

      // Remove IP entries that have a matching hostname entry
      const hostnames = new Set<string>();
      for (const d of seen.values()) {
        if (!/^\d+\.\d+\.\d+\.\d+$/.test(d.name)) hostnames.add(d.name.toLowerCase());
      }
      const deduped = Array.from(seen.values()).filter(d => {
        if (/^\d+\.\d+\.\d+\.\d+$/.test(d.name) && hostnames.size > 0) return false;
        return true;
      });

      // Sort: online first
      deduped.sort((a, b) => (a.online === b.online ? 0 : a.online ? -1 : 1));

      setDevices(deduped);
    } catch {
      // Silently fail
    }
  }, [token]);

  useEffect(() => {
    refreshDevices();
    // Poll every 10s
    const iv = setInterval(refreshDevices, 10000);
    return () => clearInterval(iv);
  }, [refreshDevices]);

  return { devices, refreshDevices };
}
