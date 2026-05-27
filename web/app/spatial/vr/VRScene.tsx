"use client";

/**
 * VRScene — the immersive-vr experience. Three floating terminal quads
 * arranged in a horizontal arc at 1.5m radius, one centered, two
 * flanking at ±35°. A tmux-style status strip floats below.
 *
 * Reproduces the MacBook+tmux feeling in VR: same monospace terminals,
 * same session strip, same density — just positioned in 3D space
 * around the user instead of squeezed into one screen.
 *
 * Per spatial constraints research (May 2026): WebGL only, no HTML.
 */

import { Canvas, useFrame } from "@react-three/fiber";
import { XR, XROrigin, createXRStore } from "@react-three/xr";
import { useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";
import type { BridgeConfig, Task, VoiceController } from "../useAgentBridge";
import { useGlassPCSessions } from "../useAgentBridge";
import { TerminalPane3D } from "./TerminalPane3D";
import { VoiceOrb3D } from "./VoiceOrb3D";
import { AppScreenPlane3D } from "./AppScreenPlane3D";
import { RemoteWindow3D } from "./RemoteWindow3D";

// Single XR store shared across the page so the "Enter VR" button
// in page.tsx can trigger session entry without prop-drilling.
export const vrStore = createXRStore({
  emulate: false,
  // We want session-level mic access for the voice orb. Note this
  // permission is granted on the 2D page before requestSession() —
  // it carries over into immersive-vr cleanly (per the WebXR camera-
  // access GH issue #87 conclusion).
});

interface Props {
  cfg: BridgeConfig;
  tasks: Task[];
  voice: VoiceController;
}

export function VRScene({ cfg, tasks, voice }: Props) {
  return (
    <Canvas
      camera={{ position: [0, 1.6, 0], fov: 75 }}
      // Hidden behind the 2D /spatial UI on the flat page (z-index: -1,
      // transparent, pointer-events disabled). When user clicks Enter
      // VR, the WebXR session takes over the GL context for the
      // headset — the on-page canvas becomes irrelevant. This keeps
      // the 2D view clean for users who can't or don't want to enter
      // immersive-vr.
      style={{
        position: "fixed",
        inset: 0,
        zIndex: -1,
        pointerEvents: "none",
        background: "transparent",
      }}
      gl={{ antialias: true, powerPreference: "high-performance", alpha: true }}
    >
      <XR store={vrStore}>
        <XROrigin position={[0, 0, 0]} />
        {/* Ambient + a subtle key light. Terminal panes use MeshBasic
            so light isn't required for them; but the status pill +
            future ground grid catch this softly. */}
        <ambientLight intensity={0.6} />
        <directionalLight position={[2, 4, 2]} intensity={0.4} />

        {/* Subtle ground anchor — gives the user a spatial reference
            so the floating panes don't induce vertigo. */}
        <GroundReference />

        <PaneArc cfg={cfg} tasks={tasks} />

        {/* Live guest-app screen — only mounts when a vibe-preview
            session is active. Sits to the right of the terminal arc,
            angled back toward the user. */}
        <AppScreenPlane3D cfg={cfg} />

        {/* "PC UI in glasses": one floating browser quad per active
            glass_pc_open session. Arranged in a row above the
            terminal arc so they don't fight for the user's gaze. */}
        <RemoteWindowStack cfg={cfg} />

        <VoiceOrb3D voice={voice} />

        <StatusStrip tasks={tasks} />
      </XR>
    </Canvas>
  );
}

function GroundReference() {
  return (
    <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, -0.01, 0]}>
      <ringGeometry args={[1.4, 1.5, 64]} />
      <meshBasicMaterial color="#1f2937" transparent opacity={0.5} />
    </mesh>
  );
}

function PaneArc({ cfg, tasks }: { cfg: BridgeConfig; tasks: Task[] }) {
  const [focusIdx, setFocusIdx] = useState(1); // middle pane by default

  // Pick 3 most-relevant tasks: running first, then review/queued,
  // then completed. Stable order by id for visual continuity across
  // polls (no flicker on /tasks refresh).
  const visible = useMemo(() => {
    const score = (t: Task) => {
      switch (t.status) {
        case "running": return 0;
        case "review": return 1;
        case "queued": return 2;
        case "completed": return 3;
        case "failed": return 4;
        default: return 5;
      }
    };
    const sorted = [...tasks].sort((a, b) => {
      const d = score(a) - score(b);
      return d !== 0 ? d : a.id.localeCompare(b.id);
    });
    return sorted.slice(0, 3);
  }, [tasks]);

  // Arrange three panes on a 1.5m radius arc at eye height (~1.6m).
  // Center pane straight ahead, flankers at ±35°.
  const RADIUS = 1.5;
  const EYE_HEIGHT = 1.6;
  const PANE_W = 1.05;  // ~80cm wide in headset's perceived FOV
  const PANE_H = 0.65;
  const ANGLES = [-Math.PI / 5.1, 0, Math.PI / 5.1]; // ~±35°

  return (
    <>
      {visible.map((task, i) => {
        const a = ANGLES[i] ?? 0;
        const x = Math.sin(a) * RADIUS;
        const z = -Math.cos(a) * RADIUS;
        return (
          <TerminalPane3D
            key={task.id}
            task={task}
            cfg={cfg}
            position={[x, EYE_HEIGHT, z]}
            rotationY={-a}
            width={PANE_W}
            height={PANE_H}
            focused={i === focusIdx}
            onFocus={() => setFocusIdx(i)}
          />
        );
      })}
      {visible.length === 0 && <EmptyHint />}
    </>
  );
}

