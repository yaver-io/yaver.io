"use client";

// RunSharedApp — runs a deployed/shared Yaver Serverless app in the browser for
// a friend (no account needed). Same generic renderer as the sandbox live
// preview (preview.ts), but its data bridge points at the host's REMOTE /data
// API with a scoped read-only token (remoteBridge.ts). This is the "USE the
// app" half of the friend-preview loop on web.

import { useEffect, useRef, useState } from "react";
import type { PhoneAppSpec, PhoneSchema } from "@/lib/agent-client";
import { buildPreviewSrcdoc } from "@/lib/sandbox/preview";
import { attachRemoteBridge } from "@/lib/sandbox/remoteBridge";

export interface RunSharedAppProps {
  name?: string;
  slug: string;
  dataBase: string;
  dataToken?: string;
  schema?: PhoneSchema | null;
  app?: PhoneAppSpec | null;
  readOnly?: boolean;
}

export default function RunSharedApp(props: RunSharedAppProps) {
  const [srcDoc, setSrcDoc] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const iframeRef = useRef<HTMLIFrameElement | null>(null);

  useEffect(() => {
    let detach: (() => void) | null = null;
    let cancelled = false;
    (async () => {
      try {
        const doc = await buildPreviewSrcdoc(props.schema ?? { tables: [] }, props.app ?? {}, {
          readOnly: props.readOnly,
        });
        if (cancelled) return;
        detach = attachRemoteBridge({
          dataBase: props.dataBase,
          slug: props.slug,
          dataToken: props.dataToken,
          schema: props.schema,
          app: props.app,
        });
        setSrcDoc(doc);
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : String(e));
      }
    })();
    return () => {
      cancelled = true;
      detach?.();
    };
  }, [props.slug, props.dataBase, props.dataToken, props.schema, props.app]);

  return (
    <div className="mx-auto flex min-h-screen max-w-3xl flex-col gap-3 p-4">
      <div className="flex items-center justify-between">
        <div>
          <div className="text-lg font-semibold text-surface-100">{props.name || props.slug}</div>
          <div className="text-xs text-surface-500">
            Running on Yaver Serverless{props.readOnly ? " · read-only preview" : ""}
          </div>
        </div>
        <span className="rounded-full border border-indigo-500/40 bg-indigo-500/10 px-3 py-1 text-xs text-indigo-300">
          Yaver
        </span>
      </div>
      {err ? (
        <div className="rounded border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-300">{err}</div>
      ) : !srcDoc ? (
        <div className="rounded border border-surface-800 bg-surface-950 p-4 text-sm text-surface-500">Loading app…</div>
      ) : (
        <iframe
          ref={iframeRef}
          title={props.name || "Shared app"}
          sandbox="allow-scripts"
          srcDoc={srcDoc}
          className="h-[560px] w-full rounded border border-surface-800 bg-white dark:bg-surface-950"
        />
      )}
    </div>
  );
}
