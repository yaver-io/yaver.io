import type { P2PClient } from './P2PClient';
import {
  DogfoodRuntimeError,
  runtimeLogLinesFromDevEvent,
  type DogfoodDriver,
} from './DogfoodRuntime';

const delay = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

function reportedPreview(status: { previewUrl?: string; bundleUrl?: string }): string {
  return String(status.previewUrl || status.bundleUrl || '').trim();
}

export interface P2PDogfoodDriverOptions {
  startupTimeoutMs?: number;
  pollIntervalMs?: number;
}

/**
 * Ready-to-use driver for app owners who authenticate with Yaver's normal
 * account flow. Their UI owns the trigger and preview surface; this adapter
 * owns the existing Projects endpoints and raw build log stream.
 */
export function createP2PDogfoodDriver(
  client: P2PClient,
  options: P2PDogfoodDriverOptions = {},
): DogfoodDriver {
  const startupTimeoutMs = Math.max(1, options.startupTimeoutMs ?? 155_000);
  const pollIntervalMs = Math.max(1, options.pollIntervalMs ?? 600);
  return {
    async prepare(context) {
      const close = client.subscribeDogfoodDevEvents(
        (event) => runtimeLogLinesFromDevEvent(event).forEach((line) => context.log(line)),
        (health) => {
          if (health) context.log({ text: `[logs] ${health.message}`, at: Date.now(), stream: 'system' });
        },
      );
      context.registerCleanup(close, 'transient');
    },
    async start(context) {
      const { project } = context;
      if (project.lane === 'webrtc') {
        context.setPhase('starting', `Finding a native runtime for ${project.name}…`);
        const capabilities = await client.getDogfoodRemoteRuntimeCapabilities(project.workDir, project.framework, context.signal);
        const nativeTargets = capabilities.targets.filter((target) => target.enabled && target.id !== 'browser-window');
        const target = project.nativeTargetId
          ? nativeTargets.find((candidate) => candidate.id === project.nativeTargetId)
          : nativeTargets[0];
        if (!target) {
          throw new DogfoodRuntimeError({
            code: 'DOGFOOD_NATIVE_RUNTIME_UNAVAILABLE',
            error: project.nativeTargetId
              ? `Native runtime ${project.nativeTargetId} is not available.`
              : 'No native simulator, emulator, or device runtime is available.',
            remedy: 'Start or install a native runtime on the selected machine, then retry WebRTC; Browser lane remains available.',
            retryable: true,
          });
        }
        context.log({
          text: `[runtime] source · ${target.label}${target.platform ? ` · ${target.platform}` : ''}`,
          at: Date.now(),
          stream: 'system',
        });
        context.setPhase('starting', `Starting ${target.label} over WebRTC…`);
        const session = await client.startDogfoodRemoteRuntime(project.workDir, project.framework, target.id, context.signal);
        context.log({
          text: `[runtime] ${session.status || 'starting'}${session.note ? ` · ${session.note}` : ''}`,
          at: Date.now(),
          stream: 'system',
        });
        context.registerCleanup(() => client.stopDogfoodRemoteRuntime(session.id), 'session');
        return {
          lane: 'webrtc',
          sessionId: session.id,
          metadata: { target, session, framework: project.framework, workDir: project.workDir },
        };
      }
      context.setPhase(project.lane === 'hermes' ? 'compiling' : 'starting',
        project.lane === 'hermes' ? `Compiling ${project.name} with Hermes…` : `Starting ${project.name} in the browser…`);
      context.log({
        text: project.lane === 'hermes'
          ? `[runtime] source · Hermes build on ${project.workDir}`
          : `[runtime] source · browser build on ${project.workDir}`,
        at: Date.now(),
        stream: 'system',
      });
      // Register before the start request so Stop reaches /dev/stop while an
      // Expo/Flutter start or Hermes build is still in flight. Registering only
      // after await made the visible Stop action a local-state no-op.
      const unregisterInFlightStop = context.registerCleanup(() => client.stopDogfoodDevServer(), 'session');
      const status = await client.startDogfoodDevServer({
        framework: project.framework,
        workDir: project.workDir,
        lane: project.lane,
      }, context.signal);
      if (project.lane === 'hermes') unregisterInFlightStop();
      if (status.error) {
        throw new DogfoodRuntimeError({
          code: 'DOGFOOD_DEV_SERVER_FAILED', error: status.error,
          remedy: 'Fix the named project/runtime error, then retry Dogfood.', retryable: true,
        });
      }
      let latest = status;
      let reported = reportedPreview(latest);
      // /dev/start may return the stable /dev/ proxy path before the child
      // process has bound a port. A URL is inventory; running/serving is the
      // operation. Poll until both are true so a stale bundleUrl cannot close
      // the setup sheet while Expo is actually failing in the background.
      if (project.lane === 'browser' && (!reported || !(latest.running || latest.serving))) {
        context.setPhase('compiling', `Compiling ${project.name} for the browser…`);
        const deadline = Date.now() + startupTimeoutMs;
        while (context.isCurrent() && Date.now() < deadline) {
          await delay(pollIntervalMs);
          const polled = await client.getDogfoodDevServerStatus(context.signal);
          if (!polled) continue;
          latest = polled;
          if (latest.error && !latest.building) {
            throw new DogfoodRuntimeError({
              code: 'DOGFOOD_DEV_SERVER_FAILED', error: latest.error,
              remedy: 'Fix the named project/runtime error, then retry Dogfood.', retryable: true,
            });
          }
          reported = reportedPreview(latest);
          if (reported && (latest.running || latest.serving)) break;
        }
        if (!context.isCurrent()) {
          throw new DogfoodRuntimeError({
            code: 'DOGFOOD_ATTEMPT_REPLACED', error: 'A newer Dogfood attempt replaced this compile.',
            remedy: 'Wait for the newer attempt.', retryable: true,
          });
        }
        if (!reported || !(latest.running || latest.serving)) {
          throw new DogfoodRuntimeError({
            code: 'DOGFOOD_NO_RENDER_URL',
            error: `The dev server did not report a browser preview URL within ${Math.ceil(startupTimeoutMs / 1000)} seconds.`,
            remedy: 'Read the live npm/compiler output above, fix the named failure, then retry. Flutter uses `-d web-server`; Expo/RN needs react-native-web.',
            retryable: true,
          });
        }
      }
      return {
        lane: project.lane,
        url: reported ? client.resolveDogfoodUrl(reported) : undefined,
        metadata: {
          framework: latest.framework || project.framework,
          workDir: latest.workDir || project.workDir,
          ...(project.lane === 'hermes' ? { delivered: latest.running === true } : {}),
        },
      };
    },
  };
}
