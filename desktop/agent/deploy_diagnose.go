package main

// deploy_diagnose.go — `yaver deploy diagnose --app X --target Y`.
// One-shot "tell me what's wrong before I run this" check.
// Composes the existing doctor + vault presence + workspace lookup
// into a single human-friendly report (and a matching JSON payload
// for programmatic callers / MCP).
//
// Why this exists: when a deploy fails, the user's first question
// is "is my machine set up right?" `yaver doctor build` answers
// part of that; `yaver vault list --project X` answers another;
// the workspace manifest answers another. Stitching them together
// into a single command turns "three things to check" into "one".

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiagnoseReport is the machine-readable output. Text rendering is
// done by printDeployDiagnose.
type DiagnoseReport struct {
	App        string            `json:"app"`
	Target     string            `json:"target"`
	Stack      string            `json:"stack,omitempty"`
	Path       string            `json:"path,omitempty"`
	PathExists bool              `json:"path_exists"`
	Workspace  string            `json:"workspace_root,omitempty"`
	Doctor     BuildDoctorReport `json:"doctor"`
	Secrets    []DiagnoseSecret  `json:"secrets"`
	Notes      []string          `json:"notes,omitempty"`
	OK         bool              `json:"ok"`
}

// DiagnoseSecret is a per-key presence record. Mirrors
// BuildSecretResult but makes the two layers (project vs. global)
// explicit so the user can see exactly which of the two supplied
// the value.
type DiagnoseSecret struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Source  string `json:"source,omitempty"`
	Project string `json:"project,omitempty"`
}

// RunDeployDiagnose performs the composite check. vs may be nil
// when the vault is unavailable; in that case secrets are probed
// against env vars only.
func RunDeployDiagnose(app, target string, vs *VaultStore) (DiagnoseReport, error) {
	if app == "" || target == "" {
		return DiagnoseReport{}, fmt.Errorf("app and target are required")
	}
	tgt, ok := buildTargets[target]
	if !ok {
		return DiagnoseReport{}, fmt.Errorf("unknown target %q — known: %v", target, BuildTargetNames())
	}

	report := DiagnoseReport{App: app, Target: target, OK: true}

	ref, rerr := resolveProjectRef(app, "")
	if rerr == nil {
		report.Stack = ref.Stack
		report.Path = ref.Path
		report.Workspace = ref.WorkspaceRoot
	} else {
		report.OK = false
		report.Notes = append(report.Notes,
			fmt.Sprintf("Could not resolve app %q from workspace/discovery data — declare it in yaver.workspace.yaml, ensure it is discoverable locally, or run `yaver deploy ship --app %s --target %s --stack ... --path ...` as owner.", app, app, target))
	}
	// Anchor path to an absolute location so existence checks work.
	absPath := report.Path
	if absPath != "" && !filepath.IsAbs(absPath) {
		base := report.Workspace
		if base == "" {
			cwd, _ := os.Getwd()
			base = cwd
		}
		absPath = filepath.Join(base, absPath)
	}
	if absPath != "" {
		if st, err := os.Stat(absPath); err == nil && st.IsDir() {
			report.PathExists = true
			report.Path = absPath
		} else {
			report.OK = false
			report.Notes = append(report.Notes,
				fmt.Sprintf("App path %q is not a directory (or is missing). Check yaver.workspace.yaml.", absPath))
		}
	}

	// Doctor — toolchain + secrets. Pass vs so secrets are probed
	// against the vault; absent vault entries fall through to env.
	doctor, derr := RunBuildDoctor(target, app, vs)
	if derr != nil {
		return report, derr
	}
	report.Doctor = doctor
	if !doctor.OK {
		report.OK = false
	}

	// Surface secrets in a compact side table so the user can see
	// project vs. global placement at a glance.
	for _, s := range doctor.Secrets {
		entry := DiagnoseSecret{Name: s.Name, Found: s.Found, Source: s.Source}
		if strings.Contains(s.Source, "project") {
			entry.Project = app
		}
		report.Secrets = append(report.Secrets, entry)
	}
	sort.Slice(report.Secrets, func(i, j int) bool {
		return report.Secrets[i].Name < report.Secrets[j].Name
	})
	_ = tgt // reserved: future rules like "warn if testflight on linux"
	return report, nil
}

