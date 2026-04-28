package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// projectstore_repo.go is the repo-tier ProjectStore implementation.
// It reads and writes a project's canonical files under
// `<root>/.yaver/`, treating the directory tree as the source of
// truth. This is what enables the "dual-mode" workflow described in
// docs/yaver-code-deploy-integration.md: a developer keeps the
// project's schema/auth/seed under git alongside the consuming app
// code, edits either side in their editor of choice, and uses the
// agent tier to apply changes back into the live runtime.
//
// File layout (one project per repo for Slice 0):
//
//   <root>/.yaver/project.yaml         — slug, name, target bindings
//   <root>/.yaver/schema.yaml          — table + column definitions
//   <root>/.yaver/auth.yaml            — auth personas / providers
//   <root>/.yaver/seed.yaml            — initial rows per table
//   <root>/.yaver/app.yaml             — optional app spec
//   <root>/.yaver/tokens.lock.yaml     — token labels (no secrets)
//
// Hard rule: nothing under `.yaver/` may contain secret material.
// API tokens stay in ~/.yaver/phone-projects/<slug>/tokens.yaml on
// the agent that minted them. tokens.lock.yaml carries labels +
// scopes only, so a `git diff` reveals "we added a web-prod token"
// without leaking the token itself.

// RepoProjectStore is the ProjectStore backed by a `.yaver/`
// directory inside a git repo (or any plain directory — the store
// does not require git).
//
// Each instance is bound to a single root path. Use NewRepoProjectStore
// to construct one rather than the zero value, which would silently
// operate on the working directory.
type RepoProjectStore struct {
	root string
}

// Compile-time interface check.
var _ ProjectStore = (*RepoProjectStore)(nil)

// NewRepoProjectStore binds a store to <root>/.yaver/.
func NewRepoProjectStore(root string) *RepoProjectStore {
	return &RepoProjectStore{root: root}
}

// Root returns the absolute repo root the store reads from. Helpful
// in error messages and tests.
func (s *RepoProjectStore) Root() string { return s.root }

func (s *RepoProjectStore) yaverDir() string {
	return filepath.Join(s.root, ".yaver")
}

// projectYAML is the on-disk shape of .yaver/project.yaml. Kept
// distinct from Project so the file format can evolve independently
// of the in-memory contract.
type projectYAML struct {
	Slug      string       `yaml:"slug"`
	Name      string       `yaml:"name"`
	Template  string       `yaml:"template,omitempty"`
	CreatedAt string       `yaml:"createdAt,omitempty"`
	UpdatedAt string       `yaml:"updatedAt,omitempty"`
	Targets   []TargetBind `yaml:"targets,omitempty"`
}

type tokensLockYAML struct {
	Tokens []TokenLabel `yaml:"tokens"`
}

type seedYAML struct {
	Tables PhoneSeed `yaml:"tables"`
}

// List returns one ProjectMeta if `.yaver/project.yaml` exists,
// otherwise an empty slice. Slice 0 supports a single project per
// repo; multi-project repos can come later without breaking the
// contract.
func (s *RepoProjectStore) List(ctx context.Context) ([]ProjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pj, err := s.readProjectYAML()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []ProjectMeta{}, nil
		}
		return nil, err
	}
	return []ProjectMeta{{
		Slug:      pj.Slug,
		Name:      pj.Name,
		Template:  pj.Template,
		CreatedAt: pj.CreatedAt,
		UpdatedAt: pj.UpdatedAt,
		Tier:      "repo",
	}}, nil
}

// Read loads the project under `<root>/.yaver/`. Returns
// ErrProjectNotFound if either the directory is missing or the
// requested slug doesn't match `project.yaml`.
func (s *RepoProjectStore) Read(ctx context.Context, slug string) (Project, error) {
	if err := ctx.Err(); err != nil {
		return Project{}, err
	}
	pj, err := s.readProjectYAML()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Project{}, NewProjectNotFound(slug)
		}
		return Project{}, fmt.Errorf("read .yaver/project.yaml: %w", err)
	}
	if strings.TrimSpace(slug) != "" && slug != pj.Slug {
		return Project{}, NewProjectNotFound(slug)
	}

	out := Project{
		Slug:      pj.Slug,
		Name:      pj.Name,
		Template:  pj.Template,
		CreatedAt: pj.CreatedAt,
		UpdatedAt: pj.UpdatedAt,
		Targets:   pj.Targets,
	}

	if schema, err := s.readSchema(); err != nil {
		return Project{}, fmt.Errorf("read schema.yaml: %w", err)
	} else if schema != nil {
		out.Schema = schema
	}
	if auth, err := s.readAuth(); err != nil {
		return Project{}, fmt.Errorf("read auth.yaml: %w", err)
	} else if auth != nil {
		out.Auth = auth
	}
	if seed, err := s.readSeed(); err != nil {
		return Project{}, fmt.Errorf("read seed.yaml: %w", err)
	} else if seed != nil {
		out.Seed = seed
	}
	if app, err := s.readApp(); err != nil {
		return Project{}, fmt.Errorf("read app.yaml: %w", err)
	} else if app != nil {
		out.App = app
	}
	if labels, err := s.readTokenLabels(); err != nil {
		return Project{}, fmt.Errorf("read tokens.lock.yaml: %w", err)
	} else if labels != nil {
		out.TokenLabels = labels
	}
	return out, nil
}

