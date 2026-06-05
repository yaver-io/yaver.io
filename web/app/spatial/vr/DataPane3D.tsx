"use client";

/**
 * DataPane3D — a generic floating data panel for the spatial scene.
 *
 * Renders a title + label/value rows + an optional sparkline to an offscreen
 * 2D canvas, wraps it in a THREE.CanvasTexture, and maps it onto a plane —
 * the same canvas-texture pattern as TerminalPane3D (WebGL only, no HTML in
 * immersive-vr per the spatial constraints research). Reusable for any
 * "company at a glance" domain (fleet, devices, mesh, machine-edge, cashflow).
 */

import { useEffect, useMemo, useRef, useState } from "react";
import { useFrame } from "@react-three/fiber";
import * as THREE from "three";

const CANVAS_W = 512;
const CANVAS_H = 320;

export interface DataPaneRow {
  label: string;
  value: string;
  color?: string;
}

export function DataPane3D({
  title,
  accent,
  rows,
  spark,
  headline,
  position,
  rotationY,
  width,
  height,
  focused,
  onFocus,
}: {
  title: string;
  accent: string;
  rows: DataPaneRow[];
  spark?: number[];
  headline?: string;
  position: [number, number, number];
  rotationY: number;
  width: number;
  height: number;
  focused: boolean;
  onFocus: () => void;
}) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const texRef = useRef<THREE.CanvasTexture | null>(null);
  const dirtyRef = useRef(true);
  const lastTexRef = useRef(0);

  // Create the canvas + texture once.
  useEffect(() => {
    const c = document.createElement("canvas");
    c.width = CANVAS_W;
    c.height = CANVAS_H;
    canvasRef.current = c;
    const t = new THREE.CanvasTexture(c);
    t.colorSpace = THREE.SRGBColorSpace;
    t.minFilter = THREE.LinearFilter;
    t.magFilter = THREE.LinearFilter;
    texRef.current = t;
    return () => {
      t.dispose();
    };
  }, []);

  // Repaint whenever the data changes.
  const dataKey = useMemo(
    () => JSON.stringify({ title, headline, rows, spark, accent }),
    [title, headline, rows, spark, accent],
  );
  useEffect(() => {
    const c = canvasRef.current;
    if (!c) return;
    const ctx = c.getContext("2d");
    if (!ctx) return;

    ctx.fillStyle = "#0a0e16";
    ctx.fillRect(0, 0, CANVAS_W, CANVAS_H);

    // accent top edge
    ctx.fillStyle = accent;
    ctx.fillRect(0, 0, CANVAS_W, 6);

    // title
    ctx.fillStyle = "#f1f5f9";
    ctx.font = "bold 30px ui-monospace, 'JetBrains Mono', Menlo, monospace";
    ctx.fillText(title, 26, 56);

    // headline
    if (headline) {
      ctx.fillStyle = "#94a3b8";
      ctx.font = "18px ui-monospace, Menlo, monospace";
      ctx.fillText(headline, 26, 86);
    }

    // rows
    ctx.font = "22px ui-monospace, Menlo, monospace";
    let y = 138;
    for (const r of rows) {
      ctx.fillStyle = "#64748b";
      ctx.fillText(r.label, 26, y);
      ctx.fillStyle = r.color ?? "#e5e7eb";
      ctx.textAlign = "right";
      ctx.fillText(r.value, CANVAS_W - 26, y);
      ctx.textAlign = "left";
      y += 34;
    }

    // sparkline
    if (spark && spark.length) {
      const baseY = CANVAS_H - 26;
      const maxH = 48;
      const gap = 4;
      const bw = (CANVAS_W - 52 - gap * (spark.length - 1)) / spark.length;
      spark.forEach((h, i) => {
        const x = 26 + i * (bw + gap);
        const bh = 3 + h * maxH;
        ctx.fillStyle = accent;
        ctx.globalAlpha = 0.35 + h * 0.55;
        ctx.fillRect(x, baseY - bh, bw, bh);
      });
      ctx.globalAlpha = 1;
    }

    dirtyRef.current = true;
  }, [dataKey, title, headline, rows, spark, accent]);

  // Throttle texture uploads to ~15Hz (Quest frame budget) and only when dirty.
  useFrame(() => {
    const tex = texRef.current;
    if (!tex || !dirtyRef.current) return;
    const now = performance.now();
    if (now - lastTexRef.current < 66) return;
    lastTexRef.current = now;
    tex.needsUpdate = true;
    dirtyRef.current = false;
  });

  const [tex, setTex] = useState<THREE.CanvasTexture | null>(null);
  useEffect(() => setTex(texRef.current), []);

  return (
    <group position={position} rotation={[0, rotationY, 0]}>
      {/* focus / frame backing */}
      <mesh position={[0, 0, -0.001]}>
        <planeGeometry args={[width + 0.04, height + 0.04]} />
        <meshBasicMaterial color={focused ? accent : "#1f2937"} />
      </mesh>
      {/* main content plane */}
      <mesh
        onClick={(e) => {
          e.stopPropagation();
          onFocus();
        }}
      >
        <planeGeometry args={[width, height]} />
        {tex ? (
          <meshBasicMaterial map={tex} toneMapped={false} />
        ) : (
          <meshBasicMaterial color="#0a0e16" />
        )}
      </mesh>
    </group>
  );
}
