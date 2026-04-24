#!/usr/bin/env bun
// yaver-web-headless CLI. Every web-dashboard action has a verb.
// Output is JSON on stdout (so pipelines can `| jq`), progress logs
// go to stderr.
//
// Quick map:
//   sign-in / sign-up / sign-out / whoami ... Convex auth
//   devices                                  ... list devices for this user
//   connect <deviceId>                       ... probe relay → tunnel → direct
//   reauth <deviceId>                        ... hand session down via relay
//   repair-relay                             ... re-sync userSettings.relayPassword
//   dev-start / dev-stop / dev-reload        ... /dev/* lifecycle
//   dev-status                               ... current dev server state
//   webview-url <deviceId>                   ... print the iframe URL the web UI uses
//   vibe <prompt>                            ... createTask (vibing composer)
//   reconnect <deviceId>                     ... full Reconnect & Fix loop
//   config                                   ... print loaded config (minus token)

import minimist from "minimist";
import { WebClient, type DevServerFramework } from "../web-client";

const VERSION = "0.1.0";
const DEFAULT_CONVEX = "https://perceptive-minnow-557.eu-west-1.convex.site";

interface Args {
  _: string[];
  [k: string]: unknown;
}

function usage(): string {
  return `
yaver-web-headless v${VERSION}

Usage:
  yaver-web-headless <command> [--flags]

Common flags (all commands):
  --convex=<url>        Convex site URL (default: $YAVER_CONVEX_URL or
                        ${DEFAULT_CONVEX})
  --token=<bearer>      Session token (default: $YAVER_TOKEN)
  --agent=<url>         Override agent base URL (skip relay probe)
  --relay-password=<s>  Seed the per-user relay password

Commands:
  sign-in --email= --password=
  sign-up --email= --password= [--full-name=]
  sign-out
  whoami

  devices
  connect <deviceId>                   Probe relay → tunnel → direct
  reauth <deviceId>                    Hand the box a fresh session
  repair-relay                         Re-sync user's relayPassword

  dev-status --device=<deviceId>
  dev-start --device=<id> --framework=<vite|nextjs|expo|react-native|flutter> --work-dir=<path>
            --app=<name>               Monorepo: resolve framework+workDir from workspace manifest
            --surface=web-reload|hot-reload   Gate app kind against the caller surface
  dev-stop  --device=<id>
  dev-reload --device=<id>
  webview-url --device=<id>            Print the iframe-ready dev URL
  workspace-apps --device=<id> [--kind=web,hybrid]   List monorepo apps
  workspace-get --device=<id>          Full manifest + resolved root
  web-preview --device=<id> --app=<name>
                                       Start a web dev server for a workspace app
                                       and print the preview URL (surface=web-reload)

  vibe --device=<id> <prompt>          Dispatch a coding task
  task-list --device=<id> [--limit=20]
  task-stop --device=<id> <taskId>
  task-continue --device=<id> <taskId> <prompt>

  reconnect --device=<id>              Run Reconnect & Fix

Examples:
  yaver-web-headless sign-in --email=you@example.com --password='...'
  yaver-web-headless devices
  yaver-web-headless connect abc123
  yaver-web-headless dev-start --device=abc123 --framework=vite --work-dir=/workspace/myapp
  yaver-web-headless webview-url --device=abc123
  yaver-web-headless vibe --device=abc123 'add a signup page'
  yaver-web-headless reconnect --device=abc123
`.trimStart();
}

function fail(msg: string, code = 1): never {
  process.stderr.write(`[yaver-web-headless] ${msg}\n`);
  process.exit(code);
}

function printJson(value: unknown) {
  process.stdout.write(JSON.stringify(value, null, 2) + "\n");
}

function strArg(args: Args, name: string, fallback?: string): string | undefined {
  const v = args[name];
  if (typeof v === "string" && v !== "") return v;
  if (fallback !== undefined) return fallback;
  return undefined;
}

function requireArg(args: Args, name: string): string {
  const v = strArg(args, name);
  if (!v) fail(`missing required --${name}`);
  return v;
}

async function buildClient(args: Args): Promise<WebClient> {
  const client = new WebClient({
    convexUrl: strArg(args, "convex", process.env.YAVER_CONVEX_URL || DEFAULT_CONVEX),
    token: strArg(args, "token", process.env.YAVER_TOKEN || undefined),
    agentBaseUrl: strArg(args, "agent"),
    userRelayPassword: strArg(args, "relay-password") || undefined,
  });
  // Warm relay config if we have a token — lets `devices`/`connect`
  // hit a relay on the first call without a manual refresh step.
  if (client.isAuthed) {
    try {
      await client.refreshRelayConfig();
    } catch {
      /* non-fatal */
    }
  }
  return client;
}

