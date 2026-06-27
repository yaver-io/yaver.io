"use client";

// OwnerGate wraps an owner-only route (the experimental hardware cells) and
// renders a friendly "not available" notice for non-owners instead of the
// cell. Pair with the daemon-side mcp_owner_gate.go (the tools are also hidden
// from the agent) and the dashboard nav filter.
import Link from "next/link";
import { useAuth } from "@/lib/use-auth";

export default function OwnerGate({ children }: { children: React.ReactNode }) {
  const { user, isLoading } = useAuth();
  if (isLoading) return null;
  if (user?.isOwner !== true) {
    return (
      <div className="mx-auto max-w-md p-10 text-center text-sm text-surface-400">
        <p className="mb-3 text-base text-surface-200">
          This feature isn’t available on your account.
        </p>
        <Link href="/dashboard" className="underline hover:text-surface-200">
          Back to dashboard
        </Link>
      </div>
    );
  }
  return <>{children}</>;
}
