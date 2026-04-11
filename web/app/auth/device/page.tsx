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
        <div className="w-full max-w-sm text-center">
          <span className="text-2xl font-bold tracking-tight text-surface-50">
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
          <p className="mt-4 text-sm text-surface-400">
            Sign in first, then enter the code from your terminal.
          </p>
          <Link href={authUrl} className="mt-6 inline-block rounded-lg bg-surface-50 px-6 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200">
            Sign In
          </Link>
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