// Write persists Project to `<root>/.yaver/`. Conflict policy
// controls behaviour when a project.yaml already exists with a
// different slug — Reject is the safe default, Overwrite replaces
// the directory, Rename is rejected (a repo cannot host a renamed
// copy without choosing a different root, so callers must pick a
// new RepoProjectStore root explicitly).
//
// Writes are atomic per-file (write to .tmp, rename) so a crash
// mid-write leaves the previous state intact.
func (s *RepoProjectStore) Write(ctx context.Context, p Project, opts WriteOptions) (ProjectMeta, error) {
	if err := ctx.Err(); err != nil {
		return ProjectMeta{}, err
	}
	if strings.TrimSpace(p.Slug) == "" {
		return ProjectMeta{}, fmt.Errorf("RepoProjectStore.Write: project Slug is required (repo tier does not slugify Name)")
	}

	if existing, err := s.readProjectYAML(); err == nil {
		if existing.Slug != p.Slug {
			switch opts.OnConflict {
			case ConflictOverwrite:
				if err := os.RemoveAll(s.yaverDir()); err != nil {
					return ProjectMeta{}, fmt.Errorf("overwrite: %w", err)
				}
			case ConflictRename:
				return ProjectMeta{}, fmt.Errorf("RepoProjectStore.Write: ConflictRename not supported in repo tier (point at a different root instead)")
			case ConflictReject:
				fallthrough
			default:
				return ProjectMeta{}, fmt.Errorf("%w in repo: existing slug %q != requested %q", ErrPhoneProjectExists, existing.Slug, p.Slug)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return ProjectMeta{}, fmt.Errorf("inspect existing project: %w", err)
	}

	if err := os.MkdirAll(s.yaverDir(), 0o755); err != nil {
		return ProjectMeta{}, fmt.Errorf("mkdir .yaver: %w", err)
	}

	pj := projectYAML{
		Slug:      p.Slug,
		Name:      p.Name,
		Template:  p.Template,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		Targets:   p.Targets,
	}
	if err := s.writeYAML("project.yaml", pj); err != nil {
		return ProjectMeta{}, err
	}
	if p.Schema != nil {
		if err := s.writeYAML("schema.yaml", p.Schema); err != nil {
			return ProjectMeta{}, err
		}
	}
	if p.Auth != nil {
		if err := s.writeYAML("auth.yaml", p.Auth); err != nil {
			return ProjectMeta{}, err
		}
	}
	if p.Seed != nil && !opts.SkipSeed {
		if err := s.writeYAML("seed.yaml", seedYAML{Tables: p.Seed}); err != nil {
			return ProjectMeta{}, err
		}
	}
	if p.App != nil {
		if err := s.writeYAML("app.yaml", p.App); err != nil {
			return ProjectMeta{}, err
		}
	}
	if len(p.TokenLabels) > 0 {
		if err := s.writeYAML("tokens.lock.yaml", tokensLockYAML{Tokens: p.TokenLabels}); err != nil {
			return ProjectMeta{}, err
		}
	}

	return ProjectMeta{
		Slug:      p.Slug,
		Name:      p.Name,
		Template:  p.Template,
		CreatedAt: p.CreatedAt,
		UpdatedAt: p.UpdatedAt,
		Tier:      "repo",
	}, nil
}

// Snapshot reads `<root>/.yaver/snapshots/` if present. Slice 0
// stub — returns "not implemented" loudly so callers don't think
// they got an empty snapshot of real data.
func (s *RepoProjectStore) Snapshot(ctx context.Context, slug string, opts SnapshotOptions) (Snapshot, error) {
	return Snapshot{}, errors.New("RepoProjectStore.Snapshot: not yet implemented (Slice 0 stub)")
}

// ApplySnapshot writes into `<root>/.yaver/snapshots/`. Slice 0 stub.
func (s *RepoProjectStore) ApplySnapshot(ctx context.Context, slug string, snap Snapshot) error {
	return errors.New("RepoProjectStore.ApplySnapshot: not yet implemented (Slice 0 stub)")
}

// ---- File I/O helpers ----

func (s *RepoProjectStore) readProjectYAML() (*projectYAML, error) {
	var pj projectYAML
	if err := s.readYAML("project.yaml", &pj); err != nil {
		return nil, err
	}
	return &pj, nil
}

func (s *RepoProjectStore) readSchema() (*PhoneSchema, error) {
	var v PhoneSchema
	if err := s.readYAML("schema.yaml", &v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (s *RepoProjectStore) readAuth() (*PhoneAuth, error) {
	var v PhoneAuth
	if err := s.readYAML("auth.yaml", &v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (s *RepoProjectStore) readSeed() (PhoneSeed, error) {
	var v seedYAML
	if err := s.readYAML("seed.yaml", &v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return v.Tables, nil
}

func (s *RepoProjectStore) readApp() (*PhoneAppSpec, error) {
	var v PhoneAppSpec
	if err := s.readYAML("app.yaml", &v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (s *RepoProjectStore) readTokenLabels() ([]TokenLabel, error) {
	var v tokensLockYAML
	if err := s.readYAML("tokens.lock.yaml", &v); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return v.Tokens, nil
}

func (s *RepoProjectStore) readYAML(name string, into interface{}) error {
	path := filepath.Join(s.yaverDir(), name)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, into)
}

// writeYAML serializes `value` to .yaver/<name> atomically: marshal
// to a buffer, write to <name>.tmp, rename. A crash between the
// write and the rename leaves the previous file intact.
func (s *RepoProjectStore) writeYAML(name string, value interface{}) error {
	path := filepath.Join(s.yaverDir(), name)
	tmp := path + ".tmp"
	b, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", name, err)
	}
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}
