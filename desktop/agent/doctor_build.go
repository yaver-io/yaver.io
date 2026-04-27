package main

// doctor_build.go — toolchain preflight for deploy targets. Answers:
// "does this machine have everything it needs to ship app X to target Y,
// including the secrets?" Output is both a JSON blob (machine-readable,
// surfaced by the MCP tool + HTTP endpoint) and a human-readable text
// report (CLI).
//
// The catalogue is intentionally small and data-driven so adding a new
// target is one line in buildTargets, not a new file. Tools are probed
// via PATH + <bin> <versionFlag> with a 2s timeout. Secrets are looked
// up in the vault (project-scoped first, then global), not in env vars —
// the whole point is to make the machine able to deploy without the
// user having to set env vars manually.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// buildTool is one binary requirement in a deploy target's toolchain.
type buildTool struct {
	Name        string   `json:"name"`
	VersionFlag string   `json:"-"` // "--version", "-version", etc.
	Required    bool     `json:"required"`
	Platforms   []string `json:"platforms,omitempty"` // empty = all
	InstallHint string   `json:"install_hint,omitempty"`
}

// buildTarget declares what a specific (stack, target) pair needs.
type buildTarget struct {
	Name        string      `json:"name"`
	Stack       string      `json:"stack,omitempty"`
	Description string      `json:"description,omitempty"`
	Tools       []buildTool `json:"tools"`
	// Secrets is the set of vault keys the generated deploy script will
	// read. Missing secrets are a warning, not a hard fail (the user
	// may have them in env vars or CI).
	Secrets []string `json:"secrets,omitempty"`
}

// buildTargets is the master catalogue. Keep entries alphabetised.
var buildTargets = map[string]buildTarget{
	"cloudflare": {
		Name:        "cloudflare",
		Stack:       "nextjs",
		Description: "Cloudflare Workers deploy via @opennextjs/cloudflare + wrangler.",
		Tools: []buildTool{
			{Name: "node", VersionFlag: "--version", Required: true, InstallHint: "brew install node"},
			{Name: "npm", VersionFlag: "--version", Required: true, InstallHint: "bundled with node"},
			{Name: "wrangler", VersionFlag: "--version", Required: true, InstallHint: "npm i -g wrangler"},
		},
		Secrets: []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ACCOUNT_ID"},
	},
	"convex": {
		Name:        "convex",
		Stack:       "convex",
		Description: "Convex backend deploy via `npx convex deploy`.",
		Tools: []buildTool{
			{Name: "node", VersionFlag: "--version", Required: true},
			{Name: "npm", VersionFlag: "--version", Required: true},
		},
		Secrets: []string{"CONVEX_DEPLOY_KEY", "CONVEX_URL"},
	},
	"playstore": {
		Name:        "playstore",
		Stack:       "react-native-expo",
		Description: "Build release AAB with Gradle and upload to Google Play internal testing.",
		Tools: []buildTool{
			{Name: "java", VersionFlag: "-version", Required: true, InstallHint: "brew install openjdk@17 (Java 17 required)"},
			{Name: "python3", VersionFlag: "--version", Required: true, InstallHint: "brew install python3 (for upload-playstore.py)"},
		},
		Secrets: []string{
			"ANDROID_KEYSTORE_PASSWORD",
			"ANDROID_KEY_ALIAS",
			"ANDROID_KEY_PASSWORD",
			"PLAY_STORE_KEY_FILE",
		},
	},
	"testflight": {
		Name:        "testflight",
		Stack:       "react-native-expo",
		Description: "Archive + export IPA with xcodebuild and upload to TestFlight via App Store Connect API.",
		Tools: []buildTool{
			{Name: "xcodebuild", VersionFlag: "-version", Required: true, Platforms: []string{"darwin"}, InstallHint: "install Xcode from the Mac App Store"},
			{Name: "pod", VersionFlag: "--version", Required: true, Platforms: []string{"darwin"}, InstallHint: "sudo gem install cocoapods"},
			{Name: "node", VersionFlag: "--version", Required: true},
			{Name: "npm", VersionFlag: "--version", Required: true},
		},
		Secrets: []string{
			"APP_STORE_KEY_PATH",
			"APP_STORE_KEY_ID",
			"APP_STORE_KEY_ISSUER",
			"APPLE_TEAM_ID",
		},
	},
}

