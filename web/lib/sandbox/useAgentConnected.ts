"use client";

// useAgentConnected — reactive view of agentClient's connection state, so the
// Phone tab can switch between the browser-local sandbox (no agent) and the
// agent-relay view (connected) without a manual toggle.

import { useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

export function useAgentConnected(): boolean {
  const [connected, setConnected] = useState<boolean>(() => agentClient.isConnected);
  useEffect(() => {
    setConnected(agentClient.isConnected);
    const off = agentClient.on("connectionState", () => setConnected(agentClient.isConnected));
    return off;
  }, []);
  return connected;
}
