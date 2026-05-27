"use client";

/**
 * TerminalPane3D — one floating terminal quad inside the immersive-vr
 * scene. xterm.js renders into an offscreen canvas, we wrap that canvas
 * in a THREE.CanvasTexture and apply it to a planar mesh.
 *
 * Why this dance: per spatial constraints research (May 2026), WebXR
 * DOM Overlay is `immersive-ar` only — you cannot put HTML <div>s
 * inside `immersive-vr`. WebGL textures are the only path. xterm.js
 * happily draws to a 2D canvas; we just sample that canvas.
 *
 * Updates: each ANSI write hits the canvas. We tick the texture in a
 * useFrame so it stays current at the headset's frame rate.
 */

import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";
import type { BridgeConfig, Task } from "../useAgentBridge";

interface Props {
  task: Task;
  cfg: BridgeConfig;
  position: [number, number, number];
  rotationY: number;
  width: number;   // world units (m)
  height: number;  // world units (m)
  focused: boolean;
  onFocus: () => void;
}

// Pixel dimensions of the offscreen canvas. We want roughly 80 cols
// at 8px char width = 640px wide. Height matches aspect ratio.
const CANVAS_W = 640;
const CANVAS_H = 400;

export function TerminalPane3D({ task, cfg, position, rotationY, width, height, focused, onFocus }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<any>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const writtenLinesRef = useRef<number>(0);
  const [textureKey, setTextureKey] = useState(0);

  // Mount xterm into a hidden DOM container. We sample its inner
  // canvas via querySelector — xterm v6 keeps the visible <canvas>
  // as the first child of the .xterm-screen element.
  useEffect(() => {
    if (typeof document === "undefined") return;
    let term: any;
    let fit: any;
    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);

      // Hidden host — kept in DOM so xterm can size + render, but
      // visually offscreen since the VR scene is the only consumer.
      const host = document.createElement("div");
      host.style.cssText = `position:absolute;left:-99999px;top:-99999px;width:${CANVAS_W}px;height:${CANVAS_H}px;`;
      document.body.appendChild(host);
      containerRef.current = host;

      term = new Terminal({
        fontFamily: "ui-monospace, 'JetBrains Mono', Menlo, monospace",
        fontSize: 12,
        theme: { background: "#0a0e16", foreground: "#e5e7eb" },
        cols: 80,
        rows: 25,
        cursorBlink: false,
        disableStdin: true,
        convertEol: true,
        scrollback: 4000,
      });
      termRef.current = term;
      fit = new FitAddon();
      term.loadAddon(fit);
      term.open(host);
      try { fit.fit(); } catch {}

      // Find the xterm-rendered canvas. xterm v6 produces multiple
      // canvases (text, link, cursor); we grab the parent screen
      // element and rasterize the whole stack onto a single canvas
      // below — simplest robust path.
      const screenEl = host.querySelector(".xterm-screen") as HTMLElement | null;
      if (!screenEl) return;
      // Composite canvas we'll texture from
      const composite = document.createElement("canvas");
      composite.width = CANVAS_W;
      composite.height = CANVAS_H;
      canvasRef.current = composite;
      // First paint
      compositeXtermCanvases(screenEl, composite);
      setTextureKey((k) => k + 1);
    })();
    return () => {
      try { termRef.current?.dispose?.(); } catch {}
      try { containerRef.current?.remove(); } catch {}
    };
  }, []);

  // Poll task output → push new lines into xterm + redraw composite.
  useEffect(() => {
    let cancelled = false;
    const tick = async () => {
      try {
        const res = await fetch(`${cfg.agentUrl}/tasks/${encodeURIComponent(task.id)}`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!res.ok) return;
        const t = (await res.json()) as Task;
        if (cancelled || !termRef.current || !canvasRef.current || !containerRef.current) return;
        const lines = Array.isArray(t.output) ? t.output : [];
        for (let i = writtenLinesRef.current; i < lines.length; i++) {
          termRef.current.writeln(lines[i]);
        }
        writtenLinesRef.current = lines.length;
        const screenEl = containerRef.current.querySelector(".xterm-screen") as HTMLElement | null;
        if (screenEl) {
          compositeXtermCanvases(screenEl, canvasRef.current);
          setTextureKey((k) => k + 1);
        }
      } catch {}
    };
    void tick();
    const i = window.setInterval(tick, 1500);
    return () => { cancelled = true; window.clearInterval(i); };
  }, [cfg, task.id]);

  // The texture is regenerated when textureKey ticks — keyed so React
  // disposes the old one cleanly.
  const texture = useMemo(() => {
    if (!canvasRef.current) return null;
    const t = new THREE.CanvasTexture(canvasRef.current);
    t.colorSpace = THREE.SRGBColorSpace;
    t.minFilter = THREE.LinearFilter;
    t.magFilter = THREE.LinearFilter;
    return t;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [textureKey]);

  // Tick texture.needsUpdate at ~15Hz instead of every frame. Quest 3
  // gives us 11.1ms/frame at 90Hz; uploading a 640x400 canvas to the
  // GPU every frame costs ~1.5ms per pane × 3 panes = 4.5ms wasted on
  // content that only changes when the agent emits a new output line.
  // 15Hz feels indistinguishable for a terminal (the cursor blink is
  // 600ms anyway) and frees the budget for reprojection.
  const lastTexUpdateRef = useRef<number>(0);
  useFrame(() => {
    if (!texture) return;
    const now = performance.now();
    if (now - lastTexUpdateRef.current < 66) return; // ~15 FPS
    lastTexUpdateRef.current = now;
    texture.needsUpdate = true;
  });

  const borderColor = focused ? "#10b981" : "#1f2937";
  const statusColor = statusColorFor(task.status);

  return (
    <group position={position} rotation={[0, rotationY, 0]}>
      {/* Frame / glow border — separate slightly larger plane behind */}
      <mesh position={[0, 0, -0.001]}>
        <planeGeometry args={[width + 0.04, height + 0.04]} />
        <meshBasicMaterial color={borderColor} />
      </mesh>
      {/* Header bar */}
      <mesh position={[0, height / 2 + 0.04, 0]}>
        <planeGeometry args={[width, 0.06]} />
        <meshBasicMaterial color={"#111827"} />
      </mesh>
      {/* Status dot on header */}
      <mesh position={[-width / 2 + 0.05, height / 2 + 0.04, 0.001]}>
        <circleGeometry args={[0.018, 16]} />
        <meshBasicMaterial color={statusColor} />
      </mesh>
      {/* Main terminal plane — sample the xterm canvas */}
      <mesh
        onClick={(e) => { e.stopPropagation(); onFocus(); }}
        onPointerDown={(e) => { e.stopPropagation(); onFocus(); }}
      >
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

function compositeXtermCanvases(screenEl: HTMLElement, dest: HTMLCanvasElement): void {
  const ctx = dest.getContext("2d");
  if (!ctx) return;
  ctx.fillStyle = "#0a0e16";
  ctx.fillRect(0, 0, dest.width, dest.height);
  const canvases = screenEl.querySelectorAll("canvas");
  canvases.forEach((c) => {
    try {
      ctx.drawImage(c as HTMLCanvasElement, 0, 0, dest.width, dest.height);
    } catch {
      // Cross-origin or detached canvases — skip silently
    }
  });
}

function statusColorFor(status: string): string {
  switch (status) {
    case "running": return "#10b981";
    case "queued": return "#94a3b8";
    case "review": return "#f59e0b";
    case "completed": return "#3b82f6";
    case "failed": return "#ef4444";
    case "stopped": return "#6b7280";
    default: return "#6b7280";
  }
}

export default TerminalPane3D;
