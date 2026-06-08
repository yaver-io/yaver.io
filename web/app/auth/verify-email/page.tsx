"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

type Status = "idle" | "loading" | "success" | "alreadyConsumed" | "expired" | "error";

function VerifyEmailContent() {
  const params = useSearchParams();
  const token = params.get("token") || "";
  const [status, setStatus] = useState<Status>("idle");
  const [errorMessage, setErrorMessage] = useState<string>("");

  useEffect(() => {
    if (!token) {
      setStatus("error");
      setErrorMessage("Missing verification token.");
      return;
    }
    let cancelled = false;
    setStatus("loading");
    (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/auth/verify-email/confirm`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ token }),
        });
        if (cancelled) return;
        if (!res.ok) {
          setStatus("error");
          setErrorMessage("Couldn't reach the server. Try again in a moment.");
          return;
        }
        const data = await res.json();
        if (data.ok) {
          setStatus(data.alreadyConsumed ? "alreadyConsumed" : "success");
          return;
        }
        if (data.error === "TOKEN_EXPIRED") {
          setStatus("expired");
          return;
        }
        if (data.error === "TOKEN_NOT_FOUND") {
          setStatus("error");
          setErrorMessage("This verification link isn't recognised — it may have already been used or replaced by a newer one.");
          return;
        }
        if (data.error === "EMAIL_CHANGED") {
          setStatus("error");
          setErrorMessage("Your account email changed since this link was issued. Sign in and request a fresh verification email from Settings.");
          return;
        }
        setStatus("error");
        setErrorMessage(data.error || "Couldn't verify your email.");
      } catch {
        if (cancelled) return;
        setStatus("error");
        setErrorMessage("Network error. Check your connection and try again.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token]);

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
      <div className="w-full max-w-md text-center">
        <div className="mb-8">
          <span className="text-2xl font-bold tracking-tight text-surface-50">
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
        </div>
        {status === "loading" || status === "idle" ? (
          <p className="text-surface-400">Verifying your email…</p>
        ) : null}
        {status === "success" ? (
          <>
            <h1 className="mb-3 text-xl font-semibold text-surface-50">Email verified ✓</h1>
            <p className="mb-6 text-sm text-surface-400">
              You can now link Apple, Google, GitHub, GitLab, or Microsoft to this account just by signing in with them.
            </p>
            <Link href="/dashboard" className="inline-block rounded-lg bg-surface-50 px-6 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200">
              Continue to dashboard
            </Link>
          </>
        ) : null}
        {status === "alreadyConsumed" ? (
          <>
            <h1 className="mb-3 text-xl font-semibold text-surface-50">Already verified</h1>
            <p className="mb-6 text-sm text-surface-400">This email was already confirmed on a previous click. Nothing else to do.</p>
            <Link href="/dashboard" className="inline-block rounded-lg bg-surface-50 px-6 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200">
              Continue to dashboard
            </Link>
          </>
        ) : null}
        {status === "expired" ? (
          <>
            <h1 className="mb-3 text-xl font-semibold text-surface-50">Link expired</h1>
            <p className="mb-6 text-sm text-surface-400">
              Verification links are valid for 24 hours. Sign in and request a fresh one from Settings.
            </p>
            <Link href="/auth" className="inline-block rounded-lg bg-surface-50 px-6 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200">
              Sign in
            </Link>
          </>
        ) : null}
        {status === "error" ? (
          <>
            <h1 className="mb-3 text-xl font-semibold text-surface-50">Verification failed</h1>
            <p className="mb-6 text-sm text-rose-700 dark:text-rose-300">{errorMessage}</p>
            <Link href="/auth" className="inline-block rounded-lg bg-surface-50 px-6 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200">
              Sign in
            </Link>
          </>
        ) : null}
      </div>
    </div>
  );
}

export default function VerifyEmailPage() {
  return (
    <Suspense fallback={<div className="flex min-h-[70vh] items-center justify-center"><span className="text-surface-500 text-sm">Loading…</span></div>}>
      <VerifyEmailContent />
    </Suspense>
  );
}