function RemoteWindowStack({ cfg }: { cfg: BridgeConfig }) {
  const { sessions } = useGlassPCSessions(cfg);
  const [focusId, setFocusId] = useState<string | null>(null);

  // Layout: arc them above the terminal panes — 2.3m height, 1.8m
  // radius, ±25° spread, max 3 visible. Less aggressive curvature
  // than the terminals so the user can read text at the edges.
  const visible = sessions.slice(0, 3);
  const RADIUS = 1.8;
  const Y = 2.35;
  const PANE_W = 1.1;
  const PANE_H = 0.7;
  const angles = visible.length === 1
    ? [0]
    : visible.length === 2
    ? [-0.25, 0.25]
    : [-0.45, 0, 0.45];

  return (
    <>
      {visible.map((s, i) => {
        const a = angles[i] ?? 0;
        const x = Math.sin(a) * RADIUS;
        const z = -Math.cos(a) * RADIUS;
        return (
          <RemoteWindow3D
            key={s.id}
            cfg={cfg}
            sessionId={s.id}
            deviceId={s.deviceId}
            url={s.url}
            title={s.title}
            position={[x, Y, z]}
            rotationY={-a}
            width={PANE_W}
            height={PANE_H}
            focused={focusId === s.id || (focusId === null && i === 0)}
            onFocus={() => setFocusId(s.id)}
          />
        );
      })}
    </>
  );
}

function EmptyHint() {
  // Spin a slowly rotating "no sessions" placeholder so user knows
  // the scene is alive even with nothing running.
  const ref = useRef<THREE.Mesh>(null);
  useFrame((_, dt) => { if (ref.current) ref.current.rotation.y += dt * 0.2; });
  return (
    <mesh ref={ref} position={[0, 1.6, -1.5]}>
      <torusKnotGeometry args={[0.12, 0.03, 64, 8]} />
      <meshStandardMaterial color="#1f2937" metalness={0.4} roughness={0.6} />
    </mesh>
  );
}

function StatusStrip({ tasks }: { tasks: Task[] }) {
  // Floating tmux-style status pill ~1m in front, just below center.
  // Pure 3D primitives — we paint a canvas texture with the tmux
  // ANSI-ish status text and apply it.
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const texRef = useRef<THREE.CanvasTexture | null>(null);
  const [, force] = useState(0);

  useEffect(() => {
    const c = document.createElement("canvas");
    c.width = 1024;
    c.height = 64;
    canvasRef.current = c;
    const t = new THREE.CanvasTexture(c);
    t.colorSpace = THREE.SRGBColorSpace;
    texRef.current = t;
    return () => { t.dispose(); };
  }, []);

  useEffect(() => {
    const c = canvasRef.current;
    if (!c) return;
    const ctx = c.getContext("2d");
    if (!ctx) return;
    ctx.fillStyle = "rgba(8,12,20,0.85)";
    ctx.fillRect(0, 0, c.width, c.height);
    ctx.font = "20px ui-monospace, Menlo, monospace";
    ctx.textBaseline = "middle";
    // tmux-ish prefix: [0] 0:yaver* 1:talos- 2:emlaq-
    const running = tasks.filter((t) => t.status === "running").length;
    const total = tasks.length;
    let x = 14;
    const y = c.height / 2;
    ctx.fillStyle = "#10b981";
    ctx.fillText(`[yaver]`, x, y);
    x += ctx.measureText(`[yaver] `).width;
    ctx.fillStyle = "#e5e7eb";
    tasks.slice(0, 4).forEach((t, i) => {
      const focused = t.status === "running" ? "*" : "-";
      const label = ` ${i}:${shortTitle(t.title, 12)}${focused}`;
      ctx.fillStyle = t.status === "running" ? "#3b82f6" : t.status === "review" ? "#f59e0b" : "#6b7280";
      ctx.fillText(label, x, y);
      x += ctx.measureText(label).width;
    });
    // Right-aligned summary
    const right = `${running}/${total} active`;
    ctx.fillStyle = "#9ca3af";
    ctx.fillText(right, c.width - 14 - ctx.measureText(right).width, y);
    if (texRef.current) texRef.current.needsUpdate = true;
    force((n) => n + 1);
  }, [tasks]);

  return (
    <mesh position={[0, 1.05, -1.5]} rotation={[-0.15, 0, 0]}>
      <planeGeometry args={[1.6, 0.1]} />
      {texRef.current ? (
        <meshBasicMaterial map={texRef.current} transparent toneMapped={false} />
      ) : (
        <meshBasicMaterial color="#0a0e16" />
      )}
    </mesh>
  );
}

function shortTitle(s: string, max: number): string {
  const t = (s ?? "").trim();
  if (t.length <= max) return t || "(task)";
  return t.slice(0, max - 1) + "…";
}

export default VRScene;