// runDeployDiagnoseCmd wires the CLI:
//
//	yaver deploy diagnose --app X --target Y [--json]
func runDeployDiagnoseCmd(args []string) {
	fs := flag.NewFlagSet("deploy diagnose", flag.ExitOnError)
	app := fs.String("app", "", "App/project (required)")
	target := fs.String("target", "", "Target (required)")
	asJSON := fs.Bool("json", false, "Emit JSON")
	fs.Parse(args)
	if *app == "" || *target == "" {
		fmt.Fprintln(os.Stderr, "Usage: yaver deploy diagnose --app <name> --target <target>")
		os.Exit(1)
	}

	var vs *VaultStore
	if store, err := openVaultOptional(); err == nil {
		vs = store
	}
	report, err := RunDeployDiagnose(*app, *target, vs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
		if !report.OK {
			os.Exit(1)
		}
		return
	}
	printDeployDiagnose(report)
	if !report.OK {
		os.Exit(1)
	}
}

func printDeployDiagnose(r DiagnoseReport) {
	status := "OK"
	if !r.OK {
		status = "FAIL"
	}
	fmt.Printf("[%s] deploy diagnose  %s / %s\n", status, r.App, r.Target)
	fmt.Println(strings.Repeat("-", 44))
	if r.Stack != "" {
		fmt.Printf("  stack     : %s\n", r.Stack)
	}
	if r.Path != "" {
		mark := "OK"
		if !r.PathExists {
			mark = "MISS"
		}
		fmt.Printf("  path      : %s  [%s]\n", r.Path, mark)
	}
	if r.Workspace != "" {
		fmt.Printf("  workspace : %s\n", r.Workspace)
	}
	fmt.Println()
	fmt.Println("  Toolchain:")
	for _, tool := range r.Doctor.Tools {
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
		fmt.Printf("    [%s] %-14s %s\n", mark, tool.Name, label)
	}
	if len(r.Secrets) > 0 {
		fmt.Println()
		fmt.Println("  Secrets (project + globals merged):")
		for _, s := range r.Secrets {
			mark := "  OK"
			label := s.Source
			if s.Project != "" {
				label += " (" + s.Project + ")"
			}
			if !s.Found {
				mark = "MISS"
				label = "not set — yaver vault add " + s.Name + " --project " + r.App
			}
			fmt.Printf("    [%s] %-30s %s\n", mark, s.Name, label)
		}
	}
	if len(r.Notes) > 0 {
		fmt.Println()
		for _, n := range r.Notes {
			fmt.Println("  * " + n)
		}
	}
}

// handleDeployDiagnose exposes the same logic over HTTP. Owner and
// guests (subject to allowedProjects) can call it as a preflight.
//
//	GET /deploy/diagnose?app=X&target=Y  → DiagnoseReport JSON
func (s *HTTPServer) handleDeployDiagnose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app"))
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if app == "" || target == "" {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": "app and target are required"})
		return
	}
	if r.Header.Get("X-Yaver-Guest") == "true" {
		if s.guestConfigMgr != nil && !s.guestConfigMgr.GuestCanAccessProject(r.Header.Get("X-Yaver-GuestUserID"), app) {
			jsonReply(w, http.StatusForbidden, map[string]string{
				"error": "guest is not authorised for this project",
			})
			return
		}
	}
	report, err := RunDeployDiagnose(app, target, s.vaultStore)
	if err != nil {
		jsonReply(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonReply(w, http.StatusOK, report)
}
