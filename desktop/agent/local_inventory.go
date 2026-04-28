package main

// local_inventory.go — capability probe for the `yaver code` welcome
// banner. Runs probes in parallel so a slow one doesn't push the
// banner past ~250 ms.
//
// Reframes the cold-start of `yaver code` from "what coding agent is
// installed" to "what can this machine actually do" — coding agents
// are one row of many: builds, deploys, vault, workspace apps, etc.
// Reinforces yaver's positioning as an orchestration layer with
// agent-wrapping as one capability among many.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// InventoryReport is the structured output of ProbeLocalInventory. It
// holds enough state for the TUI welcome banner and any future
// programmatic surfaces (HTTP / MCP / mobile) that want to render the
// same capability snapshot.
type InventoryReport struct {
	// CodingAgents is the supported runner set (claude/codex/opencode)
	// with whether each is installed. Order matches supportedRunnerIDs.
	CodingAgents []InventoryItem `json:"coding_agents"`

	// Builds lists local toolchain binaries that gate native build
	// flows (xcodebuild, gradlew, expo, wrangler, ...).
	Builds []InventoryItem `json:"builds"`

	// Deploys lists CLIs that gate `yaver deploy ship` and friends.
	Deploys []InventoryItem `json:"deploys"`

	// VaultPath is the resolved vault file path (empty if HOME isn't
	// resolvable). VaultPresent is true when the file exists; we
	// deliberately do *not* decrypt it for the probe (passphrase
	// would have to be prompted).
	VaultPath    string `json:"vault_path,omitempty"`
	VaultPresent bool   `json:"vault_present"`

	// Workspace metadata for the nearest yaver.workspace.yaml above
	// cwd. WorkspacePath is empty when no manifest is found.
	WorkspacePath string `json:"workspace_path,omitempty"`
	WorkspaceApps int    `json:"workspace_apps,omitempty"`

	// PreferredRunner is the runner ID picked for offline mode —
	// last-used (from code-config) when its binary still resolves,
	// otherwise the first installed entry from supportedRunnerIDs.
	// Empty when none of the supported runners are installed.
	PreferredRunner string `json:"preferred_runner,omitempty"`

	// PreferredRunnerReason is "last-used" / "first-installed" /
	// "" — surfaced in the banner so the user understands why the
	// fallback picked what it did.
	PreferredRunnerReason string `json:"preferred_runner_reason,omitempty"`
}

// InventoryItem is a single row in the inventory — a tool or capability
// with a presence flag and (when found) a path or version hint.
type InventoryItem struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	// Notes is a short single-line annotation shown in parens after
	// the label (e.g. "wraps BYOK Ollama / OpenRouter").
	Notes string `json:"notes,omitempty"`
}

// ProbeLocalInventory runs every probe in parallel and returns once
// they all settle (or 250 ms — whichever is shorter). Probes that
// time out are reported as not installed; this is fine because the
// inventory is best-effort UX, not a security boundary.
func ProbeLocalInventory(ctx ProbeContext) InventoryReport {
	var (
		report InventoryReport
		wg     sync.WaitGroup
		mu     sync.Mutex
	)

	addAgents := func(items []InventoryItem) {
		mu.Lock()
		report.CodingAgents = items
		mu.Unlock()
	}
	addBuilds := func(items []InventoryItem) {
		mu.Lock()
		report.Builds = items
		mu.Unlock()
	}
	addDeploys := func(items []InventoryItem) {
		mu.Lock()
		report.Deploys = items
		mu.Unlock()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		items := make([]InventoryItem, 0, len(supportedRunnerIDs))
		for _, id := range supportedRunnerIDs {
			cfg := builtinRunners[id]
			path, _ := exec.LookPath(cfg.Command)
			notes := ""
			if id == "opencode" {
				notes = "wraps BYOK Anthropic / OpenRouter / Ollama / etc."
			}
			items = append(items, InventoryItem{
				ID:        id,
				Label:     cfg.Name,
				Installed: path != "",
				Path:      path,
				Notes:     notes,
			})
		}
		addAgents(items)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		probes := []struct {
			id, label, bin, fallback string
		}{
			{"xcodebuild", "Xcode toolchain", "xcodebuild", ""},
			{"gradle", "Gradle", "gradle", "gradlew"},
			{"expo", "Expo CLI", "expo", "npx"},
			{"npm", "npm", "npm", ""},
			{"go", "Go", "go", ""},
		}
		items := make([]InventoryItem, 0, len(probes))
		for _, p := range probes {
			path, _ := exec.LookPath(p.bin)
			if path == "" && p.fallback != "" {
				path, _ = exec.LookPath(p.fallback)
			}
			items = append(items, InventoryItem{
				ID:        p.id,
				Label:     p.label,
				Installed: path != "",
				Path:      path,
			})
		}
		addBuilds(items)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		probes := []struct {
			id, label, bin string
		}{
			{"wrangler", "Cloudflare (wrangler)", "wrangler"},
			{"convex", "Convex", "convex"},
			{"xcrun", "TestFlight (xcrun altool)", "xcrun"},
			{"fastlane", "fastlane", "fastlane"},
		}
		items := make([]InventoryItem, 0, len(probes))
		for _, p := range probes {
			path, _ := exec.LookPath(p.bin)
			items = append(items, InventoryItem{
				ID:        p.id,
				Label:     p.label,
				Installed: path != "",
				Path:      path,
			})
		}
		addDeploys(items)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		path, err := VaultPath()
		if err != nil {
			return
		}
		mu.Lock()
		report.VaultPath = path
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			report.VaultPresent = true
		}
		mu.Unlock()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		manifestPath, count := nearestWorkspaceApps(ctx.WorkDir)
		if manifestPath == "" {
			return
		}
		mu.Lock()
		report.WorkspacePath = manifestPath
		report.WorkspaceApps = count
		mu.Unlock()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		// Probes that haven't reported yet just stay zero-valued.
		// Inventory is best-effort UX, not a correctness boundary.
	}

	pickPreferred(&report, ctx.LastUsedRunner)
	return report
}

