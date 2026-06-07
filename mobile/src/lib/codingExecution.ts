// codingExecution.ts — turn a resolved CodingSession (codingSession.ts) into the
// concrete surfaces the editor/terminal actually use. This is the ONE place that
// knows whether file edits + shell run on the phone or on a box; everything
// upstream is platform-oblivious.
//
// RN-coupled (connectionManager, phone-sandbox FS) — not tsx-tested. The pure
// pieces it composes ARE tested: resolveCodingSession (codingSession.test.mts),
// makeRemoteApplyTarget (remoteApplyTarget.test.mts), localBox (localBox.test.mts).
//
// Two surfaces per session:
//   applyTarget — where an EditPlan's files are written (hermes engine only;
//                 CLI engines edit through their own process).
//   exec        — run a shell command for the session's TARGET (build/test/git).
//                 Phone-local sandbox has no general shell → exec routes to the
//                 loopback agent on Android (proot) or throws "needs a machine"
//                 on iOS, matching the remote-first brain in brain.ts.

import { connectionManager } from "./connectionManager";
import { LOCAL_BOX_DEVICE_ID } from "./localBox";
import type { ApplyTarget } from "./llmClient";
import { makeRemoteApplyTarget } from "./remoteApplyTarget";
import * as phoneSandbox from "./phoneSandboxSourceDefault";
import type { CodingSession } from "./codingSession";
import { sessionEndpointDeviceId } from "./codingSession";

/** A project pinned on a remote box, identifying where edits + commands land.
 *  Comes from the project the user selected on that device. */
export interface BoxProjectRef {
  deviceId: string;
  /** host-share root id + absolute base path on the box. */
  root: string;
  rootPath: string;
}

/** The phone-local sandbox as an ApplyTarget (expo-file-system under the hood). */
export const phoneApplyTarget: ApplyTarget = {
  writeSourceFile: (slug, relPath, content) => phoneSandbox.writeSourceFile(slug, relPath, content),
  deleteSourceFile: (slug, relPath) => phoneSandbox.deleteSourceFile(slug, relPath),
};

/** The ApplyTarget for a session. For `(hermes, box)` you must pass the pinned
 *  box project (root/rootPath) — without it we can't address files on the box.
 *  For `(hermes, phone)` the box ref is ignored. CLI sessions never call this. */
export function applyTargetForSession(session: CodingSession, boxProject?: BoxProjectRef): ApplyTarget {
  if (session.target.kind === "box") {
    if (!boxProject) {
      throw new Error("box-target session needs a pinned project (root/rootPath) to apply edits");
    }
    const client = connectionManager.clientFor(boxProject.deviceId);
    return makeRemoteApplyTarget({
      baseUrl: client.baseUrl,
      headers: client.getAuthHeaders(),
      root: boxProject.root,
      rootPath: boxProject.rootPath,
    });
  }
  return phoneApplyTarget;
}

export interface ExecResult {
  execId: string;
  pid: number;
}

/** Run a shell command for the session's target. Build/test/git for a
 *  Hermes-only-remote session execute on the BOX via its agent; a phone-local
 *  Hermes session has no general shell unless the on-device proot agent is up
 *  (Android), in which case we route to the loopback agent. */
export async function execForSession(
  session: CodingSession,
  command: string,
  opts?: { workDir?: string; timeout?: number; env?: Record<string, string> },
): Promise<ExecResult> {
  const boxId = sessionEndpointDeviceId(session); // box id, or null for phone
  if (boxId) {
    return connectionManager.clientFor(boxId).startExec(command, opts);
  }
  // Phone target: only the Android on-device (proot) agent can run a shell.
  // clientFor(LOCAL_BOX) routes to 127.0.0.1:18080 (see sandboxControl wiring).
  const local = connectionManager.clientFor(LOCAL_BOX_DEVICE_ID);
  if (!local?.isConnected) {
    throw new Error(
      "this session has no machine to run commands — pair a box, or enable the on-device sandbox (Android)",
    );
  }
  return local.startExec(command, opts);
}

/** True when the session's commands run on a real machine (box or on-device
 *  proot). False ⇒ the editor should hide build/test and say "edits apply here;
 *  pair a machine to run them" (the iOS Hermes-on-phone case). */
export function sessionCanRunCommands(session: CodingSession): boolean {
  if (sessionEndpointDeviceId(session)) return true; // box
  // phone target: only if the loopback proot agent is connected
  const local = connectionManager.clientFor(LOCAL_BOX_DEVICE_ID);
  return !!local?.isConnected;
}
