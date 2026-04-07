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
      // API may return { devices: [...] } or just [...]
      const data: Device[] = Array.isArray(raw) ? raw : (raw.devices ?? []);
      setDevices(data);
    } catch {
      // Silently fail -- devices list is non-critical.
    }
  }, [token]);

  // Auto-refresh on mount
  useEffect(() => {
    refreshDevices();
  }, [refreshDevices]);

  return {
    devices,
    refreshDevices,
  };
}
