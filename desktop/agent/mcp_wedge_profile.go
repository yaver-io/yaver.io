package main

// mcp_wedge_profile.go — the ALLOWLIST that keeps Yaver's MCP surface pointed at
// what Yaver is for: a remote runtime for AI software development, mobile-app
// and UI first.
//
// ── Why an allowlist, when mcp_core_profile.go already has a denylist ───────
//
// peripheralToolFamilies is a DENYLIST, and a denylist admits every family
// nobody has classified yet. That is why the surface grew to 1135 tools: each
// new cell (robotics, circuits, ERP, betting, smart-home) arrived enabled, and
// the list of things to exclude could never keep up. An allowlist inverts the
// default — a new family is out until someone decides it belongs to the wedge —
// which is the only shape that stays lean without maintenance.
//
// It also had a hard cost. Measured 2026-07-26 on the owner's own Hetzner box:
// Yaver advertised 1135 tools, z.ai/GLM rejects any request over 1000, so
// opencode failed with
//
//	Error: Parameter The number of tools cannot exceed 1000. is invalid.
//
// exited 0, and the web Chat sat on "running" with an empty transcript. The
// product's own breadth made the product unusable, and then said nothing.
//
// ── What is IN ─────────────────────────────────────────────────────────────
//
// The families that serve the loop a user actually runs: connect to a remote
// machine → sign a runner in → open a project → vibe → render it (Hermes,
// browser lane, WebRTC, simulator/emulator) → build → ship to a store. Plus the
// generic file/exec/git/task primitives every coding agent needs to do any of
// that at all.
//
// Everything else stays reachable two ways, so nothing is lost:
//   - YAVER_MCP_PROFILE=full — the whole surface, unchanged.
//   - the `ops` grand-tool — one tool, many verbs, always advertised.

import (
	"os"
	"strings"
)

