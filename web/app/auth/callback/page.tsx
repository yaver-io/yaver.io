"use client";

import { useEffect, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { Suspense } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { sanitizeReturnTo } from "@/lib/oauth";

function CallbackHandler() {
  const searchParams = useSearchParams();
  const router = useRouter();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const token = searchParams.get("token");
    const errorParam = searchParams.get("error");
    const returnTo = sanitizeReturnTo(searchParams.get("return"));

    if (errorParam) {
      setError(errorParam);
      return;
    }

    if (!token) {
      setError("No authentication token received.");
      return;
    }

    try {
      // Store token
      localStorage.setItem("yaver_auth_token", token);

      // Also set as cookie for server-side access
      document.cookie = `yaver_auth_token=${token}; path=/; max-age=${60 * 60 * 24 * 30}; secure; samesite=lax`;

      // Validate token and check survey status
      fetch(`${CONVEX_URL}/auth/validate`, {
        method: "GET",
        headers: { Authorization: `Bearer ${token}` },
      })
        .then((res) => {
          if (!res.ok) {
            router.push(returnTo || "/dashboard");
            return;
          }
          return res.json();
        })
        .then((data) => {
          if (!data) return;
          router.push(returnTo || "/dashboard");
        })
        .catch(() => {
          // On error, default to dashboard
          router.push(returnTo || "/dashboard");
        });
    } catch {
      setError("Failed to store authentication token.");
    }
  }, [searchParams, router]);

  if (error) {
    return (
      <div className="flex min-h-[80vh] items-center justify-center px-6">
        <div className="card max-w-sm p-8 text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-red-500/10 text-red-400">
            <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
            </svg>
          </div>
          <h2 className="mb-2 text-lg font-semibold text-white">
            Authentication Failed
          </h2>
          <p className="mb-6 text-sm text-surface-400">{error}</p>
          <a href="/auth" className="btn-primary">
            Try Again
          </a>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-[80vh] items-center justify-center px-6">
      <div className="text-center">
        <div className="mx-auto mb-4 h-8 w-8 animate-spin rounded-full border-2 border-surface-600 border-t-yaver-500" />
        <p className="text-sm text-surface-400">Completing sign in...</p>
      </div>
    </div>
  );
}

export default function AuthCallbackPage() {
  return (
    <Suspense
      fallback={
        <div className="flex min-h-[80vh] items-center justify-center px-6">
          <div className="text-center">
            <div className="mx-auto mb-4 h-8 w-8 animate-spin rounded-full border-2 border-surface-600 border-t-yaver-500" />
            <p className="text-sm text-surface-400">Loading...</p>
          </div>
        </div>
      }
    >
      <CallbackHandler />
    </Suspense>
  );
}
