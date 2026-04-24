"use client";

import { useSearchParams } from "next/navigation";
import { Suspense, useState, useRef } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { hasRegisteredMachine } from "@/lib/onboarding";
import { sanitizeReturnTo } from "@/lib/oauth";

function TotpContent() {
  const params = useSearchParams();
  const pendingToken = params.get("pendingToken") || "";
  const client = params.get("client") || "web";
  const returnTo = sanitizeReturnTo(params.get("return"));
  const openerOrigin = params.get("openerOrigin") || "";

  const [code, setCode] = useState("");
  const [useRecovery, setUseRecovery] = useState(false);
  const [status, setStatus] = useState<"idle" | "loading" | "error">("idle");
  const [errorMsg, setErrorMsg] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  if (!pendingToken) {
    return (
      <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
        <div className="w-full max-w-sm text-center">
          <p className="text-sm text-surface-400">Invalid session. Please sign in again.</p>
          <a href="/auth" className="mt-4 inline-block text-sm text-surface-300 underline underline-offset-2 hover:text-surface-100">
            Sign In
          </a>
        </div>
      </div>
    );
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setStatus("loading");
    setErrorMsg("");

    try {
      const res = await fetch(`${CONVEX_URL}/auth/verify-totp`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ pendingToken, code: code.trim() }),
      });

      if (!res.ok) {
        const data = await res.json().catch(() => ({ error: "Unknown error" }));
        if (res.status === 401) {
          setErrorMsg(useRecovery ? "Invalid recovery code." : "Invalid code. Try again.");
        } else if (res.status === 410) {
          setErrorMsg("Session expired. Please sign in again.");
        } else if (res.status === 429) {
          setErrorMsg("Too many attempts. Please sign in again.");
        } else {
          setErrorMsg(data.error || "Verification failed.");
        }
        setStatus("error");
        return;
      }

      const data = await res.json();
      const token = data.token;

      // Store token
      localStorage.setItem("yaver_auth_token", token);
      document.cookie = `yaver_auth_token=${token}; path=/; max-age=${60 * 60 * 24 * 30}; secure; samesite=lax`;

      // Redirect based on client type
      if (client === "desktop") {
        window.location.href = `http://127.0.0.1:19836/callback?token=${token}`;
        return;
      }

      if (client === "mobile") {
        const deepLink = `yaver://oauth-callback?token=${token}`;
        window.location.href = deepLink;
        return;
      }

      if (client === "sdk") {
        const sdkCallbackUrl = new URL("/auth/sdk-callback", window.location.origin);
        sdkCallbackUrl.searchParams.set("token", token);
        if (openerOrigin) {
          sdkCallbackUrl.searchParams.set("openerOrigin", openerOrigin);
        }
        window.location.href = sdkCallbackUrl.toString();
        return;
      }

      if (returnTo) {
        window.location.href = returnTo;
        return;
      }

      try {
        const validateRes = await fetch(`${CONVEX_URL}/auth/validate`, {
          method: "GET",
          headers: { Authorization: `Bearer ${token}` },
        });
        if (validateRes.ok) {
          const validateData = await validateRes.json();
          const raw = validateData.user ?? validateData;
          if (!(raw?.surveyCompleted ?? false)) {
            if (await hasRegisteredMachine(token)) {
              window.location.href = "/dashboard";
              return;
            }
            window.location.href = "/survey";
            return;
          }
        }
      } catch {
        // Fall through to dashboard.
      }

      window.location.href = "/dashboard";
    } catch {
      setErrorMsg("Network error. Please try again.");
      setStatus("error");
    }
  };

  const handleCodeChange = (val: string) => {
    if (useRecovery) {
      // Recovery codes are 10-char hex
      setCode(val.toLowerCase().replace(/[^a-f0-9]/g, "").slice(0, 10));
    } else {
      // TOTP is 6 digits
      setCode(val.replace(/[^0-9]/g, "").slice(0, 6));
    }
    if (status === "error") setStatus("idle");
  };

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <span className="text-2xl font-bold tracking-tight text-surface-50">
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
          <p className="mt-3 text-sm text-surface-500">
            {useRecovery ? "Enter a recovery code" : "Enter your two-factor authentication code"}
          </p>
        </div>

        {errorMsg && (
          <div className="mb-6 rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-400">
            {errorMsg}
          </div>
        )}

        <form onSubmit={handleSubmit}>
          <input
            ref={inputRef}
            type="text"
            value={code}
            onChange={(e) => handleCodeChange(e.target.value)}
            placeholder={useRecovery ? "Recovery code" : "000000"}
            autoFocus
            autoComplete="one-time-code"
            inputMode={useRecovery ? "text" : "numeric"}
            spellCheck={false}
            className={`w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-4 text-center font-mono font-bold text-surface-100 placeholder-surface-600 outline-none transition-colors focus:border-surface-500 ${
              useRecovery ? "text-lg tracking-[0.2em]" : "text-2xl tracking-[0.4em]"
            }`}
          />

          <button
            type="submit"
            disabled={status === "loading" || (useRecovery ? code.length < 10 : code.length < 6)}
            className="mt-4 w-full rounded-lg bg-surface-50 px-4 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
          >
            {status === "loading" ? "Verifying..." : "Verify"}
          </button>
        </form>

        <button
          onClick={() => {
            setUseRecovery(!useRecovery);
            setCode("");
            setErrorMsg("");
            setStatus("idle");
          }}
          className="mt-4 w-full text-center text-sm text-surface-500 transition-colors hover:text-surface-300"
        >
          {useRecovery ? "Use authenticator app instead" : "Use a recovery code"}
        </button>
      </div>
    </div>
  );
}

export default function TotpPage() {
  return (
    <Suspense fallback={<div className="flex min-h-[70vh] items-center justify-center"><span className="text-surface-500 text-sm">Loading...</span></div>}>
      <TotpContent />
    </Suspense>
  );
}
