// Authenticated app route group — dashboard, settings, billing, etc.
// Current `/dashboard` lives at `app/dashboard/` for URL stability; new
// authenticated screens go under this group with a shared layout that can
// enforce auth guards, load user context, etc.
"use client";

import type { ReactNode } from "react";
import { useAuth } from "@/lib/use-auth";

export default function AppLayout({ children }: { children: ReactNode }) {
  const { isLoading, isAuthenticated } = useAuth();
  if (isLoading) return <div className="min-h-screen bg-surface-950" />;
  if (!isAuthenticated) {
    if (typeof window !== "undefined") window.location.href = "/auth";
    return null;
  }
  return <>{children}</>;
}
