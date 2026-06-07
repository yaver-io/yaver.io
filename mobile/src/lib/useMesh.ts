// useMesh.ts — single data hook behind every Yaver Mesh screen (home, node
// detail, exit-node picker, access rules, sharing). It centralizes the
// /mesh/* + /support/* fetches and optimistic mutations that previously lived
// inline in app/(tabs)/network.tsx, so the new multi-screen IA shares one
// source of truth instead of re-implementing fetch logic per screen.

import { useCallback, useEffect, useState } from "react";
import { Share } from "react-native";
import { useAuth } from "../context/AuthContext";
import { CONVEX_SITE_URL } from "../_core/constants";
import type { ACLRule, MeshPeer, SupportConn } from "./meshTypes";

type NodeConfigPatch = Partial<
  Pick<MeshPeer, "wantExitNode" | "wantUseExitNode" | "wantRoutes">
> & { wantEnabled?: boolean };

export type UseMesh = {
  peers: MeshPeer[];
  rules: ACLRule[];
  supporting: SupportConn[];
  supportedBy: SupportConn[];
  loading: boolean;
  error: string | null;
  setError: (e: string | null) => void;
  reload: () => Promise<void>;
  saveNodeConfig: (deviceId: string, patch: NodeConfigPatch) => Promise<void>;
  saveRules: (next: ACLRule[]) => Promise<void>;
  createSupportLink: (
    offerTerminal: boolean,
    offerDesktopControl: boolean
  ) => Promise<string | null>;
  revokeSupport: (grantId: string) => Promise<void>;
};

export function useMesh(): UseMesh {
  const { token } = useAuth();
  const [peers, setPeers] = useState<MeshPeer[]>([]);
  const [rules, setRules] = useState<ACLRule[]>([]);
  const [supporting, setSupporting] = useState<SupportConn[]>([]);
  const [supportedBy, setSupportedBy] = useState<SupportConn[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setError(null);
    const headers = { Authorization: `Bearer ${token}` };
    try {
      const [pRes, aRes] = await Promise.all([
        fetch(`${CONVEX_SITE_URL}/mesh/peers`, { headers }),
        fetch(`${CONVEX_SITE_URL}/mesh/acls`, { headers }),
      ]);
      if (!pRes.ok) throw new Error(`peers: HTTP ${pRes.status}`);
      setPeers(((await pRes.json()).peers ?? []) as MeshPeer[]);
      if (aRes.ok) setRules(((await aRes.json()).rules ?? []) as ACLRule[]);
      const cRes = await fetch(`${CONVEX_SITE_URL}/support/connections`, { headers });
      if (cRes.ok) {
        const cJson = await cRes.json();
        setSupporting(cJson.supporting ?? []);
        setSupportedBy(cJson.supportedBy ?? []);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [token]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const saveNodeConfig = useCallback(
    async (deviceId: string, patch: NodeConfigPatch) => {
      if (!token) return;
      setPeers((prev) => prev.map((p) => (p.deviceId === deviceId ? { ...p, ...patch } : p)));
      try {
        await fetch(`${CONVEX_SITE_URL}/mesh/node/config`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ deviceId, ...patch }),
        });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        void reload();
      }
    },
    [token, reload]
  );

  const saveRules = useCallback(
    async (next: ACLRule[]) => {
      if (!token) return;
      setRules(next);
      try {
        await fetch(`${CONVEX_SITE_URL}/mesh/acls/set`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ rules: next }),
        });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [token]
  );

  const createSupportLink = useCallback(
    async (offerTerminal: boolean, offerDesktopControl: boolean): Promise<string | null> => {
      if (!token) return null;
      try {
        const res = await fetch(`${CONVEX_SITE_URL}/support/invite`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ offerTerminal, offerDesktopControl }),
        });
        if (!res.ok) throw new Error(`invite: HTTP ${res.status}`);
        const url = `https://yaver.io/j/${(await res.json()).code}`;
        await Share.share({
          message: `Let me help you on your computer with Yaver — open this to connect: ${url}`,
        });
        return url;
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        return null;
      }
    },
    [token]
  );

  const revokeSupport = useCallback(
    async (grantId: string) => {
      if (!token) return;
      try {
        await fetch(`${CONVEX_SITE_URL}/support/grant/revoke`, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
          body: JSON.stringify({ grantId }),
        });
        void reload();
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [token, reload]
  );

  return {
    peers,
    rules,
    supporting,
    supportedBy,
    loading,
    error,
    setError,
    reload,
    saveNodeConfig,
    saveRules,
    createSupportLink,
    revokeSupport,
  };
}
