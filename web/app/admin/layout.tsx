// /admin route — role-gated chrome for org-wide administration. Hits
// /admin/identity at mount; redirects non-admins to /dashboard. Stamps
// body.admin-mode so globals.css can suppress the public chrome.
"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { CONVEX_URL } from "@/lib/constants";
import { Loader2, ShieldX } from "@/components/admin/icons";
import { ToastProvider } from "@/components/admin/Toaster";

function getStoredToken(): string | null {
  if (typeof window === "undefined") return null;
  const ls = localStorage.getItem("yaver_auth_token");
  if (ls) return ls;
  for (const cookie of document.cookie.split(";")) {
    const [name, value] = cookie.trim().split("=");
    if (name === "yaver_session" || name === "yaver_auth_token") return value || null;
  }
  return null;
}

type Status =
  | { kind: "loading" }
  | { kind: "ok"; email: string }
  | { kind: "anon" }
  | { kind: "forbidden"; email: string };

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [status, setStatus] = useState<Status>({ kind: "loading" });

  useEffect(() => {
    document.body.classList.add("admin-mode");
    return () => {
      document.body.classList.remove("admin-mode");
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const token = getStoredToken();
    if (!token) {
      setStatus({ kind: "anon" });
      return;
    }
    fetch(`${CONVEX_URL}/admin/identity`, {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then(async (res) => {
        if (res.status === 401) {
          if (!cancelled) setStatus({ kind: "anon" });
          return;
        }
        const json = await res.json();
        if (cancelled) return;
        if (json.isAdmin) setStatus({ kind: "ok", email: json.email });
        else setStatus({ kind: "forbidden", email: json.email });
      })
      .catch(() => {
        if (!cancelled) setStatus({ kind: "anon" });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (status.kind === "anon") {
      router.replace("/auth?next=/admin");
    }
  }, [status, router]);

  if (status.kind === "loading" || status.kind === "anon") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-surface-950 text-surface-300">
        <div className="flex items-center gap-2 text-[13px]">
          <Loader2 className="h-4 w-4 animate-spin" />
          Loading admin…
        </div>
      </div>
    );
  }

  if (status.kind === "forbidden") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-surface-950 px-4">
        <div className="w-full max-w-md rounded-md border border-surface-800 bg-surface-900 p-6">
          <div className="flex items-start gap-3">
            <div className="rounded border border-warning/40 bg-warning-soft p-1.5 text-warning-softFg">
              <ShieldX className="h-4 w-4" />
            </div>
            <div className="min-w-0">
              <div className="text-[14px] font-semibold text-surface-100">
                Not a platform admin
              </div>
              <div className="mt-1 text-[12px] leading-relaxed text-surface-300">
                You are signed in as <span className="font-mono">{status.email}</span>, but
                that identity is not on the admin allowlist for this Convex deployment.
              </div>
              <div className="mt-3 rounded border border-surface-800 bg-surface-950 p-2 font-mono text-[11px] leading-relaxed text-surface-300">
                # add yourself to the allowlist on the backend{"\n"}
                npx convex env set CLOUD_PREVIEW_OWNER_EMAILS {status.email}
              </div>
              <a
                href="/dashboard"
                className="mt-4 inline-flex items-center gap-1 text-[12px] font-medium text-warning-softFg hover:underline"
              >
                Back to my dashboard
              </a>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return <ToastProvider>{children}</ToastProvider>;
}
