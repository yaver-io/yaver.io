// Org-admin chrome — sidebar nav + top bar. Mirrors the dashboard
// chrome at the surface level but stamps a warning-amber accent
// (left bar on active item, amber border on the top bar, "Org admin"
// badge) so the operator always knows they are out of "just me" mode.
"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import React from "react";
import {
  Activity,
  ChevronLeft,
  Cpu,
  KeyRound,
  ScrollText,
  Settings2,
  ShieldCheck,
  Users,
} from "./icons";

const NAV: Array<{ label: string; href: string; icon: React.ComponentType<{ className?: string }>; }> = [
  { label: "Overview", href: "/admin", icon: Activity },
  { label: "Users", href: "/admin/users", icon: Users },
  { label: "Devices", href: "/admin/devices", icon: Cpu },
  { label: "Sessions", href: "/admin/sessions", icon: KeyRound },
  { label: "Audit log", href: "/admin/audit", icon: ScrollText },
  { label: "Policy", href: "/admin/policy", icon: Settings2 },
  { label: "SSO", href: "/admin/sso", icon: ShieldCheck },
];

export function AdminShell({
  pageTitle,
  pageSubtitle,
  actions,
  children,
}: {
  pageTitle: string;
  pageSubtitle?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
}) {
  const pathname = usePathname();

  return (
    <div className="flex min-h-screen bg-surface-950">
      {/* Sidebar */}
      <aside className="hidden w-[240px] shrink-0 border-r border-surface-800 bg-surface-850/60 lg:block">
        <div className="flex h-14 items-center gap-2 border-b border-surface-800 px-4">
          <span className="font-mono text-[14px] font-semibold tracking-tight text-surface-100">yaver</span>
          <span className="rounded bg-warning-soft px-1.5 py-0.5 font-mono text-[10px] font-semibold uppercase tracking-wider text-warning-softFg">
            admin
          </span>
        </div>
        <nav className="px-2 py-3">
          {NAV.map((item) => {
            const active =
              item.href === "/admin"
                ? pathname === "/admin"
                : pathname.startsWith(item.href);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`group relative flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] transition-colors ${
                  active
                    ? "bg-warning-soft/60 text-surface-100"
                    : "text-surface-300 hover:bg-surface-850 hover:text-surface-100"
                }`}
              >
                {active && (
                  <span className="absolute inset-y-1 left-0 w-[2px] rounded-r bg-warning" />
                )}
                <Icon className="h-[14px] w-[14px] shrink-0" />
                {item.label}
              </Link>
            );
          })}
        </nav>

        <div className="mt-4 px-2">
          <div className="rounded-md border border-surface-800 bg-surface-900 p-3">
            <div className="text-[10px] font-semibold uppercase tracking-wider text-surface-400">
              Gate
            </div>
            <div className="mt-1 text-[11px] leading-relaxed text-surface-300">
              Env-var owner allowlist (<span className="font-mono">CLOUD_PREVIEW_OWNER_EMAILS</span>).
              Schema-backed <span className="font-mono">platformRole</span> next pass.
            </div>
          </div>
        </div>
      </aside>

      {/* Main column */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-20 border-b-2 border-warning/70 bg-surface-950/90 backdrop-blur">
          <div className="flex h-14 items-center justify-between gap-3 px-4 lg:px-6">
            <div className="flex items-center gap-3 text-[13px] text-surface-300">
              <Link
                href="/dashboard"
                className="inline-flex items-center gap-1 text-surface-400 hover:text-surface-100"
              >
                <ChevronLeft className="h-4 w-4" />
                Back to my dashboard
              </Link>
            </div>
            <div className="inline-flex items-center gap-2 rounded-full border border-warning/40 bg-warning-soft px-2.5 py-1 text-[11px] font-semibold uppercase tracking-wider text-warning-softFg">
              <ShieldCheck className="h-3.5 w-3.5" />
              Org admin
            </div>
          </div>
          <div className="flex items-center justify-between gap-3 px-4 pb-4 pt-1 lg:px-6">
            <div className="min-w-0">
              <h1 className="truncate text-[20px] font-semibold leading-tight text-surface-100">
                {pageTitle}
              </h1>
              {pageSubtitle && (
                <div className="mt-0.5 truncate text-[12px] text-surface-400">
                  {pageSubtitle}
                </div>
              )}
            </div>
            {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
          </div>
        </header>

        <main className="flex-1 px-4 py-5 lg:px-6 lg:py-6">{children}</main>
      </div>
    </div>
  );
}
