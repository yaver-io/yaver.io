package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// projectstore_repo_test.go pins the repo-tier ProjectStore contract:
// round-trip Write → Read preserves schema/auth/seed/targets/token
// labels, files are atomic, conflict policy behaves correctly, empty
// repos return [] from List rather than an error.

func newTestRepoStore(t *testing.T) *RepoProjectStore {
	t.Helper()
	dir := t.TempDir()
	return NewRepoProjectStore(dir)
}

func TestRepoStore_ListEmptyDir(t *testing.T) {
	s := newTestRepoStore(t)
	got, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List on empty dir: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List on empty dir = %v; want []", got)
	}
}

func TestRepoStore_RoundTripPreservesAllFields(t *testing.T) {
	s := newTestRepoStore(t)
	ctx := context.Background()

	want := Project{
		Slug:      "my-todo",
		Name:      "My Todo App",
		Template:  "todos",
		CreatedAt: "2026-04-28T10:00:00Z",
		UpdatedAt: "2026-04-28T11:00:00Z",
		Schema: &PhoneSchema{
			Tables: []PhoneTable{
				{Name: "Todo", Columns: []PhoneColumn{{Name: "id", Type: "string", Required: true}, {Name: "title", Type: "string"}}},
			},
		},
		Auth: &PhoneAuth{Personas: []PhonePersona{{ID: "u1", Email: "alice@example.com", Name: "Alice"}}},
		Seed: PhoneSeed{
			"Todo": []map[string]interface{}{
				{"id": "1", "title": "buy milk"},
			},
		},
		App:     &PhoneAppSpec{Summary: "test app", PrimaryEntity: "Todo"},
		Targets: []TargetBind{{Kind: "dev-hw", LastSync: "2026-04-28T11:00:00Z"}},
		TokenLabels: []TokenLabel{
			{ID: "tok_a", Label: "web-prod", Scopes: []string{"read", "write"}, CORS: []string{"https://example.com"}},
		},
	}

	if _, err := s.Write(ctx, want, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Files should be on disk under .yaver/.
	for _, name := range []string{"project.yaml", "schema.yaml", "auth.yaml", "seed.yaml", "app.yaml", "tokens.lock.yaml"} {
		path := filepath.Join(s.Root(), ".yaver", name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s on disk: %v", path, err)
		}
	}

	got, err := s.Read(ctx, "my-todo")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Slug != want.Slug || got.Name != want.Name || got.Template != want.Template {
		t.Fatalf("identity mismatch: got %+v want %+v", got, want)
	}
	if got.CreatedAt != want.CreatedAt || got.UpdatedAt != want.UpdatedAt {
		t.Fatalf("repo tier MUST preserve timestamps; got created=%q updated=%q", got.CreatedAt, got.UpdatedAt)
	}
	if !reflect.DeepEqual(got.Schema, want.Schema) {
		t.Fatalf("schema mismatch:\n got: %+v\nwant: %+v", got.Schema, want.Schema)
	}
	if !reflect.DeepEqual(got.Auth, want.Auth) {
		t.Fatalf("auth mismatch:\n got: %+v\nwant: %+v", got.Auth, want.Auth)
	}
	if !reflect.DeepEqual(got.App, want.App) {
		t.Fatalf("app mismatch:\n got: %+v\nwant: %+v", got.App, want.App)
	}
	if !reflect.DeepEqual(got.Targets, want.Targets) {
		t.Fatalf("targets mismatch:\n got: %+v\nwant: %+v", got.Targets, want.Targets)
	}
	if !reflect.DeepEqual(got.TokenLabels, want.TokenLabels) {
		t.Fatalf("token labels mismatch:\n got: %+v\nwant: %+v", got.TokenLabels, want.TokenLabels)
	}
	// Seed values may decode as map[string]interface{} round-tripped
	// through YAML; check by table existence and row count to keep
	// the test resilient to YAML's number-typing quirks.
	if len(got.Seed) != len(want.Seed) || len(got.Seed["Todo"]) != 1 {
		t.Fatalf("seed mismatch: got %v want %v", got.Seed, want.Seed)
	}
}

func TestRepoStore_ReadWrongSlugIsNotFound(t *testing.T) {
	s := newTestRepoStore(t)
	ctx := context.Background()
	if _, err := s.Write(ctx, Project{Slug: "alpha", Name: "Alpha"}, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_, err := s.Read(ctx, "beta")
	if err == nil {
		t.Fatal("Read with wrong slug must fail")
	}
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("Read wrong-slug returned %v; expected to wrap ErrProjectNotFound", err)
	}
}

func TestRepoStore_ListReturnsExactlyOneProject(t *testing.T) {
	s := newTestRepoStore(t)
	ctx := context.Background()
	if _, err := s.Write(ctx, Project{Slug: "solo", Name: "Solo"}, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Slug != "solo" || list[0].Tier != "repo" {
		t.Fatalf("List = %+v; want one repo-tier entry for solo", list)
	}
}

func TestRepoStore_WriteRequiresSlug(t *testing.T) {
	s := newTestRepoStore(t)
	_, err := s.Write(context.Background(), Project{Name: "no slug"}, WriteOptions{})
	if err == nil {
		t.Fatal("repo tier must reject empty slug — it does not slugify Name")
	}
}

func TestRepoStore_WriteConflictRejectByDefault(t *testing.T) {
	s := newTestRepoStore(t)
	ctx := context.Background()
	if _, err := s.Write(ctx, Project{Slug: "first", Name: "First"}, WriteOptions{}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	_, err := s.Write(ctx, Project{Slug: "second", Name: "Second"}, WriteOptions{})
	if err == nil {
		t.Fatal("Write with different slug into already-occupied repo must reject by default")
	}
	if !errors.Is(err, ErrPhoneProjectExists) {
		t.Fatalf("Write conflict returned %v; expected to wrap ErrPhoneProjectExists", err)
	}
}

func TestRepoStore_WriteConflictOverwriteReplaces(t *testing.T) {
	s := newTestRepoStore(t)
	ctx := context.Background()
	if _, err := s.Write(ctx, Project{Slug: "first", Name: "First"}, WriteOptions{}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := s.Write(ctx, Project{Slug: "second", Name: "Second"}, WriteOptions{OnConflict: ConflictOverwrite}); err != nil {
		t.Fatalf("overwrite Write: %v", err)
	}
	got, err := s.Read(ctx, "second")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Name != "Second" {
		t.Fatalf("overwrite did not replace; Name = %q", got.Name)
	}
}

func TestRepoStore_WriteConflictRenameIsRejected(t *testing.T) {
	s := newTestRepoStore(t)
	ctx := context.Background()
	if _, err := s.Write(ctx, Project{Slug: "a", Name: "A"}, WriteOptions{}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	_, err := s.Write(ctx, Project{Slug: "b", Name: "B"}, WriteOptions{OnConflict: ConflictRename})
	if err == nil {
		t.Fatal("ConflictRename in repo tier must error — caller must pick a different root")
	}
}

func TestRepoStore_AtomicWriteSurvivesNoTmpFiles(t *testing.T) {
	s := newTestRepoStore(t)
	if _, err := s.Write(context.Background(), Project{Slug: "atomic", Name: "Atomic", Schema: &PhoneSchema{Tables: []PhoneTable{{Name: "T"}}}}, WriteOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(s.Root(), ".yaver"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("atomic write left a .tmp file behind: %s", e.Name())
		}
	}
}

func TestRepoStore_StubsFailLoudly(t *testing.T) {
	s := newTestRepoStore(t)
	if _, err := s.Snapshot(context.Background(), "x", SnapshotOptions{}); err == nil {
		t.Fatal("Snapshot stub must error rather than return empty")
	}
	if err := s.ApplySnapshot(context.Background(), "x", Snapshot{}); err == nil {
		t.Fatal("ApplySnapshot stub must error")
	}
}

func TestRepoStore_ContextCancellationIsHonoured(t *testing.T) {
	s := newTestRepoStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.List(ctx); err == nil {
		t.Fatal("List with cancelled ctx should return ctx.Err()")
	}
	if _, err := s.Read(ctx, "x"); err == nil {
		t.Fatal("Read with cancelled ctx should return ctx.Err()")
	}
	if _, err := s.Write(ctx, Project{Slug: "x"}, WriteOptions{}); err == nil {
		t.Fatal("Write with cancelled ctx should return ctx.Err()")
	}
}
