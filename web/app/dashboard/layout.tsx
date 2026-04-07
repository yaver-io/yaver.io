"use client";

import { useEffect } from "react";

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  // Hide the global header, footer, and chat widget when on dashboard
  useEffect(() => {
    document.body.classList.add("dashboard-mode");
    return () => document.body.classList.remove("dashboard-mode");
  }, []);

  return <>{children}</>;
}
