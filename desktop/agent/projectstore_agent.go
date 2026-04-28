package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// projectstore_agent.go is the agent-tier ProjectStore implementation.
// It is a thin shim over the existing CreatePhoneProject /
// LoadPhoneProject / ListPhoneProjects / phoneProjectExists /
// DeletePhoneProject / uniquePhoneSlug functions in phone_backend.go,
// not a rewrite. Self-hosted and Yaver Cloud both run this same agent
// binary, so a single implementation backs both runtime tiers.
//
// What this gives the rest of the codebase: a stable Store interface
// to call from the new yaver code phone surface (and the eventual
// repo-tier and phone-sandbox implementations) without those callers
// needing to know about the SQLite + filesystem layout under
// ~/.yaver/phone-projects/.

// AgentProjectStore is the canonical ProjectStore for the agent's
// runtime tier. Backed by ~/.yaver/phone-projects/<slug>/.
//
// AgentProjectStore is stateless: every method resolves the project
// directory from PhoneProjectsRoot() at call time, so the same value
// can be reused across goroutines and across HOME changes (which
// matter for tests that t.Setenv("HOME", ...)).
type AgentProjectStore struct{}

// Compile-time check that AgentProjectStore satisfies the contract.
// Saves a confusing "doesn't implement" error from a far-away caller
// when a future Store-method signature change skips this file.
var _ ProjectStore = AgentProjectStore{}

// List returns metadata for every project in the agent's
// phone-projects root, sorted by UpdatedAt desc (which is what
// ListPhoneProjects already does).
func (AgentProjectStore) List(ctx context.Context) ([]ProjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ps, err := ListPhoneProjects()
	if err != nil {
		return nil, err
	}
	out := make([]ProjectMeta, 0, len(ps))
	for _, p := range ps {
		out = append(out, projectMetaFromPhone(p))
	}
	return out, nil
}

// Read fully loads a project (schema, auth, seed, app spec, stats).
// Maps any "directory missing" error to ErrProjectNotFound so the
// caller doesn't have to inspect filesystem error wrapping.
func (AgentProjectStore) Read(ctx context.Context, slug string) (Project, error) {
	if err := ctx.Err(); err != nil {
		return Project{}, err
	}
	p, err := LoadPhoneProject(slug)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Project{}, NewProjectNotFound(slug)
		}
		return Project{}, err
	}
	return projectFromPhone(p), nil
}

// Write commits a Project to disk under the agent's runtime tier,
// honouring opts.OnConflict the same way ImportPhoneProject does.
//
// Slug resolution: opts-time slug from p.Slug wins; if blank the
// Project's Name is slugified; if both are blank the call fails with
// a clear error rather than persisting an unnamed project.
//
// Known limitation (intentional for Slice 0): CreatePhoneProject
// generates new CreatedAt / UpdatedAt timestamps, so a Read → Write
// round-trip does not preserve the original CreatedAt. The roundtrip
// test below pins schema/auth/seed equality, not metadata equality.
// Adding a "preserve metadata" path is a follow-up.
func (AgentProjectStore) Write(ctx context.Context, p Project, opts WriteOptions) (ProjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return ProjectMeta{}, err
	}

	targetSlug := strings.TrimSpace(p.Slug)
	if targetSlug == "" {
		targetSlug = Slugify(p.Name)
	}
	if targetSlug == "" {
		return ProjectMeta{}, fmt.Errorf("AgentProjectStore.Write: slug or name required")
	}

	// Apply conflict policy *before* spending any work building the
	// PhoneCreateSpec. Default is reject — same as the existing
	// /phone/projects/receive endpoint.
	if exists, _ := phoneProjectExists(targetSlug); exists {
		switch opts.OnConflict {
		case ConflictOverwrite:
			if err := DeletePhoneProject(targetSlug); err != nil {
				return ProjectMeta{}, fmt.Errorf("overwrite %q: %w", targetSlug, err)
			}
		case ConflictRename:
			targetSlug = uniquePhoneSlug(targetSlug)
		case ConflictReject:
			fallthrough
		default:
			return ProjectMeta{}, fmt.Errorf("%w: %s", ErrPhoneProjectExists, targetSlug)
		}
	}

	spec := PhoneCreateSpec{
		Slug:     targetSlug,
		Name:     strings.TrimSpace(p.Name),
		Template: strings.TrimSpace(p.Template),
		Schema:   p.Schema,
		Auth:     p.Auth,
		App:      p.App,
	}
	if !opts.SkipSeed {
		spec.Seed = p.Seed
	}

	created, err := CreatePhoneProject(spec)
	if err != nil {
		return ProjectMeta{}, fmt.Errorf("create %q: %w", targetSlug, err)
	}
	return projectMetaFromPhone(created), nil
}

// Snapshot is a Slice-0 stub. Live row dumps are deferred to the
// follow-up commit that wires up the repoStore round-trip.
func (AgentProjectStore) Snapshot(ctx context.Context, slug string, opts SnapshotOptions) (Snapshot, error) {
	return Snapshot{}, errors.New("AgentProjectStore.Snapshot: not yet implemented (Slice 0 stub)")
}

// ApplySnapshot is a Slice-0 stub. See Snapshot.
func (AgentProjectStore) ApplySnapshot(ctx context.Context, slug string, snap Snapshot) error {
	return errors.New("AgentProjectStore.ApplySnapshot: not yet implemented (Slice 0 stub)")
}

// projectMetaFromPhone projects a *PhoneProject down to the small
// ProjectMeta callers consult before deciding to do a full Read.
func projectMetaFromPhone(p *PhoneProject) ProjectMeta {
	return ProjectMeta{
		Slug:      p.Slug,
		Name:      p.Name,
		Template:  p.Template,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		Tier:      "agent",
	}
}

// projectFromPhone copies *PhoneProject's loaded fields into the
// Project value the Store interface returns. Deep copies the seed
// map so callers can't mutate on-disk state by editing a returned
// Project (PhoneSeed is a map, which is reference-typed in Go).
func projectFromPhone(p *PhoneProject) Project {
	out := Project{
		Slug:      p.Slug,
		Name:      p.Name,
		Template:  p.Template,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		Schema:    p.Schema,
		Auth:      p.Auth,
		App:       p.App,
		Stats:     p.Stats,
	}
	if p.Seed != nil {
		out.Seed = make(PhoneSeed, len(p.Seed))
		for k, rows := range p.Seed {
			cp := make([]map[string]interface{}, len(rows))
			for i, r := range rows {
				row := make(map[string]interface{}, len(r))
				for kk, vv := range r {
					row[kk] = vv
				}
				cp[i] = row
			}
			out.Seed[k] = cp
		}
	}
	return out
}
