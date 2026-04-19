package main

// workspace_engine.go — actions against a parsed WorkspaceManifest.
//
// Separated from parsing (workspace.go) so tests can exercise the
// action engine with in-memory manifests. Every action returns a
// list of `WorkspaceAction` results the CLI + MCP layers render.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceAction is a single thing we did (or skipped) for one app
// while walking the manifest. Rendered by CLI + MCP as a terse
// status line.
type WorkspaceAction struct {
	App     string `json:"app"`
	Action  string `json:"action"`  // "init", "check", "seed-env", ...
	Status  string `json:"status"`  // "ok", "skip", "warn", "fail"
	Detail  string `json:"detail,omitempty"`
	Command string `json:"command,omitempty"`
}

// WorkspaceInitOptions tunes what `workspace init` does per app.
type WorkspaceInitOptions struct {
	// Force: re-initialise apps that already have a yaver state dir.
	Force bool
	// DryRun: report what would happen without touching the filesystem.
	DryRun bool
	// OnlyApp: restrict to a single app (useful for adding one app
	// to an already-initialised workspace). Empty = all apps.
	OnlyApp string
	// AutoinitPrompt: when true, include "yaver autoinit" suggestions
	// in the output so the agent can run them. We don't invoke
	// autoinit automatically because it can take minutes per app.
	AutoinitPrompt bool
}

// RunWorkspaceInit walks the manifest and wires every app. Actions:
//
//   1. check: app path exists on disk, stack matches expectations
//   2. seed-env: required env vars are present (warn if missing)
//   3. init-md: cached-context file init.md exists or is scaffolded
//   4. autoinit-hint: per-app `yaver autoinit` command so autodev /
//      autoideas / autotest get richer context on next run
func RunWorkspaceInit(m *WorkspaceManifest, repoRoot string, opts WorkspaceInitOptions) []WorkspaceAction {
	var out []WorkspaceAction
	order, err := TopoSortApps(m)
	if err != nil {
		out = append(out, WorkspaceAction{Action: "validate", Status: "fail", Detail: err.Error()})
		return out
	}

	// Precompute path => app for O(1) lookups while iterating in order.
	byName := map[string]*WorkspaceApp{}
	for i := range m.Apps {
		byName[m.Apps[i].Name] = &m.Apps[i]
	}

	for _, name := range order {
		app := byName[name]
		if app == nil {
			continue
		}
		if opts.OnlyApp != "" && opts.OnlyApp != name {
			continue
		}
		actions := initOneApp(app, m, repoRoot, opts)
		out = append(out, actions...)
	}

	// Shared-env check is workspace-global, not per-app. Run once.
	if len(m.Shared.Env) > 0 {
		missing := []string{}
		for _, v := range m.Shared.Env {
			if os.Getenv(v) == "" {
				missing = append(missing, v)
			}
		}
		if len(missing) > 0 {
			out = append(out, WorkspaceAction{
				App:    "<shared>",
				Action: "shared-env",
				Status: "warn",
				Detail: fmt.Sprintf("missing shared env vars: %s", strings.Join(missing, ", ")),
			})
		} else {
			out = append(out, WorkspaceAction{
				App:    "<shared>",
				Action: "shared-env",
				Status: "ok",
				Detail: fmt.Sprintf("all %d shared env vars present", len(m.Shared.Env)),
			})
		}
	}
	return out
}

func initOneApp(app *WorkspaceApp, m *WorkspaceManifest, repoRoot string, opts WorkspaceInitOptions) []WorkspaceAction {
	var out []WorkspaceAction
	absPath := workspaceAppAbs(repoRoot, m, app.Path)

	// 1. Path check.
	if _, err := os.Stat(absPath); err != nil {
		out = append(out, WorkspaceAction{
			App:    app.Name,
			Action: "check",
			Status: "warn",
			Detail: fmt.Sprintf("path %s not found on disk (manifest declares it) — skipping further init", app.Path),
		})
		return out
	}
	out = append(out, WorkspaceAction{
		App:    app.Name,
		Action: "check",
		Status: "ok",
		Detail: fmt.Sprintf("%s (%s)", app.Path, app.Stack),
	})

	// 2. Per-app env check. Report missing; don't fail the whole init.
	if len(app.Env) > 0 {
		missing := []string{}
		for _, v := range app.Env {
			if os.Getenv(v) == "" {
				missing = append(missing, v)
			}
		}
		status := "ok"
		detail := fmt.Sprintf("all %d env vars present", len(app.Env))
		if len(missing) > 0 {
			status = "warn"
			detail = fmt.Sprintf("missing: %s", strings.Join(missing, ", "))
		}
		out = append(out, WorkspaceAction{
			App:    app.Name,
			Action: "seed-env",
			Status: status,
			Detail: detail,
		})
	}

	// 3. init.md scaffold. Autodev / autoideas / autotest read this
	// file as cached project context on every kick, so seeding it
	// saves minutes of re-grepping on the first run.
	initMdPath := filepath.Join(absPath, "init.md")
	_, err := os.Stat(initMdPath)
	switch {
	case err == nil && !opts.Force:
		out = append(out, WorkspaceAction{
			App:    app.Name,
			Action: "init-md",
			Status: "skip",
			Detail: "init.md already exists (use --force to overwrite)",
		})
	default:
		if opts.DryRun {
			out = append(out, WorkspaceAction{
				App:    app.Name,
				Action: "init-md",
				Status: "ok",
				Detail: "would scaffold init.md (dry-run)",
			})
		} else {
			content := scaffoldInitMd(app, m)
			if err := os.WriteFile(initMdPath, []byte(content), 0o644); err != nil {
				out = append(out, WorkspaceAction{
					App:    app.Name,
					Action: "init-md",
					Status: "fail",
					Detail: err.Error(),
				})
			} else {
				out = append(out, WorkspaceAction{
					App:    app.Name,
					Action: "init-md",
					Status: "ok",
					Detail: fmt.Sprintf("scaffolded %d bytes at %s", len(content), initMdPath),
				})
			}
		}
	}

	// 4. Autoinit hint — we don't run autoinit automatically because
	// it's minutes per app. The caller can opt-in by setting
	// AutoinitPrompt, in which case we emit the exact command so an
	// LLM agent (or shell loop) can execute it.
	if opts.AutoinitPrompt {
		out = append(out, WorkspaceAction{
			App:     app.Name,
			Action:  "autoinit-hint",
			Status:  "ok",
			Command: fmt.Sprintf("yaver autoinit %s", shellEscapePath(absPath)),
			Detail:  "richer cached context for autodev/autoideas/autotest",
		})
	}
	return out
}

