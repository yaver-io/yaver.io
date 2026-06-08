"use client";

// /add-device — claim a Yaver-powered device by its label QR (zero-touch /
// DPP-style). The buyer scans the QR printed on a box (Talos edge node,
// blackbox Pi, any third-party hardware) and becomes the owner; the box
// self-credentials to this account on its next boot.
//
// Two ways in: camera scan (via the browser-native BarcodeDetector — no
// dependency; falls back gracefully where unsupported) or paste the QR
// payload / enter the device id + claim secret by hand. Mirrors the agent's
// ParseProvisionQR (desktop/agent/provision.go) and the mobile flow
// (mobile/app/provision-add.tsx). Claims via POST /devices/provision-claim.

import { useCallback, useEffect, useRef, useState } from "react";
import { useAuth } from "@/lib/use-auth";
import { CONVEX_URL } from "@/lib/constants";

interface ProvisionClaim {
  deviceId: string;
  claimSecret: string;
  productId?: string;
  model?: string;
  convexSiteUrl?: string;
}

// parseProvisionQR — pure decoder for `yaver://provision/v1?...`. Returns
// null for anything that isn't a Yaver provision QR.
function parseProvisionQR(raw: string): ProvisionClaim | null {
  const s = (raw || "").trim();
  const m = s.match(/^yaver:\/\/provision(?:\/v\d+)?\?(.*)$/i);
  if (!m) return null;
  const params: Record<string, string> = {};
  for (const pair of m[1].split("&")) {
    if (!pair) continue;
    const eq = pair.indexOf("=");
    const k = eq >= 0 ? pair.slice(0, eq) : pair;
    const v = eq >= 0 ? pair.slice(eq + 1) : "";
    try {
      params[decodeURIComponent(k)] = decodeURIComponent(v.replace(/\+/g, "%20"));
    } catch {
      params[k] = v;
    }
  }
  const deviceId = (params.d || "").trim();
  const claimSecret = (params.s || "").trim();
  if (!deviceId || !claimSecret) return null;
  return {
    deviceId,
    claimSecret,
    productId: params.p || undefined,
    model: params.m || undefined,
    convexSiteUrl: params.u || undefined,
  };
}

type Status = { kind: "idle" | "claiming" | "ok" | "error"; message?: string; model?: string | null };

