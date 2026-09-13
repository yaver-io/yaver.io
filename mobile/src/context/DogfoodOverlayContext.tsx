import { router, usePathname } from "expo-router";
import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";

import { BrowserVibeBubble } from "../components/BrowserVibeBubble";
import {
  prepareDogfoodCheckoutOnly,
  prepareDogfoodMode,
  refreshAttachSession,
  reloadAttachedDogfoodBrowserLane,
  startDogfoodHermesLane,
  stopAttachSession,
  stopDogfoodDevLane,
} from "../lib/attachClient";
import {
  DogfoodController,
  DogfoodRuntimeError,
  type DogfoodLane,
  type DogfoodResult,
  type DogfoodSnapshot,
} from "../../../sdk/feedback/react-native/src/DogfoodRuntime";
import type { BrowserLaneViewport } from "../lib/browserLaneDoctor";

export type DogfoodOverlayRequest = {
  workDir: string;
  runner: string;
  deviceId: string;
  deviceName: string;
  lane: DogfoodLane;
  fallbackLane?: DogfoodLane;
  usageMode: "chat-only" | "reload-only" | "reload-and-chat";
  startBehavior: "vibe-first" | "render-on-open";
  renderBehavior: "manual" | "auto-on-request";
  sessionBehavior: "resume-last" | "new-session";
  browserViewport: BrowserLaneViewport;
};

type DogfoodOverlayValue = {
  active: boolean;
  busy: boolean;
  status: string;
  request: DogfoodOverlayRequest | null;
  snapshot: DogfoodSnapshot | null;
  issue: { message: string; fix?: () => void | Promise<void> } | null;
  begin(request: DogfoodOverlayRequest): void;
  open(): Promise<boolean>;
  retry(): void;
  reload(kind?: "fast" | "full"): Promise<boolean>;
  end(): Promise<void>;
  goHome(): void;
  goTasks(): void;
  reportIssue(issue: { message: string; fix?: () => void | Promise<void> } | null): void;
};

const DogfoodOverlayContext = createContext<DogfoodOverlayValue | null>(null);

function previewRoute(request: DogfoodOverlayRequest, result: DogfoodResult) {
  const workDir = typeof result.metadata?.workDir === "string" && result.metadata.workDir.trim()
    ? result.metadata.workDir.trim()
    : request.workDir;
  if (result.lane === "webrtc") {
    router.navigate({
      pathname: "/remote-runtime" as any,
      params: {
        project: "Yaver",
        path: workDir.replace(/\/+$/, "") + "/mobile",
        framework: "expo",
        usageMode: request.usageMode,
        renderBehavior: request.renderBehavior,
        sessionBehavior: request.sessionBehavior,
      },
    } as any);
    return;
  }
  if (result.lane === "hermes") {
    router.replace("/(tabs)/dogfood" as any);
    return;
  }
  if (result.lane !== "browser") return;
  // Reload returns here from Tasks, where Expo Router retains the earlier
  // Attach screen below the tab route. `navigate` reselects that hidden DOM
  // node on RN-web; replace creates the visible surface for the new render.
  router.replace({
    pathname: "/attach" as any,
    params: {
      sessionId: result.sessionId,
      url: result.url,
      workDir,
      runner: request.runner,
      deviceId: request.deviceId,
      deviceName: request.deviceName,
      usageMode: request.usageMode,
      renderBehavior: request.renderBehavior,
      sessionBehavior: request.sessionBehavior,
    },
  } as any);
}

