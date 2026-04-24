#!/usr/bin/env bun
// yaver-mobile-headless CLI entry point.
//
// The Go CLI's `yaver mobile-test …` subcommand shells out to this
// binary. It's designed to be thin — every command maps 1:1 to a
// MobileClient method, with JSON on stdout so humans + CI can pipe
// to jq.

import * as fs from "node:fs";
import * as path from "node:path";
import minimist from "minimist";
import { MobileClient } from "../mobile-client.js";

async function main() {
  const argv = minimist(process.argv.slice(2), {
    string: ["token", "email", "password", "device", "target", "platform", "data-dir", "tool", "preset", "parent-dir", "convex-url", "relay", "verb", "machine", "payload", "name", "template", "slug", "prompt", "base-url", "target-token", "on-conflict", "bundle-url", "module-name", "repo-url", "branch", "commit", "manifest-url", "workflow", "run-id", "provider", "out", "dir", "bootstrap-secret", "mode"],
    alias: { t: "token", d: "device" },
  });

  const [command, ...rest] = argv._;
  if (!command || argv.h || argv.help) {
    printHelp();
    process.exit(command ? 0 : 1);
  }

  const mobile = new MobileClient({
    dataDir: argv["data-dir"],
    convexUrl: argv["convex-url"],
    authToken: argv.token,
    platform: (argv.platform as any) || undefined,
  });

  switch (command) {
    case "sign-in": {
      if (argv.token) await mobile.signIn({ token: argv.token });
      else if (argv.email && argv.password) await mobile.signIn({ email: argv.email, password: argv.password });
      else die("sign-in needs --token OR --email + --password");
      out({ ok: true });
      break;
    }
    case "devices": {
      out(await mobile.listDevices());
      break;
    }
    case "primary-get":
    case "primary": {
      out({ primaryDeviceId: await mobile.getPrimaryDevice() });
      break;
    }
    case "primary-set": {
      const id = argv.device || rest[0];
      if (!id) die("primary-set needs --device <id> or positional <id>");
      await mobile.setPrimaryDevice(String(id));
      out({ ok: true, primaryDeviceId: id });
      break;
    }
    case "primary-clear": {
      await mobile.setPrimaryDevice(null);
      out({ ok: true, primaryDeviceId: null });
      break;
    }
    case "relay-presence": {
      const relay = (argv as any).relay ?? process.env.YMH_RELAY_URL;
      if (!relay) die("relay-presence needs --relay <httpUrl> or $YMH_RELAY_URL");
      const ids = rest.length > 0 ? rest.map(String) : (await mobile.listDevices()).map((d) => d.id);
      out(await mobile.relayPresence(String(relay), ids));
      break;
    }
    case "race-paths": {
      // Hit every candidate IP in parallel and return the winner — mirrors
      // the parallel race the real mobile app runs inside connect().
      const id = argv.device || rest[0];
      if (!id) die("race-paths needs --device <id>");
      const devices = await mobile.listDevices();
      const d = devices.find((x) => x.id === id);
      if (!d) die(`device not found: ${id}`);
      out(await mobile.raceDevicePaths(d!));
      break;
    }
    case "auto-connect-target": {
      // Show the device the real app would auto-connect to given current
      // state. Useful for verifying primary-device + online merges.
      const devices = await mobile.listDevices();
      const primary = await mobile.getPrimaryDevice();
      const target = mobile.pickAutoConnectTarget(devices, primary);
      out({ primaryDeviceId: primary, target: target ? { id: target.id, name: target.name } : null });
      break;
    }
    case "ops-verbs": {
      out(await mobile.opsVerbs());
      break;
    }
    case "ops": {
      const verb = (argv as any).verb ?? rest[0];
      if (!verb) die("ops needs --verb=<name> (or positional)");
      const machine = (argv as any).machine ?? "local";
      // Accept --payload as raw JSON or fall through with undefined.
      let payload: unknown;
      if ((argv as any).payload) {
        try { payload = JSON.parse(String((argv as any).payload)); }
        catch { die("--payload must be valid JSON"); }
      }
      out(await mobile.ops(String(verb), payload, String(machine)));
      break;
    }
    case "connect": {
      const id = argv.device || rest[0];
      if (!id) die("connect needs --device <id> or positional <id>");
      await mobile.connect(id);
      out({ ok: true, device: id });
      break;
    }
    case "recover-auth": {
      const id = argv.device || rest[0];
      if (!id) die("recover-auth needs --device <id> or positional <id>");
      out(await mobile.recoverDeviceAuth(String(id), {
        bootstrapSecret: argv["bootstrap-secret"] ? String(argv["bootstrap-secret"]) : undefined,
        mode: ((argv.mode as any) || "auto"),
      }));
      break;
    }
    case "infra": {
      out(await mobile.infraSummary(argv.target));
      break;
    }
    case "install-list": {
      out(await mobile.listInstallables(argv.target));
      break;
    }
    case "install": {
      const tool = argv.tool || rest[0];
      if (!tool) die("install needs --tool <name> or positional <name>");
      for await (const frame of mobile.installTool(tool, { target: argv.target })) {
        out(frame);
      }
      break;
    }
    case "sudo-respond": {
      const tool = argv.tool || rest[0];
      const pw = argv.password ?? process.env.YMH_SUDO_PASSWORD ?? "";
      if (!tool || !pw) die("sudo-respond needs --tool + --password (or $YMH_SUDO_PASSWORD)");
      await mobile.respondSudo(tool, pw, { target: argv.target });
      out({ ok: true });
      break;
    }
    case "wizard-start": {
      out(await mobile.wizard.start());
      break;
    }
    case "wizard-answer": {
      const session = argv.session || rest[0];
      const question = argv.question || rest[1];
      const answer = argv.answer ?? rest[2];
      if (!session || !question) die("wizard-answer needs --session --question --answer");
      out(await mobile.wizard.answer(session, question, String(answer)));
      break;
    }
    case "wizard-generate": {
      const session = argv.session || rest[0];
      if (!session) die("wizard-generate needs --session");
      out(await mobile.wizard.generate(session, argv["parent-dir"]));
      break;
    }
    case "phone-projects": {
      out(await mobile.phoneProjects.list());
      break;
    }
    case "phone-project-get": {
      const slug = argv.slug || rest[0];
      if (!slug) die("phone-project-get needs --slug <slug>");
      out(await mobile.phoneProjects.get(String(slug)));
      break;
    }
    case "phone-project-create": {
      const name = argv.name || rest[0];
      if (!name) die("phone-project-create needs --name <name>");
      out(await mobile.phoneProjects.create({
        name: String(name),
        slug: argv.slug ? String(argv.slug) : undefined,
        template: argv.template ? String(argv.template) : undefined,
        prompt: argv.prompt ? String(argv.prompt) : undefined,
      }));
      break;
    }
    case "phone-project-create-at": {
      const name = argv.name || rest[0];
      const baseUrl = argv["base-url"];
      if (!name || !baseUrl) die("phone-project-create-at needs --name <name> --base-url <url>");
      out(await mobile.phoneProjects.createAt({
        baseUrl: String(baseUrl),
        authToken: argv["target-token"] ? String(argv["target-token"]) : undefined,
      }, {
        name: String(name),
        slug: argv.slug ? String(argv.slug) : undefined,
        template: argv.template ? String(argv.template) : undefined,
        prompt: argv.prompt ? String(argv.prompt) : undefined,
      }));
      break;
    }
    case "phone-project-export": {
      const slug = argv.slug || rest[0];
      if (!slug) die("phone-project-export needs --slug <slug>");
      const exported = await mobile.phoneProjects.export(String(slug), {
        includeData: !!argv["include-data"],
        containerize: !!argv.containerize,
      });
      out({
        slug,
        size: exported.size,
        contentType: exported.contentType,
      });
      break;
    }
    case "phone-project-push": {
      const slug = argv.slug || rest[0];
      const baseUrl = argv["base-url"];
      if (!slug || !baseUrl) die("phone-project-push needs --slug <slug> --base-url <url>");
      out(await mobile.phoneProjects.push(String(slug), {
        baseUrl: String(baseUrl),
        authToken: argv["target-token"] ? String(argv["target-token"]) : undefined,
      }, {
        onConflict: argv["on-conflict"] as any,
        includeData: !!argv["include-data"],
        containerize: !!argv.containerize,
        skipSeed: !!argv["skip-seed"],
      }));
      break;
    }
    case "todo-cloud-bootstrap": {
      const name = argv.name || rest[0];
      const baseUrl = argv["base-url"];
      if (!name || !baseUrl) die("todo-cloud-bootstrap needs --name <name> --base-url <url>");
      out(await mobile.phoneProjects.bootstrapTodoDeploy({
        name: String(name),
        slug: argv.slug ? String(argv.slug) : undefined,
        prompt: argv.prompt ? String(argv.prompt) : undefined,
        template: argv.template ? String(argv.template) : "todos",
        target: {
          baseUrl: String(baseUrl),
          authToken: argv["target-token"] ? String(argv["target-token"]) : undefined,
        },
        includeData: !!argv["include-data"],
        containerize: argv.containerize !== false,
        onConflict: (argv["on-conflict"] as any) || "rename",
      }));
      break;
    }
    case "preview-manifest-create": {
      const name = argv.name || rest[0];
      const bundleUrl = argv["bundle-url"];
      if (!name || !bundleUrl) die("preview-manifest-create needs --name <name> --bundle-url <url>");
      const manifest = {
        version: 1,
        name: String(name),
        description: argv.prompt ? String(argv.prompt) : undefined,
        bundleUrl: String(bundleUrl),
        moduleName: argv["module-name"] ? String(argv["module-name"]) : "main",
        runtime: "hermes",
        git: {
          repoUrl: argv["repo-url"] ? String(argv["repo-url"]) : undefined,
          branch: argv.branch ? String(argv.branch) : undefined,
          commit: argv.commit ? String(argv.commit) : undefined,
        },
        feedback: {
          sdk: "yaver",
          compileTimeInjected: !!argv["compile-time-injected"],
        },
        ci: {
          provider: argv.provider ? String(argv.provider) : "github-actions",
          workflow: argv.workflow ? String(argv.workflow) : undefined,
          runId: argv["run-id"] ? String(argv["run-id"]) : undefined,
        },
        sharing: {
          hostVisible: argv["host-visible"] !== false,
          guestVisible: !!argv["guest-visible"],
        },
      };
      const outPath = argv.out ? path.resolve(String(argv.out)) : "";
      if (outPath) {
        fs.mkdirSync(path.dirname(outPath), { recursive: true });
        fs.writeFileSync(outPath, JSON.stringify(manifest, null, 2) + "\n", "utf8");
        out({ ok: true, path: outPath, manifest });
      } else {
        out(manifest);
      }
      break;
    }
    case "repo-bootstrap-remote": {
      const repoUrl = argv["repo-url"] || rest[0];
      if (!repoUrl) die("repo-bootstrap-remote needs --repo-url <url>");
      out(await mobile.repos.bootstrapRemote({
        repoUrl: String(repoUrl),
        branch: argv.branch ? String(argv.branch) : undefined,
        targetDir: argv.dir ? String(argv.dir) : undefined,
        feedbackPlatform: (argv.platform ? String(argv.platform) : "react-native") as any,
        ciTargets: rest.slice(1).length ? rest.slice(1).map(String) : undefined,
      }));
      break;
    }
    case "guests": {
      out(await mobile.guests.list());
      break;
    }
    case "raw-get": {
      const path = rest[0] || argv.path;
      if (!path) die("raw-get needs a path arg");
      out(await mobile.raw.get(path));
      break;
    }
    case "raw-post": {
      const path = rest[0] || argv.path;
      if (!path) die("raw-post needs a path arg");
      let body: any = undefined;
      if (argv.body) body = JSON.parse(argv.body);
      out(await mobile.raw.post(path, body));
      break;
    }
    case "mcp": {
      // Dispatch into the MCP server. Separate file so its deps
      // (@modelcontextprotocol/sdk) don't load on every CLI invocation.
      await import("./mcp.js");
      break;
    }
    default:
      die(`unknown command: ${command}`);
  }
}

