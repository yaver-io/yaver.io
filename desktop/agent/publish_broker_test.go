package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublishPlanExposesMutationIdentityAndProof(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".yaver"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `version: 2
project: acme
defaultTarget: npm-sdk
targets:
  - id: npm-sdk
    kind: npm
    workDir: packages/sdk
    requiresConfirmation: true
    identity:
      packageName: "@acme/sdk"
    postconditions:
      - kind: npm-version-visible
`
	if err := os.MkdirAll(filepath.Join(dir, "packages", "sdk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publishConfigPath(dir), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildPublishPlan(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.MutatesExternalState || plan.Confirmation != "npm-sdk" {
		t.Fatalf("mutation/confirmation missing: %+v", plan)
	}
	if plan.Target.Identity.PackageName != "@acme/sdk" || len(plan.Postconditions) != 1 {
		t.Fatalf("identity/proof missing: %+v", plan)
	}
	for _, warning := range plan.Warnings {
		if strings.Contains(warning, "no store/registry postcondition") {
			t.Fatalf("declared postcondition was ignored: %v", plan.Warnings)
		}
	}
}

func TestPublishHTTPGateFailsClosedForNonOwner(t *testing.T) {
	ownerVerdictMu.Lock()
	previousKnown, previousValue, previousExpiry := ownerVerdictKnown, ownerVerdictValue, ownerVerdictExpires
	ownerVerdictKnown = true
	ownerVerdictValue = false
	ownerVerdictExpires = time.Now().Add(time.Minute)
	ownerVerdictMu.Unlock()
	t.Cleanup(func() {
		ownerVerdictMu.Lock()
		ownerVerdictKnown, ownerVerdictValue, ownerVerdictExpires = previousKnown, previousValue, previousExpiry
		ownerVerdictMu.Unlock()
	})
	recorder := httptest.NewRecorder()
	if requirePublishOwner(recorder) {
		t.Fatal("non-owner unexpectedly passed the publish HTTP gate")
	}
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("publish HTTP gate status=%d, want 403", recorder.Code)
	}
}

func TestPublishConfirmationFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".yaver"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &PublishConfig{Version: 2, Project: "acme", DefaultTarget: "ship", Targets: []PublishTarget{{ID: "ship", Kind: "custom", PublishCmd: "true", RequiresConfirmation: true, Identity: PublishIdentity{ProductID: "acme"}, Postconditions: []PublishPostcond{{Kind: "command-completed"}}}}}
	if err := savePublishConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	pm := NewPublishManager(nil, nil, dir)
	if _, err := pm.StartRun(dir, "ship", false); err == nil || !strings.Contains(err.Error(), `equal to "ship"`) {
		t.Fatalf("missing exact-confirmation failure: %v", err)
	}
}

func TestPublishManifestV2RequiresGuardsAndRejectsInlineSecrets(t *testing.T) {
	base := PublishConfig{Version: 2, Project: "acme", DefaultTarget: "ship", Targets: []PublishTarget{{
		ID: "ship", Kind: "custom", PublishCmd: "true", RequiresConfirmation: true,
		Identity:       PublishIdentity{ProductID: "acme"},
		Postconditions: []PublishPostcond{{Kind: "command-completed"}},
	}}}
	if err := validatePublishConfig(&base); err != nil {
		t.Fatalf("valid guarded config: %v", err)
	}
	unguarded := base
	unguarded.Targets = append([]PublishTarget(nil), base.Targets...)
	unguarded.Targets[0].RequiresConfirmation = false
	if err := validatePublishConfig(&unguarded); err == nil {
		t.Fatal("unguarded v2 mutation must be rejected")
	}
	secret := base
	secret.Targets = append([]PublishTarget(nil), base.Targets...)
	secret.Targets[0].Env = map[string]string{"API_SECRET": "plaintext"}
	if err := validatePublishConfig(&secret); err == nil {
		t.Fatal("inline secret-like env must be rejected")
	}
	unknownProof := base
	unknownProof.Targets = append([]PublishTarget(nil), base.Targets...)
	unknownProof.Targets[0].Postconditions = []PublishPostcond{{Kind: "wishful-thinking"}}
	if err := validatePublishConfig(&unknownProof); err == nil {
		t.Fatal("unknown postcondition must be rejected before mutation")
	}
}

func TestPublishEnvNamedProjectDoesNotInheritOperatorEnvironment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CUSTOMER_TOKEN", "operator-secret")
	t.Setenv("GH_CUSTOMER_TOKEN", "operator-github-secret")
	target := PublishTarget{
		EnvFromVault:  map[string]string{"CUSTOMER_TOKEN": "customer-token"},
		EnvFromGitHub: map[string]string{"SECOND_TOKEN": "GH_CUSTOMER_TOKEN"},
	}
	got := resolvePublishEnv(target, "third-party")
	if got["CUSTOMER_TOKEN"] != "" || got["SECOND_TOKEN"] != "" {
		t.Fatalf("named project inherited operator environment: %#v", got)
	}
}

func TestPublishStartFailsBeforeMutationWhenCredentialMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	cfg := &PublishConfig{Version: 2, Project: "acme", DefaultTarget: "ship", Targets: []PublishTarget{{
		ID: "ship", Kind: "custom", PublishCmd: "touch should-not-exist", RequiresConfirmation: true,
		Identity: PublishIdentity{ProductID: "acme"}, EnvFromVault: map[string]string{"ACME_TOKEN": "release-token"},
		Postconditions: []PublishPostcond{{Kind: "command-completed"}},
	}}}
	if err := savePublishConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	pm := NewPublishManager(nil, nil, dir)
	if _, err := pm.StartRunConfirmed(dir, "ship", false, "ship"); err == nil || !strings.Contains(err.Error(), "ACME_TOKEN") {
		t.Fatalf("missing credential must fail before dispatch, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatal("publish command ran despite failed credential preflight")
	}
}

func TestPublishScaffoldUsesV2SafetyContractAndDerivedIdentities(t *testing.T) {
	dir := t.TempDir()
	packageJSON := `{"name":"@acme/app","version":"1.0.0","dependencies":{"expo":"latest"}}`
	appJSON := `{"expo":{"ios":{"bundleIdentifier":"com.acme.app"},"android":{"package":"com.acme.app"}}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(packageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.json"), []byte(appJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := scaffoldPublishConfig(dir)
	if cfg.Version != 2 || cfg.Project == "" {
		t.Fatalf("unsafe scaffold header: %+v", cfg)
	}
	if err := validatePublishConfig(cfg); err != nil {
		t.Fatalf("generated v2 manifest must validate: %v", err)
	}
	kinds := map[string]PublishTarget{}
	for _, target := range cfg.Targets {
		kinds[target.Kind] = target
		if !target.RequiresConfirmation || len(target.Postconditions) == 0 {
			t.Fatalf("generated mutation lacks guard/proof: %+v", target)
		}
	}
	if kinds["npm"].Identity.PackageName != "@acme/app" || kinds["testflight"].Identity.BundleID != "com.acme.app" || kinds["playstore"].Identity.PackageName != "com.acme.app" {
		t.Fatalf("generated identities are wrong: %+v", kinds)
	}
}
