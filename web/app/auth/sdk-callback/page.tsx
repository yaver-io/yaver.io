"use client";

import { Suspense, useEffect, useState } from "react";
import { useSearchParams } from "next/navigation";

/**
 * Popup callback page for the yaver-feedback-web SDK.
 *
 * The SDK opens `/api/auth/oauth/<provider>?client=sdk` in a popup window.
 * After OAuth completes, the callback redirects here with `?token=...`.
 * This page calls `window.opener.postMessage({ type: 'yaver-feedback-auth', token })`
 * and closes itself, completing the SDK login flow.
 */
function SdkCallbackHandler() {
  const searchParams = useSearchParams();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const token = searchParams.get("token");
    const errParam = searchParams.get("error");

    if (errParam) {
      setError(errParam);
      try {
        window.opener?.postMessage(
          { type: "yaver-feedback-auth", error: errParam },
          window.location.origin,
        );
      } catch {
        // opener may be blocked
      }
      return;
    }

    if (!token) {
      setError("No authentication token received.");
      return;
    }

    try {
      window.opener?.postMessage(
        { type: "yaver-feedback-auth", token },
        window.location.origin,
      );
      // Give the opener a tick to process the message before closing.
      setTimeout(() => {
        try {
          window.close();
        } catch {
          // some browsers refuse window.close on unrelated origins
        }
      }, 200);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to deliver token to opener");
    }
  }, [searchParams]);

  if (error) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center px-6">
        <div className="card max-w-sm p-8 text-center">
          <h2 className="mb-2 text-lg font-semibold text-white">Sign-in failed</h2>
          <p className="mb-6 text-sm text-surface-400">{error}</p>
          <p className="text-xs text-surface-500">You can close this window.</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-[60vh] items-center justify-center px-6">
      <div className="text-center">
        <div className="mx-auto mb-4 h-8 w-8 animate-spin rounded-full border-2 border-surface-600 border-t-yaver-500" />
        <p className="text-sm text-surface-400">Sending you back to your app…</p>
      </div>
    </div>
  );
}

export default function SdkCallbackPage() {
  return (
    <Suspense
      fallback={
        <div className="flex min-h-[60vh] items-center justify-center px-6">
          <div className="text-center">
            <div className="mx-auto mb-4 h-8 w-8 animate-spin rounded-full border-2 border-surface-600 border-t-yaver-500" />
            <p className="text-sm text-surface-400">Loading…</p>
          </div>
        </div>
      }
    >
      <SdkCallbackHandler />
    </Suspense>
  );
}
