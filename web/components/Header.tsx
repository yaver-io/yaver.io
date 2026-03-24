"use client";

import Link from "next/link";
import { useState, useRef, useEffect } from "react";
import { useTheme } from "./ThemeProvider";
import SearchBar from "./SearchBar";
import { useAuth } from "@/lib/use-auth";

function UserMenu({ user, logout }: { user: { email: string; name?: string; avatarUrl?: string }; logout: () => void }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  const initials = (user.name || user.email)
    .split(" ")
    .map((w) => w[0])
    .slice(0, 2)
    .join("")
    .toUpperCase();

  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-2 rounded-full transition-opacity hover:opacity-80"
        aria-label="User menu"
      >
        {user.avatarUrl ? (
          <img
            src={user.avatarUrl}
            alt=""
            className="h-8 w-8 rounded-full border border-surface-700"
            referrerPolicy="no-referrer"
          />
        ) : (
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-surface-700 text-xs font-semibold text-surface-200">
            {initials}
          </div>
        )}
      </button>

      {open && (
        <div className="absolute right-0 top-full mt-2 w-56 rounded-lg border border-surface-800 bg-surface-950 py-1 shadow-xl">
          <div className="border-b border-surface-800 px-4 py-3">
            {user.name && (
              <p className="text-sm font-medium text-surface-200">{user.name}</p>
            )}
            <p className="truncate text-xs text-surface-500">{user.email}</p>
          </div>
          <Link
            href="/dashboard"
            className="flex items-center gap-2 px-4 py-2.5 text-sm text-surface-400 transition-colors hover:bg-surface-900 hover:text-surface-200"
            onClick={() => setOpen(false)}
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75a2.25 2.25 0 012.25-2.25H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z" />
            </svg>
            Account
          </Link>
          <button
            onClick={() => { setOpen(false); logout(); }}
            className="flex w-full items-center gap-2 px-4 py-2.5 text-sm text-surface-400 transition-colors hover:bg-surface-900 hover:text-surface-200"
          >
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15m3 0l3-3m0 0l-3-3m3 3H9" />
            </svg>
            Sign out
          </button>
        </div>
      )}
    </div>
  );
}

export default function Header() {
  const [mobileOpen, setMobileOpen] = useState(false);
  const { theme, toggle } = useTheme();
  const { user, isLoading, isAuthenticated, logout } = useAuth();

  return (
    <header className="sticky top-0 z-50 border-b border-surface-800/60 bg-surface-950/80 backdrop-blur-xl">
      <nav className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4">
        <div className="flex items-center gap-3">
          <Link href="/" className="flex items-center gap-2.5">
            <span className="text-xl font-bold tracking-tight text-surface-50">
              yaver<span className="font-normal text-surface-500">.io</span>
            </span>
          </Link>
        </div>

        <div className="hidden items-center gap-8 md:flex">
          {!isLoading && isAuthenticated && user ? (
            <>
              <Link href="/dashboard" className="text-sm text-surface-400 transition-colors hover:text-surface-50">
                Account
              </Link>
              <button
                onClick={toggle}
                className="rounded-lg p-2 text-surface-400 transition-colors hover:bg-surface-900 hover:text-surface-50"
                aria-label="Toggle theme"
              >
                {theme === "dark" ? (
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" />
                  </svg>
                ) : (
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z" />
                  </svg>
                )}
              </button>
              <UserMenu user={user} logout={logout} />
            </>
          ) : (
            <>
              <Link href="/faq" className="text-sm text-surface-400 transition-colors hover:text-surface-50">
                FAQ
              </Link>
              <Link href="/docs" className="text-sm text-surface-400 transition-colors hover:text-surface-50">
                Docs
              </Link>
              <Link href="/docs/developers" className="text-sm text-surface-400 transition-colors hover:text-surface-50">
                Developers
              </Link>
              <Link href="/download" className="text-sm text-surface-400 transition-colors hover:text-surface-50">
                Download
              </Link>
              <Link href="/integrations" className="text-sm text-surface-400 transition-colors hover:text-surface-50">
                Integrations
              </Link>
              <SearchBar />
              <button
                onClick={toggle}
                className="rounded-lg p-2 text-surface-400 transition-colors hover:bg-surface-900 hover:text-surface-50"
                aria-label="Toggle theme"
              >
                {theme === "dark" ? (
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" />
                  </svg>
                ) : (
                  <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z" />
                  </svg>
                )}
              </button>
              <Link href="/auth" className="btn-primary px-5 py-2 text-sm">
                Log in
              </Link>
            </>
          )}
        </div>

        <div className="flex items-center gap-2 md:hidden">
          <button
            onClick={toggle}
            className="rounded-lg p-2 text-surface-400 transition-colors hover:text-surface-50"
            aria-label="Toggle theme"
          >
            {theme === "dark" ? (
              <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 3v2.25m6.364.386l-1.591 1.591M21 12h-2.25m-.386 6.364l-1.591-1.591M12 18.75V21m-4.773-4.227l-1.591 1.591M5.25 12H3m4.227-4.773L5.636 5.636M15.75 12a3.75 3.75 0 11-7.5 0 3.75 3.75 0 017.5 0z" />
              </svg>
            ) : (
              <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M21.752 15.002A9.718 9.718 0 0118 15.75c-5.385 0-9.75-4.365-9.75-9.75 0-1.33.266-2.597.748-3.752A9.753 9.753 0 003 11.25C3 16.635 7.365 21 12.75 21a9.753 9.753 0 009.002-5.998z" />
              </svg>
            )}
          </button>
          {!isLoading && isAuthenticated && user ? (
            <UserMenu user={user} logout={logout} />
          ) : (
            <button
              className="text-surface-400 hover:text-surface-50"
              onClick={() => setMobileOpen(!mobileOpen)}
              aria-label="Toggle menu"
            >
              <svg className="h-6 w-6" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                {mobileOpen ? (
                  <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
                ) : (
                  <path strokeLinecap="round" strokeLinejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
                )}
              </svg>
            </button>
          )}
        </div>
      </nav>

      {mobileOpen && !isAuthenticated && (
        <div className="border-t border-surface-800 bg-surface-950 px-6 py-4 md:hidden">
          <div className="flex flex-col gap-4">
            <Link href="/faq" className="text-sm text-surface-400 hover:text-surface-50" onClick={() => setMobileOpen(false)}>FAQ</Link>
            <Link href="/docs" className="text-sm text-surface-400 hover:text-surface-50" onClick={() => setMobileOpen(false)}>Docs</Link>
            <Link href="/docs/developers" className="text-sm text-surface-400 hover:text-surface-50" onClick={() => setMobileOpen(false)}>Developers</Link>
            <Link href="/download" className="text-sm text-surface-400 hover:text-surface-50" onClick={() => setMobileOpen(false)}>Download</Link>
            <Link href="/integrations" className="text-sm text-surface-400 hover:text-surface-50" onClick={() => setMobileOpen(false)}>Integrations</Link>
            <Link href="/auth" className="btn-primary text-center text-sm" onClick={() => setMobileOpen(false)}>Log in</Link>
          </div>
        </div>
      )}
    </header>
  );
}
