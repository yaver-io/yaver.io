// pairLinkHandler.tsx — global deep-link handler for pair URLs.
//
// Listens for two URL shapes:
//   - https://yaver.io/pair?...   (Universal Link / system-camera scan)
//   - yaver://pair?...            (custom-scheme deep link)
//
// On a recognised URL it routes the user to the More tab with the
// full pair URL in a `?pair=` query param. The More screen reads it,
// pre-fills the existing manual-entry pair form, and opens the pair
// modal — but never auto-submits a token. The user always taps the
// explicit "Pair" button after seeing the device summary.
//
// QR pairing is purely additive — every existing flow (manual passkey
// in-app, beacon-discovered bootstrap pairing, `yaver auth send`)
// keeps working without this handler firing.

import { useEffect } from "react";
import * as Linking from "expo-linking";
import { router } from "expo-router";
import { parsePairUrl } from "./pairDevice";

// A remote/off-LAN box that can't be silently adopted over the LAN
// beacon prints a device-code URL after `yaver auth`:
//   https://yaver.io/auth/device?code=ABCD-1234   (universal link / system-camera scan)
//   yaver://auth/device?code=ABCD-1234            (custom scheme)
// Scanning that QR used to open the browser and force a second sign-in.
// Route it into the in-app one-tap approver instead — the phone is
// already signed in (see app/approve-device.tsx).
function isApproveUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    const path = (u.pathname || "").replace(/\/+$/, "");
    if (path === "/auth/device") return true;
    // yaver://auth/device?code=… parses host="auth", pathname="/device".
    if ((u.protocol === "yaver:" || u.protocol === "yaver://") && u.host === "auth" && path === "/device") return true;
    return false;
  } catch {
    return false;
  }
}

function routePairUrl(raw: string) {
  // Device-code approve takes precedence — it's a distinct path from
  // the /pair token-submit flow and parsePairUrl wouldn't recognise it.
  if (isApproveUrl(raw)) {
    // Forward the whole URL; approve-device extracts ?code= itself so we
    // stay compatible with extra params (convex=, etc.).
    router.navigate({ pathname: "/approve-device", params: { url: raw } } as any);
    return true;
  }
  const payload = parsePairUrl(raw);
  if (!payload) return false;
  // Encode the full URL — the receiver will re-parse it and
  // therefore stays compatible with future query-param additions
  // we make to the canonical pair URL.
  router.navigate({
    pathname: "/(tabs)/more",
    params: { pair: raw },
  } as any);
  return true;
}

/**
 * Mount once near the app root. Handles cold-start (initial URL) and
 * warm-start (later Linking events) symmetrically.
 */
export function PairLinkHandler() {
  useEffect(() => {
    // Cold start: app was launched by tapping a pair link.
    let cancelled = false;
    (async () => {
      try {
        const initial = await Linking.getInitialURL();
        if (cancelled || !initial) return;
        routePairUrl(initial);
      } catch {
        // No initial URL or the platform doesn't expose one — fine,
        // the warm-start listener still catches in-session links.
      }
    })();

    // Warm start: a deep link arrived while the app was already open.
    const sub = Linking.addEventListener("url", (event) => {
      if (!event?.url) return;
      routePairUrl(event.url);
    });

    return () => {
      cancelled = true;
      sub.remove();
    };
  }, []);

  return null;
}
