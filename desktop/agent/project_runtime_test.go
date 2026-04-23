package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectRuntimeRolePrefersPrimaryDevice(t *testing.T) {
	machines := []MachineInfo{
		{
			DeviceID: "other",
			Name:     "Other Box",
			IsOnline: true,
			Provider: "hetzner",
			Capabilities: &MachineCapabilities{
				SupportsDocker: true,
				MaxTaskSlots:   2,
			},
		},
		{
			DeviceID: "primary",
			Name:     "Primary Box",
			IsOnline: true,
			Provider: "local-mac",
			Capabilities: &MachineCapabilities{
				SupportsDocker: true,
				MaxTaskSlots:   2,
			},
		},
	}

	got := resolveProjectRuntimeRole(machines, "primary", ManifestMachineRole{
		Mode:         "owned",
		Capabilities: []string{"docker"},
	}, "primary", false)

	if got.Machine == nil || got.Machine.DeviceID != "primary" {
		t.Fatalf("expected primary device to win, got %+v", got.Machine)
	}
}

func TestResolveProjectRuntimeRoleFallsBackToManaged(t *testing.T) {
	machines := []MachineInfo{
		{
			DeviceID: "offline-local",
			Name:     "Laptop",
			IsOnline: false,
			Provider: "local-mac",
		},
		{
			DeviceID: "cloud-1",
			Name:     "Cloud Box",
			IsOnline: true,
			Provider: "hetzner",
			Capabilities: &MachineCapabilities{
				SupportsLocalLLM: true,
				MaxTaskSlots:     3,
			},
		},
	}

	got := resolveProjectRuntimeRole(machines, "gpu", ManifestMachineRole{
		Mode:         "owned",
		Capabilities: []string{"local-llm"},
	}, "", true)

	if got.Machine == nil || got.Machine.DeviceID != "cloud-1" {
		t.Fatalf("expected managed fallback, got %+v", got.Machine)
	}
}