function out(value: unknown) {
  process.stdout.write(JSON.stringify(value, null, 2) + "\n");
}

function die(msg: string): never {
  process.stderr.write("yaver-mobile-headless: " + msg + "\n");
  process.exit(2);
}

function printHelp() {
  process.stdout.write(`yaver-mobile-headless — headless surrogate for the Yaver mobile app.

Usage:
  yaver-mobile-headless sign-in --token=...         # or --email + --password
  yaver-mobile-headless devices                     # list paired devices (now includes lanIps[])
  yaver-mobile-headless connect <deviceId>
  yaver-mobile-headless recover-auth <deviceId> [--mode=auto|direct|pair|device-code]
  yaver-mobile-headless primary                     # read primaryDeviceId
  yaver-mobile-headless primary-set <deviceId>      # mark auto-connect target
  yaver-mobile-headless primary-clear               # unset primary preference
  yaver-mobile-headless relay-presence --relay=<httpUrl> [id...]
                                                     # live tunnel-up state per deviceId
  yaver-mobile-headless race-paths --device=<id>    # race beacon + lanIps + host in parallel
  yaver-mobile-headless auto-connect-target          # which device would the app auto-pick?
  yaver-mobile-headless infra [--target=<id>]
  yaver-mobile-headless install-list [--target=<id>]
  yaver-mobile-headless install <tool> [--target=<id>]   # streams JSONL
  yaver-mobile-headless sudo-respond <tool> --password=...
  yaver-mobile-headless wizard-start
  yaver-mobile-headless wizard-answer <session> <question> <answer>
  yaver-mobile-headless wizard-generate <session> [--parent-dir=...]
  yaver-mobile-headless phone-projects
  yaver-mobile-headless phone-project-get --slug=<slug>
  yaver-mobile-headless phone-project-create --name="Todo App" [--template=todos] [--prompt=...]
  yaver-mobile-headless phone-project-create-at --base-url=<host> --name="Todo App"
  yaver-mobile-headless phone-project-export --slug=<slug> [--include-data] [--containerize]
  yaver-mobile-headless phone-project-push --slug=<slug> --base-url=<host> [--target-token=...]
  yaver-mobile-headless todo-cloud-bootstrap --name="Todo App" --base-url=<host> [--target-token=...]
  yaver-mobile-headless preview-manifest-create --name="Todo Preview" --bundle-url=<url> [--out=preview.json]
  yaver-mobile-headless repo-bootstrap-remote --repo-url=<url> [--platform=react-native] [hermes feedback]
  yaver-mobile-headless guests
  yaver-mobile-headless raw-get /info
  yaver-mobile-headless raw-post /some/path --body='{"k":"v"}'
  yaver-mobile-headless mcp                         # stdio MCP server

Every command emits JSON on stdout. Pipe to jq.
Env:
  YMH_DATA_DIR   isolation sandbox for storage + secure-store
  YMH_PLATFORM   "ios" | "android"
  YMH_DEVICE_NAME
`);
}

main().catch((e) => {
  process.stderr.write("error: " + (e?.stack || e?.message || String(e)) + "\n");
  process.exit(1);
});
