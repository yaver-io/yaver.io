"use client";

// /account/merge — approval page for a manual account merge.
// Reached from a URL like https://yaver.io/account/merge?token=<mergeToken>
// that the TARGET account (the one that will be kept) generated from
// Settings → "Merge another account". The user opens this URL on a browser
// where the SOURCE account (the one that will be deleted) is signed in,
// confirms, and the merge runs.
//
// Flow:
//   1. Load /auth/account/merge/status?token=... to show the target email.
//   2. User must already be signed into the source account locally —
//      if not, we send them to /login with a return URL.
//   3. On "Confirm", we POST /auth/account/merge/complete with the source's
//      own session token in Authorization + the mergeToken in the body.
//   4. On success we wipe the source's local token and redirect to /dashboard
//      where the now-surviving target account will be signed in on next auth.

import Link from "next/link";
import { useEffect, useState, Suspense } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { CONVEX_URL } from "@/lib/constants";

type MergeStatus =
  | { status: "pending"; targetEmail: string; expiresAt: number }
  | { status: "completed"; targetEmail: string; completedAt: number }
  | { status: "cancelled"; targetEmail: string }
  | { status: "expired"; targetEmail?: string }
  | { status: "unknown" };

function MergePage() {
  const router = useRouter();
  const params = useSearchParams();
  const token = params.get("token") ?? "";

  const [status, setStatus] = useState<MergeStatus | null>(null);
  const [sourceEmail, setSourceEmail] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<{ mergedFrom: string; mergedInto: string } | null>(null);

  const sourceToken =
    typeof window !== "undefined"
      ? localStorage.getItem("yaver_auth_token")
      : null;

  useEffect(() => {
    if (!token) {
      setError("Missing merge token in URL.");
      setLoading(false);
      return;
    }
    (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/auth/account/merge/status?token=${encodeURIComponent(token)}`);
        const data = (await res.json()) as MergeStatus;
        setStatus(data);
      } catch {
        setError("Could not load merge status. Check your connection and try again.");
      } finally {
        setLoading(false);
      }
    })();
  }, [token]);

  useEffect(() => {
    if (!sourceToken) return;
    (async () => {
      try {
        const res = await fetch(`${CONVEX_URL}/auth/validate`, {
          headers: { Authorization: `Bearer ${sourceToken}` },
        });
        if (!res.ok) return;
        const data = await res.json();
        setSourceEmail(data?.user?.email ?? null);
      } catch {
        // ignore
      }
    })();
  }, [sourceToken]);

  const handleConfirm = async () => {
    if (!sourceToken) return;
    setConfirming(true);
    setError(null);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/account/merge/complete`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${sourceToken}`,
        },
        body: JSON.stringify({ mergeToken: token }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(data?.error || "Merge failed.");
        setConfirming(false);
        return;
      }
      // Source user was deleted — clear local session and send them to
      // the dashboard (they'll need to re-auth under the target account).
      localStorage.removeItem("yaver_auth_token");
      document.cookie = "yaver_auth_token=; path=/; max-age=0; secure; samesite=lax";
      document.cookie = "yaver_session=; path=/; max-age=0; secure; samesite=lax";
      setDone({ mergedFrom: data.mergedFrom, mergedInto: data.mergedInto });
    } catch {
      setError("Network error — try again.");
    } finally {
      setConfirming(false);
    }
  };

  if (loading) {
    return (
      <div className="mx-auto max-w-lg px-4 py-20 text-center text-sm text-surface-400">
        Loading merge request…
      </div>
    );
  }

  if (!token || error) {
    return (
      <div className="mx-auto max-w-lg px-4 py-20 text-center">
        <h1 className="text-xl font-semibold text-surface-100">Merge request unavailable</h1>
        <p className="mt-3 text-sm text-red-400">{error || "Missing merge token."}</p>
        <Link className="mt-6 inline-block text-sm text-surface-300 underline" href="/dashboard">
          Back to dashboard
        </Link>
      </div>
    );
  }

  if (done) {
    return (
      <div className="mx-auto max-w-lg px-4 py-20 text-center">
        <h1 className="text-xl font-semibold text-emerald-300">Accounts merged</h1>
        <p className="mt-3 text-sm text-surface-300">
          <span className="text-surface-100">{done.mergedFrom}</span> has been merged into{" "}
          <span className="text-surface-100">{done.mergedInto}</span>. Sign in with any of{" "}
          {done.mergedInto}&apos;s linked providers to continue.
        </p>
        <Link className="mt-6 inline-block rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-200 hover:bg-surface-800/50" href="/auth">
          Sign in
        </Link>
      </div>
    );
  }

  if (!status || status.status === "unknown") {
    return (
      <div className="mx-auto max-w-lg px-4 py-20 text-center">
        <h1 className="text-xl font-semibold text-surface-100">Merge request not found</h1>
        <p className="mt-3 text-sm text-surface-400">
          This link is invalid or the target account cancelled the merge.
        </p>
      </div>
    );
  }

  if (status.status !== "pending") {
    const label =
      status.status === "completed"
        ? "Already completed"
        : status.status === "cancelled"
        ? "Cancelled by the target account"
        : "Expired";
    return (
      <div className="mx-auto max-w-lg px-4 py-20 text-center">
        <h1 className="text-xl font-semibold text-surface-100">Merge {label}</h1>
        <p className="mt-3 text-sm text-surface-400">
          Ask the target account (
          <span className="text-surface-200">{"targetEmail" in status ? status.targetEmail : ""}</span>
          ) to start a new merge if you still want to combine accounts.
        </p>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-lg px-4 py-20">
      <h1 className="mb-6 text-2xl font-semibold text-surface-100">Merge accounts</h1>
      <div className="card mb-6">
        <p className="mb-3 text-xs uppercase tracking-wider text-surface-500">Target account (keeps data)</p>
        <p className="text-sm text-surface-100">{status.targetEmail}</p>
      </div>
      <div className="card mb-6">
        <p className="mb-3 text-xs uppercase tracking-wider text-surface-500">This browser is signed in as</p>
        {sourceToken ? (
          <p className="text-sm text-surface-100">{sourceEmail || "Loading…"}</p>
        ) : (
          <>
            <p className="mb-3 text-sm text-surface-400">
              Not signed in on this browser. Sign into the account you want to merge AWAY,
              then re-open this link.
            </p>
            <Link
              href={`/auth?return=${encodeURIComponent(`/account/merge?token=${token}`)}`}
              className="inline-block rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-200 hover:bg-surface-800/50"
            >
              Sign in
            </Link>
          </>
        )}
      </div>

      {sourceToken && sourceEmail && (
        <>
          <div className="card mb-6 border-amber-500/30">
            <p className="text-sm text-amber-200">
              <span className="font-medium">Heads up:</span> this deletes{" "}
              <span className="font-medium">{sourceEmail}</span> and moves all of its devices,
              linked providers, settings, and history onto <span className="font-medium">{status.targetEmail}</span>.
              This cannot be undone.
            </p>
          </div>
          <button
            onClick={handleConfirm}
            disabled={confirming}
            className="w-full rounded-lg border border-red-500/30 bg-red-500/5 px-4 py-3 text-sm font-medium text-red-300 transition-colors hover:bg-red-500/10 disabled:opacity-50"
          >
            {confirming ? "Merging…" : `Merge ${sourceEmail} into ${status.targetEmail}`}
          </button>
          <button
            onClick={() => router.push("/dashboard")}
            className="mt-3 w-full rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-300 hover:bg-surface-800/50"
          >
            Cancel
          </button>
        </>
      )}
    </div>
  );
}

export default function Page() {
  return (
    <Suspense fallback={<div className="mx-auto max-w-lg px-4 py-20 text-center text-sm text-surface-400">Loading…</div>}>
      <MergePage />
    </Suspense>
  );
}
