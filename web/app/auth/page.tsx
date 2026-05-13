"use client";

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { startAuthentication, startRegistration, browserSupportsWebAuthn } from "@simplewebauthn/browser";
import { CONVEX_URL } from "@/lib/constants";
import { hasRegisteredMachine } from "@/lib/onboarding";
import { sanitizeReturnTo } from "@/lib/oauth";

function AuthContent() {
  const params = useSearchParams();
  const error = params.get("error");
  const client = params.get("client") || "web";
  const isSdkPopup = client === "sdk";
  const returnUrl = sanitizeReturnTo(params.get("return"));
  const isDeviceAuth = !!returnUrl && returnUrl.startsWith("/auth/device");
  const pendingDeviceCode = isDeviceAuth ? new URLSearchParams(returnUrl.split("?")[1] || "").get("code") : null;

  const [mode, setMode] = useState<"signin" | "signup">("signin");
  const [fullName, setFullName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [rePassword, setRePassword] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [passkeyLoading, setPasskeyLoading] = useState(false);
  // Hide the passkey button on browsers that don't speak WebAuthn (very
  // old / privacy-mode locked configs). Existing OAuth + email flows are
  // unaffected — passkey is purely additive.
  const [passkeySupported, setPasskeySupported] = useState(false);
  useEffect(() => {
    setPasskeySupported(browserSupportsWebAuthn());
  }, []);

  const redirectAfterAuth = async (token: string) => {
    if (client === "desktop") {
      window.location.href = `http://127.0.0.1:19836/callback?token=${token}`;
      return;
    }

    if (returnUrl) {
      window.location.href = returnUrl;
      return;
    }

    try {
      const res = await fetch(`${CONVEX_URL}/auth/validate`, {
        method: "GET",
        headers: { Authorization: `Bearer ${token}` },
      });
      if (res.ok) {
        const data = await res.json();
        const raw = data.user ?? data;
        if (!(raw?.surveyCompleted ?? false)) {
          if (await hasRegisteredMachine(token)) {
            window.location.href = "/dashboard";
            return;
          }
          window.location.href = "/survey";
          return;
        }
      }
    } catch {
      // Best-effort check. Fall back to dashboard when validation fails.
    }

    window.location.href = "/dashboard";
  };

  const handlePasskeyLogin = async () => {
    setFormError(null);
    setPasskeyLoading(true);
    try {
      // 0. Preflight when the email field is filled — browsers fold
      //    "no credentials" into the same NotAllowedError as "user
      //    cancelled", so without this check the user sees the sheet
      //    auto-dismiss with no actionable feedback. Skip the preflight
      //    when no email was typed (true discoverable-credentials flow,
      //    user expects the picker to enumerate).
      if (email.trim()) {
        try {
          const probeRes = await fetch(
            `${CONVEX_URL}/auth/passkey/check?email=${encodeURIComponent(email.trim().toLowerCase())}`,
          );
          if (probeRes.ok) {
            const probe = (await probeRes.json()) as {
              hasPasskey: boolean;
              emailRegistered: boolean;
            };
            if (probe.emailRegistered && !probe.hasPasskey) {
              setFormError(
                "No passkey on this account yet. Sign in with an OAuth provider or email below, then add a passkey from Settings.",
              );
              setPasskeyLoading(false);
              return;
            }
            if (!probe.emailRegistered) {
              setFormError(
                "No account for that email. Sign up first with email/OAuth.",
              );
              setPasskeyLoading(false);
              return;
            }
          }
        } catch {
          // Network blip on the preflight — fall through to the live
          // sheet rather than block sign-in on a non-critical probe.
        }
      }

      // 1. Pull a fresh challenge from the backend.
      const startRes = await fetch(`${CONVEX_URL}/auth/passkey/login/start`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      if (!startRes.ok) {
        const text = await startRes.text();
        setFormError(text || "Could not start passkey sign-in.");
        setPasskeyLoading(false);
        return;
      }
      const { options } = await startRes.json();

      // 2. Have the browser sign the challenge with whichever passkey
      //    the user picks (or the auto-fill credential they already
      //    chose). Distinguish "user cancelled the sheet" (slow dismiss,
      //    > 800 ms) from "no credentials found / browser auto-dismissed"
      //    (fast dismiss, < 800 ms) — both surface as NotAllowedError.
      let asseResp;
      const sheetStartedAt = Date.now();
      try {
        asseResp = await startAuthentication({ optionsJSON: options });
      } catch (err: any) {
        const looksLikeCancel =
          err?.name === "NotAllowedError" || err?.name === "AbortError";
        const elapsed = Date.now() - sheetStartedAt;
        if (looksLikeCancel) {
          if (elapsed < 800) {
            setFormError(
              "No passkey found on this browser. Sign in with an OAuth provider or email below, then add a passkey from Settings.",
            );
          }
          setPasskeyLoading(false);
          return;
        }
        setFormError(err?.message || "Passkey sign-in failed.");
        setPasskeyLoading(false);
        return;
      }

      // 3. Verify the assertion server-side; on success a session token
      //    drops out, identical shape to /auth/login.
      const finishRes = await fetch(`${CONVEX_URL}/auth/passkey/login/finish`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ response: asseResp }),
      });
      if (!finishRes.ok) {
        const text = await finishRes.text();
        setFormError(text || "Passkey verification failed. Use email or OAuth instead.");
        setPasskeyLoading(false);
        return;
      }
      const data = await finishRes.json();
      const token = data?.token;
      if (!token) {
        setFormError("No token received from server.");
        setPasskeyLoading(false);
        return;
      }
      localStorage.setItem("yaver_auth_token", token);
      document.cookie = `yaver_auth_token=${token}; path=/; max-age=${60 * 60 * 24 * 30}; secure; samesite=lax`;
      await redirectAfterAuth(token);
    } catch {
      setFormError("Network error. Please try again.");
      setPasskeyLoading(false);
    }
  };

  const handlePasskeySignup = async () => {
    setFormError(null);
    if (!email.trim() || !email.includes("@")) {
      setFormError("Enter your email first.");
      return;
    }
    setPasskeyLoading(true);
    try {
      // 1. Start signup — server checks email is unused, returns
      //    options. EMAIL_EXISTS branches into a helpful redirect to
      //    sign-in instead of silent failure mid-Touch-ID.
      const startRes = await fetch(`${CONVEX_URL}/auth/passkey/signup/start`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, fullName }),
      });
      if (!startRes.ok) {
        const text = await startRes.text();
        setFormError(text || "Could not start passkey sign-up.");
        setPasskeyLoading(false);
        return;
      }
      const startData = await startRes.json();
      if (startData?.ok === false) {
        if (startData.error === "EMAIL_EXISTS") {
          // Mirror mobile: route the user to their existing sign-in
          // method when we have one. If the existing user has OAuth
          // providers, naming them explicitly turns this from a
          // dead-end into a one-tap link.
          const oauthProviders = Array.isArray(startData.providers)
            ? (startData.providers as string[]).filter((p) =>
                ["google", "microsoft", "apple", "github", "gitlab"].includes(p),
              )
            : [];
          if (startData.hasPasskey) {
            setFormError(
              "An account with that email already exists. Use 'Sign in with passkey' instead.",
            );
          } else if (oauthProviders.length > 0) {
            const labels = oauthProviders
              .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
              .join(" / ");
            setFormError(
              `An account with that email already exists. Sign in with ${labels} below — your passkey will be added to that account automatically.`,
            );
          } else {
            setFormError(
              "An account with that email already exists. Sign in with your existing method, then add a passkey from settings.",
            );
          }
        } else if (startData.error === "INVALID_EMAIL") {
          setFormError("Email looks invalid.");
        } else {
          setFormError("Could not start passkey sign-up.");
        }
        setPasskeyLoading(false);
        return;
      }

      // 2. Browser produces an attestation. Cancellation is the most
      //    common "error" — treat it silently.
      let attResp;
      try {
        attResp = await startRegistration({ optionsJSON: startData.options });
      } catch (err: any) {
        if (err?.name === "NotAllowedError" || err?.name === "AbortError") {
          setPasskeyLoading(false);
          return;
        }
        setFormError(err?.message || "Passkey sign-up cancelled.");
        setPasskeyLoading(false);
        return;
      }

      // 3. Verify on the server; success creates the user atomically
      //    and mints a session token (same shape as login).
      const finishRes = await fetch(`${CONVEX_URL}/auth/passkey/signup/finish`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, fullName, response: attResp }),
      });
      if (!finishRes.ok) {
        const text = await finishRes.text();
        setFormError(text || "Passkey sign-up failed.");
        setPasskeyLoading(false);
        return;
      }
      const data = await finishRes.json();
      const token = data?.token;
      if (!token) {
        setFormError("No token received from server.");
        setPasskeyLoading(false);
        return;
      }
      localStorage.setItem("yaver_auth_token", token);
      document.cookie = `yaver_auth_token=${token}; path=/; max-age=${60 * 60 * 24 * 30}; secure; samesite=lax`;
      await redirectAfterAuth(token);
    } catch {
      setFormError("Network error. Please try again.");
      setPasskeyLoading(false);
    }
  };

  const handleOAuth = (provider: "google" | "microsoft" | "apple" | "github" | "gitlab") => {
    const qs = new URLSearchParams({ client });
    if (returnUrl) {
      qs.set("return", returnUrl);
    }
    window.location.href = `/api/auth/oauth/${provider}?${qs.toString()}`;
  };

  const handleEmailSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);

    if (mode === "signup" && password !== rePassword) {
      setFormError("Passwords do not match.");
      return;
    }

    if (password.length < 8) {
      setFormError("Password must be at least 8 characters.");
      return;
    }

    setLoading(true);

    try {
      const endpoint = mode === "signup" ? "/auth/signup" : "/auth/login";
      const body: Record<string, string> = { email, password };
      if (mode === "signup") {
        body.fullName = fullName;
      }

      const res = await fetch(`${CONVEX_URL}${endpoint}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });

      if (!res.ok) {
        const text = await res.text();
        // EMAIL_EXISTS on signup → fetch the provider list and route the
        // user to "Continue with their existing OAuth" instead of dead-
        // ending. The backend auto-links the new identity by verified
        // email so they land on the same account.
        if (mode === "signup" && text.includes("EMAIL_EXISTS") && email.trim()) {
          try {
            const probeRes = await fetch(
              `${CONVEX_URL}/auth/email-providers?email=${encodeURIComponent(email.trim().toLowerCase())}`,
            );
            if (probeRes.ok) {
              const probe = (await probeRes.json()) as {
                exists: boolean;
                providers: string[];
                hasPasskey: boolean;
              };
              const oauthProviders = (probe.providers || []).filter((p) =>
                ["google", "microsoft", "apple", "github", "gitlab"].includes(p),
              );
              if (oauthProviders.length > 0) {
                const labels = oauthProviders
                  .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
                  .join(" / ");
                setFormError(
                  `An account with that email already exists. Sign in with ${labels} above — it'll be linked automatically.`,
                );
                setLoading(false);
                return;
              }
              if (probe.hasPasskey) {
                setFormError(
                  "An account with that email already exists. Use 'Sign in with passkey' above.",
                );
                setLoading(false);
                return;
              }
            }
          } catch {
            // ignore; fall through to generic error.
          }
        }
        setFormError(text || "Something went wrong. Please try again.");
        setLoading(false);
        return;
      }

      const data = await res.json();

      // 2FA required
      if (data.requires2fa && data.pendingToken) {
        const totpUrl = `/auth/totp?pendingToken=${data.pendingToken}&client=${client}`;
        window.location.href = returnUrl
          ? `${totpUrl}&return=${encodeURIComponent(returnUrl)}`
          : totpUrl;
        return;
      }

      const token = data.token;

      if (!token) {
        setFormError("No token received from server.");
        setLoading(false);
        return;
      }

      // Store token
      localStorage.setItem("yaver_auth_token", token);
      document.cookie = `yaver_auth_token=${token}; path=/; max-age=${60 * 60 * 24 * 30}; secure; samesite=lax`;

      await redirectAfterAuth(token);
    } catch {
      setFormError("Network error. Please try again.");
      setLoading(false);
    }
  };

  const displayError = formError || error;
  const containerClass = isSdkPopup
    ? "w-full max-w-[380px] rounded-3xl border border-surface-800 bg-surface-950/95 px-6 py-6 shadow-2xl shadow-black/40"
    : "w-full max-w-sm";
  const controlClass = isSdkPopup
    ? "rounded-xl px-4 py-3.5"
    : "rounded-lg px-4 py-3";

  return (
    <div className={`flex items-center justify-center px-6 ${isSdkPopup ? "min-h-screen py-8" : "min-h-[70vh] py-20"}`}>
      <div className={containerClass}>
        <div className={`${isSdkPopup ? "mb-6" : "mb-8"} text-center`}>
          <span className={`${isSdkPopup ? "text-xl" : "text-2xl"} font-bold tracking-tight text-surface-50`}>
            yaver<span className="font-normal text-surface-500">.io</span>
          </span>
          {isDeviceAuth && (
            <div className="mx-auto mt-6 max-w-sm rounded-2xl border border-indigo-500/20 bg-indigo-500/10 px-4 py-4 text-left">
              <div className="text-xs font-semibold uppercase tracking-[0.18em] text-indigo-300">
                Remote Device Authorization
              </div>
              <p className="mt-2 text-sm text-surface-200">
                You are signing in to authorize a waiting Yaver machine. After login, Yaver returns to the device page and completes the remote sign-in automatically.
              </p>
              {pendingDeviceCode && (
                <div className="mt-4 rounded-xl border border-surface-700 bg-surface-950/70 px-4 py-3 text-center">
                  <div className="text-[11px] uppercase tracking-[0.18em] text-surface-500">Device Code</div>
                  <div className="mt-1 font-mono text-xl font-bold tracking-[0.24em] text-surface-50">
                    {pendingDeviceCode}
                  </div>
                </div>
              )}
            </div>
          )}
          <p className={`mt-3 ${isSdkPopup ? "text-[13px]" : "text-sm"} text-surface-500`}>
            {isSdkPopup
              ? mode === "signin"
                ? "Sign in to connect this browser app to your Yaver machine."
                : "Create a Yaver account for the SDK popup flow."
              : mode === "signin"
                ? isDeviceAuth
                  ? "Sign in to continue authorizing the remote machine"
                  : "Sign in to get started"
                : "Create an account with email"}
          </p>
        </div>

        {displayError && (
          <div className="mb-6 rounded-lg border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm text-red-400">
            {displayError}
          </div>
        )}

        <div className="space-y-3">
          {/* Passkey sign-in. Hidden when the browser doesn't support
              WebAuthn or the user is in signup mode (passkey enrollment
              happens after the first sign-in, see /dashboard prompt). */}
          {mode === "signin" && passkeySupported && (
            <button
              onClick={handlePasskeyLogin}
              disabled={passkeyLoading}
              className={`flex w-full items-center justify-center gap-3 border border-cyan-400/40 bg-cyan-400/10 text-sm font-medium text-cyan-100 transition-colors hover:border-cyan-300/60 hover:bg-cyan-400/15 disabled:opacity-50 ${controlClass}`}
            >
              <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <rect x="3" y="11" width="18" height="11" rx="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
                <circle cx="12" cy="16" r="1.5" />
              </svg>
              {passkeyLoading ? "Waiting for passkey..." : "Sign in with passkey"}
            </button>
          )}

          {/* Passkey sign-up. Requires email to be filled in; the form
              field below is the source. EMAIL_EXISTS routes the user
              back to sign-in via an inline hint instead of failing
              silently after Touch ID. */}
          {mode === "signup" && passkeySupported && (
            <button
              onClick={handlePasskeySignup}
              disabled={passkeyLoading || !email.trim()}
              className={`flex w-full items-center justify-center gap-3 border border-cyan-400/40 bg-cyan-400/10 text-sm font-medium text-cyan-100 transition-colors hover:border-cyan-300/60 hover:bg-cyan-400/15 disabled:opacity-50 ${controlClass}`}
            >
              <svg className="h-5 w-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <rect x="3" y="11" width="18" height="11" rx="2" />
                <path d="M7 11V7a5 5 0 0 1 10 0v4" />
                <circle cx="12" cy="16" r="1.5" />
              </svg>
              {passkeyLoading ? "Waiting for passkey..." : "Sign up with passkey"}
            </button>
          )}

          <button
            onClick={() => handleOAuth("apple")}
            className={`flex w-full items-center justify-center gap-3 border border-surface-700 bg-surface-900 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50 ${controlClass}`}
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M17.05 20.28c-.98.95-2.05.88-3.08.4-1.09-.5-2.08-.48-3.24 0-1.44.62-2.2.44-3.06-.4C2.79 15.25 3.51 7.59 9.05 7.31c1.35.07 2.29.74 3.08.8 1.18-.24 2.31-.93 3.57-.84 1.51.12 2.65.72 3.4 1.8-3.12 1.87-2.38 5.98.48 7.13-.57 1.5-1.31 2.99-2.54 4.09zM12.03 7.25c-.15-2.23 1.66-4.07 3.74-4.25.29 2.58-2.34 4.5-3.74 4.25z" />
            </svg>
            Continue with Apple
          </button>

          <button
            onClick={() => handleOAuth("github")}
            className={`flex w-full items-center justify-center gap-3 border border-surface-700 bg-surface-900 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50 ${controlClass}`}
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M12 .5C5.65.5.5 5.7.5 12.12c0 5.14 3.3 9.5 7.88 11.03.58.11.79-.25.79-.57 0-.28-.01-1.02-.02-2-3.2.71-3.88-1.57-3.88-1.57-.52-1.35-1.28-1.71-1.28-1.71-1.04-.73.08-.72.08-.72 1.15.08 1.75 1.19 1.75 1.19 1.02 1.77 2.67 1.26 3.32.96.1-.76.4-1.27.73-1.56-2.56-.29-5.26-1.3-5.26-5.77 0-1.27.45-2.31 1.18-3.13-.12-.29-.51-1.47.11-3.06 0 0 .97-.32 3.18 1.2a10.9 10.9 0 0 1 5.8 0c2.2-1.52 3.17-1.2 3.17-1.2.63 1.59.24 2.77.12 3.06.73.82 1.18 1.86 1.18 3.13 0 4.48-2.7 5.48-5.28 5.77.41.36.78 1.08.78 2.18 0 1.58-.02 2.85-.02 3.24 0 .32.2.69.8.57A11.63 11.63 0 0 0 23.5 12.12C23.5 5.7 18.35.5 12 .5z" />
            </svg>
            Continue with GitHub
          </button>

          <button
            onClick={() => handleOAuth("gitlab")}
            className={`flex w-full items-center justify-center gap-3 border border-surface-700 bg-surface-900 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50 ${controlClass}`}
          >
            <span className="inline-flex h-5 w-5 items-center justify-center rounded-full bg-orange-500/15 text-[10px] font-semibold text-orange-300">GL</span>
            Continue with GitLab
          </button>

          <button
            onClick={() => handleOAuth("google")}
            className={`flex w-full items-center justify-center gap-3 border border-surface-700 bg-surface-900 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50 ${controlClass}`}
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24">
              <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 01-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" />
              <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" />
              <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" />
              <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" />
            </svg>
            Continue with Google
          </button>

          <button
            onClick={() => handleOAuth("microsoft")}
            className={`flex w-full items-center justify-center gap-3 border border-surface-700 bg-surface-900 text-sm font-medium text-surface-200 transition-colors hover:border-surface-600 hover:text-surface-50 ${controlClass}`}
          >
            <svg className="h-5 w-5" viewBox="0 0 24 24">
              <path fill="#F25022" d="M1 1h10v10H1z" />
              <path fill="#00A4EF" d="M1 13h10v10H1z" />
              <path fill="#7FBA00" d="M13 1h10v10H13z" />
              <path fill="#FFB900" d="M13 13h10v10H13z" />
            </svg>
            Continue with Microsoft
          </button>

        </div>

        {/* Divider */}
        <div className={`${isSdkPopup ? "my-5" : "my-6"} flex items-center gap-3`}>
          <div className="h-px flex-1 bg-surface-700" />
          <span className="text-xs text-surface-500">or</span>
          <div className="h-px flex-1 bg-surface-700" />
        </div>

        {/* Email/Password Form */}
        <form onSubmit={handleEmailSubmit} className="space-y-3">
          {mode === "signup" && (
            <input
              type="text"
              placeholder="Full name"
              value={fullName}
              onChange={(e) => setFullName(e.target.value)}
              required
              className={`w-full border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500 ${isSdkPopup ? "rounded-xl" : "rounded-lg"}`}
            />
          )}
          <input
            type="email"
            placeholder="Email address"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            className={`w-full border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500 ${isSdkPopup ? "rounded-xl" : "rounded-lg"}`}
          />
          <input
            type="password"
            placeholder="Password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            className={`w-full border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500 ${isSdkPopup ? "rounded-xl" : "rounded-lg"}`}
          />
          {mode === "signup" && (
            <input
              type="password"
              placeholder="Confirm password"
              value={rePassword}
              onChange={(e) => setRePassword(e.target.value)}
              required
              className={`w-full border border-surface-700 bg-surface-900 px-4 py-3 text-sm text-surface-200 placeholder-surface-500 outline-none transition-colors focus:border-surface-500 ${isSdkPopup ? "rounded-xl" : "rounded-lg"}`}
            />
          )}
          <button
            type="submit"
            disabled={loading}
            className={`w-full bg-surface-50 px-4 py-3 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50 ${isSdkPopup ? "rounded-xl" : "rounded-lg"}`}
          >
            {loading ? "Please wait..." : mode === "signin" ? "Sign In" : "Sign Up"}
          </button>
        </form>

        {/* Forgot password (sign-in mode only) */}
        {mode === "signin" && (
          <p className="mt-2 text-right text-sm">
            <Link
              href="/auth/reset-password"
              className="text-surface-500 hover:text-surface-300 transition-colors"
            >
              Forgot password?
            </Link>
          </p>
        )}

        {/* Toggle mode */}
        <p className={`${isSdkPopup ? "mt-5" : "mt-4"} text-center text-sm text-surface-500`}>
          {mode === "signin" ? (
            <>
              Don&apos;t have an account?{" "}
              <button
                onClick={() => { setMode("signup"); setFormError(null); }}
                className="text-surface-300 hover:text-surface-50 transition-colors"
              >
                Sign Up
              </button>
            </>
          ) : (
            <>
              Already have an account?{" "}
              <button
                onClick={() => { setMode("signin"); setFormError(null); }}
                className="text-surface-300 hover:text-surface-50 transition-colors"
              >
                Sign In
              </button>
            </>
          )}
        </p>

        <p className={`${isSdkPopup ? "mt-5" : "mt-6"} text-center text-xs text-surface-600`}>
          By continuing, you agree to our{" "}
          <Link href="/terms" className="text-surface-400 hover:text-surface-50">Terms</Link>{" "}
          and{" "}
          <Link href="/privacy" className="text-surface-400 hover:text-surface-50">Privacy Policy</Link>.
        </p>
      </div>
    </div>
  );
}

export default function AuthPage() {
  return (
    <Suspense fallback={<div className="flex min-h-[70vh] items-center justify-center"><span className="text-surface-500 text-sm">Loading...</span></div>}>
      <AuthContent />
    </Suspense>
  );
}