async function main() {
  const args = minimist(process.argv.slice(2)) as Args;
  const cmd = String(args._[0] || "");

  if (!cmd || cmd === "help" || args["help"] || args["h"]) {
    process.stdout.write(usage());
    return;
  }
  if (cmd === "version" || args["version"]) {
    printJson({ version: VERSION });
    return;
  }

  switch (cmd) {
    case "sign-in": {
      const client = await buildClient(args);
      const email = requireArg(args, "email");
      const password = requireArg(args, "password");
      const token = await client.signIn({ email, password });
      printJson({ ok: true, token });
      return;
    }
    case "sign-up": {
      const client = await buildClient(args);
      const email = requireArg(args, "email");
      const password = requireArg(args, "password");
      const fullName = strArg(args, "full-name") || undefined;
      const token = await client.signUp({ email, password, fullName });
      printJson({ ok: true, token });
      return;
    }
    case "sign-out": {
      const client = await buildClient(args);
      await client.signOut();
      printJson({ ok: true });
      return;
    }
    case "whoami": {
      const client = await buildClient(args);
      printJson(await client.whoami());
      return;
    }

    case "devices": {
      const client = await buildClient(args);
      printJson(await client.listDevices());
      return;
    }

    case "connect": {
      const client = await buildClient(args);
      const deviceId = String(args._[1] || requireArg(args, "device"));
      const res = await client.connect(deviceId);
      printJson({ ...res, baseUrl: client.baseUrl, mode: client.connectionMode });
      return;
    }

    case "reauth": {
      const client = await buildClient(args);
      const deviceId = String(args._[1] || requireArg(args, "device"));
      printJson(await client.reauthAgent({ deviceId }));
      return;
    }

    case "repair-relay": {
      const client = await buildClient(args);
      printJson(await client.repairRelay());
      return;
    }

    case "dev-status":
    case "dev-start":
    case "dev-stop":
    case "dev-reload":
    case "webview-url":
    case "workspace-apps":
    case "workspace-get":
    case "web-preview":
    case "vibe":
    case "task-list":
    case "task-stop":
    case "task-continue":
    case "reconnect": {
      // All these need an active agent connection.
      const client = await buildClient(args);
      const deviceId = requireArg(args, "device");
      const connected = await client.connect(deviceId);
      if (!connected.ok) {
        fail(
          `could not reach ${deviceId}. Diagnostics: ` +
            JSON.stringify(connected.diagnostics),
        );
      }
      switch (cmd) {
        case "dev-status":
          printJson((await client.getDevServerStatus()) ?? { running: false });
          return;
        case "dev-start": {
          // Three ways to start a dev server:
          //   1. --app=<name>  (resolves framework + workDir from manifest)
          //   2. --framework=<f> --work-dir=<path>  (explicit)
          //   3. --app=<name> --surface=web-reload  (monorepo + gated)
          const app = strArg(args, "app");
          const surface = strArg(args, "surface") as
            | "web-reload"
            | "hot-reload"
            | undefined;
          if (app) {
            await client.startDevServer({ app, surface });
          } else {
            const framework = requireArg(args, "framework") as DevServerFramework;
            const workDir = requireArg(args, "work-dir");
            await client.startDevServer({ framework, workDir, surface });
          }
          printJson({ ok: true });
          return;
        }
        case "workspace-apps": {
          const kind = strArg(args, "kind");
          printJson(await client.getWorkspaceApps(kind));
          return;
        }
        case "workspace-get": {
          printJson(await client.getWorkspace());
          return;
        }
        case "web-preview": {
          const app = requireArg(args, "app");
          await client.startDevServer({ app, surface: "web-reload" });
          // Poll status briefly so the dev server has a chance to bind
          // its port before we print the URL.
          for (let i = 0; i < 10; i++) {
            const s = await client.getDevServerStatus();
            if (s?.running) break;
            await new Promise((r) => setTimeout(r, 200));
          }
          const status = await client.getDevServerStatus();
          printJson({
            ok: true,
            app,
            previewUrl: client.devPreviewUrl,
            eventsUrl: client.devEventsUrl,
            status,
          });
          return;
        }
        case "dev-stop":
          await client.stopDevServer();
          printJson({ ok: true });
          return;
        case "dev-reload":
          printJson(await client.reloadDevServer());
          return;
        case "webview-url":
          printJson({
            previewUrl: client.devPreviewUrl,
            eventsUrl: client.devEventsUrl,
            mode: client.connectionMode,
          });
          return;
        case "vibe": {
          const prompt = (args._[1] ? String(args._[1]) : "") || requireArg(args, "prompt");
          const workDir = strArg(args, "work-dir");
          const runner = strArg(args, "runner");
          const title = (prompt as string).slice(0, 80);
          const task = await client.createTask({
            title,
            description: prompt as string,
            runner,
            workDir,
          });
          printJson(task);
          return;
        }
        case "task-list": {
          const limit = args["limit"] ? Number(args["limit"]) : undefined;
          printJson(await client.listTasks(limit));
          return;
        }
        case "task-stop": {
          const taskId = String(args._[1] || requireArg(args, "task"));
          await client.stopTask(taskId);
          printJson({ ok: true });
          return;
        }
        case "task-continue": {
          const taskId = String(args._[1] || requireArg(args, "task"));
          const prompt = String(args._[2] || requireArg(args, "prompt"));
          await client.continueTask(taskId, prompt);
          printJson({ ok: true });
          return;
        }
        case "reconnect": {
          const report = await client.reconnectAndFix({
            deviceId,
            log: (line) => process.stderr.write(line + "\n"),
          });
          printJson(report);
          return;
        }
      }
      return;
    }

    case "config": {
      const client = await buildClient(args);
      printJson({
        convexUrl: client.convexUrl,
        hasToken: client.isAuthed,
        mode: client.connectionMode,
      });
      return;
    }
  }

  fail(`unknown command: ${cmd}\n\n${usage()}`);
}

main().catch((err) => {
  fail(err?.message || String(err));
});
