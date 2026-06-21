"use client";

// /a — public "open a shared Yaver app" route. A friend lands here from a share
// link (?host=<serverless origin>&code=<join code>), the page resolves the share
// via the host's public /phone/projects/join, and runs the app read-only against
// the host's /data API. No account or install needed — the friend just opens the
// link in a browser. (Mobile gets the same via the Yaver app deep link.)

import { useEffect, useState } from "react";
import RunSharedApp from "@/components/RunSharedApp";
import type { PhoneAppSpec, PhoneSchema } from "@/lib/agent-client";

interface ResolvedShare {
  slug: string;
  name?: string;
  dataUrl?: string;
  dataToken?: string;
  schema?: PhoneSchema;
  app?: PhoneAppSpec;
  error?: string;
}

export default function OpenSharedAppPage() {
  const [state, setState] = useState<{ host: string; share: ResolvedShare } | null>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const host = (params.get("host") || "").replace(/\/$/, "");
    const code = params.get("code") || "";
    if (!host || !code) {
      setErr("This link is missing its host or code. Ask for a fresh share link.");
      return;
    }
    (async () => {
      try {
        const r = await fetch(`${host}/phone/projects/join?code=${encodeURIComponent(code)}`);
        if (!r.ok) {
          setErr(r.status === 404 ? "This share code is invalid or expired." : `Could not open (${r.status}).`);
          return;
        }
        const share = (await r.json()) as ResolvedShare;
        if (share.error) {
          setErr(share.error);
          return;
        }
        setState({ host, share });
      } catch (e) {
        setErr(e instanceof Error ? e.message : "Could not reach the host.");
      }
    })();
  }, []);

  if (err) {
    return (
      <div className="mx-auto max-w-md p-8 text-center text-surface-300">
        <div className="text-lg font-semibold">Can’t open this app</div>
        <p className="mt-2 text-sm text-surface-500">{err}</p>
      </div>
    );
  }
  if (!state) {
    return <div className="p-8 text-sm text-surface-500">Opening shared app…</div>;
  }
  return (
    <RunSharedApp
      name={state.share.name}
      slug={state.share.slug}
      dataBase={state.host}
      dataToken={state.share.dataToken}
      schema={state.share.schema}
      app={state.share.app}
      readOnly
    />
  );
}
