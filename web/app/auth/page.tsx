"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

function AuthContent() {
  const params = useSearchParams();
  const error = params.get("error");
  const client = params.get("client") || "web";
  const returnUrl = params.get("return");

  const [mode, setMode] = useState<"signin" | "signup">("signin");
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [rePassword, setRePassword] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const handleOAuth = (provider: "google" | "microsoft" | "apple") => {
    const qs = new URLSearchParams({ client });
    if (returnUrl) {
      qs.set("return", returnUrl);
    }
    window.location.href = `/api/auth/oauth/${provider}?${qs.toString()}`;
  };

  const handleEmailSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);

    if (mode === "signup" && password !== rePassword) {
      setFormError("Passwords do not match.");
      return;
    }

    if (password.length < 8) {
      setFormError("Password must be at least 8 characters.");
      return;
    }

    setLoading(true);

    try {
      const endpoint = mode === "signup" ? "/auth/signup" : "/auth/login";
      const body: Record<string, string> = { email, password };
      if (mode === "signup") {
        body.fullName = fullName;
      }

      const res = await fetch(`${CONVEX_URL}${endpoint}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });

      if (!res.ok) {
        const text = await res.text();
        setFormError(text || "Something went wrong. Please try again.");
        setLoading(false);
        return;
      }

      const data = await res.json();

      // 2FA required
      if (data.requires2fa && data.pendingToken) {
        const totpUrl = `/auth/totp?pendingToken=${data.pendingToken}&client=${client}`;
        window.location.href = returnUrl
          ? `${totpUrl}&return=${encodeURIComponent(returnUrl)}`
          : totpUrl;
        return;
      }

      const token = data.token;

      if (!token) {
        setFormError("No token received from server.");
        setLoading(false);
        return;
      }

      // Store token
      localStorage.setItem("yaver_auth_token", token);
      document.cookie = `yaver_auth_token=${token}; path=/; max-age=${60 * 60 * 24 * 30}; secure; samesite=lax`;

      // Check if desktop client - redirect to localhost callback
      if (client === "desktop") {
        window.location.href = `http://127.0.0.1:19836/callback?token=${token}`;
        return;
      }

      // Return to original page if specified (e.g. device code page)
      if (returnUrl) {
        window.location.href = returnUrl;
        return;
      }

      window.location.href = "/dashboard";
    } catch {
      setFormError("Network error. Please try again.");
      setLoading(false);
    }
  };

  const displayError = formError || error;

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <span className="text-2xl font-bold tracking-tight text-surface-50">
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
          <p className="mt-3 text-sm text-surface-500">
            {mode === "signin" ? "Sign in to get started" : "Create an account with email"}
          </p>
        </div>

        {displayError && (
          <div className="mb-6 rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-400">
            {displayError}
          </div>
        )}

        <div className="space-y-3">
          <button
            onClick={() => handleOAuth("apple")}
            className="flex w-full items-center justify-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M17.05 20.28c-.98.95-2.05.88-3.08.4-1.09-.5-2.08-.48-3.24 0-1.44.62-2.2.44-3.06-.4C2.79 15.25 3.51 7.59 9.05 7.31c1.35.07 2.29.74 3.08.8 1.18-.24 2.31-.93 3.57-.84 1.51.12 2.65.72 3.4 1.8-3.12 1.87-2.38 5.98.48 7.13-.57 1.5-1.31 2.99-2.54 4.09zM12.03 7.25c-.15-2.23 1.66-4.07 3.74-4.25.29 2.58-2.34 4.5-3.74 4.25z" />
            </svg>
            Continue with Apple
          </button>

          <button
            onClick={() => handleOAuth("google")}
            className="flex w-full items-center justify-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24">
              <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 01-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" />
              <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" />
              <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" />
              <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" />
            </svg>
            Continue with Google
          </button>

          <button
            onClick={() => handleOAuth("microsoft")}
            className="flex w-full items-center justify-center gap-3 rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50"
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24">
              <path fill="#F25022" d="M1 1h10v10H1z" />
              <path fill="#00A4EF" d="M1 13h10v10H1z" />
              <path fill="#7FBA00" d="M13 1h10v10H13z" />
              <path fill="#FFB900" d="M13 13h10v10H13z" />
            </svg>
            Continue with Microsoft
          </button>
        </div>

        {/* Divider */}
        <div className="my-6 flex items-center gap-3">
          <div className="h-px flex-1 bg-surface-700" />
          <span className="text-xs text-surface-500">or</span>
          <div className="h-px flex-1 bg-surface-700" />
        </div>

        {/* Email/Password Form */}
        <form onSubmit={handleEmailSubmit} className="space-y-3">
          {mode === "signup" && (
            <input
              type="text"
              placeholder="Full name"
              value={fullName}
              onChange={(e) => setFullName(e.target.value)}
              required
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
            />
          )}
          <input
            type="email"
            placeholder="Email address"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
          />
          <input
            type="password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
          />
          {mode === "signup" && (
            <input
              type="password"
              placeholder="Confirm password"
              value={rePassword}
              onChange={(e) => setRePassword(e.target.value)}
              required
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
            />
          )}
          <button
            type="submit"
            disabled={loading}
            className="w-full rounded-lg bg-surface-50 px-4 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
          >
            {loading ? "Please wait..." : mode === "signin" ? "Sign In" : "Sign Up"}
          </button>
        </form>

        {/* Forgot password (sign-in mode only) */}
        {mode === "signin" && (
          <p className="mt-2 text-right text-sm">
            <Link
              href="/auth/reset-password"
              className="text-surface-500 hover:text-surface-300 transition-colors"
            >
              Forgot password?
            </Link>
          </p>
        )}

        {/* Toggle mode */}
        <p className="mt-4 text-center text-sm text-surface-500">
          {mode === "signin" ? (
            <>
              Don&apos;t have an account?{" "}
              <button
                onClick={() => { setMode("signup"); setFormError(null); }}
                className="text-surface-300 hover:text-surface-50 transition-colors"
              >
                Sign Up
              </button>
            </>
          ) : (
            <>
              Already have an account?{" "}
              <button
                onClick={() => { setMode("signin"); setFormError(null); }}
                className="text-surface-300 hover:text-surface-50 transition-colors"
              >
                Sign In
              </button>
            </>
          )}
        </p>

        <p className="mt-6 text-center text-xs text-surface-600">
          By continuing, you agree to our{" "}
          <Link href="/terms" className="text-surface-400 hover:text-surface-50">Terms</Link>{" "}
          and{" "}
          <Link href="/privacy" className="text-surface-400 hover:text-surface-50">Privacy Policy</Link>.
        </p>
      </div>
    </div>
  );
}

export default function AuthPage() {
  return (
    <Suspense fallback={<div className="flex min-h-[70vh] items-center justify-center"><span className="text-surface-500 text-sm">Loading...</span></div>}>
      <AuthContent />
    </Suspense>
  );
}
