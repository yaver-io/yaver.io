"use client";

// RecycleBoxDialog — Phase C web surface for the BYO host-recycle
// flow (docs/managed-cloud-host-lifecycle.md). It is a THIN trigger:
// every safety guard (no self-destruct, snapshot-before-delete,
// rollback-keep-old) lives in the agent's `recycle` ops verb. The
// UI's only job is collect inputs → dry-run → show the plan →
// require an explicit confirm → execute → show the steps.

import { useState } from "react";
import { agentClient } from "@/lib/agent-client";

interface RecycleBoxDialogProps {
  deviceId: string;
  deviceName: string;
  onClose: () => void;
}

type Phase = "form" | "preview" | "running" | "done";

export function RecycleBoxDialog({ deviceId, deviceName, onClose }: RecycleBoxDialogProps) {
  const [oldServerId, setOldServerId] = useState("");
  const [newName, setNewName] = useState(`${deviceName || "box"}-new`);
  const [plan, setPlan] = useState("starter");
  const [region, setRegion] = useState("eu");
  const [phase, setPhase] = useState<Phase>("form");
  const [steps, setSteps] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const basePayload = () => ({
    targetDeviceId: deviceId,
    oldServerId: oldServerId.trim(),
    newName: newName.trim(),
    plan,
    region,
  });

  async function run(confirm: boolean) {
    setBusy(true);
    setError(null);
    try {
      const res = await agentClient.callOps("recycle", { ...basePayload(), confirm });
      const r = res.initial || {};
      setSteps(Array.isArray(r.steps) ? r.steps : []);
      if (res.ok === false || r.error) {
        setError(r.error || res.error || "recycle failed");
        setPhase(confirm ? "done" : "form");
      } else {
        setPhase(confirm ? "done" : "preview");
      }
    } catch (e: any) {
      setError(e?.message || String(e));
      setPhase("form");
    } finally {
      setBusy(false);
    }
  }

  const canSubmit = oldServerId.trim() !== "" && newName.trim() !== "" && !busy;

  return (
    <div
      style={{
        position: "fixed", inset: 0, background: "rgba(0,0,0,0.55)",
        display: "flex", alignItems: "center", justifyContent: "center", zIndex: 1000,
      }}
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--card-bg, #15151a)", color: "var(--fg, #eaeaea)",
          padding: 24, borderRadius: 12, width: 460, maxWidth: "92vw",
          maxHeight: "88vh", overflow: "auto", border: "1px solid #2a2a33",
        }}
      >
        <h3 style={{ margin: "0 0 4px" }}>Recycle box — {deviceName || deviceId.slice(0, 8)}</h3>
        <p style={{ fontSize: 13, opacity: 0.7, margin: "0 0 16px" }}>
          Creates a fresh Hetzner box, health-checks it, then snapshots &amp;
          deletes the old one. The old box keeps serving until the new one is
          healthy — a failure rolls back with nothing destroyed. The agent
          refuses if you target the device it runs on.
        </p>

        {phase === "form" || phase === "preview" ? (
          <div style={{ display: "grid", gap: 10 }}>
            <label style={{ fontSize: 12, opacity: 0.8 }}>
              Old Hetzner server id (numeric — exact, never guessed)
              <input
                value={oldServerId}
                onChange={(e) => setOldServerId(e.target.value)}
                placeholder="e.g. 48211903"
                disabled={phase === "preview"}
                style={inp}
              />
            </label>
            <label style={{ fontSize: 12, opacity: 0.8 }}>
              New box name
              <input value={newName} onChange={(e) => setNewName(e.target.value)} disabled={phase === "preview"} style={inp} />
            </label>
            <div style={{ display: "flex", gap: 10 }}>
              <label style={{ fontSize: 12, opacity: 0.8, flex: 1 }}>
                Plan
                <select value={plan} onChange={(e) => setPlan(e.target.value)} disabled={phase === "preview"} style={inp}>
                  <option value="starter">starter</option>
                  <option value="pro">pro</option>
                  <option value="scale">scale</option>
                </select>
              </label>
              <label style={{ fontSize: 12, opacity: 0.8, flex: 1 }}>
                Region
                <select value={region} onChange={(e) => setRegion(e.target.value)} disabled={phase === "preview"} style={inp}>
                  <option value="eu">eu</option>
                  <option value="us">us</option>
                </select>
              </label>
            </div>
          </div>
        ) : null}

        {steps.length > 0 ? (
          <pre style={{
            marginTop: 14, background: "#0c0c10", padding: 12, borderRadius: 8,
            fontSize: 12, whiteSpace: "pre-wrap", maxHeight: 220, overflow: "auto",
          }}>
            {steps.join("\n")}
          </pre>
        ) : null}

        {error ? (
          <p style={{ color: "#ff6b6b", fontSize: 13, marginTop: 12 }}>{error}</p>
        ) : null}

        <div style={{ display: "flex", gap: 10, marginTop: 18, justifyContent: "flex-end" }}>
          <button onClick={onClose} style={btnGhost} disabled={busy}>
            {phase === "done" ? "Close" : "Cancel"}
          </button>
          {phase === "form" ? (
            <button onClick={() => run(false)} style={btn} disabled={!canSubmit}>
              {busy ? "Previewing…" : "Preview plan (dry-run)"}
            </button>
          ) : null}
          {phase === "preview" ? (
            <button onClick={() => run(true)} style={btnDanger} disabled={busy}>
              {busy ? "Recycling…" : "Confirm & recycle (destructive)"}
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

const inp: React.CSSProperties = {
  width: "100%", marginTop: 4, padding: "8px 10px", borderRadius: 6,
  border: "1px solid #2a2a33", background: "#0c0c10", color: "inherit", fontSize: 13,
};
const btn: React.CSSProperties = {
  padding: "8px 14px", borderRadius: 6, border: "1px solid #3a3a45",
  background: "#23232b", color: "#eaeaea", cursor: "pointer", fontSize: 13,
};
const btnGhost: React.CSSProperties = { ...btn, background: "transparent" };
const btnDanger: React.CSSProperties = { ...btn, background: "#7a1f1f", borderColor: "#9c2a2a" };