export function DogfoodOverlayProvider({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const controllerRef = useRef<DogfoodController | null>(null);
  const requestRef = useRef<DogfoodOverlayRequest | null>(null);
  const runRef = useRef<Promise<DogfoodResult> | null>(null);
  const [request, setRequest] = useState<DogfoodOverlayRequest | null>(null);
  const [snapshot, setSnapshot] = useState<DogfoodSnapshot | null>(null);
  const [issue, setIssue] = useState<{ message: string; fix?: () => void | Promise<void> } | null>(null);

  // `kind` is present only for an explicit Reload control. Entering with
  // "render on open" hands off the already-proven surface without asking the
  // box to reload it a second time. Before this distinction, Fast Reload from
  // Tasks only navigated to the existing attach session: no request reached
  // the browser lane, so the UI honestly did nothing.
  const openPreparedPreview = useCallback(async (kind?: "fast" | "full") => {
    const controller = controllerRef.current;
    const activeRequest = requestRef.current;
    if (!controller || !activeRequest) return false;
    const result = runRef.current ? await runRef.current : controller.snapshot().result || await controller.trigger();
    if (controllerRef.current !== controller || requestRef.current !== activeRequest) return false;
    const workDir = typeof result.metadata?.workDir === "string" && result.metadata.workDir.trim()
      ? result.metadata.workDir.trim()
      : activeRequest.workDir;
    if (kind && result.lane === "browser") {
      const reload = await reloadAttachedDogfoodBrowserLane(activeRequest.deviceId, workDir, kind);
      if (!reload.ok) {
        const message = reload.message || reload.error || "Dogfood browser reload failed.";
        throw new Error(reload.remedy ? `${message} ${reload.remedy}` : message);
      }
    }
    if (controller.snapshot().phase !== "ready") {
      throw new Error("Dogfood is not ready to open yet. Wait for preparation to finish, then try again.");
    }
    // The root provider remains the lifecycle owner while Attach, Dogfood and
    // Tasks move above one another. Calling controller.handoff() here discarded
    // its session cleanup, so Exit Dogfood could no longer revoke the browser
    // capability after the user visited Tasks. Navigation is a view change,
    // never an ownership transfer.
    previewRoute(activeRequest, result);
    return true;
  }, []);

  const begin = useCallback((next: DogfoodOverlayRequest) => {
    const previous = controllerRef.current;
    if (previous) void previous.stop();
    requestRef.current = next;
    setRequest(next);
    setIssue(null);

    const controller = new DogfoodController({
      name: "Yaver",
      workDir: next.workDir,
      framework: "expo",
      lane: next.lane,
      fallbackLane: next.fallbackLane,
      repositoryUrl: "https://github.com/yaver-io/yaver.io.git",
    }, {
      async start(context) {
        if (context.project.lane === "hermes") {
          // Register before the long request. Stop must reach the agent's
          // active-build registry while Metro/hermesc is still running.
          const unregisterInFlightStop = context.registerCleanup(async () => { await stopDogfoodDevLane(next.deviceId); }, "session");
          const prepared = await prepareDogfoodCheckoutOnly(next.deviceId, next.workDir, (message) => {
            context.setPhase("preparing", message);
            context.log({ text: message, at: Date.now(), stream: "system" });
          }, context.signal);
          if (!prepared.ok) throw new DogfoodRuntimeError({
            code: prepared.code, error: prepared.error, remedy: prepared.remedy,
            retryable: true, fixPrompt: prepared.fixPrompt,
          });
          context.setPhase("compiling", "Compiling Yaver for the installed Hermes host…");
          const delivered = await startDogfoodHermesLane(next.deviceId, prepared.workDir, context.signal);
          if (!delivered.ok) throw new DogfoodRuntimeError({
            code: delivered.code, error: delivered.error, remedy: delivered.remedy, retryable: true,
          });
          unregisterInFlightStop();
          context.log({ text: delivered.message, at: Date.now(), stream: "system" });
          return { lane: "hermes", metadata: { workDir: prepared.workDir, branch: prepared.branch, pushPolicy: prepared.pushPolicy } };
        }
        if (context.project.lane === "webrtc") {
          const prepared = await prepareDogfoodCheckoutOnly(next.deviceId, next.workDir, (message) => {
            context.setPhase("preparing", message);
            context.log({ text: message, at: Date.now(), stream: "system" });
          }, context.signal);
          if (!prepared.ok) throw new DogfoodRuntimeError({
            code: prepared.code, error: prepared.error, remedy: prepared.remedy,
            retryable: true, fixPrompt: prepared.fixPrompt,
          });
          context.setPhase("starting", "Opening Yaver's native WebRTC runtime…");
          return { lane: "webrtc", metadata: { workDir: prepared.workDir, branch: prepared.branch, pushPolicy: prepared.pushPolicy } };
        }
        // The attach session can be minted before Expo finishes. Broad revoke
        // plus /dev/stop are safe, idempotent, and available immediately when
        // the launch screen's Stop action is tapped.
        context.registerCleanup(async () => { await stopAttachSession(next.deviceId); }, "session");
        context.registerCleanup(async () => { await stopDogfoodDevLane(next.deviceId); }, "session");
        const result = await prepareDogfoodMode(
          next.deviceId,
          next.workDir,
          (message) => {
            const lower = message.toLowerCase();
            context.setPhase(lower.includes("compil") ? "compiling" : lower.includes("start") || lower.includes("authoriz") ? "starting" : "preparing", message);
            context.log({ text: message, at: Date.now(), stream: "system" });
          },
          (line) => context.log(line),
          context.signal,
          next.browserViewport,
        );
        if (!result.ok) throw new DogfoodRuntimeError({
          code: result.code, error: result.error, remedy: result.remedy,
          retryable: true, fixPrompt: result.fixPrompt,
        });
        return {
          lane: "browser",
          sessionId: result.sessionId,
          url: result.url,
          metadata: { workDir: result.workDir, branch: result.branch, pushPolicy: result.pushPolicy },
        };
      },
    }, {
      maxLogLines: 200,
      onChange(nextSnapshot) {
        if (controllerRef.current === controller) setSnapshot(nextSnapshot);
      },
    });
    controllerRef.current = controller;
    setSnapshot(controller.snapshot());
    const run = controller.trigger();
    runRef.current = run;
    void run.then(async () => {
      if (controllerRef.current !== controller) return;
      runRef.current = null;
      if (next.startBehavior === "render-on-open") await openPreparedPreview();
    }).catch(() => {
      if (controllerRef.current === controller) runRef.current = null;
    });
  }, [openPreparedPreview]);

  const end = useCallback(async () => {
    const controller = controllerRef.current;
    controllerRef.current = null;
    requestRef.current = null;
    runRef.current = null;
    setRequest(null);
    setSnapshot(null);
    setIssue(null);
    if (controller) await controller.stop();
  }, []);

  const goHome = useCallback(() => {
    // Push the native menu above the guest so returning does not tear down the
    // tested surface. Exit Dogfood is the only action that stops the runtime.
    router.push("/(tabs)/dogfood" as any);
  }, []);

  const goTasks = useCallback(() => {
    const activeRequest = requestRef.current;
    const current = controllerRef.current?.snapshot();
    const resolved = typeof current?.result?.metadata?.workDir === "string"
      ? current.result.metadata.workDir.trim()
      : "";
    const workDir = resolved || activeRequest?.workDir.trim() || "";
    router.navigate({
      pathname: "/(tabs)/tasks" as any,
      params: {
        ...(workDir ? { dir: workDir, selectProject: "1" } : {}),
        ...(activeRequest?.runner ? { runner: activeRequest.runner } : {}),
        sessionStartedFrom: "vibing",
      },
    } as any);
  }, []);

  const retry = useCallback(() => {
    const controller = controllerRef.current;
    const activeRequest = requestRef.current;
    if (!controller || !activeRequest || runRef.current) return;
    const run = controller.retry();
    runRef.current = run;
    void run.then(async () => {
      if (controllerRef.current !== controller) return;
      runRef.current = null;
      if (activeRequest.startBehavior === "render-on-open") await openPreparedPreview();
    }).catch(() => {
      if (controllerRef.current === controller) runRef.current = null;
    });
  }, [openPreparedPreview]);

  useEffect(() => () => {
    const controller = controllerRef.current;
    controllerRef.current = null;
    if (controller) void controller.stop();
  }, []);

  useEffect(() => {
    const sessionId = snapshot?.result?.lane === "browser" ? snapshot.result.sessionId : undefined;
    if (!request || !sessionId) return;
    const timer = setInterval(() => { void refreshAttachSession(request.deviceId, sessionId); }, 4 * 60_000);
    return () => clearInterval(timer);
  }, [request, snapshot?.result]);

  const previewOwnsOverlay = pathname === "/attach" || pathname === "/remote-runtime";
  const nativeDogfoodOwnsControls = pathname === "/dogfood" || pathname === "/(tabs)/dogfood";
  const overlayWorkDir = typeof snapshot?.result?.metadata?.workDir === "string" && snapshot.result.metadata.workDir.trim()
    ? snapshot.result.metadata.workDir.trim()
    : request?.workDir || "";
  return (
    <DogfoodOverlayContext.Provider value={{
      active: request !== null,
      busy: !!snapshot && ["preparing", "starting", "compiling"].includes(snapshot.phase),
      status: snapshot?.result?.metadata?.branch
        ? `${snapshot.message} · ${String(snapshot.result.metadata.branch)}`
        : snapshot?.message || "Dogfood is active.",
      request,
      snapshot,
      issue,
      begin,
      open: async () => openPreparedPreview(),
      retry,
      reload: async (kind = "fast") => openPreparedPreview(kind),
      end,
      goHome,
      goTasks,
      reportIssue: setIssue,
    }}>
      {children}
      {request && snapshot?.phase === "ready" && !previewOwnsOverlay && !nativeDogfoodOwnsControls ? (
        <BrowserVibeBubble
          projectPath={overlayWorkDir}
          projectName="Yaver"
          usageMode={request.usageMode}
          renderBehavior={request.renderBehavior}
          sessionBehavior={request.sessionBehavior}
          exitLabel="Open Dogfood"
          endLabel="End Dogfood"
          onGoHome={goHome}
          onExitPreview={() => { void end(); }}
          onReload={openPreparedPreview}
          reloadBusy={["preparing", "starting", "compiling"].includes(snapshot.phase)}
          reloadProgress={{
            lane: snapshot.project.lane,
            phase: snapshot.phase,
            message: snapshot.message,
            logs: snapshot.logs,
            failure: snapshot.failure,
          }}
        />
      ) : null}
    </DogfoodOverlayContext.Provider>
  );
}

export function useDogfoodOverlay(): DogfoodOverlayValue {
  const value = useContext(DogfoodOverlayContext);
  if (!value) throw new Error("useDogfoodOverlay must be used inside DogfoodOverlayProvider");
  return value;
}
