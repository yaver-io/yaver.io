"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { useAuth } from "@/lib/use-auth";
import { isCloudPreviewUser } from "@/lib/cloud-preview";
import { CONVEX_URL } from "@/lib/constants";
import { getManagedSubscription } from "@/lib/subscription";
import { getYaverCloudHost } from "@/lib/yaver-cloud";

export default function PricingPage() {
  const { user, token, isLoading } = useAuth();
  const canSeeCloudPreview = isCloudPreviewUser(user?.email);
  const [hasManagedCloud, setHasManagedCloud] = useState(false);
  const [startingCheckout, setStartingCheckout] = useState(false);
  const [activatingPreview, setActivatingPreview] = useState(false);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);
  const canShowCloud = canSeeCloudPreview || hasManagedCloud;
  const cloudHeading = canSeeCloudPreview ? "Private Preview" : "Managed Cloud";

  useEffect(() => {
    let cancelled = false;
    if (!token) {
      setHasManagedCloud(false);
      return;
    }
    void getManagedSubscription(token).then((summary) => {
      if (cancelled || !summary) return;
      const hasMachine = Array.isArray(summary.machines)
        && summary.machines.some((machine) => machine.status !== "stopped");
      const hasSubscription = !!summary.subscription;
      setHasManagedCloud(hasMachine || hasSubscription);
    });
    return () => {
      cancelled = true;
    };
  }, [token]);

  async function startCloudCheckout() {
    if (!token) {
      setCheckoutError("Sign in first.");
      return;
    }
    setStartingCheckout(true);
    setCheckoutError(null);
    try {
      const response = await fetch(`${CONVEX_URL}/billing/yaver-cloud/checkout`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ region: "eu" }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok || typeof data.url !== "string") {
        throw new Error(data.error || "Could not start checkout.");
      }
      window.location.href = data.url;
    } catch (error) {
      setCheckoutError(error instanceof Error ? error.message : "Could not start checkout.");
    } finally {
      setStartingCheckout(false);
    }
  }

  async function activatePreviewWithoutCheckout() {
    if (!token) {
      setCheckoutError("Sign in first.");
      return;
    }
    setActivatingPreview(true);
    setCheckoutError(null);
    try {
      const response = await fetch(`${CONVEX_URL}/billing/yaver-cloud/dev-activate`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ region: "eu" }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data.error || "Could not activate preview.");
      }
      window.location.reload();
    } catch (error) {
      setCheckoutError(error instanceof Error ? error.message : "Could not activate preview.");
    } finally {
      setActivatingPreview(false);
    }
  }

  return (
    <div className="px-6 py-20">
      <div className="mx-auto max-w-5xl">
        <div className="mx-auto max-w-3xl rounded-3xl border border-surface-800 bg-surface-900/60 p-8 text-center">
          <div className="mb-3 text-[11px] font-semibold uppercase tracking-[0.24em] text-surface-500">
            Local First
          </div>
          <h1 className="text-3xl font-bold text-surface-50 md:text-4xl">
            Your machine is your cloud
          </h1>
          <p className="mx-auto mt-4 max-w-2xl text-sm leading-relaxed text-surface-400">
            Yaver runs on your phone, your Mac, your Linux box, your Pi, or any server you
            already control. The public product story stays focused on that path.
          </p>
          <div className="mt-6 flex flex-wrap justify-center gap-2 text-xs">
            <span className="rounded-full border border-surface-800 bg-surface-950 px-3 py-1 text-surface-300">
              phone sandbox
            </span>
            <span className="rounded-full border border-surface-800 bg-surface-950 px-3 py-1 text-surface-300">
              your dev machine
            </span>
            <span className="rounded-full border border-surface-800 bg-surface-950 px-3 py-1 text-surface-300">
              self-hosted relay
            </span>
            <span className="rounded-full border border-surface-800 bg-surface-950 px-3 py-1 text-surface-300">
              portable exports
            </span>
          </div>
        </div>

        <section className="mt-10 grid gap-5 md:grid-cols-2">
          <div className="rounded-2xl border border-surface-800 bg-surface-900/50 p-6">
            <h2 className="text-lg font-semibold text-surface-100">Start on your own hardware</h2>
            <p className="mt-2 text-sm leading-relaxed text-surface-400">
              Build on the phone, push to your own machine, keep the same project shape, and
              export whenever you want. No managed account is required.
            </p>
            <div className="mt-4 space-y-2 text-sm text-surface-300">
              <div>CLI, mobile app, dashboard, relay, and SDKs</div>
              <div>Hermes reload and remote coding on your own box</div>
              <div>Local to remote project promotion with portable bundles</div>
            </div>
          </div>

          <div className="rounded-2xl border border-surface-800 bg-surface-900/50 p-6">
            <h2 className="text-lg font-semibold text-surface-100">Self-host when you need more</h2>
            <p className="mt-2 text-sm leading-relaxed text-surface-400">
              Run Yaver on a VPS, a Mac mini, a Pi, or a headless Linux node. The public docs stay
              centered on that path while the managed cloud flow is still in private rollout.
            </p>
            <div className="mt-4 flex gap-3 text-sm">
              <Link href="/docs/self-hosting" className="text-indigo-300 hover:text-indigo-200">
                Self-hosting guide
              </Link>
              <Link href="/manuals/relay-setup" className="text-indigo-300 hover:text-indigo-200">
                Relay setup
              </Link>
            </div>
          </div>
        </section>

        {!isLoading && canShowCloud ? (
          <section className="mt-10 rounded-3xl border border-indigo-500/30 bg-indigo-500/10 p-8">
            <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.24em] text-indigo-300">
              {cloudHeading}
            </div>
            <h2 className="text-2xl font-semibold text-surface-50">Yaver Cloud</h2>
            <p className="mt-3 max-w-3xl text-sm leading-relaxed text-surface-300">
              Dedicated managed machine with relay included. Intended for Hermes reload, phone to
              cloud promotion, and lower remote coding cost by keeping your workspace warm and
              persistent.
            </p>
            <div className="mt-6 grid gap-4 md:grid-cols-2">
              <div className="rounded-2xl border border-indigo-400/30 bg-surface-950/70 p-5">
                <div className="text-base font-semibold text-surface-100">Managed machine</div>
                <div className="mt-3 space-y-2 text-sm text-surface-300">
                  <div>16 GB RAM</div>
                  <div>256 GB SSD</div>
                  <div>Warm agent runtime</div>
                  <div>Relay included</div>
                  <div>Local to cloud project import/export</div>
                  <div>Aider / Ollama / coding tools on-box</div>
                  <div>DNS + TLS terminate on {getYaverCloudHost()}</div>
                </div>
              </div>
              <div className="rounded-2xl border border-indigo-400/30 bg-surface-950/70 p-5">
                <div className="text-base font-semibold text-surface-100">Checkout</div>
                <p className="mt-3 text-sm leading-relaxed text-surface-400">
                  Purchase is intentionally web-only for now. Mobile apps can use an already-owned
                  cloud machine, but they do not present billing flows.
                </p>
                <div className="mt-5 flex flex-wrap gap-3">
                  {canSeeCloudPreview ? (
                    <button
                      type="button"
                      disabled={activatingPreview}
                      onClick={() => void activatePreviewWithoutCheckout()}
                      className="inline-flex rounded-xl bg-indigo-500 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-400 disabled:cursor-not-allowed disabled:opacity-60"
                    >
                      {activatingPreview ? "Activating…" : "Activate preview machine"}
                    </button>
                  ) : null}
                  <button
                    type="button"
                    disabled={startingCheckout}
                    onClick={() => void startCloudCheckout()}
                    className="inline-flex rounded-xl border border-indigo-400/40 px-4 py-2 text-sm font-medium text-indigo-100 hover:bg-indigo-500/10 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {startingCheckout ? "Opening checkout…" : "Test hosted checkout later"}
                  </button>
                </div>
                {checkoutError ? (
                  <div className="mt-4 rounded-xl border border-amber-500/30 bg-amber-500/10 px-4 py-3 text-sm text-amber-200">
                    {checkoutError}
                  </div>
                ) : null}
                <div className="mt-4 text-xs leading-relaxed text-surface-500">
                  {canSeeCloudPreview
                    ? "Preview runs on the shared Yaver public machine for now. DNS, TLS, relay, and callback-friendly hosted URLs stay bundled so GitHub, GitLab, Google, Microsoft, Apple, and similar OAuth providers can point at one stable cloud host."
                    : "Managed cloud machines stay web-provisioned, then show up automatically in Yaver mobile, desktop, and web once the account is active."}
                </div>
              </div>
            </div>
          </section>
        ) : null}

        <div className="mt-10 text-center">
          <Link href="/" className="text-xs text-surface-500 hover:text-surface-50">
            Back to home
          </Link>
        </div>
      </div>
    </div>
  );
}
