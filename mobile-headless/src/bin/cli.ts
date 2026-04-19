#!/usr/bin/env bun
// yaver-mobile-headless CLI entry point.
//
// The Go CLI's `yaver mobile-test …` subcommand shells out to this
// binary. It's designed to be thin — every command maps 1:1 to a
// MobileClient method, with JSON on stdout so humans + CI can pipe
// to jq.

import minimist from "minimist";
import { MobileClient } from "../mobile-client.js";

async function main() {
  const argv = minimist(process.argv.slice(2), {
    string: ["token", "email", "password", "device", "target", "platform", "data-dir", "tool", "preset", "parent-dir", "convex-url", "relay"],
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
    case "connect": {
      const id = argv.device || rest[0];
      if (!id) die("connect needs --device <id> or positional <id>");
      await mobile.connect(id);
      out({ ok: true, device: id });
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
