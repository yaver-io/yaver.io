"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { browserSupportsWebAuthn, startAuthentication } from "@simplewebauthn/browser";
import { CONVEX_URL } from "@/lib/constants";

export type DeviceCodeInfo = null | {
  machineName: string | null;
  platform: string | null;
  arch: string | null;
  shell: string | null;
  environment: string | null;
  runtimeVersion: string | null;
  preferredProvider: string | null;
  isWsl: boolean;
  expiresAt: number;
  status?: "pending" | "authorized" | "expired";
};

const expiredCodeMessage = "This code has expired. Run 'yaver auth --headless' again to get a new code.";
const invalidOrExpiredCodeMessage = "This code is invalid or has expired. Run 'yaver auth --headless' again to get a fresh code.";
const authorizeTimeoutMessage = "Authorization timed out. If the terminal has stopped waiting, run 'yaver auth --headless' again to get a fresh code.";

export default function DeviceCodeClient({
  initialCode = "",
  initialDeviceInfo = null,
  initialConvexUrl = CONVEX_URL,
}: {
  initialCode?: string;
  initialDeviceInfo?: DeviceCodeInfo;
  initialConvexUrl?: string;
}) {
  const params = useSearchParams();
  const prefillCode = params.get("code") || initialCode;
  const convexUrl = params.get("convex") || initialConvexUrl;
  const alreadyAuthorized = params.get("authorized") === "1";
  const providerHint = (params.get("provider") || "").toLowerCase();
  const [deviceInfo, setDeviceInfo] = useState<DeviceCodeInfo>(initialDeviceInfo);
  const [code, setCode] = useState(prefillCode);
  const [status, setStatus] = useState<"idle" | "loading" | "success" | "error">(
    alreadyAuthorized || initialDeviceInfo?.status === "authorized" ? "success" : "idle"
  );
  const [errorMsg, setErrorMsg] = useState(
    initialDeviceInfo?.status === "expired"
      ? expiredCodeMessage
      : ""
  );
  const inputRef = useRef<HTMLInputElement>(null);

  const [token, setToken] = useState<string | null>(null);
  const [checking, setChecking] = useState(true);
  const [preferredProvider, setPreferredProvider] = useState<"apple" | "github" | "google" | "microsoft" | "gitlab" | null>(null);
  const [passkeySupported, setPasskeySupported] = useState(false);
  const [passkeyLoading, setPasskeyLoading] = useState(false);
  const [passkeyError, setPasskeyError] = useState<string | null>(null);

  useEffect(() => {
    setPasskeySupported(browserSupportsWebAuthn());
  }, []);

  // Headless-machine authorization needs the user to be signed in on
  // *this* browser. Passkey works the same as OAuth here: success →
  // store the token → render the authorize-this-device card.
  // Mirrors the elapsed-time heuristic from /auth so a missing
  // credential doesn't appear as a silent revert.
  const handlePasskeyLogin = async () => {
    setPasskeyError(null);
    setPasskeyLoading(true);
    try {
      const startRes = await fetch(`${convexUrl}/auth/passkey/login/start`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      if (!startRes.ok) {
        setPasskeyError((await startRes.text()) || "Could not start passkey sign-in.");
        return;
      }
      const { options } = await startRes.json();

      let asseResp;
      const sheetStartedAt = Date.now();
      try {
        asseResp = await startAuthentication({ optionsJSON: options });
      } catch (err: any) {
        const looksLikeCancel = err?.name === "NotAllowedError" || err?.name === "AbortError";
        const elapsed = Date.now() - sheetStartedAt;
        if (looksLikeCancel) {
          if (elapsed < 800) {
            setPasskeyError(
              "No passkey found on this browser. Use an OAuth provider below, or sign in with passkey on yaver.io first.",
            );
          }
          return;
        }
        setPasskeyError(err?.message || "Passkey sign-in failed.");
        return;
      }

      const finishRes = await fetch(`${convexUrl}/auth/passkey/login/finish`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ response: asseResp }),
      });
      if (!finishRes.ok) {
        setPasskeyError((await finishRes.text()) || "Passkey verification failed.");
        return;
      }
      const data = await finishRes.json();
      const issuedToken = data?.token;
      if (!issuedToken) {
        setPasskeyError("No token received from server.");
        return;
      }
      localStorage.setItem("yaver_auth_token", issuedToken);
      document.cookie = `yaver_auth_token=${issuedToken}; path=/; max-age=${60 * 60 * 24 * 30}; secure; samesite=lax`;
      setToken(issuedToken);
    } catch {
      setPasskeyError("Network error. Please try again.");
    } finally {
      setPasskeyLoading(false);
    }
  };

  useEffect(() => {
    const stored = localStorage.getItem("yaver_auth_token");
    if (stored) {
      fetch(`${convexUrl}/auth/validate`, {
        headers: { Authorization: `Bearer ${stored}` },
      })
        .then((res) => {
          if (res.ok) setToken(stored);
          setChecking(false);
        })
        .catch(() => setChecking(false));
    } else {
      setChecking(false);
    }
  }, [convexUrl]);

  useEffect(() => {
    if (!prefillCode) return;
    if (status === "success") return;

    let cancelled = false;
    const poll = async () => {
      try {
        const res = await fetch(`${convexUrl}/auth/device-code/info?user_code=${encodeURIComponent(prefillCode)}`, {
          cache: "no-store",
        });
        if (!res.ok) {
          if (res.status === 404 || res.status === 410) {
            setStatus("error");
            setErrorMsg(invalidOrExpiredCodeMessage);
          }
          return;
        }
        const data = await res.json();
        if (cancelled || !data) return;
        setDeviceInfo(data);
        if (data.status === "authorized") {
          setStatus("success");
          setErrorMsg("");
        } else if (data.status === "expired") {
          setStatus("error");
          setErrorMsg(expiredCodeMessage);
        }
      } catch {
        // Keep the current UI; polling is best-effort only.
      }
    };

    void poll();
    const id = window.setInterval(poll, 1500);
    return () => {
      cancelled = true;
      window.clearInterval(id);
    };
  }, [convexUrl, prefillCode, status]);

  useEffect(() => {
    const hintedProvider = providerHint || deviceInfo?.preferredProvider || "";
    if (
      hintedProvider === "apple" ||
      hintedProvider === "github" ||
      hintedProvider === "google" ||
      hintedProvider === "microsoft" ||
      hintedProvider === "gitlab"
    ) {
      setPreferredProvider(hintedProvider);
      return;
    }
    if (typeof navigator === "undefined") return;
    const ua = navigator.userAgent || "";
    setPreferredProvider(/iPhone|iPad|Macintosh|Mac OS X/i.test(ua) ? "apple" : "github");
  }, [deviceInfo?.preferredProvider, providerHint]);

  useEffect(() => {
    if (prefillCode && token && status === "idle") {
      handleAuthorize(prefillCode, token);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [prefillCode, token]);

  useEffect(() => {
    if (alreadyAuthorized) {
      setStatus("success");
      setErrorMsg("");
    }
  }, [alreadyAuthorized]);

  const authUrlFor = (provider: "apple" | "github" | "google" | "microsoft" | "gitlab") => {
    const qs = new URLSearchParams({ client: "web" });
    const returnParams = new URLSearchParams();
    if (prefillCode) returnParams.set("code", prefillCode);
    if (convexUrl) returnParams.set("convex", convexUrl);
    if (providerHint) returnParams.set("provider", providerHint);
    const returnQuery = returnParams.toString();
    const returnUrl = `/auth/device${returnQuery ? `?${returnQuery}` : ""}`;
    qs.set("return", returnUrl);
    return `/api/auth/oauth/${provider}?${qs.toString()}`;
  };

  const providers: Array<{
    key: "apple" | "github" | "google" | "microsoft" | "gitlab";
    label: string;
    href: string;
    icon: ReactNode;
    primary?: boolean;
  }> = [
    {
      key: "apple",
      label: "Continue with Apple",
      href: authUrlFor("apple"),
      primary: preferredProvider === "apple",
      icon: (
        <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
          <path d="M17.05 20.28c-.98.95-2.05.88-3.08.4-1.09-.5-2.08-.48-3.24 0-1.44.62-2.2.44-3.06-.4C2.79 15.25 3.51 7.59 9.05 7.31c1.35.07 2.29.74 3.08.8 1.18-.24 2.31-.93 3.57-.84 1.51.12 2.65.72 3.4 1.8-3.12 1.87-2.38 5.98.48 7.13-.57 1.5-1.31 2.99-2.54 4.09zM12.03 7.25c-.15-2.23 1.66-4.07 3.74-4.25.29 2.58-2.34 4.5-3.74 4.25z" />
        </svg>
      ),
    },
    {
      key: "github",
      label: "Continue with GitHub",
      href: authUrlFor("github"),
      primary: preferredProvider === "github",
      icon: (
        <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
          <path d="M12 .5C5.65.5.5 5.7.5 12.12c0 5.14 3.3 9.5 7.88 11.03.58.11.79-.25.79-.57 0-.28-.01-1.02-.02-2-3.2.71-3.88-1.57-3.88-1.57-.52-1.35-1.28-1.71-1.28-1.71-1.04-.73.08-.72.08-.72 1.15.08 1.75 1.19 1.75 1.19 1.02 1.77 2.67 1.26 3.32.96.1-.76.4-1.27.73-1.56-2.56-.29-5.26-1.3-5.26-5.77 0-1.27.45-2.31 1.18-3.13-.12-.29-.51-1.47.11-3.06 0 0 .97-.32 3.18 1.2a10.9 10.9 0 0 1 5.8 0c2.2-1.52 3.17-1.2 3.17-1.2.63 1.59.24 2.77.12 3.06.73.82 1.18 1.86 1.18 3.13 0 4.48-2.7 5.48-5.28 5.77.41.36.78 1.08.78 2.18 0 1.58-.02 2.85-.02 3.24 0 .32.2.69.8.57A11.63 11.63 0 0 0 23.5 12.12C23.5 5.7 18.35.5 12 .5z" />
        </svg>
      ),
    },
    {
      key: "google",
      label: "Continue with Google",
      href: authUrlFor("google"),
      primary: preferredProvider === "google",
      icon: (
        <svg className="h-5 w-5" viewBox="0 0 24 24">
          <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 01-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" />
          <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" />
          <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" />
          <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" />
        </svg>
      ),
    },
    {
      key: "microsoft",
      label: "Continue with Microsoft",
      href: authUrlFor("microsoft"),
      primary: preferredProvider === "microsoft",
      icon: (
        <svg className="h-5 w-5" viewBox="0 0 24 24">
          <path fill="#F25022" d="M1 1h10v10H1z" />
          <path fill="#00A4EF" d="M1 13h10v10H1z" />
          <path fill="#7FBA00" d="M13 1h10v10H13z" />
          <path fill="#FFB900" d="M13 13h10v10H13z" />
        </svg>
      ),
    },
    {
      key: "gitlab",
      label: "Continue with GitLab",
      href: authUrlFor("gitlab"),
      primary: preferredProvider === "gitlab",
      icon: <span className="inline-flex h-5 w-5 items-center justify-center rounded-full bg-orange-500/15 text-[10px] font-semibold text-orange-700 dark:text-orange-300">GL</span>,
    },
  ];
  const orderedProviders = [...providers].sort((a, b) => Number(!!b.primary) - Number(!!a.primary));

  const devicePlatformLabel = (() => {
    if (!deviceInfo?.platform) return null;
    switch (deviceInfo.platform) {
      case "wsl1":
        return "WSL1";
      case "wsl2":
        return "WSL2";
      case "darwin":
        return "macOS";
      case "linux":
        return "Linux";
      case "windows":
        return "Windows";
      default:
        return deviceInfo.platform;
    }
  })();
  const codeNeedsRefresh =
    deviceInfo?.status === "expired" ||
    errorMsg === expiredCodeMessage ||
    errorMsg === invalidOrExpiredCodeMessage ||
    errorMsg === authorizeTimeoutMessage;

  const handleAuthorize = async (userCode: string, authToken: string) => {
    const cleaned = userCode.toUpperCase().replace(/[^A-Z0-9]/g, "");
    if (cleaned.length < 8) {
      setErrorMsg("Code must be 8 characters (e.g. ABCD-1234)");
      setStatus("error");
      return;
    }
    const formatted = cleaned.slice(0, 4) + "-" + cleaned.slice(4, 8);

    setStatus("loading");
    setErrorMsg("");

    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 12000);
    try {
      const res = await fetch(`/api/auth/device/authorize`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${authToken}`,
        },
        signal: controller.signal,
        body: JSON.stringify({ userCode: formatted, convexUrl }),
      });
      window.clearTimeout(timeout);

      if (!res.ok) {
        const data = await res.json().catch(() => ({ error: "Unknown error" }));
        if (res.status === 404) {
          setErrorMsg("Invalid code. Check the code in your terminal and try again.");
        } else if (res.status === 410) {
          setErrorMsg(expiredCodeMessage);
        } else if (res.status === 409) {
          setErrorMsg("This code has already been used.");
        } else {
          setErrorMsg(data.error || "Something went wrong.");
        }
        setStatus("error");
        return;
      }

      setStatus("success");
    } catch (error) {
      window.clearTimeout(timeout);
      if (error instanceof DOMException && error.name === "AbortError") {
        setErrorMsg(authorizeTimeoutMessage);
      } else {
        setErrorMsg("Could not reach Yaver. Check your connection and try again.");
      }
      setStatus("error");
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!token) return;
    handleAuthorize(code, token);
  };

  const handleCodeChange = (val: string) => {
    const stripped = val.toUpperCase().replace(/[^A-Z0-9]/g, "");
    if (stripped.length <= 4) {
      setCode(stripped);
    } else {
      setCode(stripped.slice(0, 4) + "-" + stripped.slice(4, 8));
    }
    if (status === "error") setStatus("idle");
  };

  if (checking) {
    return (
      <div className="flex min-h-[70vh] items-center justify-center">
        <span className="text-surface-500 text-sm">Loading...</span>
      </div>
    );
  }

  if (status === "success") {
    return <DeviceAuthorizedSuccess machineName={deviceInfo?.machineName ?? null} />;
  }

  if (!token) {
    const returnUrl = `/auth/device${prefillCode ? `?code=${prefillCode}` : ""}`;
    const authUrl = `/auth?client=web&return=${encodeURIComponent(returnUrl)}`;
    if (codeNeedsRefresh) {
      return (
        <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
          <div className="w-full max-w-md">
            <div className="mb-8 text-center">
              <span className="text-2xl font-bold tracking-tight text-surface-50">
                yaver<span className="font-normal text-surface-500">.io</span>
              </span>
              <p className="mt-3 text-sm text-surface-500">
                Remote machine authorization
              </p>
            </div>
            <div className="rounded-2xl border border-amber-500/20 bg-amber-500/10 px-5 py-5">
              <div className="text-xs font-semibold uppercase tracking-[0.18em] text-amber-700 dark:text-amber-300">
                Code expired
              </div>
              <p className="mt-3 text-sm leading-relaxed text-surface-200">
                {errorMsg || expiredCodeMessage}
              </p>
              {prefillCode ? (
                <div className="mt-4 rounded-xl border border-surface-700 bg-surface-950/70 px-4 py-3 text-center">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-surface-500">Expired Device Code</div>
                  <div className="mt-1 font-mono text-2xl font-bold tracking-[0.28em] text-surface-50">
                    {prefillCode}
                  </div>
                </div>
              ) : null}
              <div className="mt-5 rounded-xl border border-surface-800 bg-surface-950/70 px-4 py-4 text-sm text-surface-300">
                <div className="font-semibold text-surface-50">Next step</div>
                <p className="mt-2">
                  Go back to the terminal on the remote machine and run{" "}
                  <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver auth --headless</code>{" "}
                  again to generate a fresh code.
                </p>
              </div>
            </div>
          </div>
        </div>
      );
    }
    return (
      <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
        <div className="w-full max-w-md">
          <div className="mb-8 text-center">
            <span className="text-2xl font-bold tracking-tight text-surface-50">
              yaver<span className="font-normal text-surface-500">.io</span>
            </span>
            <div className="mx-auto mt-6 max-w-sm rounded-2xl border border-indigo-500/20 bg-indigo-500/10 px-4 py-4 text-left">
              <div className="text-xs font-semibold uppercase tracking-[0.18em] text-indigo-700 dark:text-indigo-300">
                Authorize Remote Machine
              </div>
              <p className="mt-2 text-sm text-surface-200">
                You are authorizing a waiting Yaver machine. This browser only completes sign-in and sends the result back to the remote device.
              </p>
              {prefillCode ? (
                <div className="mt-4 rounded-xl border border-surface-700 bg-surface-950/70 px-4 py-3 text-center">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-surface-500">Device Code</div>
                  <div className="mt-1 font-mono text-2xl font-bold tracking-[0.28em] text-surface-50">
                    {prefillCode}
                  </div>
                </div>
              ) : (
                <p className="mt-4 text-sm text-surface-400">
                  Get a code from <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver auth --headless</code>, then come back here.
                </p>
              )}
              {deviceInfo && (
                <div className="mt-4 rounded-xl border border-surface-800 bg-surface-950/70 px-4 py-3 text-sm text-surface-300">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-surface-500">Waiting Machine</div>
                  <div className="mt-2 font-semibold text-surface-50">
                    {deviceInfo.machineName || "Unnamed machine"}
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2 text-xs">
                    {devicePlatformLabel && (
                      <span className="rounded-full border border-surface-700 px-2 py-1 text-surface-300">
                        {devicePlatformLabel}
                      </span>
                    )}
                    {deviceInfo.arch && (
                      <span className="rounded-full border border-surface-700 px-2 py-1 text-surface-300">
                        {deviceInfo.arch}
                      </span>
                    )}
                    {deviceInfo.shell && (
                      <span className="rounded-full border border-surface-700 px-2 py-1 text-surface-300">
                        {deviceInfo.shell.split("/").pop()}
                      </span>
                    )}
                    {deviceInfo.preferredProvider && (
                      <span className="rounded-full border border-indigo-400/30 px-2 py-1 text-indigo-700 dark:text-indigo-200">
                        prefers {deviceInfo.preferredProvider}
                      </span>
                    )}
                  </div>
                  {deviceInfo.platform === "wsl1" && (
                    <div className="mt-3 rounded-lg border border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-200">
                      WSL1 detected. Yaver requires WSL2 for most mobile and React Native features.
                    </div>
                  )}
                </div>
              )}
              <div className="mt-4 space-y-1 text-xs text-surface-400">
                <p>1. Sign in with Apple, Google, or Microsoft.</p>
                <p>2. Yaver returns here and authorizes the waiting machine automatically.</p>
                <p>3. The remote machine finishes sign-in without opening a browser.</p>
              </div>
            </div>
          </div>

          {passkeySupported && (
            <div className="mb-3 space-y-2">
              <button
                type="button"
                onClick={handlePasskeyLogin}
                disabled={passkeyLoading}
                className="flex w-full items-center justify-center gap-3 rounded-lg border border-indigo-400/40 bg-indigo-500/15 px-4 py-3 text-sm font-medium text-surface-50 transition-colors hover:border-indigo-300 hover:bg-indigo-500/20 disabled:cursor-wait disabled:opacity-60"
              >
                <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <circle cx="8" cy="11" r="4" />
                  <path d="m11 13 7 7" />
                  <path d="m15 16 2 2" />
                </svg>
                {passkeyLoading ? "Waiting for passkey…" : "Sign in with passkey"}
              </button>
              {passkeyError && (
                <p className="text-xs text-rose-700 dark:text-rose-300" role="alert">{passkeyError}</p>
              )}
            </div>
          )}

          <div className="space-y-3">
            {orderedProviders.map((provider) => (
              <Link
                key={provider.key}
                href={provider.href}
                className={`flex w-full items-center justify-center gap-3 rounded-lg px-4 py-3 text-sm font-medium transition-colors ${
                  provider.primary
                    ? "border border-indigo-400/40 bg-indigo-500/15 text-surface-50 hover:border-indigo-300 hover:bg-indigo-500/20"
                    : "border border-surface-700 bg-surface-900 text-surface-200 hover:border-surface-600 hover:text-surface-50"
                }`}
              >
                {provider.icon}
                {provider.label}
                {provider.primary && (
                  <span className="rounded-full border border-indigo-500/40 bg-indigo-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.16em] text-indigo-700 dark:border-indigo-300/30 dark:bg-transparent dark:text-indigo-200">
                    Recommended
                  </span>
                )}
              </Link>
            ))}
          </div>

          <div className="my-6 flex items-center gap-3">
            <div className="h-px flex-1 bg-surface-800" />
            <span className="text-xs text-surface-600">or</span>
            <div className="h-px flex-1 bg-surface-800" />
          </div>

          <div className="text-center">
            <p className="text-sm text-surface-500">
              Prefer email/password? Sign in first, then return to authorize this machine.
            </p>
            <Link href={authUrl} className="mt-4 inline-block rounded-lg bg-surface-50 px-6 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200">
              Sign In with Email
            </Link>
          </div>

          <p className="mt-6 text-center text-xs text-surface-600">
            After login, Yaver comes back here and links the remote machine automatically.
          </p>
        </div>
      </div>
    );
  }

  if (codeNeedsRefresh) {
    return (
      <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
        <div className="w-full max-w-sm">
          <div className="mb-8 text-center">
            <span className="text-2xl font-bold tracking-tight text-surface-50">
              yaver<span className="font-normal text-surface-500">.io</span>
            </span>
            <p className="mt-3 text-sm text-surface-500">
              Remote machine authorization
            </p>
          </div>
          <div className="rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-4 text-sm text-amber-700 dark:text-amber-200">
            {errorMsg || expiredCodeMessage}
          </div>
          {prefillCode ? (
            <div className="mt-4 rounded-xl border border-surface-700 bg-surface-950/70 px-4 py-3 text-center">
              <div className="text-[11px] uppercase tracking-[0.18em] text-surface-500">Expired Device Code</div>
              <div className="mt-1 font-mono text-2xl font-bold tracking-[0.28em] text-surface-50">
                {prefillCode}
              </div>
            </div>
          ) : null}
          <p className="mt-6 text-center text-sm text-surface-400">
            Generate a fresh code from the terminal with{" "}
            <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-300">yaver auth --headless</code>.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 py-20">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <span className="text-2xl font-bold tracking-tight text-surface-50">
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
          <p className="mt-3 text-sm text-surface-500">
            Enter the code shown in your terminal
          </p>
        </div>

        {errorMsg && (
          <div className="mb-6 rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-400">
            {errorMsg}
          </div>
        )}

        <form onSubmit={handleSubmit}>
          <input
            ref={inputRef}
            type="text"
            value={code}
            onChange={(e) => handleCodeChange(e.target.value)}
            placeholder="ABCD-1234"
            maxLength={9}
            autoFocus
            autoComplete="off"
            spellCheck={false}
            className="w-full rounded-lg border border-surface-700 bg-surface-900 px-4 py-4 text-center text-2xl font-mono font-bold tracking-[0.3em] text-surface-100 placeholder-surface-600 outline-none transition-colors focus:border-surface-500"
          />

          <button
            type="submit"
            disabled={status === "loading" || code.replace(/-/g, "").length < 8}
            className="mt-4 w-full rounded-lg bg-surface-50 px-4 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
          >
            {status === "loading" ? "Authorizing..." : "Authorize Device"}
          </button>
        </form>

        <p className="mt-6 text-center text-xs text-surface-600">
          Run <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-400">yaver auth --headless</code> to get a code
        </p>
        <p className="mt-2 text-center text-xs text-surface-600">
          Already signed in on another machine? Skip the OAuth flow entirely:<br />
          <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-400">yaver auth pair</code> on the headless box,
          then <code className="rounded bg-surface-800 px-1.5 py-0.5 text-surface-400">yaver auth send &lt;code&gt; &lt;url&gt;</code> from the signed-in machine.
        </p>
      </div>
    </div>
  );
}

// Detects iOS / Android from the user-agent so the success screen can
// suggest the right app store. Runs client-side only, safe if it
// returns "other" (we just show both buttons).
function detectMobileOS(): "ios" | "android" | "other" {
  if (typeof navigator === "undefined") return "other";
  const ua = navigator.userAgent || "";
  if (/iPhone|iPad|iPod/i.test(ua)) return "ios";
  if (/Android/i.test(ua)) return "android";
  return "other";
}

// DeviceAuthorizedSuccess is the post-OAuth confirmation. It does two
// jobs at once:
//
//   1. Tell the user the terminal handoff is already complete — their
//      coding agent at home is now signed in, no further action needed.
//   2. Hand them the Yaver mobile app so they don't have to figure out
//      "so what do I install next?". The cousin-at-a-cafe path lands
//      here right after tapping a single link; making him root around
//      for the App Store would break the "pasted one sentence, done"
//      seamlessness we built the whole resumable-auth flow for.
//
// On iOS we send the user to the official Yaver download page; on
// Android, Play; otherwise both.
function DeviceAuthorizedSuccess({ machineName }: { machineName: string | null }) {
  const [os, setOs] = useState<"ios" | "android" | "other">("other");
  useEffect(() => {
    setOs(detectMobileOS());
  }, []);

  const iosHref = "https://apps.apple.com/us/app/yaver-io/id6760467669";
  const playHref = "https://play.google.com/store/apps/details?id=io.yaver.mobile";

  return (
    <div className="flex min-h-[70vh] items-center justify-center px-6 py-14">
      <div className="w-full max-w-md">
        <div className="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-full bg-green-500/10">
          <svg className="h-8 w-8 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" />
          </svg>
        </div>
        <h2 className="text-center text-2xl font-bold text-surface-50">
          {machineName ? `${machineName} is signed in` : "Machine signed in"}
        </h2>
        <p className="mt-3 text-center text-sm text-surface-400">
          Your terminal picked up the token automatically. You can close this
          tab and go back to your coding agent — it will continue from here.
        </p>

        <div className="mt-8 rounded-2xl border border-indigo-500/30 bg-indigo-500/5 p-5">
          <div className="text-xs font-semibold uppercase tracking-[0.16em] text-indigo-700 dark:text-indigo-300">
            On this phone
          </div>
          <h3 className="mt-2 text-base font-semibold text-surface-50">
            Open the Yaver app
          </h3>
          <p className="mt-2 text-sm leading-relaxed text-surface-400">
            If you already have the Yaver app, open it and sign in with the{" "}
            <em>same</em> OAuth provider you just used. Your dev machine should
            appear in the device list within seconds.
          </p>
          <p className="mt-2 text-sm leading-relaxed text-surface-500">
            If the app is not installed on this phone yet, you can install it
            below.
          </p>

          <div className="mt-4 flex flex-col gap-2">
            {(os === "ios" || os === "other") && (
              <a
                href={iosHref}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center justify-center gap-2 rounded-xl bg-surface-50 px-4 py-3 text-sm font-semibold text-surface-950 transition-colors hover:bg-surface-200"
              >
                <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M17.05 20.28c-.98.95-2.05.88-3.08.4-1.09-.5-2.08-.48-3.24 0-1.44.62-2.2.44-3.06-.4C2.79 15.25 3.51 7.59 9.05 7.31c1.35.07 2.29.74 3.08.8 1.18-.24 2.31-.93 3.57-.84 1.51.12 2.65.72 3.4 1.8-3.12 1.87-2.38 5.98.48 7.13-.57 1.5-1.31 2.99-2.54 4.09zM12.03 7.25c-.15-2.23 1.66-4.07 3.74-4.25.29 2.58-2.34 4.5-3.74 4.25z" />
                </svg>
                Get Yaver for iPhone
              </a>
            )}
            {(os === "android" || os === "other") && (
              <a
                href={playHref}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center justify-center gap-2 rounded-xl bg-surface-800 px-4 py-3 text-sm font-semibold text-surface-50 transition-colors hover:bg-surface-700"
              >
                <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M3.609 1.814l10.14 10.151-10.14 10.151c-.366-.19-.609-.57-.609-1.007V2.821c0-.437.243-.817.609-1.007zm11.04 11.04l2.614 2.614-11.69 6.753 9.076-9.367zm0-2l-9.076-9.367 11.69 6.753-2.614 2.614zm4.01 1l3.41 1.97a1.166 1.166 0 0 1 0 2.03l-3.41 1.97-2.853-2.985 2.853-2.985z" />
                </svg>
                Install on Android (Play)
              </a>
            )}
          </div>

          <p className="mt-4 text-[11px] leading-relaxed text-surface-500">
            Same email, any provider: if you signed in above with Apple,
            Google, or Microsoft, use the same one in the app. Your machine
            appears automatically — no re-pairing, no passwords.
          </p>
        </div>
      </div>
    </div>
  );
}
