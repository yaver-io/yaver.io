"use client";

// /launch — zero-friction cloud-box launcher.
//
// User lands here from yaver.io marketing, signs in if they haven't,
// picks a provider, clicks the button. The button is a deep-link into
// the provider's native console (AWS Launch Stack, GCP Deployment
// Manager, Hetzner cloud-init form) with our infra template +
// pre-authorized device-code baked in. The provider's UI accepts the
// click, asks the user for any provider-side input (key pair, region,
// etc.), then provisions. Box claims itself on first boot. No local
// terminal involved.
//
// The crucial bit: this page mints a fresh device-code AND authorizes
// it server-side with the signed-in user's token BEFORE rendering any
// button. So the deep-link encodes a code that the new box can
// immediately exchange for a session token under this user's identity.

import Link from "next/link";
import { useEffect, useState } from "react";
import { useAuth } from "@/lib/use-auth";
import { CONVEX_URL } from "@/lib/constants";

const DEVICE_CODE_TTL_HINT_MIN = 15;

interface DeviceCode {
  userCode: string;
  deviceCode: string;
  expiresAt: number;
}

export default function LaunchPage() {
  const { isAuthenticated, isLoading } = useAuth();
  const [code, setCode] = useState<DeviceCode | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [provider, setProvider] = useState<"aws" | "gcp" | "hetzner" | "ssh" | null>(null);

  useEffect(() => {
    if (isLoading || !isAuthenticated) return;
    if (code) return;
    let cancelled = false;
    (async () => {
      try {
        const token = localStorage.getItem("yaver_auth_token");
        if (!token) throw new Error("Missing auth token in localStorage");

        // 1. Mint a fresh device-code.
        const mint = await fetch(`${CONVEX_URL}/auth/device-code`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({}),
        });
        if (!mint.ok) throw new Error(`Mint failed: ${mint.status}`);
        const dc: DeviceCode = await mint.json();

        // 2. Authorize it as ourselves so the new box can immediately
        //    redeem it without any browser round-trip.
        const auth = await fetch(`${CONVEX_URL}/auth/device-code/authorize`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
          },
          body: JSON.stringify({ userCode: dc.userCode }),
        });
        if (!auth.ok) {
          const body = await auth.text();
          throw new Error(`Authorize failed: ${auth.status} ${body}`);
        }

        if (!cancelled) setCode(dc);
      } catch (e: any) {
        if (!cancelled) setError(e?.message ?? String(e));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [isAuthenticated, isLoading, code]);

  if (isLoading) {
    return <Centered>Loading…</Centered>;
  }
  if (!isAuthenticated) {
    return (
      <Centered>
        <h1 className="text-2xl font-semibold mb-3">Sign in to launch a Yaver box</h1>
        <p className="text-zinc-400 mb-6 max-w-md">
          Launch generates a one-time code that the new box uses to claim itself under
          your account. We need you signed in first.
        </p>
        <Link
          href="/auth?next=/launch"
          className="px-5 py-2.5 bg-white text-black rounded-md font-medium"
        >
          Sign in
        </Link>
      </Centered>
    );
  }
  if (error) {
    return (
      <Centered>
        <h1 className="text-xl font-semibold mb-3 text-red-400">Couldn't prepare a launch code</h1>
        <code className="text-sm text-zinc-400">{error}</code>
        <button
          onClick={() => {
            setError(null);
            setCode(null);
          }}
          className="mt-6 px-4 py-2 bg-zinc-800 rounded-md text-sm"
        >
          Retry
        </button>
      </Centered>
    );
  }
  if (!code) {
    return <Centered>Preparing your launch code…</Centered>;
  }

  return (
    <div className="min-h-screen bg-black text-white">
      <div className="max-w-3xl mx-auto px-6 py-16">
        <h1 className="text-3xl font-semibold mb-3">Launch a Yaver box</h1>
        <p className="text-zinc-400 mb-8">
          Pick a provider. Your box comes online under your Yaver account in ~90 seconds —
          claude-code, codex, and opencode credentials are mirrored from your existing
          devices automatically. You'll use your own cloud account (free tier works); we
          never see your provider credentials.
        </p>

        <div className="mb-8 p-4 rounded-md bg-zinc-900 border border-zinc-800">
          <div className="text-xs text-zinc-500 uppercase tracking-wider">Your one-time code</div>
          <div className="font-mono text-2xl tracking-widest mt-1">{code.userCode}</div>
          <div className="text-xs text-zinc-500 mt-2">
            Valid for {DEVICE_CODE_TTL_HINT_MIN} minutes. Don't share it.
          </div>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <ProviderCard
            id="aws"
            title="AWS"
            sub="EC2 t4g.small via CloudFormation"
            active={provider === "aws"}
            onSelect={() => setProvider("aws")}
            launchUrl={awsLaunchUrl(code.userCode)}
          />
          <ProviderCard
            id="gcp"
            title="Google Cloud"
            sub="Compute Engine via Deployment Manager"
            active={provider === "gcp"}
            onSelect={() => setProvider("gcp")}
            launchUrl={gcpLaunchUrl(code.userCode)}
          />
          <ProviderCard
            id="hetzner"
            title="Hetzner"
            sub="Cloud Console — cax21 + cloud-init"
            active={provider === "hetzner"}
            onSelect={() => setProvider("hetzner")}
            launchUrl={hetznerLaunchUrl(code.userCode)}
          />
          <ProviderCard
            id="ssh"
            title="Adopt existing box"
            sub="Any Linux you can SSH to (NAS, VPS, homelab)"
            active={provider === "ssh"}
            onSelect={() => setProvider("ssh")}
            cliOnly
          />
        </div>

        {provider === "ssh" && (
          <div className="mt-6 p-4 rounded-md bg-zinc-900 border border-zinc-800">
            <div className="text-sm text-zinc-400 mb-2">
              SSH adoption uses your local <code>yaver</code> CLI:
            </div>
            <pre className="bg-black p-3 rounded text-xs overflow-x-auto">
              <code>{`# from your terminal
yaver launch ssh user@your-box.example.com`}</code>
            </pre>
            <div className="text-xs text-zinc-500 mt-2">
              Don't have the CLI? <Link href="/install" className="underline">Install it</Link> first.
            </div>
          </div>
        )}

        <p className="text-xs text-zinc-500 mt-10">
          Prefer the CLI? <code className="text-zinc-300">yaver launch hetzner</code> (or aws / gcp)
          does the same thing from your terminal. <Link href="/docs" className="underline">Docs</Link>.
        </p>
      </div>
    </div>
  );
}

// ─── helpers ──────────────────────────────────────────────────────────

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="min-h-screen bg-black text-white flex flex-col items-center justify-center text-center px-6">
      {children}
    </div>
  );
}

