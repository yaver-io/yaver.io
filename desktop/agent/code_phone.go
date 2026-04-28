package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// code_phone.go is the `yaver code` slash-command surface for phone
// projects. Slice 0: just `/phone status`, which renders a one-screen
// summary so a developer in a phone-project workdir sees what the
// terminal already knows about the project before issuing any verb.
//
// Detection rule (kept deliberately simple for Slice 0):
//
//   1. If `<workdir>/.yaver/project.yaml` exists, treat the workdir
//      as a repo-tier phone project and read it through
//      RepoProjectStore. This is the dual-mode case from
//      docs/yaver-code-deploy-integration.md.
//
//   2. Else if `<workdir>` is itself ~/.yaver/phone-projects/<slug>/,
//      treat it as the agent's runtime tier and read it through
//      AgentProjectStore. This catches the developer who cd's
//      directly into the agent's storage to inspect a live project.
//
//   3. Else: no phone project here. Print a short hint instead of an
//      error — the user may have typed /phone status by mistake or
//      to discover the verb.

// renderPhoneStatus is the pure rendering function. Returns the
// human-facing text (no trailing newline) and an error only when
// the underlying store fails — "no project here" is not an error,
// it's a hint string.
//
// workDir defaults to os.Getwd() when empty. Tests pass an explicit
// path; the live terminal passes whatever attachInfo.WorkDir says.
func renderPhoneStatus(ctx context.Context, workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("phone status: cwd unavailable: %w", err)
		}
	}

	tier, slug, project, err := resolvePhoneProjectAt(ctx, workDir)
	if err != nil {
		return "", err
	}
	if tier == "" {
		return phoneStatusHintNoProject(workDir), nil
	}
	return formatPhoneStatus(tier, slug, project, workDir), nil
}

// resolvePhoneProjectAt checks the workdir against the two tiers
// described in the file header. Returns ("","",Project{},nil) when
// neither tier matches — that's the "no project here" case, not an
// error.
func resolvePhoneProjectAt(ctx context.Context, workDir string) (tier string, slug string, p Project, err error) {
	// Tier 1 — repo tier. Detect by `.yaver/project.yaml`.
	repoMarker := filepath.Join(workDir, ".yaver", "project.yaml")
	if _, statErr := os.Stat(repoMarker); statErr == nil {
		store := NewRepoProjectStore(workDir)
		// Pass empty slug so RepoProjectStore.Read returns whatever's
		// in project.yaml without filtering.
		got, readErr := store.Read(ctx, "")
		if readErr != nil && !errors.Is(readErr, ErrProjectNotFound) {
			return "", "", Project{}, fmt.Errorf("repo store read: %w", readErr)
		}
		if readErr == nil {
			return "repo", got.Slug, got, nil
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", Project{}, fmt.Errorf("stat .yaver/project.yaml: %w", statErr)
	}

	// Tier 2 — agent runtime. Detect by workdir matching
	// PhoneProjectsRoot()/<slug>/. We don't insist the directory
	// already exists in the agent — LoadPhoneProject will surface
	// "not found" via ErrProjectNotFound.
	root, rootErr := PhoneProjectsRoot()
	if rootErr == nil {
		abs, _ := filepath.Abs(workDir)
		absRoot, _ := filepath.Abs(root)
		if rel, relErr := filepath.Rel(absRoot, abs); relErr == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) >= 1 && parts[0] != "" && parts[0] != "." {
				agentSlug := parts[0]
				agent := AgentProjectStore{}
				got, readErr := agent.Read(ctx, agentSlug)
				if readErr == nil {
					return "agent", got.Slug, got, nil
				}
				if !errors.Is(readErr, ErrProjectNotFound) {
					return "", "", Project{}, fmt.Errorf("agent store read: %w", readErr)
				}
			}
		}
	}

	return "", "", Project{}, nil
}

func phoneStatusHintNoProject(workDir string) string {
	return strings.Join([]string{
		"phone-project: no project at this path",
		"  workdir: " + workDir,
		"",
		"  cd into a project that has a .yaver/project.yaml at its root,",
		"  or cd ~/.yaver/phone-projects/<slug>/ to inspect a live agent project.",
	}, "\n")
}

