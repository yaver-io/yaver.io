"use client";

/**
 * surfaceDetect — classifies the browsing surface so /spatial can
 * render the right defaults per device.
 *
 * Detection priority:
 *   1. URL override (?surface=quest|vision|raybanvr|mobile|desktop) —
 *      lets you share dedicated links + test other surfaces from a
 *      regular browser without a WebXR emulator.
 *   2. User-Agent sniffing for known headsets (Quest Browser, Vision
 *      Pro Safari, Meta Ray-Ban Display).
 *   3. Feature detection (navigator.xr immersive-vr support).
 *   4. Viewport size (small → mobile-class, large → desktop).
 *
 * Returns one of: quest | vision-pro | ray-ban-display |
 *                  mobile-webview | desktop | unknown
 */

export type Surface = "quest" | "vision-pro" | "ray-ban-display" | "mobile-webview" | "desktop" | "unknown";

export interface SurfaceInfo {
  surface: Surface;
  /** Did the user force this via ?surface= override? */
  forced: boolean;
  /** Browser ships immersive-vr. Independent of detected surface so
   *  a desktop with the WebXR emulator extension still gets the VR
   *  features lit up. */
  webxrAvailable: boolean;
  /** Viewport class — informs the 2D-fallback layout. */
  viewport: "small" | "medium" | "large";
  /** Surface-pretty name for UI display. */
  label: string;
  /** TaskViewport.surface enum string for the agent's prompt wrapper.
   *  Matches the values in desktop/agent/tasks.go::TaskViewport. */
  taskSurface: string;
}

export function detectSurface(): SurfaceInfo {
  if (typeof window === "undefined") {
    return { surface: "unknown", forced: false, webxrAvailable: false, viewport: "large", label: "Unknown", taskSurface: "" };
  }
  const url = new URL(window.location.href);
  const forced = (url.searchParams.get("surface") ?? "").toLowerCase().trim() as Surface | "";
  const ua = navigator.userAgent || "";
  const w = window.innerWidth;
  const h = window.innerHeight;

  const viewport: SurfaceInfo["viewport"] = w <= 800 ? "small" : w <= 1600 ? "medium" : "large";
  const webxrAvailable = typeof (navigator as any).xr?.isSessionSupported === "function";

  let surface: Surface = "unknown";

  if (forced && ["quest", "vision-pro", "ray-ban-display", "mobile-webview", "desktop"].includes(forced)) {
    surface = forced as Surface;
    return { surface, forced: true, webxrAvailable, viewport, label: labelFor(surface), taskSurface: taskSurfaceFor(surface) };
  }

  // UA sniffing — order matters. Vision Pro UA contains "Mac OS X"
  // so visionOS must come before generic mac/desktop detection.
  // Ray-Ban Display Web Apps run in a 600x600 chrome with a UA
  // string starting with "MetaWearables" per developers.meta.com
  // /wearables docs (May 2026) — we accept either the UA or the
  // viewport fingerprint.
  if (/OculusBrowser|Quest|Meta Quest/i.test(ua)) {
    surface = "quest";
  } else if (
    // visionOS Safari 26.x UA shape: "Mozilla/5.0 (Vision Pro;
    // CPU OS 26_0 like Mac OS X) AppleWebKit/... Safari/..."
    // Pre-26 builds said "Vision Pro" in Mobile/Touch hint.
    /Vision Pro|visionOS|Mobile.*Mac OS X.*WebKit.*RealityKit/i.test(ua)
  ) {
    surface = "vision-pro";
  } else if (
    // Wearables Device Access Toolkit Web App UA marker per Meta's
    // May 2026 dev-preview docs. We also accept the 600x600
    // viewport fingerprint since some builds strip the UA.
    /Ray-?Ban|Meta Wearables|MetaWearables|WearablesAccess/i.test(ua) ||
    (w === 600 && h === 600)
  ) {
    surface = "ray-ban-display";
  } else if (/Yaver-RN-WebView/.test(ua)) {
    // We set this UA marker in the mobile RN WebView so the same URL
    // renders a preview-tuned layout inside the SpatialPreview pane.
    surface = "mobile-webview";
  } else if (w >= 1024) {
    surface = "desktop";
  } else if (w < 800) {
    surface = "mobile-webview"; // catch-all small viewport
  }

  return {
    surface,
    forced: false,
    webxrAvailable,
    viewport,
    label: labelFor(surface),
    taskSurface: taskSurfaceFor(surface),
  };
}

function labelFor(s: Surface): string {
  switch (s) {
    case "quest": return "Meta Quest";
    case "vision-pro": return "Apple Vision Pro";
    case "ray-ban-display": return "Meta Ray-Ban Display";
    case "mobile-webview": return "Mobile WebView";
    case "desktop": return "Desktop Browser";
    case "unknown": return "Unknown";
  }
}

function taskSurfaceFor(s: Surface): string {
  switch (s) {
    case "quest": return "web-spatial-vr";
    case "vision-pro": return "web-spatial-vr";
    case "ray-ban-display": return "glasses-ray-ban";
    case "mobile-webview": return "web-spatial-hud";
    case "desktop": return "web-desktop";
    case "unknown": return "";
  }
}

/** Hook variant — re-runs on window resize so adaptive layouts stay
 *  in sync. Returns the SurfaceInfo and a manual refresh function. */
import { useEffect, useState } from "react";

export function useSurface(): SurfaceInfo {
  const [info, setInfo] = useState<SurfaceInfo>(() => detectSurface());
  useEffect(() => {
    const refresh = () => setInfo(detectSurface());
    window.addEventListener("resize", refresh);
    window.addEventListener("popstate", refresh);
    return () => {
      window.removeEventListener("resize", refresh);
      window.removeEventListener("popstate", refresh);
    };
  }, []);
  return info;
}
