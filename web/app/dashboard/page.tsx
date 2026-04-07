"use client";

import { useEffect } from "react";

export default function DashboardPage() {
  useEffect(() => {
    // Dashboard moved to Yaver Desktop app — redirect to download page
    window.location.href = "/download";
  }, []);

  return (
    <div className="flex min-h-[80vh] items-center justify-center">
      <div className="text-center">
        <div className="h-6 w-6 mx-auto animate-spin rounded-full border-2 border-surface-600 border-t-emerald-400 mb-4" />
        <p className="text-sm text-surface-500">Redirecting to download page...</p>
        <p className="text-xs text-surface-600 mt-2">
          The web dashboard has moved to the <a href="/download" className="text-indigo-400 hover:text-indigo-300">Yaver Desktop app</a>.
        </p>
      </div>
    </div>
  );
}
