"use client";

import { useState, useEffect } from "react";
import { CONVEX_URL } from "@/lib/constants";

type TwoFactorViewProps = {
  token: string | null;
};

export default function TwoFactorView({ token }: TwoFactorViewProps) {
  const [enabled, setEnabled] = useState(false);
  const [recoveryCodesRemaining, setRecoveryCodesRemaining] = useState(0);
  const [loading, setLoading] = useState(true);

  // Setup flow state
  const [step, setStep] = useState<"idle" | "setup" | "verify" | "recovery" | "disable">("idle");
  const [secret, setSecret] = useState("");
  const [otpAuthUrl, setOtpAuthUrl] = useState("");
  const [code, setCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    if (!token) return;
    fetchStatus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token]);

  const fetchStatus = async () => {
    try {
      const res = await fetch(`${CONVEX_URL}/auth/totp/status`, {
        headers: { Authorization: `Bearer ${token}` },
      });
      if (res.ok) {
        const data = await res.json();
        setEnabled(data.enabled);
        setRecoveryCodesRemaining(data.recoveryCodesRemaining);
      }
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  };

  const handleSetup = async () => {
    setError("");
    setSubmitting(true);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/totp/setup`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.error || "Failed to setup 2FA");
        setSubmitting(false);
        return;
      }
      const data = await res.json();
      setSecret(data.secret);
      setOtpAuthUrl(data.otpAuthUrl);
      setStep("setup");
    } catch {
      setError("Network error");
    } finally {
      setSubmitting(false);
    }
  };

  const handleVerify = async () => {
    setError("");
    setSubmitting(true);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/totp/enable`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ code: code.trim() }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.error || "Invalid code");
        setSubmitting(false);
        return;
      }
      const data = await res.json();
      setRecoveryCodes(data.recoveryCodes);
      setStep("recovery");
      setEnabled(true);
      setRecoveryCodesRemaining(data.recoveryCodes.length);
    } catch {
      setError("Network error");
    } finally {
      setSubmitting(false);
    }
  };

  const handleDisable = async () => {
    setError("");
    setSubmitting(true);
    try {
      const res = await fetch(`${CONVEX_URL}/auth/totp/disable`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ code: code.trim() }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.error || "Invalid code");
        setSubmitting(false);
        return;
      }
      setEnabled(false);
      setStep("idle");
      setCode("");
      setRecoveryCodes([]);
      setRecoveryCodesRemaining(0);
    } catch {
      setError("Network error");
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopyRecoveryCodes = () => {
    navigator.clipboard.writeText(recoveryCodes.join("\n"));
  };

  if (loading) return null;

  return (
    <div className="card mb-6">
      <h2 className="mb-4 text-lg font-semibold text-surface-50">Two-Factor Authentication</h2>

      {/* Idle state — show status */}
      {step === "idle" && (
        <>
          {enabled ? (
            <div>
              <div className="flex items-center gap-2">
                <span className="inline-block h-2 w-2 rounded-full bg-green-500" />
                <span className="text-sm text-surface-200">2FA is enabled</span>
              </div>
              <p className="mt-1 text-xs text-surface-500">
                {recoveryCodesRemaining} recovery code{recoveryCodesRemaining !== 1 ? "s" : ""} remaining
              </p>
              <button
                onClick={() => { setStep("disable"); setCode(""); setError(""); }}
                className="mt-4 rounded-lg border border-red-500/30 px-4 py-2 text-sm text-red-400 transition-colors hover:border-red-500/50 hover:text-red-700 dark:hover:text-red-300"
              >
                Disable 2FA
              </button>
            </div>
          ) : (
            <div>
              <p className="text-sm text-surface-400">
                Add an extra layer of security to your account with a TOTP authenticator app.
              </p>
              <button
                onClick={handleSetup}
                disabled={submitting}
                className="mt-4 rounded-lg bg-surface-50 px-4 py-2 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
              >
                {submitting ? "Setting up..." : "Enable 2FA"}
              </button>
            </div>
          )}
        </>
      )}

      {/* Setup state — show QR code and secret */}
      {step === "setup" && (
        <div>
          <p className="mb-4 text-sm text-surface-400">
            Scan this QR code with your authenticator app (Google Authenticator, 1Password, Authy, etc.):
          </p>

          <div className="mx-auto mb-4 w-fit rounded-lg bg-white p-4">
            {/* Use Google Charts QR API for display */}
            <img
              src={`https://chart.googleapis.com/chart?cht=qr&chs=200x200&chl=${encodeURIComponent(otpAuthUrl)}&choe=UTF-8`}
              alt="TOTP QR Code"
              width={200}
              height={200}
            />
          </div>

          <div className="mb-4">
            <p className="text-xs text-surface-500">Or enter this secret manually:</p>
            <code className="mt-1 block rounded-lg bg-surface-800 px-3 py-2 text-xs text-surface-200 select-all break-all">
              {secret}
            </code>
          </div>

          {error && (
            <div className="mb-4 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-sm text-red-400">
              {error}
            </div>
          )}

          <p className="mb-2 text-sm text-surface-400">Enter the 6-digit code from your app to verify:</p>
          <div className="flex gap-2">
            <input
              type="text"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/[^0-9]/g, "").slice(0, 6))}
              placeholder="000000"
              inputMode="numeric"
              autoComplete="one-time-code"
              className="w-32 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-center font-mono text-lg text-surface-100 placeholder-surface-600 outline-none focus:border-surface-500"
            />
            <button
              onClick={handleVerify}
              disabled={submitting || code.length < 6}
              className="rounded-lg bg-surface-50 px-4 py-2 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200 disabled:opacity-50"
            >
              {submitting ? "Verifying..." : "Verify & Enable"}
            </button>
          </div>

          <button
            onClick={() => { setStep("idle"); setCode(""); setError(""); }}
            className="mt-3 text-sm text-surface-500 hover:text-surface-300"
          >
            Cancel
          </button>
        </div>
      )}

      {/* Recovery codes — show once after enabling */}
      {step === "recovery" && (
        <div>
          <div className="mb-4 rounded-lg border border-amber-500/20 bg-amber-500/10 px-4 py-3">
            <p className="text-sm font-medium text-amber-400">Save your recovery codes</p>
            <p className="mt-1 text-xs text-amber-400/70">
              These codes can be used to access your account if you lose your authenticator device. Each code can only be used once. Store them securely.
            </p>
          </div>

          <div className="mb-4 grid grid-cols-2 gap-2 rounded-lg bg-surface-800 p-4">
            {recoveryCodes.map((rc, i) => (
              <code key={i} className="text-sm text-surface-200">{rc}</code>
            ))}
          </div>

          <div className="flex gap-2">
            <button
              onClick={handleCopyRecoveryCodes}
              className="rounded-lg border border-surface-700 px-4 py-2 text-sm text-surface-300 transition-colors hover:border-surface-600 hover:text-surface-100"
            >
              Copy codes
            </button>
            <button
              onClick={() => { setStep("idle"); setRecoveryCodes([]); }}
              className="rounded-lg bg-surface-50 px-4 py-2 text-sm font-medium text-surface-950 transition-colors hover:bg-surface-200"
            >
              Done
            </button>
          </div>
        </div>
      )}

      {/* Disable state — requires TOTP code */}
      {step === "disable" && (
        <div>
          <p className="mb-3 text-sm text-surface-400">Enter a code from your authenticator app to disable 2FA:</p>

          {error && (
            <div className="mb-3 rounded-lg border border-red-500/20 bg-red-500/10 px-3 py-2 text-sm text-red-400">
              {error}
            </div>
          )}

          <div className="flex gap-2">
            <input
              type="text"
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/[^0-9]/g, "").slice(0, 6))}
              placeholder="000000"
              inputMode="numeric"
              autoComplete="one-time-code"
              autoFocus
              className="w-32 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-center font-mono text-lg text-surface-100 placeholder-surface-600 outline-none focus:border-surface-500"
            />
            <button
              onClick={handleDisable}
              disabled={submitting || code.length < 6}
              className="rounded-lg border border-red-500/30 px-4 py-2 text-sm text-red-400 transition-colors hover:border-red-500/50 hover:text-red-700 dark:hover:text-red-300 disabled:opacity-50"
            >
              {submitting ? "Disabling..." : "Disable"}
            </button>
          </div>

          <button
            onClick={() => { setStep("idle"); setCode(""); setError(""); }}
            className="mt-3 text-sm text-surface-500 hover:text-surface-300"
          >
            Cancel
          </button>
        </div>
      )}
    </div>
  );
}
