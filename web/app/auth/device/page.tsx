"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useState, useEffect, useRef } from "react";
import { CONVEX_URL } from "@/lib/constants";

function DeviceCodeContent() {
  const params = useSearchParams();
  const prefillCode = params.get("code") || "";

  const [code, setCode] = useState(prefillCode);
  const [status, setStatus] = useState<"idle" | "loading" | "success" | "error">("idle");
  const [errorMsg, setErrorMsg] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);

  // Check if user is authenticated
  const [token, setToken] = useState<string | null>(null);
  const [checking, setChecking] = useState(true);

  useEffect(() => {
    const stored = localStorage.getItem("yaver_auth_token");
    if (stored) {
      // Validate it
      fetch(`${CONVEX_URL}/auth/validate`, {
        headers: { Authorization: `Bearer ${stored}` },
      })
        .then((res) => {
          if (res.ok) {
            setToken(stored);
          }
          setChecking(false);
        })
        .catch(() => setChecking(false));
    } else {
      setChecking(false);
    }
  }, []);

  // If prefill code and token are both available, auto-submit
  useEffect(() => {
    if (prefillCode && token && status === "idle") {
      handleAuthorize(prefillCode, token);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [prefillCode, token]);

  const authUrlFor = (provider: "apple" | "google" | "microsoft") => {
    const qs = new URLSearchParams({ client: "web" });
    const returnUrl = `/auth/device${prefillCode ? `?code=${prefillCode}` : ""}`;
    qs.set("return", returnUrl);
    return `/api/auth/oauth/${provider}?${qs.toString()}`;
  };

  const handleAuthorize = async (userCode: string, authToken: string) => {
    const cleaned = userCode.toUpperCase().replace(/[^A-Z0-9]/g, "");
    if (cleaned.length < 8) {
      setErrorMsg("Code must be 8 characters (e.g. ABCD-1234)");
      setStatus("error");
      return;
    }
    // Format as XXXX-YYYY
    const formatted = cleaned.slice(0, 4) + "-" + cleaned.slice(4, 8);

    setStatus("loading");
    setErrorMsg("");

    try {
      const res = await fetch(`${CONVEX_URL}/auth/device-code/authorize`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${authToken}`,
        },
        body: JSON.stringify({ userCode: formatted }),
      });

      if (!res.ok) {
        const data = await res.json().catch(() => ({ error: "Unknown error" }));
        if (res.status === 404) {
          setErrorMsg("Invalid code. Check the code in your terminal and try again.");
        } else if (res.status === 410) {
          setErrorMsg("This code has expired. Run 'yaver auth' again to get a new code.");
        } else if (res.status === 409) {
          setErrorMsg("This code has already been used.");
        } else {
          setErrorMsg(data.error || "Something went wrong.");
        }
        setStatus("error");
        return;
      }

      setStatus("success");
    } catch {
      setErrorMsg("Network error. Please try again.");
      setStatus("error");
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!token) return;
    handleAuthorize(code, token);
  };

  // Format input as user types (auto-insert hyphen)
  const handleCodeChange = (val: string) => {
    const stripped = val.toUpperCase().replace(/[^A-Z0-9]/g, "");
    if (stripped.length <= 4) {
      setCode(stripped);
    } else {
      setCode(stripped.slice(0, 4) + "-" + stripped.slice(4, 8));
    }
    if (status === "error") setStatus("idle");
  };

  if (checking) {
    return (
      <div className="flex min-h-[70vh] items-center justify-center">
        <span className="text-surface-500 text-sm">Loading...</span>
      </div>
    );
  }

  // Not logged in — redirect to auth with return URL
  if (!token) {
    const returnUrl = `/auth/device${prefillCode ? `?code=${prefillCode}` : ""}`;
    const authUrl = `/auth?client=web&return=${encodeURIComponent(returnUrl)}`;
    return (
      <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
        <div className="w-full max-w-md">
          <div className="mb-8 text-center">
            <span className="text-2xl font-bold tracking-tight text-surface-50">
              yaver<span className="font-normal text-surface-500">.io</span>
            </span>
            <div className="mx-auto mt-6 max-w-sm rounded-2xl border border-indigo-500/20 bg-indigo-500/10 px-4 py-4 text-left">
              <div className="text-xs font-semibold uppercase tracking-[0.18em] text-indigo-300">
                Authorize Remote Machine
              </div>
              <p className="mt-2 text-sm text-surface-200">
                You are authorizing a waiting Yaver machine. This browser only completes sign-in and sends the result back to the remote device.
              </p>
              {prefillCode ? (
                <div className="mt-4 rounded-xl border border-surface-700 bg-surface-950/70 px-4 py-3 text-center">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-surface-500">Device Code</div>
                  <div className="mt-1 font-mono text-2xl font-bold tracking-[0.28em] text-surface-50">
                    {prefillCode}
                  </div>
                </div>
              ) : (
                <p className="mt-4 text-sm text-surface-400">
                  Get a code from <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver auth --headless</code>, then come back here.
                </p>
              )}
              <div className="mt-4 space-y-1 text-xs text-surface-400">
                <p>1. Sign in with Apple, Google, or Microsoft.</p>
                <p>2. Yaver returns here and authorizes the waiting machine automatically.</p>
                <p>3. The remote machine finishes sign-in without opening a browser.</p>
              </div>
            </div>
          </div>

          <div className="space-y-3">
            <Link
              href={authUrlFor("apple")}
              className="flex w-full items-center justify-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
            >
              <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
                <path d="M17.05 20.28c-.98.95-2.05.88-3.08.4-1.09-.5-2.08-.48-3.24 0-1.44.62-2.2.44-3.06-.4C2.79 15.25 3.51 7.59 9.05 7.31c1.35.07 2.29.74 3.08.8 1.18-.24 2.31-.93 3.57-.84 1.51.12 2.65.72 3.4 1.8-3.12 1.87-2.38 5.98.48 7.13-.57 1.5-1.31 2.99-2.54 4.09zM12.03 7.25c-.15-2.23 1.66-4.07 3.74-4.25.29 2.58-2.34 4.5-3.74 4.25z" />
              </svg>
              Continue with Apple
            </Link>

            <Link
              href={authUrlFor("google")}
              className="flex w-full items-center justify-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
            >
              <svg className="h-5 w-5" viewBox="0 0 24 24">
                <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 01-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" />
                <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" />
                <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" />
                <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" />
              </svg>
              Continue with Google
            </Link>

            <Link
              href={authUrlFor("microsoft")}
              className="flex w-full items-center justify-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
            >
              <svg className="h-5 w-5" viewBox="0 0 24 24">
                <path fill="#F25022" d="M1 1h10v10H1z" />
                <path fill="#00A4EF" d="M1 13h10v10H1z" />
                <path fill="#7FBA00" d="M13 1h10v10H13z" />
                <path fill="#FFB900" d="M13 13h10v10H13z" />
              </svg>
              Continue with Microsoft
            </Link>
          </div>

          <div className="my-6 flex items-center gap-3">
            <div className="h-px flex-1 bg-surface-800" />
            <span className="text-xs text-surface-600">or</span>
            <div className="h-px flex-1 bg-surface-800" />
          </div>

          <div className="text-center">
            <p className="text-sm text-surface-500">
              Prefer email/password? Sign in first, then return to authorize this machine.
            </p>
            <Link href={authUrl} className="mt-4 inline-block rounded-lg bg-surface-50 px-6 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200">
              Sign In with Email
            </Link>
          </div>

          <p className="mt-6 text-center text-xs text-surface-600">
            After login, Yaver comes back here and links the remote machine automatically.
          </p>
        </div>
      </div>
    );
  }

  // Success state
  if (status === "success") {
    return (
      <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
        <div className="w-full max-w-sm text-center">
          <div className="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-full bg-green-500/10">
            <svg className="h-8 w-8 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
            </svg>
          </div>
          <h2 className="text-xl font-bold text-surface-50">Device authorized</h2>
          <p className="mt-3 text-sm text-surface-400">
            Your terminal should sign in automatically. You can close this page.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <span className="text-2xl font-bold tracking-tight text-surface-50">
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
          <p className="mt-3 text-sm text-surface-500">
            Enter the code shown in your terminal
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
            placeholder="ABCD-1234"
            maxLength={9}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-4 text-center text-2xl font-mono font-bold tracking-[0.3em] text-surface-100 placeholder-surface-600 outline-none transition-colors focus:border-surface-500"
          />

          <button
            type="submit"
            disabled={status === "loading" || code.replace(/-/g, "").length < 8}
            className="mt-4 w-full rounded-lg bg-surface-50 px-4 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
          >
            {status === "loading" ? "Authorizing..." : "Authorize Device"}
          </button>
        </form>

        <p className="mt-6 text-center text-xs text-surface-600">
          Run <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-400">yaver auth --headless</code> to get a code
        </p>
        <p className="mt-2 text-center text-xs text-surface-600">
          Already signed in on another machine? Skip the OAuth flow entirely:<br />
          <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-400">yaver auth pair</code> on the headless box,
          then <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-400">yaver auth send &lt;code&gt; &lt;url&gt;</code> from the signed-in machine.
        </p>
      </div>
    </div>
  );
}

export default function DeviceCodePage() {
  return (
    <Suspense fallback={<div className="flex min-h-[70vh] items-center justify-center"><span className="text-surface-500 text-sm">Loading...</span></div>}>
      <DeviceCodeContent />
    </Suspense>
  );
}
