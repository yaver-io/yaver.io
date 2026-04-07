"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

function ResetPasswordContent() {
  const params = useSearchParams();
  const token = params.get("token");

  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [done, setDone] = useState(false);

  // No token → show "request reset" form
  const [email, setEmail] = useState("");
  const [requested, setRequested] = useState(false);

  const handleRequestReset = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!email.trim()) {
      setError("Enter your email address.");
      return;
    }

    setLoading(true);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/forgot-password`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: email.trim() }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.error || "Something went wrong.");
        setLoading(false);
        return;
      }
      setRequested(true);
    } catch {
      setError("Network error. Please try again.");
    }
    setLoading(false);
  };

  const handleResetPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    if (password.length < 8) {
      setError("Password must be at least 8 characters.");
      return;
    }
    if (password !== confirmPassword) {
      setError("Passwords do not match.");
      return;
    }

    setLoading(true);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/reset-password`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token, password }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data.error || "Reset failed.");
        setLoading(false);
        return;
      }
      setDone(true);
    } catch {
      setError("Network error. Please try again.");
    }
    setLoading(false);
  };

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <span className="text-2xl font-bold tracking-tight text-surface-50">
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
          <p className="mt-3 text-sm text-surface-500">
            {token ? "Set a new password" : "Reset your password"}
          </p>
        </div>

        {error && (
          <div className="mb-6 rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-400">
            {error}
          </div>
        )}

        {done ? (
          <div className="text-center">
            <div className="mb-4 rounded-lg border border-green-500/20 bg-green-500/10 px-4 py-3 text-sm text-green-400">
              Password reset successfully. All sessions have been logged out.
            </div>
            <Link
              href="/auth"
              className="text-sm text-surface-300 hover:text-surface-50 transition-colors"
            >
              Sign in with your new password
            </Link>
          </div>
        ) : token ? (
          <form onSubmit={handleResetPassword} className="space-y-3">
            <input
              type="password"
              placeholder="New password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
            />
            <input
              type="password"
              placeholder="Confirm new password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              required
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
            />
            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-lg bg-surface-50 px-4 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
            >
              {loading ? "Please wait..." : "Reset Password"}
            </button>
          </form>
        ) : requested ? (
          <div className="text-center">
            <div className="mb-4 rounded-lg border border-surface-700 bg-surface-900 px-4 py-4 text-sm text-surface-300">
              If an account exists for that email, we sent a reset link. Check your inbox.
            </div>
            <Link
              href="/auth"
              className="text-sm text-surface-300 hover:text-surface-50 transition-colors"
            >
              Back to sign in
            </Link>
          </div>
        ) : (
          <form onSubmit={handleRequestReset} className="space-y-3">
            <input
              type="email"
              placeholder="Enter your email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500"
            />
            <button
              type="submit"
              disabled={loading}
              className="w-full rounded-lg bg-surface-50 px-4 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
            >
              {loading ? "Please wait..." : "Send Reset Link"}
            </button>
            <p className="text-center text-sm text-surface-500">
              <Link
                href="/auth"
                className="text-surface-300 hover:text-surface-50 transition-colors"
              >
                Back to sign in
              </Link>
            </p>
          </form>
        )}
      </div>
    </div>
  );
}

export default function ResetPasswordPage() {
  return (
    <Suspense>
      <ResetPasswordContent />
    </Suspense>
  );
}
