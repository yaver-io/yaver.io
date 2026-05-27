"use client";

/**
 * EnterVRButton — large "Enter VR" affordance that appears only when
 * the browser exposes WebXR `immersive-vr`. Clicking it requests the
 * session via the shared vrStore.
 *
 * On Quest Browser, click → headset enters immersive scene rendering
 * the floating terminal panes. On a desktop Chrome with no WebXR,
 * the button is hidden entirely so the page stays clean for users
 * who can't enter VR.
 */

import { useEffect, useState } from "react";
import { vrStore } from "./VRScene";

export function EnterVRButton() {
  const [available, setAvailable] = useState(false);
  const [inSession, setInSession] = useState(false);

  useEffect(() => {
    const xr = (navigator as any).xr;
    if (!xr || typeof xr.isSessionSupported !== "function") return;
    xr.isSessionSupported("immersive-vr").then((ok: boolean) => setAvailable(!!ok)).catch(() => setAvailable(false));
  }, []);

  useEffect(() => {
    const unsub = vrStore.subscribe((s: any) => setInSession(!!s.session));
    return () => unsub();
  }, []);

  if (!available) return null;

  return (
    <button
      onClick={() => {
        if (inSession) {
          vrStore.getState().session?.end();
        } else {
          vrStore.enterVR();
        }
      }}
      style={{
        position: "fixed",
        top: 16,
        right: 16,
        zIndex: 100001,
        padding: "10px 16px",
        background: inSession ? "#ef4444" : "#10b981",
        color: "#fff",
        border: "none",
        borderRadius: 8,
        fontSize: 13,
        fontWeight: 600,
        cursor: "pointer",
        boxShadow: "0 4px 12px rgba(0,0,0,0.3)",
      }}
    >
      {inSession ? "Exit VR" : "Enter VR"}
    </button>
  );
}

export default EnterVRButton;
