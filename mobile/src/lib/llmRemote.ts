// llmRemote.ts — "remote runner" implementation of the LlmProvider contract
// (llmClient.ts). Unlike the on-device / BYO-key cloud backends, this one does
// NOT call a model API from the phone: it ships the sandbox's files + prompt to
// a connected Yaver box, which runs OpenCode against z.ai's coding-plan GLM
// model over them and returns an EditPlan-shaped diff. The phone
// then previews + applies that plan against its local sandbox tree, exactly like
// every other backend.
//
// Why this exists: the Mobile Sandbox is phone-only (no checkout on any box), so
// the usual remote-coding paths (sendTask / agent graphs) — which edit a repo on
// the machine — don't apply. The agent's POST /sandbox/run closes that gap.
//
// OpenCode-only for now. The box holds the z.ai credential; the phone never does.
//
// PURE + RN-free (tsx-tested): the network call is injected as `dispatch`, so
// codingBackendStore wires it to quicClient.sandboxRun and tests pass a fake.

import {
  assertRequestSize,
  type EditFilesRequest,
  type EditPlan,
  type FileEdit,
  type LlmProvider,
} from "./llmClient";

/** Request shipped to the box's POST /sandbox/run. Mirrors the agent's
 *  sandboxRunRequest (desktop/agent/sandbox_remote.go). */
export interface RemoteSandboxRequest {
  prompt: string;
  files: Array<{ path: string; content: string }>;
  framework?: string;
  schema?: unknown;
  runner?: string;
  timeoutMs?: number;
}

/** Response from the box. Mirrors the agent's sandboxRunResponse. */
export interface RemoteSandboxResponse {
  ok: boolean;
  rationale?: string;
  edits?: Array<{
    action: "create" | "update" | "delete";
    path: string;
    content?: string;
    reason?: string;
  }>;
  runner?: string;
  model?: string;
  error?: string;
}

/** Performs the network round-trip to the connected box. Injected so the pure
 *  provider stays testable. Production wires this to quicClient.sandboxRun. */
export type RemoteSandboxDispatch = (
  req: RemoteSandboxRequest,
) => Promise<RemoteSandboxResponse>;

export interface RemoteProviderOptions {
  dispatch: RemoteSandboxDispatch;
  /** Label for the UI ("zai-coding-plan/glm-4.7"). */
  model?: string;
}

/** Build an LlmProvider that runs the remote OpenCode runner on a connected box. */
export function createRemoteProvider(opts: RemoteProviderOptions): LlmProvider {
  if (typeof opts.dispatch !== "function") {
    throw new Error("createRemoteProvider: dispatch is required (the box round-trip).");
  }
  const model = opts.model ?? "zai-coding-plan/glm-4.7";

  return {
    id: "remote",
    model,

    async editFiles(req: EditFilesRequest): Promise<EditPlan> {
      assertRequestSize(req);

      const res = await opts.dispatch({
        prompt: req.prompt,
        files: req.files.map((f) => ({ path: f.path, content: f.content })),
        framework: req.framework,
        schema: req.schema,
        runner: "opencode",
        timeoutMs: req.timeoutMs,
      });

      // A transport-level failure (relay down, box unreachable) should already
      // have rejected in dispatch; this guards a structured { ok:false } body.
      if (!res || (res.ok === false && (!res.edits || res.edits.length === 0))) {
        throw new Error(
          `Remote runner failed: ${res?.error?.trim() || "no edits returned"}`,
        );
      }

      const edits: FileEdit[] = (res.edits ?? []).map((e) => ({
        action: e.action,
        path: e.path,
        content: e.content,
        reason: e.reason,
      }));

      let rationale = res.rationale?.trim() ?? "";
      // If the box surfaced a partial error alongside edits, fold it into the
      // rationale so the preview pane shows what went wrong.
      if (res.error?.trim() && edits.length > 0) {
        rationale = rationale
          ? `${rationale}\n\n(partial: ${res.error.trim()})`
          : `(partial: ${res.error.trim()})`;
      }

      return {
        rationale,
        edits,
        debug: { runner: res.runner, model: res.model },
      };
    },
  };
}
