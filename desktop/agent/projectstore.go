package main

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// projectstore.go defines the unified Read/Write contract that every
// runtime tier of a phone project must satisfy. Today three storages
// hold a project:
//
//   • the agent's SQLite-backed runtime (~/.yaver/phone-projects/<slug>/)
//   • a developer's git repo (.yaver/ directory inside the repo)
//   • the phone's offline sandbox (expo-sqlite on-device)
//
// All three reduce to two primitives: Read(slug) → Project and
// Write(Project) → ProjectMeta. Every higher-level verb (push, pull,
// export, import, deploy) is sugar on top of these primitives plus
// an HTTP transport when the source and destination live in
// different processes.
//
// Self-hosted and Yaver Cloud are the same tier — both run this same
// agent binary — so the agent has a single ProjectStore implementation
// (AgentProjectStore, defined alongside phone_backend.go) that backs
// both of them. The repo and phone-sandbox tiers each get their own
// implementation.
//
// This file declares the contract only. Implementations land in
// follow-up commits per the slice plan in
// docs/yaver-code-deploy-integration.md.

// ProjectStore is the unified Read/Write contract for a phone project
// across the three runtime tiers. Implementations MUST keep the
// canonical project files (schema, auth, seed, app spec) in sync with
// whatever lower-level storage they back onto, and MUST surface
// ProjectMeta deterministically so callers can reason about where a
// project currently lives without re-reading the whole bundle.
type ProjectStore interface {
	// List returns metadata for every project this store knows about.
	// The slice MAY be empty but MUST NOT be nil.
	List(ctx context.Context) ([]ProjectMeta, error)

	// Read materialises a project's canonical files into memory.
	// Returns ErrProjectNotFound if the slug is unknown to this store.
	// The returned Project's Stats field MAY be nil — stats are an
	// agent-only concern; the repo store has nothing to report.
	Read(ctx context.Context, slug string) (Project, error)

	// Write commits a project bundle into the store. WriteOptions
	// controls conflict behaviour when the slug already exists.
	// The returned ProjectMeta reflects the resolved slug (which may
	// differ from p.Slug under ConflictRename) and the post-write
	// timestamps so the caller doesn't have to re-Read.
	Write(ctx context.Context, p Project, opts WriteOptions) (ProjectMeta, error)

	// Snapshot dumps the live row data for the project, optionally
	// filtered by table or capped by row count. The agent store
	// drives this from the SQLite backend; the repo store reads from
	// .yaver/snapshots/. Phone-sandbox reads from on-device SQLite.
	// Returns ErrProjectNotFound if the slug is unknown.
	Snapshot(ctx context.Context, slug string, opts SnapshotOptions) (Snapshot, error)

	// ApplySnapshot writes a snapshot back into the store's live data
	// using the same conflict semantics as Write (controlled by the
	// snapshot's onConflict field). A no-op for stores that only hold
	// the declarative bundle without live rows.
	ApplySnapshot(ctx context.Context, slug string, snap Snapshot) error
}