func TestBuildProjectRuntimeSummaryLoadsNearestWorkspace(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(filepath.Join(appDir, ".yaver"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := `version: 1
name: test-workspace
workspace:
  root: .
  primary_device: auto
  placement:
    default_execution_role: primary
    managed_cloud_fallback: true
apps:
  - name: mobile
    path: ./apps/mobile
    stack: react-native-expo
`
	project := `name: demo
stack: react-native-monorepo
backend: convex
runtime:
  mobile:
    app: mobile
placement:
  roles:
    primary:
      mode: owned
  assignments:
    mobile: primary
jobs:
  - id: sync
    kind: convex-action
    machine_role: primary
`
	if err := os.WriteFile(filepath.Join(root, "yaver.workspace.yaml"), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".yaver", "project.yaml"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := BuildProjectRuntimeSummary(context.Background(), nil, appDir)
	if err != nil {
		t.Fatalf("BuildProjectRuntimeSummary: %v", err)
	}
	if summary.Workspace == nil || summary.Workspace.Manifest == nil {
		t.Fatalf("expected workspace manifest to be discovered")
	}
	if summary.Workspace.Root != root {
		t.Fatalf("workspace root = %q, want %q", summary.Workspace.Root, root)
	}
	if len(summary.ResolvedAssignments) == 0 {
		t.Fatalf("expected at least one resolved assignment")
	}
}

func TestBuildProjectRuntimeSummaryIncludesExportPlansAndProviders(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "apps", "mobile")
	if err := os.MkdirAll(filepath.Join(appDir, ".yaver"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := `version: 1
name: monorepo
workspace:
  root: .
  placement:
    default_execution_role: primary
    managed_cloud_fallback: true
apps:
  - name: mobile
    path: ./apps/mobile
    stack: react-native-expo
`
	project := `name: demo
stack: react-native-monorepo
runtime:
  frontend:
    kind: cloudflare-pages
    app: web
    credential_ref: team.cloudflare
    exports:
      - kind: cloudflare-pages
        app: web
        target: self-hosted-machine
  backend:
    kind: convex
    app: backend
    exports:
      - kind: convex
        app: backend
        target: self-hosted-machine
      - kind: yaver-cloud
        app: backend
        target: managed-cloud
  mobile:
    app: mobile
    sandbox:
      project_slug: phone-demo
      exports:
        - kind: convex
          project_slug: phone-demo
        - kind: cloudflare-workers
          app: mobile-api
placement:
  roles:
    primary:
      mode: owned
  assignments:
    frontend: primary
    backend: primary
`
	if err := os.WriteFile(filepath.Join(root, "yaver.workspace.yaml"), []byte(workspace), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".yaver", "project.yaml"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := BuildProjectRuntimeSummary(context.Background(), nil, appDir)
	if err != nil {
		t.Fatalf("BuildProjectRuntimeSummary: %v", err)
	}
	if len(summary.ExportPlans) != 6 {
		t.Fatalf("expected 6 export plans, got %d", len(summary.ExportPlans))
	}
	if len(summary.ProviderRequirements) != 3 {
		t.Fatalf("expected 3 provider requirements, got %d", len(summary.ProviderRequirements))
	}
	providers := map[string]bool{}
	for _, req := range summary.ProviderRequirements {
		providers[req.Provider] = true
	}
	for _, provider := range []string{"cloudflare", "convex", "yaver"} {
		if !providers[provider] {
			t.Fatalf("expected provider requirement for %s", provider)
		}
	}
}

func TestApplyProjectRuntimeMutationDryRunDoesNotPersist(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resp, err := ApplyProjectRuntimeMutation(context.Background(), nil, projectDir, ProjectRuntimeApplyRequest{
		Name:   "demo",
		Stack:  "react-native-monorepo",
		DryRun: true,
		Runtime: &ManifestRuntimeConfig{
			Frontend: &ManifestRuntimeSurface{Kind: "cloudflare-pages", App: "web"},
		},
	})
	if err != nil {
		t.Fatalf("ApplyProjectRuntimeMutation dry run: %v", err)
	}
	if !resp.OK || resp.Summary == nil {
		t.Fatalf("expected dry-run response summary, got %+v", resp)
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".yaver", "project.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected no manifest written during dry run, got err=%v", err)
	}
}

func TestApplyProjectRuntimeMutationPersistsManifestAndProvider(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	prevAccounts := globalAccountsManager
	globalAccountsManager = &AccountsManager{baseDir: filepath.Join(root, "secrets")}
	t.Cleanup(func() { globalAccountsManager = prevAccounts })

	resp, err := ApplyProjectRuntimeMutation(context.Background(), nil, projectDir, ProjectRuntimeApplyRequest{
		Name:  "demo",
		Stack: "react-native-monorepo",
		Runtime: &ManifestRuntimeConfig{
			Backend: &ManifestRuntimeBackend{
				Kind: "convex",
				App:  "backend",
			},
		},
		Providers: []ProjectRuntimeProviderInput{
			{
				Provider: "cloudflare",
				Label:    "Team CF",
				Fields: map[string]string{
					"token":     "token-123",
					"accountId": "acc-1",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ApplyProjectRuntimeMutation: %v", err)
	}
	if !resp.OK || !resp.ManifestSaved {
		t.Fatalf("expected successful apply, got %+v", resp)
	}
	manifest, err := LoadManifest(projectDir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.Runtime == nil || manifest.Runtime.Backend == nil || manifest.Runtime.Backend.Kind != "convex" {
		t.Fatalf("expected backend runtime persisted, got %+v", manifest.Runtime)
	}
	rec, err := globalAccountsManager.Get(ProviderCloudflare)
	if err != nil {
		t.Fatalf("Get cloudflare account: %v", err)
	}
	if rec == nil || rec.Fields["token"] != "token-123" {
		t.Fatalf("expected cloudflare account stored, got %+v", rec)
	}
}

func TestApplyProjectRuntimeMutationResolvesPhoneSlug(t *testing.T) {
	root := t.TempDir()
	prevHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Setenv("HOME", prevHome) })

	project, err := CreatePhoneProject(PhoneCreateSpec{Name: "Runtime Phone", Template: "todos"})
	if err != nil {
		t.Fatalf("CreatePhoneProject: %v", err)
	}

	resp, err := ApplyProjectRuntimeMutation(context.Background(), nil, "", ProjectRuntimeApplyRequest{
		PhoneSlug: project.Slug,
		Runtime: &ManifestRuntimeConfig{
			Backend: &ManifestRuntimeBackend{Kind: "convex", App: "backend"},
		},
	})
	if err != nil {
		t.Fatalf("ApplyProjectRuntimeMutation phoneSlug: %v", err)
	}
	if !resp.OK || !resp.ManifestSaved {
		t.Fatalf("expected phone slug apply success, got %+v", resp)
	}
	manifest, err := LoadManifest(project.Dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest.Runtime == nil || manifest.Runtime.Backend == nil || manifest.Runtime.Backend.Kind != "convex" {
		t.Fatalf("expected phone manifest runtime to be updated, got %+v", manifest.Runtime)
	}
}
