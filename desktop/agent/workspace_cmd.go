package main

// workspace_cmd.go — `yaver workspace` subcommand.
//
// The monorepo entry point. Declarative — every command reads
// yaver.workspace.yaml and converges the machine toward it. Commands:
//
//   yaver workspace init                     # wire every declared app
//   yaver workspace init --scaffold          # write a starter yaver.workspace.yaml then wire
//   yaver workspace init --force             # overwrite existing init.md files
//   yaver workspace init --dry-run           # show what would happen
//   yaver workspace init --app=<name>        # just one app
//   yaver workspace list                     # apps declared in the manifest
//   yaver workspace status                   # per-app on-disk + env + init.md status

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runWorkspace(args []string) {
	if len(args) == 0 {
		workspaceUsage()
		return
	}
	switch args[0] {
	case "init":
		runWorkspaceInit(args[1:])
	case "list", "ls":
		runWorkspaceList(args[1:])
	case "status":
		runWorkspaceStatus(args[1:])
	case "help", "-h", "--help":
		workspaceUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand: yaver workspace %s\n\n", args[0])
		workspaceUsage()
		os.Exit(1)
	}
}

func workspaceUsage() {
	fmt.Print(`yaver workspace — declarative monorepo setup.

Reads yaver.workspace.yaml at the repo root. Each app declared there
gets wired with its own init.md (cached context for autodev/autoideas/
autotest), env-check, and per-app primary-device binding.

Usage:
  yaver workspace init [--scaffold] [--force] [--dry-run] [--app=<name>] [--autoinit]
                                             Wire every app (or one).
                                             --scaffold generates a starter manifest first.
                                             --force overwrites existing init.md files.
                                             --autoinit prints per-app 'yaver autoinit' commands.
  yaver workspace list                       List apps declared in the manifest.
  yaver workspace status                     Per-app on-disk + env + init.md status.

Example yaver.workspace.yaml:

  version: 1
  name: my-monorepo
  workspace:
    root: .
    primary_device: auto
    relay: managed
  apps:
    - name: web
      path: ./web
      stack: nextjs
      provider:
        deploy: cloudflare
    - name: mobile
      path: ./mobile
      stack: react-native-expo
      depends: [backend]
    - name: backend
      path: ./backend
      stack: convex
  shared:
    env: [APPLE_TEAM_ID, CONVEX_URL]
`)
}

func runWorkspaceInit(args []string) {
	opts := WorkspaceInitOptions{}
	scaffold := false
	manifestPath := ""
	root := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--scaffold":
			scaffold = true
		case a == "--force":
			opts.Force = true
		case a == "--dry-run":
			opts.DryRun = true
		case a == "--autoinit":
			opts.AutoinitPrompt = true
		case strings.HasPrefix(a, "--app="):
			opts.OnlyApp = strings.TrimPrefix(a, "--app=")
		case a == "--app" && i+1 < len(args):
			opts.OnlyApp = args[i+1]
			i++
		case strings.HasPrefix(a, "--file="):
			manifestPath = strings.TrimPrefix(a, "--file=")
		case a == "--file" && i+1 < len(args):
			manifestPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--root="):
			root = strings.TrimPrefix(a, "--root=")
		case a == "--root" && i+1 < len(args):
			root = args[i+1]
			i++
		}
	}
	if manifestPath != "" {
		WorkspaceManifestPathOverride = manifestPath
	}
	if root == "" {
		root, _ = os.Getwd()
	}

	// Scaffold path: write the starter manifest before the usual init.
	if scaffold {
		manifestFile := WorkspaceManifestPathOverride
		if manifestFile == "" {
			manifestFile = filepath.Join(root, WorkspaceManifestPath)
		}
		if _, err := os.Stat(manifestFile); err == nil && !opts.Force {
			fmt.Fprintf(os.Stderr, "manifest already exists at %s (use --force to overwrite, or drop --scaffold)\n", manifestFile)
			os.Exit(1)
		}
		data, m, err := ScaffoldWorkspaceManifest(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scaffold failed: %v\n", err)
			os.Exit(1)
		}
		if opts.DryRun {
			fmt.Println("// dry-run: would write", manifestFile)
			fmt.Println(string(data))
			// Fall through — the user asked for init + dry-run, so
			// surface what downstream would look like.
			fmt.Println("// downstream actions (dry-run):")
			for _, act := range RunWorkspaceInit(m, root, opts) {
				printWorkspaceAction(act)
			}
			return
		}
		if err := os.WriteFile(manifestFile, data, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write manifest: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Scaffolded %s with %d app(s):\n", manifestFile, len(m.Apps))
		for _, a := range m.Apps {
			fmt.Printf("  - %-18s %s\n", a.Name, a.Stack)
		}
		fmt.Println()
	}

	m, err := LoadWorkspaceManifest(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	actions := RunWorkspaceInit(m, root, opts)
	for _, act := range actions {
		printWorkspaceAction(act)
	}
}

func runWorkspaceList(args []string) {
	_ = args
	m, err := LoadWorkspaceManifest("")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	order, _ := TopoSortApps(m)
	// Map name → stack for quick render.
	byName := map[string]WorkspaceApp{}
	for _, a := range m.Apps {
		byName[a.Name] = a
	}
	fmt.Printf("%s — %d app(s):\n", workspaceFirstNonEmpty(m.Name, "workspace"), len(m.Apps))
	for _, n := range order {
		a := byName[n]
		dep := ""
		if len(a.Depends) > 0 {
			dep = "  ← " + strings.Join(a.Depends, ", ")
		}
		fmt.Printf("  - %-18s %-20s %s%s\n", a.Name, a.Stack, a.Path, dep)
	}
}

func runWorkspaceStatus(args []string) {
	_ = args
	root, _ := os.Getwd()
	m, err := LoadWorkspaceManifest(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
	stats := CollectWorkspaceStatus(m, root)
	fmt.Printf("%s — %d app(s):\n", workspaceFirstNonEmpty(m.Name, "workspace"), len(stats))
	for _, s := range stats {
		flags := []string{}
		if !s.OnDisk {
			flags = append(flags, "missing-on-disk")
		}
		if !s.HasInitMd {
			flags = append(flags, "no-init.md")
		}
		if len(s.MissingEnvVars) > 0 {
			flags = append(flags, "env: "+strings.Join(s.MissingEnvVars, ","))
		}
		tag := "ok"
		if len(flags) > 0 {
			tag = strings.Join(flags, "; ")
		}
		fmt.Printf("  - %-18s %-20s %s\n", s.App.Name, s.App.Stack, tag)
	}
}

func printWorkspaceAction(a WorkspaceAction) {
	prefix := " "
	switch a.Status {
	case "ok":
		prefix = "✓"
	case "skip":
		prefix = "·"
	case "warn":
		prefix = "!"
	case "fail":
		prefix = "✗"
	}
	who := a.App
	if who == "" {
		who = "-"
	}
	if a.Command != "" {
		fmt.Printf("%s %-18s %-14s %s\n    $ %s\n", prefix, who, a.Action, a.Detail, a.Command)
	} else if a.Detail != "" {
		fmt.Printf("%s %-18s %-14s %s\n", prefix, who, a.Action, a.Detail)
	} else {
		fmt.Printf("%s %-18s %s\n", prefix, who, a.Action)
	}
}

// workspaceFirstNonEmpty is the file-local helper equivalent of firstNonEmpty
// in httpserver.go — scoped here so the two definitions don't clash and
// readers can spot which one is for workspace-cmd output.
func workspaceFirstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if strings.TrimSpace(c) != "" {
			return c
		}
	}
	return ""
}