// ProbeContext is the inputs the inventory probe needs from its
// caller — kept narrow so the probe can be unit-tested without the
// full TUI session shape.
type ProbeContext struct {
	WorkDir        string
	LastUsedRunner string
}

// nearestWorkspaceApps walks up from start looking for a
// yaver.workspace.yaml. Returns its path + app count, or ("", 0) when
// nothing is found within ~6 levels (deep enough to escape a
// repo-in-repo monorepo without scanning the whole filesystem).
func nearestWorkspaceApps(start string) (string, int) {
	if strings.TrimSpace(start) == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", 0
		}
	}
	dir := start
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "yaver.workspace.yaml")
		if _, err := os.Stat(candidate); err == nil {
			m, err := LoadWorkspaceManifest(dir)
			if err == nil {
				return candidate, len(m.Apps)
			}
			return candidate, 0
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", 0
		}
		dir = parent
	}
	return "", 0
}

func pickPreferred(report *InventoryReport, lastUsed string) {
	lastUsed = strings.TrimSpace(strings.ToLower(lastUsed))
	if lastUsed != "" {
		// Normalize legacy aliases to canonical IDs.
		switch lastUsed {
		case "claude-code":
			lastUsed = "claude"
		}
		for _, agent := range report.CodingAgents {
			if agent.ID == lastUsed && agent.Installed {
				report.PreferredRunner = agent.ID
				report.PreferredRunnerReason = "last-used"
				return
			}
		}
	}
	for _, agent := range report.CodingAgents {
		if agent.Installed {
			report.PreferredRunner = agent.ID
			report.PreferredRunnerReason = "first-installed"
			return
		}
	}
}

// RenderInventoryBanner builds the multi-line capability table shown
// at the top of `yaver code`'s welcome screen. ANSI codes are
// returned inline; callers in raw mode should pass it through
// rawifyLines first if they need CRLF line endings.
func RenderInventoryBanner(report InventoryReport, hostname string) string {
	var b strings.Builder
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	b.WriteString("\033[2mLocal capabilities — ")
	b.WriteString(hostname)
	b.WriteString("\033[0m\n")

	b.WriteString("  Coding agents : ")
	b.WriteString(joinInventoryRow(report.CodingAgents))
	b.WriteString("\n")

	b.WriteString("  Builds        : ")
	b.WriteString(joinInventoryRow(report.Builds))
	b.WriteString("\n")

	b.WriteString("  Deploys       : ")
	b.WriteString(joinInventoryRow(report.Deploys))
	b.WriteString("\n")

	if report.VaultPresent {
		b.WriteString("  Vault         : configured · ")
		b.WriteString(report.VaultPath)
		b.WriteString("\n")
	} else {
		b.WriteString("  Vault         : \033[2mnot set up — `yaver vault add` to create\033[0m\n")
	}

	if report.WorkspacePath != "" {
		b.WriteString("  Workspace     : ")
		if report.WorkspaceApps > 0 {
			b.WriteString(formatApps(report.WorkspaceApps))
			b.WriteString(" from ")
		}
		b.WriteString(report.WorkspacePath)
		b.WriteString("\n")
	}

	if report.PreferredRunner != "" {
		b.WriteString("\n\033[2m▸ Default runner: \033[0m")
		b.WriteString(report.PreferredRunner)
		switch report.PreferredRunnerReason {
		case "last-used":
			b.WriteString(" \033[2m(last used)\033[0m")
		case "first-installed":
			b.WriteString(" \033[2m(first installed of supported set)\033[0m")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func joinInventoryRow(items []InventoryItem) string {
	if len(items) == 0 {
		return "\033[2m(none probed)\033[0m"
	}
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(it.Label)
		b.WriteString(" ")
		if it.Installed {
			b.WriteString("\033[32m✓\033[0m")
		} else {
			b.WriteString("\033[2m—\033[0m")
		}
		if it.Notes != "" && it.Installed {
			b.WriteString(" \033[2m(")
			b.WriteString(it.Notes)
			b.WriteString(")\033[0m")
		}
	}
	return b.String()
}

func formatApps(n int) string {
	if n == 1 {
		return "1 app"
	}
	return joinIntDecimal(n) + " apps"
}

// joinIntDecimal prints small ints without bringing strconv into
// every call site. Limit is workspace-app counts which never reach
// thousands.
func joinIntDecimal(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	digits := make([]byte, 0, 6)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
