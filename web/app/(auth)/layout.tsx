// Auth route group — login, signup, device-code, etc. Shares a minimal layout
// without the full marketing chrome. Existing `/auth` and `/login` routes
// stay where they are for URL stability.
import type { ReactNode } from "react";

export default function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-screen flex items-center justify-center bg-surface-950">
      {children}
    </div>
  );
}
