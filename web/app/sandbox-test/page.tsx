"use client";

// sandbox-test — a no-auth dev harness for the browser-local sandbox + mini-figma
// design layer. useAuth() is provider-free (reads localStorage), so BrowserSandbox
// renders standalone here for local + browser-automation testing. Not linked from
// the app; safe to delete. The AI paths (draft/design-chat) stay disabled unless
// NEXT_PUBLIC_YAVER_GATEWAY_URL + a session token are present.

import BrowserSandbox from "@/components/dashboard/BrowserSandbox";

export default function SandboxTestPage() {
  // Dev-only: never serve this unauthenticated harness in a production build.
  if (process.env.NODE_ENV === "production") return null;
  return (
    <div className="min-h-screen bg-surface-950 p-6 text-surface-100">
      <BrowserSandbox />
    </div>
  );
}
