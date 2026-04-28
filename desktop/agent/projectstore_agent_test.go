package main

import (
	"context"
	"errors"
	"testing"
)

// projectstore_agent_test.go pins the contract AgentProjectStore must
// satisfy: List/Read/Write happy-path + the three conflict policies.
// Snapshot/ApplySnapshot remain stubs in Slice 0; we only assert they
// fail loudly so callers don't silently get an empty body and assume
// success.

func TestAgentStore_RoundTripBlankProject(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	ctx := context.Background()

	// Write
	meta, err := store.Write(ctx, Project{Name: "Round Trip", Template: "blank"}, WriteOptions{})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if meta.Slug != "round-trip" {
		t.Fatalf("Write: slug = %q, want round-trip", meta.Slug)
	}
	if meta.Tier != "agent" {
		t.Fatalf("Write: Tier = %q, want agent", meta.Tier)
	}

	// Read it back
	got, err := store.Read(ctx, "round-trip")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Slug != "round-trip" || got.Name != "Round Trip" || got.Template != "blank" {
		t.Fatalf("Read returned wrong identity: slug=%q name=%q template=%q",
			got.Slug, got.Name, got.Template)
	}

	// List should now include it
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, m := range list {
		if m.Slug == "round-trip" {
			found = true
			if m.Tier != "agent" {
				t.Errorf("List entry Tier = %q, want agent", m.Tier)
			}
			break
		}
	}
	if !found {
		t.Fatalf("List did not include the project we just Wrote")
	}
}

func TestAgentStore_ReadUnknownSlugReturnsProjectNotFound(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	_, err := store.Read(context.Background(), "no-such-thing")
	if err == nil {
		t.Fatal("Read of unknown slug must fail")
	}
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("Read returned %v; expected to wrap ErrProjectNotFound so callers can map to 404", err)
	}
}

func TestAgentStore_WriteConflictReject(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	ctx := context.Background()

	if _, err := store.Write(ctx, Project{Name: "Dup"}, WriteOptions{}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	_, err := store.Write(ctx, Project{Name: "Dup"}, WriteOptions{})
	if err == nil {
		t.Fatal("second Write with default policy must reject collision")
	}
	if !errors.Is(err, ErrPhoneProjectExists) {
		t.Fatalf("Write conflict returned %v; expected to wrap ErrPhoneProjectExists", err)
	}
}

func TestAgentStore_WriteConflictRenameSuffixes(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	ctx := context.Background()

	if _, err := store.Write(ctx, Project{Name: "Same"}, WriteOptions{}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	meta, err := store.Write(ctx, Project{Name: "Same"}, WriteOptions{OnConflict: ConflictRename})
	if err != nil {
		t.Fatalf("rename Write: %v", err)
	}
	if meta.Slug == "same" {
		t.Fatalf("rename slug must differ from base; got %q", meta.Slug)
	}
	// Both projects should appear in List — the original at "same" and
	// the renamed copy at whatever uniquePhoneSlug picked.
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	hits := 0
	for _, m := range list {
		if m.Slug == "same" || m.Slug == meta.Slug {
			hits++
		}
	}
	if hits < 2 {
		t.Fatalf("List should contain both original and renamed; got %d hits", hits)
	}
}

func TestAgentStore_WriteConflictOverwriteReplaces(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	ctx := context.Background()

	if _, err := store.Write(ctx, Project{Slug: "ow", Name: "First", Template: "blank"}, WriteOptions{}); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := store.Write(ctx, Project{Slug: "ow", Name: "Second", Template: "blank"}, WriteOptions{OnConflict: ConflictOverwrite}); err != nil {
		t.Fatalf("overwrite Write: %v", err)
	}
	got, err := store.Read(ctx, "ow")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Name != "Second" {
		t.Fatalf("overwrite did not replace; Name = %q, want Second", got.Name)
	}
}

func TestAgentStore_WriteRequiresSlugOrName(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	_, err := store.Write(context.Background(), Project{}, WriteOptions{})
	if err == nil {
		t.Fatal("Write must reject empty Slug + empty Name; would create unnamed project otherwise")
	}
}

func TestAgentStore_StubsFailLoudly(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	ctx := context.Background()
	if _, err := store.Snapshot(ctx, "x", SnapshotOptions{}); err == nil {
		t.Fatal("Snapshot stub must error rather than return empty body silently")
	}
	if err := store.ApplySnapshot(ctx, "x", Snapshot{}); err == nil {
		t.Fatal("ApplySnapshot stub must error rather than return nil silently")
	}
}

func TestAgentStore_ContextCancellationIsHonoured(t *testing.T) {
	setupPhoneTestHome(t)
	store := AgentProjectStore{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.List(ctx); err == nil {
		t.Fatal("List with cancelled ctx should return ctx.Err()")
	}
	if _, err := store.Read(ctx, "x"); err == nil {
		t.Fatal("Read with cancelled ctx should return ctx.Err()")
	}
	if _, err := store.Write(ctx, Project{Name: "x"}, WriteOptions{}); err == nil {
		t.Fatal("Write with cancelled ctx should return ctx.Err()")
	}
}
