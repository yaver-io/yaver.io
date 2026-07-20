package main

import (
	"os"
	"strings"
)

// MCP "core" profile — lean the tools/list surface for a fresh user so their
// coding agent sees the dev/hermes/runner/deploy wedge, not ~675 tools. The
// heavy hardware families (robot/arm/circuit/printer/appletv/capture) are
// already owner-gated (mcp_owner_gate.go); this trims the remaining peripheral
// families (smart-home, consumer-info, business/SaaS, extra cloud providers,
// language toolchains/linters/profilers, deep sysadmin/networking).
//
// Who sees the full surface:
//   - Owners (currentUserIsOwner) — so this account never loses tools.
//   - Anyone who sets YAVER_MCP_PROFILE=full (explicit opt-in).
//
// Everyone else (the HN normie) gets the lean core by default.
//
// Reverse the whole lean-down by setting mcpCoreProfileDefault below to
// "full", or per-machine with YAVER_MCP_PROFILE=full.
const mcpCoreProfileDefault = "core"

// Sandbox app-development tools are intentionally hidden while the product
// requires a real remote box (self-hosted Yaver mesh or Yaver Managed Cloud).
// Keep the implementation on disk for a future phone-local LLM path, but do
// not advertise it to MCP clients or let models choose it as the happy path.
var hiddenSandboxMCPTools = map[string]bool{
	"sandbox_run":        true,
	"sandbox_status":     true,
	"sandbox_config":     true,
	"sandbox_quickstart": true,
}

// peripheralToolFamilies are hidden from the lean "core" profile. Key = the
// tool-name family (the segment before the first underscore, or the whole name
// if it has none). Grouped by category for easy auditing/tuning. Deliberately
// conservative: genuinely dev-adjacent families (docker, go, npm, git, github,
// eslint, prettier, tsc, pytest, convex, cf, supabase, drizzle, prisma, db,
// flutter, gradle, xcode, expo, eas, adb, models, runner, code, …) stay in core.
var peripheralToolFamilies = map[string]bool{
	// Smart-home / IoT / desktop-control
	"hue": true, "govee": true, "sonos": true, "shelly": true, "tasmota": true,
	"nanoleaf": true, "elgato": true, "ha": true, "mqtt": true, "wake": true,
	"cast": true, "clip": true, "volume": true, "brightness": true,

	// Consumer info / novelty
	"weather": true, "news": true, "eczane": true, "nobetci": true, "crypto": true,
	"stock": true, "hotels": true, "restaurants": true, "directions": true, "ev": true,
	"translate": true, "currency": true, "geocode": true, "places": true, "world": true,
	"music": true, "say": true, "figlet": true, "lorem": true, "countdown": true,
	"timer": true, "color": true, "raycast": true, "convert": true, "tldr": true,

	// Business / SaaS / marketing
	"lemonsqueezy": true, "invoice": true, "newsletter": true, "waitlist": true,
	"affiliate": true, "ab": true, "seo": true, "form": true, "cms": true,
	"meeting": true, "stripe": true, "customer": true, "linear": true, "notion": true,
	"sentry": true, "standup": true, "short": true,

	// Extra cloud providers (core keeps convex + cf + supabase)
	"fly": true, "railway": true, "netlify": true, "firebase": true, "lambda": true,
	"pscale": true, "k8s": true, "helm": true, "tf": true,

	// Language toolchains / linters / profilers / debuggers (beyond core JS/TS/Go)
	"cargo": true, "clang": true, "cmake": true, "gcc": true, "gdb": true, "lldb": true,
	"valgrind": true, "ruff": true, "mypy": true, "bandit": true, "semgrep": true,
	"black": true, "biome": true, "brakeman": true, "gosec": true, "hadolint": true,
	"shellcheck": true, "sonarscanner": true, "lizard": true, "cppcheck": true,
	"cyclomatic": true, "heaptrack": true, "ltrace": true, "strace": true,
	"objdump": true, "coredump": true, "trivy": true, "safety": true, "gem": true,
	"crates": true, "maven": true, "nuget": true, "pubdev": true, "perf": true,

	// Deep sysadmin / networking diagnostics
	"iptables": true, "ufw": true, "insmod": true, "rmmod": true, "modinfo": true,
	"lsmod": true, "lspci": true, "lsusb": true, "lsblk": true, "fdisk": true,
	"dmesg": true, "sysctl": true, "syslog": true, "journalctl": true, "vmstat": true,
	"iostat": true, "mounts": true, "swap": true, "sensors": true, "battery": true,
	"tcpdump": true, "tshark": true, "pcap": true, "nmap": true, "arp": true,
	"mtr": true, "traceroute": true, "netcat": true, "bandwidth": true, "subnet": true,
	"whois": true,

	// Enterprise monitoring / misc peripheral
	"screenlog": true, "ghost": true, "uptime": true, "analytics": true, "mail": true,
	"mock": true,
}

// mcpToolFamily returns the family segment of a tool name (before the first
// underscore, or the whole name).
func mcpToolFamily(name string) string {
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i]
	}
	return name
}

// mcpProfileIsFull reports whether the caller should see the full tool surface:
// owners always do, plus anyone with YAVER_MCP_PROFILE=full.
func mcpProfileIsFull() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("YAVER_MCP_PROFILE")), "full") {
		return true
	}
	if strings.EqualFold(mcpCoreProfileDefault, "full") {
		return true
	}
	return currentUserIsOwner()
}

// filterToCoreProfile drops peripheral-family tools from the list unless the
// caller sees the full surface. Mirrors filterOwnerOnlyTools.
func filterToCoreProfile(tools []map[string]interface{}) []map[string]interface{} {
	if mcpProfileIsFull() {
		return tools
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name, _ := t["name"].(string)
		if peripheralToolFamilies[mcpToolFamily(name)] {
			continue
		}
		out = append(out, t)
	}
	return out
}

func filterHiddenSandboxTools(tools []map[string]interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name, _ := t["name"].(string)
		if hiddenSandboxMCPTools[name] {
			continue
		}
		out = append(out, t)
	}
	return out
}
