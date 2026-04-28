package main

import (
	"context"
	"reflect"
	"testing"
)

// projectstore_crosstier_test.go pins the cross-tier contract: a
// project written in one tier and read in another must preserve the
// canonical fields. This is the regression test that protects the
// dual-mode workflow described in
// docs/yaver-code-deploy-integration.md — without it, a future
// change to either store could silently corrupt schema/auth/seed
// when a developer flips between editing in their repo and editing
// on the agent.
//
// Two round-trip directions matter:
//
//   1. agent → repo → agent: a developer pulls from the live agent,
//      edits .yaver/ files in their editor, pushes back. The schema
//      / auth / seed they wrote must round-trip exactly.
//
//   2. repo → agent → repo: a developer hand-writes .yaver/ files
//      from scratch (typical for the create-yaver-app starter
//      template), pushes to an agent, and the agent's exported
//      view of that project still matches the repo's view.
//
// The agent tier mints fresh CreatedAt/UpdatedAt on every Write
// (per the Slice 0 known limitation in projectstore_agent.go), so
// these tests deliberately pin schema/auth/seed/app/targets equality
// rather than metadata equality. A follow-up "preserve metadata"
// path will let us extend these tests to cover timestamps too.

func TestCrossTier_AgentToRepoToAgent(t *testing.T) {
	setupPhoneTestHome(t)
	agent := AgentProjectStore{}
	repo := NewRepoProjectStore(t.TempDir())
	ctx := context.Background()

	// Seed the agent with a non-trivial project (schema + auth + seed).
	original := Project{
		Slug:     "round",
		Name:     "Round Trip",
		Template: "blank",
		Schema: &PhoneSchema{
			Tables: []PhoneTable{
				{Name: "Item", Columns: []PhoneColumn{{Name: "id", Type: "string", Required: true}, {Name: "label", Type: "string"}}},
			},
		},
		Auth: &PhoneAuth{Personas: []PhonePersona{{ID: "u1", Email: "alice@example.com", Name: "Alice"}}},
		Seed: PhoneSeed{
			"Item": []map[string]interface{}{{"id": "1", "label": "first"}},
		},
		App: &PhoneAppSpec{Summary: "round-trip fixture", PrimaryEntity: "Item"},
	}
	if _, err := agent.Write(ctx, original, WriteOptions{}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	// agent → repo
	fromAgent, err := agent.Read(ctx, "round")
	if err != nil {
		t.Fatalf("agent.Read: %v", err)
	}
	if _, err := repo.Write(ctx, fromAgent, WriteOptions{}); err != nil {
		t.Fatalf("repo.Write: %v", err)
	}

	// repo → agent (overwrite, because the agent already holds the
	// original copy — this is the realistic edit-in-editor → push-back
	// flow)
	fromRepo, err := repo.Read(ctx, "round")
	if err != nil {
		t.Fatalf("repo.Read: %v", err)
	}
	if _, err := agent.Write(ctx, fromRepo, WriteOptions{OnConflict: ConflictOverwrite}); err != nil {
		t.Fatalf("agent.Write back: %v", err)
	}

	// The agent must now show the same schema/auth/seed/app as the
	// original. Anything missing means a tier dropped data on the way
	// through.
	final, err := agent.Read(ctx, "round")
	if err != nil {
		t.Fatalf("agent.Read final: %v", err)
	}
	if !reflect.DeepEqual(final.Schema, original.Schema) {
		t.Fatalf("schema drift across agent → repo → agent:\n got: %+v\nwant: %+v", final.Schema, original.Schema)
	}
	if !reflect.DeepEqual(final.Auth, original.Auth) {
		t.Fatalf("auth drift:\n got: %+v\nwant: %+v", final.Auth, original.Auth)
	}
	if !reflect.DeepEqual(final.App, original.App) {
		t.Fatalf("app drift:\n got: %+v\nwant: %+v", final.App, original.App)
	}
	if len(final.Seed) != len(original.Seed) || len(final.Seed["Item"]) != 1 {
		t.Fatalf("seed drift: final=%v original=%v", final.Seed, original.Seed)
	}
}

func TestCrossTier_RepoToAgentToRepo(t *testing.T) {
	setupPhoneTestHome(t)
	agent := AgentProjectStore{}
	repoA := NewRepoProjectStore(t.TempDir())
	repoB := NewRepoProjectStore(t.TempDir())
	ctx := context.Background()

	// Hand-write a project in repoA — this is the "create-yaver-app
	// starter template" path, so timestamps + targets + token labels
	// matter and must survive the round trip.
	original := Project{
		Slug:      "hand",
		Name:      "Hand Written",
		Template:  "blank",
		CreatedAt: "2026-04-28T12:00:00Z",
		UpdatedAt: "2026-04-28T13:00:00Z",
		Schema: &PhoneSchema{
			Tables: []PhoneTable{{Name: "Note", Columns: []PhoneColumn{{Name: "id", Type: "string"}}}},
		},
		Auth:    &PhoneAuth{Personas: []PhonePersona{{ID: "u1", Email: "x@x.com"}}},
		App:     &PhoneAppSpec{Summary: "hand"},
		Targets: []TargetBind{{Kind: "dev-hw", LastSync: "2026-04-28T13:00:00Z"}},
		TokenLabels: []TokenLabel{
			{ID: "tok_1", Label: "web-prod", Scopes: []string{"read"}},
		},
	}

	if _, err := repoA.Write(ctx, original, WriteOptions{}); err != nil {
		t.Fatalf("repoA.Write: %v", err)
	}

	// repoA → agent
	fromRepoA, err := repoA.Read(ctx, "hand")
	if err != nil {
		t.Fatalf("repoA.Read: %v", err)
	}
	if _, err := agent.Write(ctx, fromRepoA, WriteOptions{}); err != nil {
		t.Fatalf("agent.Write: %v", err)
	}

	// agent → repoB. The agent loses targets + tokenLabels (they
	// don't have a phone_backend storage path yet), but the repo
	// preserved them when repoA was written. To make the round-trip
	// work end-to-end through the agent, the caller will need to
	// re-merge the repo-only fields. This test pins that current
	// reality so a future enhancement makes the test stricter.
	fromAgent, err := agent.Read(ctx, "hand")
	if err != nil {
		t.Fatalf("agent.Read: %v", err)
	}

	// Re-merge the repo-only fields explicitly (mirrors what the
	// `code phone pull` command will do when bringing a project from
	// the agent into a fresh repo).
	fromAgent.Targets = original.Targets
	fromAgent.TokenLabels = original.TokenLabels
	fromAgent.CreatedAt = original.CreatedAt
	fromAgent.UpdatedAt = original.UpdatedAt

	if _, err := repoB.Write(ctx, fromAgent, WriteOptions{}); err != nil {
		t.Fatalf("repoB.Write: %v", err)
	}

	final, err := repoB.Read(ctx, "hand")
	if err != nil {
		t.Fatalf("repoB.Read: %v", err)
	}
	if !reflect.DeepEqual(final.Schema, original.Schema) {
		t.Fatalf("schema drift:\n got: %+v\nwant: %+v", final.Schema, original.Schema)
	}
	if !reflect.DeepEqual(final.Auth, original.Auth) {
		t.Fatalf("auth drift")
	}
	if !reflect.DeepEqual(final.App, original.App) {
		t.Fatalf("app drift")
	}
	if !reflect.DeepEqual(final.Targets, original.Targets) {
		t.Fatalf("targets drift across agent — caller MUST re-merge repo-only fields:\n got: %+v\nwant: %+v", final.Targets, original.Targets)
	}
	if !reflect.DeepEqual(final.TokenLabels, original.TokenLabels) {
		t.Fatalf("token labels drift across agent — caller MUST re-merge:\n got: %+v\nwant: %+v", final.TokenLabels, original.TokenLabels)
	}
	if final.CreatedAt != original.CreatedAt || final.UpdatedAt != original.UpdatedAt {
		t.Fatalf("timestamps drift:\n got: created=%q updated=%q\nwant: created=%q updated=%q",
			final.CreatedAt, final.UpdatedAt, original.CreatedAt, original.UpdatedAt)
	}
}

func TestCrossTier_AgentDropsRepoOnlyFields(t *testing.T) {
	// This test exists to make the asymmetry in the previous test
	// explicit and intentional: the agent tier today has no place to
	// persist Targets / TokenLabels / CreatedAt / UpdatedAt the way
	// the repo tier does. If a future change adds a backing store
	// for those fields on the agent side (e.g. project.yaml in
	// ~/.yaver/phone-projects/<slug>/.yaver/) this test will start
	// failing — which is good, because that's the cue to drop the
	// re-merge step from `code phone pull`.
	setupPhoneTestHome(t)
	agent := AgentProjectStore{}
	ctx := context.Background()

	if _, err := agent.Write(ctx, Project{
		Slug:        "drop",
		Name:        "Drop Test",
		Targets:     []TargetBind{{Kind: "dev-hw"}},
		TokenLabels: []TokenLabel{{ID: "tok", Label: "x"}},
	}, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := agent.Read(ctx, "drop")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Targets != nil && len(got.Targets) != 0 {
		t.Fatalf("agent tier unexpectedly persisted Targets — update TestCrossTier_RepoToAgentToRepo to drop the re-merge step. got=%+v", got.Targets)
	}
	if got.TokenLabels != nil && len(got.TokenLabels) != 0 {
		t.Fatalf("agent tier unexpectedly persisted TokenLabels — update TestCrossTier_RepoToAgentToRepo to drop the re-merge step. got=%+v", got.TokenLabels)
	}
}
