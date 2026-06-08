"use client";

import Link from "next/link";
import { useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

const CONVEX_SITE_URL = CONVEX_URL;

export default function DeleteAccountPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [status, setStatus] = useState<"idle" | "loading" | "done" | "error">("idle");
  const [errorMsg, setErrorMsg] = useState("");

  const canSubmit = email && password && confirm === "delete my account" && status !== "loading";

  const handleDelete = async () => {
    setStatus("loading");
    setErrorMsg("");

    try {
      // Step 1: Log in to get a token
      const loginRes = await fetch(`${CONVEX_SITE_URL}/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: email.toLowerCase().trim(), password }),
      });

      if (!loginRes.ok) {
        const body = await loginRes.json().catch(() => ({}));
        throw new Error(body.error || "Invalid email or password");
      }

      const { token } = await loginRes.json();

      // Step 2: Delete the account
      const deleteRes = await fetch(`${CONVEX_SITE_URL}/auth/delete-account`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
      });

      if (!deleteRes.ok) {
        throw new Error("Failed to delete account. Please try again.");
      }

      setStatus("done");
    } catch (e: unknown) {
      setErrorMsg(e instanceof Error ? e.message : "Something went wrong");
      setStatus("error");
    }
  };

  if (status === "done") {
    return (
      <div className="mx-auto max-w-lg px-6 py-16 md:py-24 text-center">
        <h1 className="mb-4 text-2xl font-bold text-surface-50">Account Deleted</h1>
        <p className="text-surface-400 mb-8">
          Your account and all associated data have been permanently deleted.
        </p>
        <Link href="/" className="text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-300 text-sm">
          &larr; Back to Home
        </Link>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-lg px-6 py-16 md:py-24">
      <div className="mb-8">
        <Link href="/" className="text-sm text-surface-500 hover:text-surface-300">
          &larr; Back to Home
        </Link>
      </div>

      <h1 className="mb-2 text-2xl font-bold text-surface-50">Delete Your Account</h1>
      <p className="mb-8 text-sm text-surface-400">
        This will permanently delete your Yaver account and all associated data including
        your device registrations, settings, and session tokens. This action cannot be undone.
      </p>

      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-surface-300 mb-1">Email</label>
          <input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="your@email.com"
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-100 placeholder-surface-600 focus:border-indigo-500 focus:outline-none"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-surface-300 mb-1">Password</label>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Your password"
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-100 placeholder-surface-600 focus:border-indigo-500 focus:outline-none"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-surface-300 mb-1">
            Type <code className="text-surface-200 bg-surface-800 px-1 py-0.5 rounded text-xs">delete my account</code> to confirm
          </label>
          <input
            type="text"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            placeholder="delete my account"
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-100 placeholder-surface-600 focus:border-red-500 focus:outline-none"
          />
        </div>

        {errorMsg && (
          <p className="text-sm text-red-400">{errorMsg}</p>
        )}

        <button
          onClick={handleDelete}
          disabled={!canSubmit}
          className={`w-full rounded-lg border px-4 py-3 text-sm font-semibold transition-colors ${
            canSubmit
              ? "border-red-500/30 bg-red-500/10 text-red-400 hover:bg-red-500/20 cursor-pointer"
              : "border-surface-700 bg-surface-800/50 text-surface-600 cursor-not-allowed"
          }`}
        >
          {status === "loading" ? "Deleting..." : "Permanently Delete My Account"}
        </button>
      </div>

      <div className="mt-12 rounded-lg border border-surface-800 bg-surface-900/50 p-4">
        <h2 className="text-sm font-semibold text-surface-200 mb-2">You can also delete from the app</h2>
        <p className="text-xs text-surface-500 leading-relaxed">
          Open the Yaver app &rarr; Settings &rarr; scroll to &ldquo;Danger Zone&rdquo; &rarr;
          type &ldquo;delete my account&rdquo; &rarr; tap &ldquo;Delete My Account&rdquo;.
        </p>
      </div>
    </div>
  );
}