func formatPhoneStatus(tier, slug string, p Project, workDir string) string {
	var b strings.Builder
	header := fmt.Sprintf("%s · phone-project · %s", tierBadge(tier, slug), workDir)
	fmt.Fprintln(&b, header)
	if p.Name != "" && p.Name != slug {
		fmt.Fprintf(&b, "name:    %s\n", p.Name)
	}
	if p.Template != "" {
		fmt.Fprintf(&b, "template:%s\n", p.Template)
	}
	if p.UpdatedAt != "" {
		fmt.Fprintf(&b, "updated: %s\n", relativeAge(p.UpdatedAt))
	}

	// Schema summary — table count + per-table column count, sorted by
	// table name so the output is stable across repeated calls.
	if p.Schema != nil && len(p.Schema.Tables) > 0 {
		names := make([]string, 0, len(p.Schema.Tables))
		colCounts := map[string]int{}
		for _, t := range p.Schema.Tables {
			names = append(names, t.Name)
			colCounts[t.Name] = len(t.Columns)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, n := range names {
			parts = append(parts, fmt.Sprintf("%s(%d)", n, colCounts[n]))
		}
		fmt.Fprintf(&b, "schema:  %d tables — %s\n", len(names), strings.Join(parts, ", "))
	} else {
		fmt.Fprintln(&b, "schema:  (none yet)")
	}

	// Auth summary — persona count.
	if p.Auth != nil && len(p.Auth.Personas) > 0 {
		fmt.Fprintf(&b, "auth:    %d personas\n", len(p.Auth.Personas))
	} else {
		fmt.Fprintln(&b, "auth:    (none)")
	}

	// Seed summary — total rows across tables.
	if len(p.Seed) > 0 {
		var rows int
		tables := make([]string, 0, len(p.Seed))
		for k, v := range p.Seed {
			rows += len(v)
			tables = append(tables, k)
		}
		sort.Strings(tables)
		fmt.Fprintf(&b, "seed:    %d rows across %s\n", rows, strings.Join(tables, ", "))
	} else {
		fmt.Fprintln(&b, "seed:    (no seed)")
	}

	// Token labels — show count + first label as a sample. Never the
	// secret material; tokens.lock.yaml only carries labels.
	if len(p.TokenLabels) > 0 {
		labels := make([]string, 0, len(p.TokenLabels))
		for _, t := range p.TokenLabels {
			labels = append(labels, t.Label)
		}
		fmt.Fprintf(&b, "tokens:  %d active — %s\n", len(labels), strings.Join(labels, ", "))
	}

	// Target bindings — repo tier carries them, agent tier does not.
	if len(p.Targets) > 0 {
		bindings := make([]string, 0, len(p.Targets))
		for _, t := range p.Targets {
			bindings = append(bindings, t.Kind)
		}
		sort.Strings(bindings)
		fmt.Fprintf(&b, "targets: %s\n", strings.Join(bindings, ", "))
	} else if tier == "repo" {
		fmt.Fprintln(&b, "targets: (none bound — `code phone push --to dev-hw` to bind one)")
	}

	// Live stats — only the agent tier reports these.
	if p.Stats != nil && p.Stats.RowCount > 0 {
		fmt.Fprintf(&b, "live:    %d rows · %d KB SQLite\n", p.Stats.RowCount, p.Stats.DBBytes/1024)
	}

	return strings.TrimRight(b.String(), "\n")
}

func tierBadge(tier, slug string) string {
	switch tier {
	case "agent":
		return slug + " · agent"
	case "repo":
		return slug + " · repo"
	default:
		return slug
	}
}

// relativeAge renders an RFC3339 timestamp as "5m ago" / "2h ago".
// Returns the raw input on parse failure so the user still sees
// *something* useful instead of an empty field.
func relativeAge(rfc string) string {
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return rfc
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		// Go's reference time is 2006-01-02 — anything else is a no-op format.
		return t.Format("2006-01-02")
	}
}
