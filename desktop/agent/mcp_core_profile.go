package main

import (
	"fmt"
	"os"
	"strconv"
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
	"screenlog": true, "ghost": true, "desktop": true, "uptime": true, "analytics": true, "mail": true,
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

// mcpProfileIsFull reports whether the caller should see the full tool surface.
//
// YAVER_MCP_PROFILE is honoured in BOTH directions — "full" opts up, "core"
// opts down — and the explicit setting wins over the owner default.
//
// It used to only opt UP: owners returned true unconditionally, on the
// reasoning "so this account never loses tools". That made the owner the one
// account that could not shrink its surface, and on 2026-07-26 it is what broke
// the owner's own box. Yaver advertised 1135 tools; z.ai/GLM hard-caps a
// request at 1000; opencode got
//
//	Error: Parameter The number of tools cannot exceed 1000. is invalid.
//
// and STILL EXITED 0, so the web Chat showed "running" with an empty transcript
// forever. A privilege that cannot be declined is not a privilege — the owner
// needs the same lean surface a capped provider requires.
func mcpProfileIsFull() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("YAVER_MCP_PROFILE"))) {
	case "full":
		return true
	case "core", "lean":
		// Explicit opt-down, owner or not.
		return false
	}
	if strings.EqualFold(mcpCoreProfileDefault, "full") {
		return true
	}
	return currentUserIsOwner()
}

// mcpToolBudget returns the maximum number of tools to advertise, or 0 for no
// limit. Set with YAVER_MCP_MAX_TOOLS.
//
// Exists because "how many tools may I send" is a property of the PROVIDER, not
// of the user or the profile: z.ai caps at 1000, and a future provider will pick
// another number. Trimming by family (the core profile) cannot guarantee a
// count, so a hard budget is the only thing that can honour a hard cap.
func mcpToolBudget() int {
	raw := strings.TrimSpace(os.Getenv("YAVER_MCP_MAX_TOOLS"))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// applyMCPToolBudget trims tools to the configured budget, keeping the most
// useful ones, and reports what it dropped.
//
// CLAUDE.md, "No silent caps": if a limit bounds coverage, SAY what was dropped
// — a silently truncated list reads as "this is everything Yaver can do", which
// is how an agent concludes a capability does not exist. The notice goes to
// stderr so it lands in the agent log without corrupting the JSON-RPC stream on
// stdout.
//
// Priority order is deliberate: `ops` is the grand-tool that reaches 20 verbs by
// itself, so it must never be the thing that falls off the end.
func applyMCPToolBudget(tools []map[string]interface{}, budget int) []map[string]interface{} {
	if budget <= 0 || len(tools) <= budget {
		return tools
	}
	priority := func(name string) int {
		switch {
		case name == "ops" || name == "ops_plan" || name == "ops_verbs":
			return 0 // one tool, twenty verbs — always keep
		case peripheralToolFamilies[mcpToolFamily(name)]:
			return 2
		default:
			return 1
		}
	}
	kept := make([]map[string]interface{}, 0, budget)
	// Stable pass per priority tier so the surface does not reshuffle between
	// calls — an agent that re-lists tools must not see them move.
	for tier := 0; tier <= 2 && len(kept) < budget; tier++ {
		for _, t := range tools {
			if len(kept) >= budget {
				break
			}
			name, _ := t["name"].(string)
			if priority(name) == tier {
				kept = append(kept, t)
			}
		}
	}
	fmt.Fprintf(os.Stderr,
		"yaver mcp: advertising %d of %d tools (YAVER_MCP_MAX_TOOLS=%d). %d dropped — "+
			"peripheral families first, `ops` always kept. Raise the budget or set "+
			"YAVER_MCP_PROFILE=full to see everything.\n",
		len(kept), len(tools), budget, len(tools)-len(kept))
	return kept
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