// wedgeToolFamilies is the allowlist, keyed by tool-name family (the segment
// before the first underscore, or the whole name when there is none) — the same
// key mcpToolFamily produces for the denylist.
//
// Grouped by the step of the loop each family serves, so a reviewer can ask
// "which part of vibing on a remote box does this enable?" and get an answer.
// A family that cannot answer that question does not belong here.
var wedgeToolFamilies = map[string]bool{
	// ── The grand-tool. One tool, ~20 verbs. Never droppable.
	"ops": true,

	// ── Yaver itself: auth, identity, devices, health, onboarding.
	"yaver": true, "get": true, "config": true, "diagnose": true,
	"account": true, "totp": true, "change": true, "forgot": true,
	"oauth": true, "sdk": true, "session": true, "support": true,

	// ── Connect a remote machine + keep it reachable. The "remote runtime" half.
	"device": true, "primary": true, "console": true, "machine": true, "agent": true,
	"relay": true, "add": true, "remove": true, "tunnel": true, "expose": true,
	"acl": true, "ping": true, "port": true, "net": true, "dns": true,
	"public": true, "wifi": true, "network": true, "listen": true, "http": true,
	"ssl": true, "domain": true, "proxy": true, "companion": true,
	"microservice": true,

	// ── Native runner inventory + install-only bootstrap. Authentication is
	// performed by the runner itself inside an encrypted PTY.
	"runner": true, "list": true, "switch": true, "opencode": true, "copilot": true,
	"models": true, "auth": true,

	// ── Tasks: dispatch, follow, steer.
	"create": true, "stop": true, "continue": true, "fork": true, "schedule": true,
	"cancel": true, "chat": true, "standup": true, "commit": true, "tmux": true,

	// ── Projects + workspaces + files. Needed to do any work at all.
	"project": true, "phone": true, "workspace": true, "set": true,
	"read": true, "write": true, "search": true, "tree": true, "diff": true,
	"archive": true, "find": true, "du": true, "df": true, "disk": true,
	"exec": true, "process": true, "ps": true, "tail": true, "log": true,

	// ── Git + forge. Vibing means committing and pushing.
	"git": true, "github": true, "gitlab": true, "gh": true, "glab": true,
	"create_gist": true, "gitignore": true,

	// ── RENDERING: dev servers, previews, Hermes, WebRTC, browser lane.
	// This is the wedge's centre of gravity.
	"dev": true, "preview": true, "web": true, "vibe": true, "expo": true,
	"dogfood": true,
	"native":  true, "mobile": true, "hotreload": true, "push": true,
	"browser": true, "selenium": true, "remote": true, "runtime": true, "cast": true,
	"record": true, "screenshot": true, "screenlog": true, "clip": true,
	"studio": true, "droid": true, "robot": true, "desktop": true, "artifact": true,
	"real_device_probe": true,

	// ── Mobile build + device toolchains.
	"build": true, "xcode": true, "gradle": true, "pod": true, "flutter": true,
	"eas": true, "adb": true, "android": true, "simulator": true, "simulators": true,
	"emulators": true, "wire": true, "wireless": true, "install": true, "pkg": true,
	"compiler": true, "make": true, "cmake": true,

	// ── Ship it: stores + release.
	"publish": true, "playstore": true, "testflight": true, "appstore": true,
	"app": true, "release": true, "deploy": true, "sourcemaps": true, "changelog": true,
	"version": true, "license": true,

	// ── App backends a mobile app actually uses (kept narrow on purpose).
	"convex": true, "db": true, "data": true, "backend": true, "supabase": true,
	"cf": true, "storage": true, "env": true, "vault": true, "op": true,
	"migrate": true, "drizzle": true, "prisma": true,

	// ── Test + verify the thing you just rendered.
	"run": true, "test": true, "testkit": true, "type": true, "tsc": true,
	"lint": true, "eslint": true, "prettier": true, "format": true, "check": true,
	"go": true, "npm": true, "pytest": true, "loc": true, "lines": true,
	"benchmark": true, "feedback": true, "error": true, "sentry_issues": true,

	// ── Infra basics for a remote box (not the deep sysadmin family).
	"system": true, "cpu": true, "free": true, "load": true, "uptime": true,
	"uname": true, "hostname": true, "service": true, "services": true,
	"systemctl": true, "docker": true, "cloud": true, "infra": true, "sandbox": true,
	"jobs": true, "monitor": true, "notify": true, "open": true, "clipboard": true,

	// ── Small generic utilities an agent reaches for constantly.
	"hash": true, "base64": true, "jq": true, "uuid": true, "regex": true,
	"jwt": true, "qr": true, "calculate": true, "epoch": true, "password": true,
	"fake": true, "template": true, "init": true, "docs": true, "help": true,
	"ask": true, "web_search": true,
}

// mcpWedgeProfileEnabled reports whether the allowlist is active.
//
// Default ON — the lean surface IS the product now. Escape hatches:
//
//	YAVER_MCP_PROFILE=full   → everything (also what owners got implicitly before)
//	YAVER_MCP_PROFILE=legacy → the old denylist behaviour, allowlist off
func mcpWedgeProfileEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_MCP_PROFILE"))) {
	case "full", "legacy":
		return false
	}
	return true
}

// filterToWedgeProfile keeps only allowlisted families.
//
// Runs BEFORE the hard budget: shaping the surface by meaning is always better
// than trimming it by count, so the budget should rarely have anything left to
// do. Never returns empty — if an allowlist typo would drop everything, the
// unfiltered list is safer than a tool-less agent, and the caller logs it.
func filterToWedgeProfile(tools []map[string]interface{}) []map[string]interface{} {
	if !mcpWedgeProfileEnabled() {
		return tools
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name, _ := t["name"].(string)
		if name == "" {
			continue
		}
		if wedgeToolFamilies[name] || wedgeToolFamilies[mcpToolFamily(name)] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return tools
	}
	return out
}
