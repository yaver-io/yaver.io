"use client";

/**
 * AppScreenPlane3D — floating 3D plane in the VR scene that shows the
 * live screen of the user's running guest app. So when the user says
 * "launch sfmg" via voice, not only does the phone load the app — the
 * VR-wearer SEES sfmg come up live on a quad next to their terminal
 * panes, same way they'd watch it on the phone.
 *
 * Data source: the agent's existing /vibing/preview pipeline.
 *   GET  /vibing/preview/status            — list active sessions
 *   POST /vibing/preview/snapshot {project} — force capture, returns hash
 *   GET  /vibing/preview/frames/:hash      — image bytes
 *
 * Polls /status every 3s. When a session exists, pulls a snapshot and
 * maps it onto a THREE.CanvasTexture. Auto-hides when no session has
 * been active for >8s.
 *
 * Per spatial constraints (project_spatial_constraints_2026): WebGL
 * only — no HTML <div> inside immersive-vr.
 */

import { useEffect, useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { BridgeConfig } from "../useAgentBridge";

interface Props {
  cfg: BridgeConfig;
  /** World-space anchor; default places the plane to the right of
   *  the central terminal pane. */
  position?: [number, number, number];
  rotationY?: number;
  /** Plane size in meters. Defaults match the terminal panes for
   *  visual consistency. */
  width?: number;
  height?: number;
}

interface PreviewSession {
  id: string;
  project: string;
  frameCount?: number;
  lastFrame?: string;
}

/** Compose the snapshot polling + texture loading lifecycle. */
function useLatestPreviewFrame(cfg: BridgeConfig): {
  texture: THREE.Texture | null;
  project: string;
  active: boolean;
} {
  const [texture, setTexture] = useState<THREE.Texture | null>(null);
  const [project, setProject] = useState("");
  const [active, setActive] = useState(false);
  const lastHashRef = useRef<string>("");
  const lastActiveAtRef = useRef<number>(0);

  useEffect(() => {
    let cancelled = false;

    const tick = async () => {
      try {
        const statusRes = await fetch(`${cfg.agentUrl}/vibing/preview/status`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!statusRes.ok) return;
        const { sessions } = (await statusRes.json()) as { sessions: PreviewSession[] };
        if (cancelled) return;

        const session = (sessions ?? []).find((s) => s.project && (s.frameCount ?? 0) > 0)
          ?? (sessions ?? []).find((s) => !!s.project);

        if (!session) {
          // No active session — hide after 8s grace period so a brief
          // session restart doesn't make the plane disappear/reappear
          if (Date.now() - lastActiveAtRef.current > 8000) setActive(false);
          return;
        }

        setProject(session.project);
        lastActiveAtRef.current = Date.now();
        setActive(true);

        // Force a snapshot (cheap if the session is already capturing on its own cadence).
        const snapRes = await fetch(`${cfg.agentUrl}/vibing/preview/snapshot`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${cfg.token}`,
          },
          body: JSON.stringify({ project: session.project }),
        });
        if (!snapRes.ok) return;
        const snap = (await snapRes.json()) as { hash?: string };
        if (cancelled || !snap.hash) return;
        if (snap.hash === lastHashRef.current) return;
        lastHashRef.current = snap.hash;

        // Fetch frame bytes + create an Image element → CanvasTexture.
        // We can't use TextureLoader directly because it doesn't carry
        // our Bearer header; manual fetch + blob URL is the fix.
        const frameRes = await fetch(`${cfg.agentUrl}/vibing/preview/frames/${encodeURIComponent(snap.hash)}`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!frameRes.ok || cancelled) return;
        const blob = await frameRes.blob();
        const url = URL.createObjectURL(blob);
        const img = new window.Image();
        img.onload = () => {
          if (cancelled) {
            URL.revokeObjectURL(url);
            return;
          }
          const tex = new THREE.Texture(img);
          tex.colorSpace = THREE.SRGBColorSpace;
          tex.minFilter = THREE.LinearFilter;
          tex.magFilter = THREE.LinearFilter;
          tex.needsUpdate = true;
          setTexture((prev) => {
            // dispose the previous texture to avoid GPU memory leaks
            prev?.dispose();
            return tex;
          });
          URL.revokeObjectURL(url);
        };
        img.onerror = () => URL.revokeObjectURL(url);
        img.src = url;
      } catch {
        /* swallow — polling resumes next tick */
      }
    };

    void tick();
    const id = window.setInterval(tick, 3000);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [cfg]);

  return { texture, project, active };
}

export function AppScreenPlane3D({
  cfg,
  position = [1.45, 1.55, -0.6],  // right of the central pane, slightly forward
  rotationY = -Math.PI / 4.5,     // angled back toward the user
  width = 0.95,
  height = 0.55,
}: Props) {
  const { texture, project, active } = useLatestPreviewFrame(cfg);
  const headerCanvasRef = useRef<HTMLCanvasElement | null>(null);
  const headerTexRef = useRef<THREE.CanvasTexture | null>(null);
  const meshRef = useRef<THREE.Mesh>(null);

  // Header label texture — written once project name is known.
  useEffect(() => {
    if (typeof document === "undefined") return;
    if (!headerCanvasRef.current) {
      const c = document.createElement("canvas");
      c.width = 512; c.height = 48;
      headerCanvasRef.current = c;
      const t = new THREE.CanvasTexture(c);
      t.colorSpace = THREE.SRGBColorSpace;
      headerTexRef.current = t;
    }
    const c = headerCanvasRef.current;
    if (!c || !headerTexRef.current) return;
    const ctx = c.getContext("2d");
    if (!ctx) return;
    ctx.fillStyle = "rgba(8,12,20,0.85)";
    ctx.fillRect(0, 0, c.width, c.height);
    ctx.font = "18px ui-monospace, Menlo, monospace";
    ctx.textBaseline = "middle";
    ctx.fillStyle = "#10b981";
    ctx.fillText(`● ${project || "preview"}`, 16, c.height / 2);
    ctx.fillStyle = "#9ca3af";
    const right = "live";
    ctx.fillText(right, c.width - 16 - ctx.measureText(right).width, c.height / 2);
    headerTexRef.current.needsUpdate = true;
  }, [project]);

  // Subtle pulse to draw the eye on the first paint after a launch.
  const settleStartRef = useRef<number>(Date.now());
  useEffect(() => { settleStartRef.current = Date.now(); }, [project]);
  useFrame(() => {
    if (!meshRef.current) return;
    const t = (Date.now() - settleStartRef.current) / 1000;
    const pulse = t < 1.2 ? 1.0 + Math.sin(t * Math.PI * 3) * 0.04 * (1 - t / 1.2) : 1.0;
    meshRef.current.scale.setScalar(pulse);
  });

  if (!active) return null;

  return (
    <group position={position} rotation={[0, rotationY, 0]}>
      {/* Glow border */}
      <mesh position={[0, 0, -0.002]}>
        <planeGeometry args={[width + 0.04, height + 0.04]} />
        <meshBasicMaterial color={"#10b981"} />
      </mesh>
      {/* Header bar */}
      <mesh position={[0, height / 2 + 0.04, 0]}>
        <planeGeometry args={[width, 0.06]} />
        {headerTexRef.current ? (
          <meshBasicMaterial map={headerTexRef.current} transparent toneMapped={false} />
        ) : (
          <meshBasicMaterial color={"#0a0e16"} />
        )}
      </mesh>
      {/* Live screen plane */}
      <mesh ref={meshRef}>
        <planeGeometry args={[width, height]} />
        {texture ? (
          <meshBasicMaterial map={texture} toneMapped={false} />
        ) : (
          <meshBasicMaterial color={"#0a0e16"} />
        )}
      </mesh>
    </group>
  );
}

export default AppScreenPlane3D;