export default function AddDevicePage() {
  const { token, isAuthenticated, isLoading } = useAuth();
  const [payload, setPayload] = useState("");
  const [deviceId, setDeviceId] = useState("");
  const [secret, setSecret] = useState("");
  const [name, setName] = useState("");
  const [status, setStatus] = useState<Status>({ kind: "idle" });
  const [scanning, setScanning] = useState(false);
  const [scanSupported, setScanSupported] = useState(false);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const rafRef = useRef<number | null>(null);

  useEffect(() => {
    setScanSupported(typeof window !== "undefined" && "BarcodeDetector" in window);
  }, []);

  const stopScan = useCallback(() => {
    setScanning(false);
    if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
    rafRef.current = null;
    if (streamRef.current) {
      streamRef.current.getTracks().forEach((t) => t.stop());
      streamRef.current = null;
    }
  }, []);

  useEffect(() => () => stopScan(), [stopScan]);

  const onDetected = useCallback(
    (raw: string) => {
      const claim = parseProvisionQR(raw);
      if (!claim) return false;
      stopScan();
      setPayload(raw);
      setDeviceId(claim.deviceId);
      setSecret(claim.claimSecret);
      setName(claim.model ?? "");
      return true;
    },
    [stopScan],
  );

  const startScan = useCallback(async () => {
    if (!scanSupported) return;
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: "environment" },
      });
      streamRef.current = stream;
      setScanning(true);
      const video = videoRef.current;
      if (!video) return;
      video.srcObject = stream;
      await video.play();
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const Detector = (window as any).BarcodeDetector;
      const detector = new Detector({ formats: ["qr_code"] });
      const tick = async () => {
        if (!streamRef.current) return;
        try {
          const codes = await detector.detect(video);
          for (const c of codes) {
            if (onDetected(c.rawValue ?? "")) return;
          }
        } catch {
          /* transient detect error — keep scanning */
        }
        rafRef.current = requestAnimationFrame(tick);
      };
      rafRef.current = requestAnimationFrame(tick);
    } catch {
      setScanning(false);
      setStatus({ kind: "error", message: "Couldn't access the camera. Paste the QR payload instead." });
    }
  }, [scanSupported, onDetected]);

  // Keep the manual deviceId/secret fields in sync when a full payload is pasted.
  useEffect(() => {
    const claim = parseProvisionQR(payload);
    if (claim) {
      setDeviceId(claim.deviceId);
      setSecret(claim.claimSecret);
      if (!name) setName(claim.model ?? "");
    }
  }, [payload]); // eslint-disable-line react-hooks/exhaustive-deps

  const claim = useCallback(async () => {
    if (!token) {
      setStatus({ kind: "error", message: "Sign in first." });
      return;
    }
    const id = deviceId.trim();
    const sec = secret.trim();
    if (!id || !sec) {
      setStatus({ kind: "error", message: "Provide a QR payload, or a device id and claim secret." });
      return;
    }
    // Honor a self-hosted backend URL baked into the scanned QR.
    const parsed = parseProvisionQR(payload);
    const base = (parsed?.convexSiteUrl || CONVEX_URL).replace(/\/$/, "");
    setStatus({ kind: "claiming" });
    try {
      const res = await fetch(`${base}/devices/provision-claim`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify({ deviceId: id, claimSecret: sec, name: name.trim() || undefined }),
      });
      const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
      if (!res.ok) {
        setStatus({ kind: "error", message: (data.error as string) || `Claim failed (${res.status})` });
        return;
      }
      setStatus({ kind: "ok", model: (data.model as string) ?? parsed?.model ?? null });
    } catch (e) {
      setStatus({ kind: "error", message: e instanceof Error ? e.message : "Network error" });
    }
  }, [token, deviceId, secret, name, payload]);

  if (isLoading) {
    return <main className="min-h-screen flex items-center justify-center text-zinc-400">Loading…</main>;
  }
  if (!isAuthenticated) {
    return (
      <main className="min-h-screen flex flex-col items-center justify-center gap-3 p-8 text-center">
        <h1 className="text-xl font-semibold">Add a device</h1>
        <p className="text-zinc-400">Sign in to claim a device.</p>
        <a href="/auth" className="rounded-lg bg-emerald-500 px-5 py-2 font-medium text-black">Sign in</a>
      </main>
    );
  }

  return (
    <main className="mx-auto max-w-md p-6">
      <h1 className="mb-1 text-2xl font-semibold">Add a device</h1>
      <p className="mb-6 text-sm text-zinc-400">
        Scan the QR on your Yaver device&apos;s label (or paste it below) to take ownership. The
        device connects to your account automatically the next time it powers on.
      </p>

      {status.kind === "ok" ? (
        <div className="rounded-xl border border-emerald-700 bg-emerald-950/40 p-5 text-center">
          <p className="text-lg font-semibold text-emerald-700 dark:text-emerald-300">✓ You own this device</p>
          <p className="mt-1 text-sm text-zinc-300">
            {status.model ? `${status.model} ` : ""}will appear in your devices and come online on its
            next boot.
          </p>
          <a href="/dashboard" className="mt-4 inline-block rounded-lg bg-emerald-500 px-5 py-2 font-medium text-black">
            Go to devices
          </a>
        </div>
      ) : (
        <div className="space-y-4">
          {scanSupported && (
            <div>
              {scanning ? (
                <div className="overflow-hidden rounded-xl border border-zinc-700">
                  {/* eslint-disable-next-line jsx-a11y/media-has-caption */}
                  <video ref={videoRef} className="aspect-square w-full object-cover" muted playsInline />
                  <button onClick={stopScan} className="w-full bg-zinc-800 py-2 text-sm text-zinc-200">
                    Stop scanning
                  </button>
                </div>
              ) : (
                <button
                  onClick={startScan}
                  className="w-full rounded-lg bg-emerald-500 py-3 font-medium text-black"
                >
                  Scan QR with camera
                </button>
              )}
            </div>
          )}

          <div>
            <label className="mb-1 block text-xs uppercase tracking-wide text-zinc-500">QR payload</label>
            <textarea
              value={payload}
              onChange={(e) => setPayload(e.target.value)}
              placeholder="yaver://provision/v1?d=…&s=…"
              rows={2}
              className="w-full rounded-lg border border-zinc-700 bg-zinc-900 p-2 text-sm text-zinc-100"
            />
          </div>

          <details className="text-sm text-zinc-400">
            <summary className="cursor-pointer">Enter device id + secret manually</summary>
            <div className="mt-2 space-y-2">
              <input
                value={deviceId}
                onChange={(e) => setDeviceId(e.target.value)}
                placeholder="device id"
                className="w-full rounded-lg border border-zinc-700 bg-zinc-900 p-2 text-sm text-zinc-100"
              />
              <input
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                placeholder="claim secret"
                className="w-full rounded-lg border border-zinc-700 bg-zinc-900 p-2 text-sm text-zinc-100"
              />
            </div>
          </details>

          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Name this device (optional)"
            className="w-full rounded-lg border border-zinc-700 bg-zinc-900 p-2 text-sm text-zinc-100"
          />

          {status.kind === "error" && <p className="text-sm text-red-400">{status.message}</p>}

          <button
            onClick={claim}
            disabled={status.kind === "claiming"}
            className="w-full rounded-lg bg-emerald-500 py-3 font-medium text-black disabled:opacity-60"
          >
            {status.kind === "claiming" ? "Claiming…" : "Claim device"}
          </button>
        </div>
      )}
    </main>
  );
}