function ProviderCard({
  title,
  sub,
  active,
  onSelect,
  launchUrl,
  cliOnly,
}: {
  id: string;
  title: string;
  sub: string;
  active: boolean;
  onSelect: () => void;
  launchUrl?: string;
  cliOnly?: boolean;
}) {
  return (
    <div
      className={`p-5 rounded-md border transition-colors ${
        active ? "border-white bg-zinc-900" : "border-zinc-800 bg-zinc-950 hover:border-zinc-700"
      }`}
    >
      <button onClick={onSelect} className="block w-full text-left">
        <div className="font-semibold mb-1">{title}</div>
        <div className="text-xs text-zinc-500">{sub}</div>
      </button>
      {launchUrl && (
        <a
          href={launchUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-3 inline-block px-3 py-1.5 bg-white text-black rounded-md text-sm font-medium"
        >
          Launch →
        </a>
      )}
      {cliOnly && <div className="mt-3 text-xs text-zinc-500">CLI only · see below</div>}
    </div>
  );
}

// ─── provider deep-links ──────────────────────────────────────────────
// Each provider has a native "launch with these parameters" URL. We
// pre-fill UserCode so the user clicks once and the cloud-init seed
// is ready to go without manual edits.

function awsLaunchUrl(userCode: string): string {
  const templateURL = encodeURIComponent(
    "https://yaver.io/infra/cloudformation/yaver-launch.yaml"
  );
  const stackName = `yaver-${userCode.toLowerCase()}`;
  // CloudFormation Launch Stack URL — region defaults to whatever the
  // user is currently viewing in the console.
  return (
    `https://console.aws.amazon.com/cloudformation/home#/stacks/new` +
    `?stackName=${stackName}` +
    `&templateURL=${templateURL}` +
    `&param_UserCode=${userCode}`
  );
}

function gcpLaunchUrl(userCode: string): string {
  // GCP Deployment Manager doesn't accept inline overrides via URL,
  // so we link to the new-deployment console with the config URL
  // pre-filled; the user pastes UserCode in the inline editor (one
  // edit) before clicking Deploy. The portal also displays the user
  // code prominently so they can copy + paste in one motion.
  const configURL = encodeURIComponent("https://yaver.io/infra/gcp/yaver-launch.yaml");
  return `https://console.cloud.google.com/dm/deployments/new?config=${configURL}&userCode=${userCode}`;
}

function hetznerLaunchUrl(userCode: string): string {
  // Hetzner has no native one-click cloud-init URL on console.hetzner.cloud,
  // so we send users to the new-server page with the cloud-init template
  // available at a publicly fetchable URL. They paste the URL into the
  // "user data" field. The portal copy below the button explains.
  return `https://console.hetzner.cloud/?yaverUserCode=${userCode}`;
}