// ProjectMeta is the small struct callers consult before deciding
// whether to do a full Read. It mirrors the fields a list endpoint
// would surface.
type ProjectMeta struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Template  string `json:"template,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	// Tier identifies which storage this project lives in. One of
	// "agent", "repo", "phone-sandbox". Useful when callers compose
	// multiple stores.
	Tier string `json:"tier,omitempty"`
}

// Project is the canonical in-memory representation of a phone
// project. The fields mirror the existing PhoneProject value used by
// phone_backend.go but are restated here so a future package split
// (e.g. moving projectstore into its own Go module) does not pull in
// SQLite or HTTP machinery.
type Project struct {
	Slug      string
	Name      string
	Template  string
	CreatedAt string
	UpdatedAt string
	Schema    *PhoneSchema
	Auth      *PhoneAuth
	Seed      PhoneSeed
	App       *PhoneAppSpec
	// Stats are populated by the agent store from the SQLite backend.
	// Repo and phone-sandbox stores leave this nil.
	Stats *PhoneStats
	// Targets records the deployment bindings the project knows about
	// (dev-hw / yaver-cloud / cloudflare-workers). Persisted to
	// .yaver/project.yaml in the repo tier.
	Targets []TargetBind
	// TokenLabels is the list of token *labels* (no secret material).
	// Persisted to .yaver/tokens.lock.yaml. The actual hashed tokens
	// live per-machine in ~/.yaver/phone-projects/<slug>/tokens.yaml
	// and never leave the machine that minted them.
	TokenLabels []TokenLabel
}

// TargetBind records a single deploy binding. Unbound targets are
// omitted entirely; an empty slice means "no targets bound yet."
type TargetBind struct {
	Kind     string `yaml:"kind" json:"kind"` // dev-hw | yaver-cloud | cloudflare-workers
	BaseURL  string `yaml:"baseUrl,omitempty" json:"baseUrl,omitempty"`
	LastSync string `yaml:"lastSync,omitempty" json:"lastSync,omitempty"`
	Notes    string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// TokenLabel is the public-safe metadata for an API token. The
// actual `pp_<slug>_<hex>` secret never appears here.
type TokenLabel struct {
	ID     string   `yaml:"id" json:"id"`
	Label  string   `yaml:"label" json:"label"`
	Scopes []string `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	CORS   []string `yaml:"cors,omitempty" json:"cors,omitempty"`
}

// WriteOptions tunes the Write call. Defaults reject collisions —
// ConflictPolicy must be set explicitly to overwrite or rename, the
// same way the existing /phone/projects/receive endpoint behaves.
type WriteOptions struct {
	OnConflict ConflictPolicy
	// SkipSeed leaves Project.Seed in memory but does not replay it
	// onto the live runtime. Lets callers replay seed only on first
	// import, not on every push.
	SkipSeed bool
	// IncludeData, when true, expects Project to carry a non-nil
	// Stats and an inline snapshot (or for the underlying transport
	// to follow up with ApplySnapshot). When false, only the
	// declarative bundle is written.
	IncludeData bool
}

// ConflictPolicy mirrors the existing phone-receive contract.
type ConflictPolicy string

const (
	ConflictReject    ConflictPolicy = ""          // default — fail if slug exists
	ConflictRename    ConflictPolicy = "rename"    // suffix the slug to make it unique
	ConflictOverwrite ConflictPolicy = "overwrite" // replace the existing project
)

// SnapshotOptions tunes Snapshot.
type SnapshotOptions struct {
	// Tables, when non-empty, limits the dump to the named tables.
	// Empty means "every table in the schema."
	Tables []string
	// MaxRowsPerTable caps the rows per table. Zero means unbounded.
	MaxRowsPerTable int
	// Compress requests gzip framing on the underlying io.Reader the
	// caller will read. Implementations that don't compress treat
	// this as a hint; HTTP transports use it to set Content-Encoding.
	Compress bool
}

// Snapshot is a streaming dump of live data. Body is JSONL by
// convention (one row per line, prefixed with the table name). The
// caller is responsible for closing Body.
type Snapshot struct {
	Slug       string
	TakenAt    string
	Rows       int64
	OnConflict ConflictPolicy // how ApplySnapshot should resolve row collisions
	Body       io.ReadCloser
}

// ErrProjectNotFound is the canonical sentinel for "no project with
// this slug." HTTP transports translate this to 404; CLI surfaces
// translate it to a clear "no such project" message instead of a
// generic "internal error." Callers detect it with errors.Is.
var ErrProjectNotFound = errors.New("project not found")

// NewProjectNotFound returns an error that carries the slug for
// human-facing messages while still satisfying
// errors.Is(err, ErrProjectNotFound) so callers at the HTTP/CLI
// boundary can map to 404 / non-generic error text.
func NewProjectNotFound(slug string) error {
	return fmt.Errorf("%w: %s", ErrProjectNotFound, slug)
}