// BuildTargetNames returns the sorted catalogue keys — handy for UIs.
func BuildTargetNames() []string {
	out := make([]string, 0, len(buildTargets))
	for k := range buildTargets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BuildDoctorReport is the machine-readable outcome of a preflight run.
type BuildDoctorReport struct {
	Target  string                `json:"target"`
	Stack   string                `json:"stack,omitempty"`
	Project string                `json:"project,omitempty"`
	OK      bool                  `json:"ok"`
	Tools   []BuildToolResult     `json:"tools"`
	Secrets []BuildSecretResult   `json:"secrets,omitempty"`
	Notes   []string              `json:"notes,omitempty"`
}

type BuildToolResult struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Found       bool   `json:"found"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	InstallHint string `json:"install_hint,omitempty"`
	Skipped     bool   `json:"skipped,omitempty"` // e.g. platform mismatch
	SkipReason  string `json:"skip_reason,omitempty"`
}

type BuildSecretResult struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Source  string `json:"source,omitempty"` // "vault:project", "vault:global", "env"
	Project string `json:"project,omitempty"`
}

// RunBuildDoctor probes the local machine for the given target (e.g.
// "testflight") and returns a BuildDoctorReport. If vs is nil, secret
// checks are skipped (only toolchain is probed).
func RunBuildDoctor(target, project string, vs *VaultStore) (BuildDoctorReport, error) {
	t, ok := buildTargets[target]
	if !ok {
		return BuildDoctorReport{}, fmt.Errorf("unknown target %q — known: %v", target, BuildTargetNames())
	}

	report := BuildDoctorReport{
		Target:  target,
		Stack:   t.Stack,
		Project: project,
		OK:      true,
	}

	for _, tool := range t.Tools {
		res := probeTool(tool)
		report.Tools = append(report.Tools, res)
		if tool.Required && !res.Found && !res.Skipped {
			report.OK = false
		}
	}

	for _, name := range t.Secrets {
		res := BuildSecretResult{Name: name}
		if vs != nil {
			if project != "" {
				if e, err := vs.Get(project, name); err == nil && e.Value != "" {
					res.Found = true
					res.Source = "vault:project"
					res.Project = project
				}
			}
			if !res.Found {
				if e, err := vs.Get("", name); err == nil && e.Value != "" {
					res.Found = true
					res.Source = "vault:global"
				}
			}
		}
		if !res.Found {
			if v := strings.TrimSpace(os.Getenv(name)); v != "" {
				res.Found = true
				res.Source = "env"
			}
		}
		report.Secrets = append(report.Secrets, res)
		if !res.Found {
			report.Notes = append(report.Notes,
				fmt.Sprintf("%s not found in vault or env — add with: yaver vault add %s%s",
					name, name, projectFlag(project)))
		}
	}

	if !report.OK && len(report.Notes) == 0 {
		report.Notes = append(report.Notes, "Install the missing required tools, then re-run `yaver doctor build`.")
	}

	return report, nil
}

func projectFlag(p string) string {
	if p == "" {
		return ""
	}
	return " --project " + p
}

// probeTool runs exec.LookPath + <bin> <versionFlag> with a 2s timeout.
// Platform mismatch (e.g. xcodebuild on Linux) is reported as "skipped"
// so the report stays informative without failing.
func probeTool(t buildTool) BuildToolResult {
	res := BuildToolResult{
		Name:        t.Name,
		Required:    t.Required,
		InstallHint: t.InstallHint,
	}
	if len(t.Platforms) > 0 {
		ok := false
		for _, p := range t.Platforms {
			if p == runtime.GOOS {
				ok = true
				break
			}
		}
		if !ok {
			res.Skipped = true
			res.SkipReason = fmt.Sprintf("only on %s (this host: %s)", strings.Join(t.Platforms, "/"), runtime.GOOS)
			return res
		}
	}
	path, err := exec.LookPath(t.Name)
	if err != nil {
		return res
	}
	res.Found = true
	res.Path = path

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, t.VersionFlag)
	out, err := cmd.CombinedOutput()
	if err == nil || len(out) > 0 {
		// Many tools (java) print version to stderr with non-zero exit;
		// CombinedOutput captures it either way.
		res.Version = firstLine(strings.TrimSpace(string(out)))
	}
	return res
}

// --- CLI ---

func runDoctorBuild(args []string) {
	fs := flag.NewFlagSet("doctor build", flag.ExitOnError)
	target := fs.String("target", "", "Target to probe (testflight, playstore, cloudflare, convex). Empty = all.")
	project := fs.String("project", "", "Project scope for vault secret lookup (empty = global)")
	asJSON := fs.Bool("json", false, "Emit JSON")
	fs.Parse(args)

	var vs *VaultStore
	// openVaultOptional swallows "not authenticated" — preserves doctor
	// usefulness before first auth.
	if store, err := openVaultOptional(); err == nil {
		vs = store
	}

	targets := []string{}
	if strings.TrimSpace(*target) != "" {
		if _, ok := buildTargets[*target]; !ok {
			fmt.Fprintf(os.Stderr, "Unknown target %q. Known: %v\n", *target, BuildTargetNames())
			os.Exit(1)
		}
		targets = []string{*target}
	} else {
		targets = BuildTargetNames()
	}

	var reports []BuildDoctorReport
	for _, t := range targets {
		r, err := RunBuildDoctor(t, *project, vs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error probing %s: %v\n", t, err)
			os.Exit(1)
		}
		reports = append(reports, r)
	}

	if *asJSON {
		b, _ := json.MarshalIndent(reports, "", "  ")
		fmt.Println(string(b))
		return
	}

	overallOK := true
	for _, r := range reports {
		printBuildDoctorReport(r)
		fmt.Println()
		if !r.OK {
			overallOK = false
		}
	}
	if !overallOK {
		os.Exit(1)
	}
}

func printBuildDoctorReport(r BuildDoctorReport) {
	status := "OK"
	if !r.OK {
		status = "FAIL"
	}
	header := fmt.Sprintf("[%s] %s", status, r.Target)
	if r.Stack != "" {
		header += "  (" + r.Stack + ")"
	}
	if r.Project != "" {
		header += "  project=" + r.Project
	}
	fmt.Println(header)
	fmt.Println(strings.Repeat("-", len(header)))
	for _, tool := range r.Tools {
		mark := "  OK"
		label := tool.Path + " " + tool.Version
		switch {
		case tool.Skipped:
			mark = "SKIP"
			label = tool.SkipReason
		case !tool.Found && tool.Required:
			mark = "MISS"
			label = "not on PATH — " + tool.InstallHint
		case !tool.Found:
			mark = "opt "
			label = "not installed (optional)"
		}
		fmt.Printf("  [%s] %-14s %s\n", mark, tool.Name, label)
	}
	for _, s := range r.Secrets {
		mark := "  OK"
		label := s.Source
		if s.Project != "" {
			label += " (" + s.Project + ")"
		}
		if !s.Found {
			mark = "MISS"
			label = "not set in vault or env"
		}
		fmt.Printf("  [%s] %-30s %s\n", mark, s.Name, label)
	}
	for _, n := range r.Notes {
		fmt.Println("  * " + n)
	}
}
