"use client";

/**
 * VoiceOrb3D — voice mic affordance INSIDE the immersive-vr scene.
 *
 * Without this, voice doesn't work in VR: the 2D DOM <button> on the
 * flat /spatial page is invisible to the headset. This component is
 * the same affordance, ray-pointable via Quest controllers / Vision
 * Pro gaze+pinch / hand tracking.
 *
 * Floating sphere at ~0.8m height, 1m in front of the user, slightly
 * below the terminal arc. Tints green/red/blue/purple/amber for
 * idle/recording/connecting/thinking/speaking — same vocabulary as
 * the 2D orb so the UX is consistent across surfaces.
 *
 * Tap to toggle session start/stop. Pulses scale (1.0 → 1.15) at
 * audio-amplitude approximation while recording.
 */

import { useFrame } from "@react-three/fiber";
import { useEffect, useMemo, useRef, useState } from "react";
import * as THREE from "three";
import type { VoiceController, VoiceStatus } from "../useAgentBridge";

const COLOR_FOR_STATUS: Record<VoiceStatus, string> = {
  idle: "#10b981",
  connecting: "#3b82f6",
  recording: "#ef4444",
  uploading: "#3b82f6",
  thinking: "#8b5cf6",
  speaking: "#f59e0b",
  error: "#6b7280",
};

const LABEL_FOR_STATUS: Record<VoiceStatus, string> = {
  idle: "Tap to speak",
  connecting: "Connecting…",
  recording: "Listening…",
  uploading: "Sending…",
  thinking: "Thinking…",
  speaking: "Reading back…",
  error: "Try again",
};

export function VoiceOrb3D({ voice }: { voice: VoiceController }) {
  const meshRef = useRef<THREE.Mesh>(null);
  const matRef = useRef<THREE.MeshStandardMaterial>(null);
  const targetScale = useRef(1.0);
  const pulse = useRef(1.0);

  const status = voice.state.status as VoiceStatus;
  const color = COLOR_FOR_STATUS[status] ?? "#6b7280";

  // Smoothly transition material color when status changes.
  useEffect(() => {
    if (matRef.current) {
      matRef.current.color.set(color);
      matRef.current.emissive.set(color);
      matRef.current.emissiveIntensity = status === "idle" ? 0.3 : 0.6;
    }
  }, [color, status]);

  // Pulse while recording or speaking.
  useFrame((_, dt) => {
    const wantsPulse = status === "recording" || status === "speaking";
    targetScale.current = wantsPulse ? 1.15 : 1.0;
    // Critically damped lerp toward target
    pulse.current += (targetScale.current - pulse.current) * Math.min(1, dt * 6);
    if (meshRef.current) {
      // Add a tiny sine wobble during recording so it feels alive
      const wobble = wantsPulse ? Math.sin(performance.now() / 280) * 0.04 : 0;
      const s = pulse.current + wobble;
      meshRef.current.scale.setScalar(s);
    }
  });

  // Floating label below the orb showing status / transcript.
  const labelCanvas = useMemo(() => {
    if (typeof document === "undefined") return null;
    const c = document.createElement("canvas");
    c.width = 1024;
    c.height = 96;
    return c;
  }, []);
  const labelTex = useMemo(() => {
    if (!labelCanvas) return null;
    const t = new THREE.CanvasTexture(labelCanvas);
    t.colorSpace = THREE.SRGBColorSpace;
    return t;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [labelCanvas]);

  // Repaint label whenever transcript / status / errorMsg changes.
  const transcript = voice.state.transcript;
  const errorMsg = voice.state.errorMsg;
  useEffect(() => {
    if (!labelCanvas || !labelTex) return;
    const ctx = labelCanvas.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, labelCanvas.width, labelCanvas.height);
    ctx.font = "28px ui-monospace, Menlo, monospace";
    ctx.textAlign = "center";
    ctx.textBaseline = "middle";
    ctx.fillStyle = errorMsg ? "#ef4444" : "#e5e7eb";
    const text = errorMsg
      ? errorMsg.slice(0, 80)
      : transcript
      ? `"${transcript.slice(0, 70)}"`
      : LABEL_FOR_STATUS[status];
    ctx.fillText(text, labelCanvas.width / 2, labelCanvas.height / 2);
    labelTex.needsUpdate = true;
  }, [transcript, errorMsg, status, labelCanvas, labelTex]);

  return (
    <group position={[0, 0.95, -1.1]}>
      <mesh
        ref={meshRef}
        onClick={(e) => {
          e.stopPropagation();
          if (status === "idle" || status === "error") void voice.start();
          else if (status === "recording") void voice.stop();
          else voice.cancel();
        }}
        onPointerDown={(e) => e.stopPropagation()}
      >
        <sphereGeometry args={[0.06, 32, 32]} />
        <meshStandardMaterial
          ref={matRef}
          color={color}
          emissive={color}
          emissiveIntensity={0.5}
          metalness={0.2}
          roughness={0.35}
        />
      </mesh>
      {/* Floating status / transcript label below the orb */}
      {labelTex && (
        <mesh position={[0, -0.12, 0]}>
          <planeGeometry args={[0.7, 0.07]} />
          <meshBasicMaterial map={labelTex} transparent toneMapped={false} />
        </mesh>
      )}
    </group>
  );
}

export default VoiceOrb3D;