// workspaceAppAbs resolves an app's path to an absolute filesystem
// path, honouring the manifest's Workspace.Root override.
func workspaceAppAbs(repoRoot string, m *WorkspaceManifest, appPath string) string {
	if filepath.IsAbs(appPath) {
		return appPath
	}
	root := repoRoot
	if root == "" {
		root = "."
	}
	if m != nil && strings.TrimSpace(m.Workspace.Root) != "" && m.Workspace.Root != "." {
		root = filepath.Join(root, m.Workspace.Root)
	}
	return filepath.Clean(filepath.Join(root, appPath))
}

// scaffoldInitMd produces the initial init.md for an app. Minimal on
// purpose — `yaver autoinit <path>` is the fuller version; this
// seed gives autodev etc. enough context to not re-grep.
func scaffoldInitMd(app *WorkspaceApp, m *WorkspaceManifest) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s (workspace app)\n\n", app.Name)
	if m != nil && m.Name != "" {
		fmt.Fprintf(&sb, "Part of the `%s` monorepo.\n\n", m.Name)
	}
	if app.Stack != "" {
		fmt.Fprintf(&sb, "**Stack:** %s\n\n", app.Stack)
	}
	if len(app.Depends) > 0 {
		fmt.Fprintf(&sb, "**Depends on:** %s\n\n", strings.Join(app.Depends, ", "))
	}
	if len(app.Provider) > 0 {
		sb.WriteString("**Providers:**\n")
		for k, v := range app.Provider {
			fmt.Fprintf(&sb, "- %s → %s\n", k, v)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("> This file is a scaffold. Run `yaver autoinit .` in this directory\n")
	sb.WriteString("> to replace it with a rich stack-aware description (layout,\n")
	sb.WriteString("> conventions, build/test/deploy commands, recent direction).\n")
	return sb.String()
}

// shellEscapePath wraps a filesystem path in single-quotes only when
// it contains spaces or shell-metacharacters. Used for user-facing
// command hints in WorkspaceAction.Command so copy/paste Just Works.
func shellEscapePath(p string) string {
	if !strings.ContainsAny(p, " \t$`\\\"'&;|<>()") {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// WorkspaceAppStatus snapshots one app's runtime status — stack
// detected, on-disk presence, init.md freshness.
type WorkspaceAppStatus struct {
	App            WorkspaceApp `json:"app"`
	OnDisk         bool         `json:"onDisk"`
	HasInitMd      bool         `json:"hasInitMd"`
	AbsolutePath   string       `json:"absolutePath"`
	MissingEnvVars []string     `json:"missingEnvVars,omitempty"`
}

// CollectWorkspaceStatus returns a WorkspaceAppStatus per declared app.
// Read-only — never touches the filesystem beyond stat calls.
func CollectWorkspaceStatus(m *WorkspaceManifest, repoRoot string) []WorkspaceAppStatus {
	out := make([]WorkspaceAppStatus, 0, len(m.Apps))
	for _, app := range m.Apps {
		abs := workspaceAppAbs(repoRoot, m, app.Path)
		st := WorkspaceAppStatus{App: app, AbsolutePath: abs}
		if _, err := os.Stat(abs); err == nil {
			st.OnDisk = true
		}
		if _, err := os.Stat(filepath.Join(abs, "init.md")); err == nil {
			st.HasInitMd = true
		}
		for _, v := range app.Env {
			if os.Getenv(v) == "" {
				st.MissingEnvVars = append(st.MissingEnvVars, v)
			}
		}
		out = append(out, st)
	}
	return out
}
