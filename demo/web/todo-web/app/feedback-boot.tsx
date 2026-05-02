"use client";

import { useEffect } from "react";

// Boot the Yaver Feedback web SDK once on the client. Lives in its
// own component so the import only runs in the browser — the SDK
// touches `window`/`document` and would crash Next's RSC pre-render
// if pulled into a server component.
export function FeedbackBoot() {
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const mod = await import("yaver-feedback-web");
        if (cancelled) return;
        const YaverFeedback = (mod as any).YaverFeedback ?? (mod as any).default;
        if (!YaverFeedback?.init) return;
        // Zero-config: floating Y button, in-app sign-in modal,
        // device picker. No agentUrl / authToken needed — the SDK
        // discovers the user's reachable Yaver agents after login.
        YaverFeedback.init({ trigger: "floating-button" });
      } catch (err) {
        // Don't break the host app if the SDK fails to load (offline
        // dev, package not yet installed, …). The console hint is
        // intentional — we want the developer to notice in dev but
        // never crash the user's todo flow.
        console.warn("[yaver-feedback] init skipped:", err);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);
  return null;
}
