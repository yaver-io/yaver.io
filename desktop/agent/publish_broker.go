package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func validatePublishConfig(cfg *PublishConfig) error {
	if cfg.Version < 1 || cfg.Version > 2 {
		return fmt.Errorf("unsupported publish manifest version %d", cfg.Version)
	}
	if cfg.Version >= 2 && strings.TrimSpace(cfg.Project) == "" {
		return fmt.Errorf("publish manifest version 2 requires project")
	}
	seen := map[string]bool{}
	for i, target := range cfg.Targets {
		if strings.TrimSpace(target.ID) == "" || strings.TrimSpace(target.Kind) == "" {
			return fmt.Errorf("publish target %d requires id and kind", i)
		}
		switch target.Kind {
		case "npm", "pypi", "pubdev", "custom", "validate", "build", "testflight", "playstore":
		default:
			return fmt.Errorf("publish target %q has unsupported kind %q", target.ID, target.Kind)
		}
		if seen[target.ID] {
			return fmt.Errorf("duplicate publish target id %q", target.ID)
		}
		seen[target.ID] = true
		clean := filepath.Clean(target.WorkDir)
		if filepath.IsAbs(target.WorkDir) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("publish target %q workDir must stay inside the project", target.ID)
		}
		if cfg.Version >= 2 && target.Kind != "validate" && target.Kind != "build" {
			if !target.RequiresConfirmation {
				return fmt.Errorf("publish target %q mutates external state and requires requiresConfirmation: true", target.ID)
			}
			if target.Identity.PackageName == "" && target.Identity.BundleID == "" && target.Identity.ProductID == "" {
				return fmt.Errorf("publish target %q mutates external state and requires an explicit packageName, bundleId, or productId identity", target.ID)
			}
			if len(target.Postconditions) == 0 {
				return fmt.Errorf("publish target %q requires at least one postcondition", target.ID)
			}
			switch target.Kind {
			case "npm", "pypi", "pubdev", "playstore":
				if target.Identity.PackageName == "" {
					return fmt.Errorf("publish target %q kind %s requires identity.packageName", target.ID, target.Kind)
				}
			case "testflight":
				if target.Identity.BundleID == "" {
					return fmt.Errorf("publish target %q kind testflight requires identity.bundleId", target.ID)
				}
			}
		}
		for _, condition := range target.Postconditions {
			if !supportedPublishPostcondition(condition.Kind) {
				return fmt.Errorf("publish target %q declares unsupported postcondition %q", target.ID, condition.Kind)
			}
		}
		for key := range target.Env {
			upper := strings.ToUpper(key)
			if strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.HasSuffix(upper, "_TOKEN") || strings.HasSuffix(upper, "_PRIVATE_KEY") {
				return fmt.Errorf("publish target %q puts secret-like %s in env; use envFromVault", target.ID, key)
			}
		}
	}
	if cfg.DefaultTarget != "" && !seen[cfg.DefaultTarget] {
		return fmt.Errorf("defaultTarget %q does not exist", cfg.DefaultTarget)
	}
	return nil
}

// PublishPlan is the read-only release-broker view shared by CLI and MCP. It
// makes mutation, identity, credential references and proof obligations
// visible before any build or upload starts.
type PublishPlan struct {
	Project              string            `json:"project"`
	ProjectDir           string            `json:"projectDir"`
	Target               PublishTarget     `json:"target"`
	WorkDir              string            `json:"workDir"`
	Command              string            `json:"command"`
	MutatesExternalState bool              `json:"mutatesExternalState"`
	Confirmation         string            `json:"confirmation,omitempty"`
	CredentialRefs       map[string]string `json:"credentialRefs,omitempty"`
	ArtifactGlobs        []string          `json:"artifactGlobs,omitempty"`
	Postconditions       []PublishPostcond `json:"postconditions,omitempty"`
	MissingCredentials   []string          `json:"missingCredentials,omitempty"`
	Warnings             []string          `json:"warnings,omitempty"`
}

func buildPublishPlan(projectDir, targetID string) (*PublishPlan, error) {
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}
	cfg, err := loadPublishConfig(abs)
	if err != nil {
		return nil, err
	}
	target, err := findPublishTarget(cfg, targetID)
	if err != nil {
		return nil, err
	}
	workDir := abs
	if target.WorkDir != "" {
		workDir = filepath.Join(abs, filepath.FromSlash(target.WorkDir))
	}
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("publish target %q workDir is not a directory: %s", target.ID, workDir)
	}
	cmd := strings.TrimSpace(target.PublishCmd)
	if cmd == "" {
		switch target.Kind {
		case "npm":
			cmd = `npm pack && npm publish <packed-tarball> --access public`
		case "pypi":
			cmd = "python -m build && python -m twine upload dist/*"
		case "pubdev":
			cmd = "flutter pub publish --force"
		case "testflight", "playstore":
			cmd = "build artifact, archive receipt, then invoke native store uploader"
		}
	}
	project := strings.TrimSpace(cfg.Project)
	if project == "" {
		project = filepath.Base(abs)
	}
	mutates := target.Kind != "build" && target.Kind != "validate"
	plan := &PublishPlan{
		Project: project, ProjectDir: abs, Target: target, WorkDir: workDir,
		Command: cmd, MutatesExternalState: mutates, CredentialRefs: target.EnvFromVault,
		ArtifactGlobs: target.ArtifactGlobs, Postconditions: target.Postconditions,
	}
	if target.RequiresConfirmation {
		plan.Confirmation = target.ID
	}
	plan.MissingCredentials = missingPublishCredentials(target, project)
	if len(plan.MissingCredentials) > 0 {
		plan.Warnings = append(plan.Warnings, "declared project credentials are missing; the run will fail closed before execution")
	}
	if mutates && !target.RequiresConfirmation {
		plan.Warnings = append(plan.Warnings, "mutating target has no requiresConfirmation guard")
	}
	if mutates && len(target.Postconditions) == 0 {
		plan.Warnings = append(plan.Warnings, "no store/registry postcondition is declared; exit zero alone cannot prove publication")
	}
	if cfg.Version < 2 {
		plan.Warnings = append(plan.Warnings, "legacy publish manifest; upgrade to version 2 for the release-broker safety contract")
	}
	return plan, nil
}

func missingPublishCredentials(target PublishTarget, project string) []string {
	resolved := resolvePublishEnv(target, project)
	missing := make([]string, 0)
	seen := map[string]bool{}
	for envKey := range target.EnvFromVault {
		if resolved[envKey] == "" && !seen[envKey] {
			missing = append(missing, envKey)
			seen[envKey] = true
		}
	}
	for envKey := range target.EnvFromGitHub {
		if resolved[envKey] == "" && !seen[envKey] {
			missing = append(missing, envKey)
			seen[envKey] = true
		}
	}
	sort.Strings(missing)
	return missing
}

func runPublishPlan(args []string) {
	fs := flag.NewFlagSet("publish plan", flag.ExitOnError)
	dir := fs.String("dir", ".", "Project directory")
	target := fs.String("target", "", "Target ID from .yaver/publish.yaml")
	fs.Parse(args)
	plan, err := buildPublishPlan(*dir, *target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish plan: %v\n", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(plan, "", "  ")
	fmt.Println(string(out))
}
