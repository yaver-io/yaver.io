package main

// package_store.go — local-first store + manifest types for Yaver Task Packages:
// portable, declarative, consent-gated units of work an owner authors once and
// shares to another person who runs them on their phone or a worker box.
// See docs/yaver-task-packages.md.
//
// PRIVACY: like the collection store, this is LOCAL ONLY (~/.yaver/packages/
// store.json). Convex carries only bookkeeping (handled elsewhere); collected
// data and secret-bearing config never live here in a shareable form — the
// shareable manifest is the metadata, secrets resolve on-device at run time.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// --- manifest (yaver/v1) ----------------------------------------------------

// TaskPackage is the portable artifact. It is the in-memory form of a
// yaml/json `yaver.package.yaml`.
type TaskPackage struct {
	APIVersion string      `json:"apiVersion"` // yaver/v1
	Kind       string      `json:"kind"`       // TaskPackage
	Metadata   PackageMeta `json:"metadata"`
	Spec       PackageSpec `json:"spec"`
	CreatedAt  int64       `json:"createdAt"`
	UpdatedAt  int64       `json:"updatedAt"`
}

type PackageMeta struct {
	Name        string `json:"name"`
	Owner       string `json:"owner,omitempty"`
	Version     int    `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
}

type PackageSpec struct {
	Task     PackageTask     `json:"task"`
	Runtimes []string        `json:"runtimes,omitempty"` // mobile | agent | docker | worker
	Image    string          `json:"image,omitempty"`    // docker target
	Vantage  PackageVantage  `json:"vantage,omitempty"`
	Schedule PackageSchedule `json:"schedule,omitempty"`
	Output   PackageOutput   `json:"output,omitempty"`
	Consent  PackageConsent  `json:"consent,omitempty"`
	Guard    PackageGuard    `json:"guard,omitempty"`
}

// PackageTask is the body. It can be declarative sources, an imperative step,
// an MCP-over-MCP call (use an existing MCP server to do the work), or an
// agent goal — see docs §2b/§3a.
type PackageTask struct {
	Kind    string              `json:"kind"`              // collect|probe|monitor|operate|agent
	Engines []string            `json:"engines,omitempty"` // fetch|webview|playwright|redroid|mcp|runner
	Sources []PackageSource     `json:"sources,omitempty"`
	Steps   []PackageStep       `json:"steps,omitempty"`
	MCP     []PackageMCPBinding `json:"mcp,omitempty"` // MCP-over-MCP bindings
	Goal    string              `json:"goal,omitempty"`
	Tools   []string            `json:"tools,omitempty"` // scoped tool allowlist for agent runner
	Dataset string              `json:"dataset,omitempty"`
}

type PackageSource struct {
	ID      string                    `json:"id"`
	URL     string                    `json:"url"`
	Render  string                    `json:"render,omitempty"` // auto|fetch|webview
	Extract map[string]PackageExtract `json:"extract,omitempty"`
}

// PackageExtract pulls one field. jsonPath (dot path) is resolved by the Go
// runtime for fetch/JSON sources; selector is for the mobile WebView target.
type PackageExtract struct {
	Selector string `json:"selector,omitempty"`
	JSONPath string `json:"jsonPath,omitempty"`
	As       string `json:"as,omitempty"` // number|text (coercion hint)
}

type PackageStep struct {
	Run     string `json:"run"`
	Workdir string `json:"workdir,omitempty"`
}

// PackageMCPBinding is "MCP over MCP": a task uses another MCP server (e.g. the
// owner's already-built yaver-bet MCP) to do work. The server can be remote
// (http) or a local Yaver ops verb (verb). The result is captured into the
// run's fields under `as` (default the binding name).
type PackageMCPBinding struct {
	Name      string                 `json:"name"`
	URL       string                 `json:"url,omitempty"`       // remote http MCP endpoint
	AuthToken string                 `json:"authToken,omitempty"` // resolved on-device; not shipped in the public manifest
	Tool      string                 `json:"tool,omitempty"`      // tools/call name (http transport)
	Verb      string                 `json:"verb,omitempty"`      // local Yaver ops verb (local transport)
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	As        string                 `json:"as,omitempty"`
}

func (b PackageMCPBinding) transport() string {
	if strings.TrimSpace(b.URL) != "" {
		return "http"
	}
	if strings.TrimSpace(b.Verb) != "" {
		return "local"
	}
	return ""
}

type PackageVantage struct {
	Geo         []string `json:"geo,omitempty"` // required region(s); empty = anywhere
	Residential bool     `json:"residential,omitempty"`
}

type PackageSchedule struct {
	Every    string `json:"every,omitempty"` // "10m"; Android WorkManager floor is 15m
	Jitter   string `json:"jitter,omitempty"`
	Wakeable bool   `json:"wakeable,omitempty"`
}

type PackageOutput struct {
	Sink    string `json:"sink,omitempty"`    // owner_box | ingest:<url> | dataset:<name>
	Dataset string `json:"dataset,omitempty"` // dataset name for owner_box sink
}

type PackageConsent struct {
	Summary   string   `json:"summary,omitempty"`
	WillNot   []string `json:"willNot,omitempty"`
	DataShown []string `json:"dataShown,omitempty"`
}

// PackageGuard separates READ-ONLY (collect/probe/monitor) from ACTING
// (operate/agent, any write). ACTING requires explicit confirmation at run time.
type PackageGuard struct {
	Tier    string `json:"tier,omitempty"`    // read_only | acting
	Confirm string `json:"confirm,omitempty"` // never | per-run | per-action
	Sandbox string `json:"sandbox,omitempty"` // required | none
}

var readOnlyKinds = map[string]bool{"collect": true, "probe": true, "monitor": true}
var validPackageKinds = map[string]bool{
	"collect": true, "probe": true, "monitor": true, "operate": true, "agent": true,
}

// effectiveTier returns the guard tier, defaulting from the task kind: read-only
// kinds default read_only, operate/agent default acting.
func (p *TaskPackage) effectiveTier() string {
	if t := strings.TrimSpace(p.Spec.Guard.Tier); t != "" {
		return t
	}
	if readOnlyKinds[p.Spec.Task.Kind] {
		return "read_only"
	}
	return "acting"
}

// validatePackage applies defaults and checks the minimum required shape.
func validatePackage(p *TaskPackage) error {
	if p.APIVersion == "" {
		p.APIVersion = "yaver/v1"
	}
	if p.APIVersion != "yaver/v1" {
		return fmt.Errorf("unsupported apiVersion %q (want yaver/v1)", p.APIVersion)
	}
	if p.Kind == "" {
		p.Kind = "TaskPackage"
	}
	if strings.TrimSpace(p.Metadata.Name) == "" {
		return fmt.Errorf("metadata.name required")
	}
	if p.Spec.Task.Kind == "" {
		p.Spec.Task.Kind = "collect"
	}
	if !validPackageKinds[p.Spec.Task.Kind] {
		return fmt.Errorf("invalid task.kind %q", p.Spec.Task.Kind)
	}
	if len(p.Spec.Task.Sources) == 0 && len(p.Spec.Task.Steps) == 0 &&
		len(p.Spec.Task.MCP) == 0 && strings.TrimSpace(p.Spec.Task.Goal) == "" {
		return fmt.Errorf("task needs at least one of: sources, steps, mcp, goal")
	}
	if p.Metadata.Version == 0 {
		p.Metadata.Version = 1
	}
	return nil
}

// --- allocations + runs -----------------------------------------------------

// PackageAllocation binds a package to a runner device + target, under consent.
type PackageAllocation struct {
	AllocationID   string `json:"allocationId"`
	PackageName    string `json:"packageName"`
	RunnerDeviceID string `json:"runnerDeviceId"`
	Target         string `json:"target"` // mobile | agent | docker | worker
	Status         string `json:"status"` // proposed | accepted | active | paused | revoked
	ConsentAt      int64  `json:"consentAt,omitempty"`
	WifiOnly       bool   `json:"wifiOnly"`
	ChargingOnly   bool   `json:"chargingOnly"`
	RunCount       int    `json:"runCount"`
	BlockCount     int    `json:"blockCount"`
	LastRunAt      int64  `json:"lastRunAt,omitempty"`
	LastStatus     string `json:"lastStatus,omitempty"`
	LastCountry    string `json:"lastCountry,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// PackageRun is a local audit row for one execution (counts + a small summary).
type PackageRun struct {
	RunID          string                 `json:"runId"`
	PackageName    string                 `json:"packageName"`
	At             int64                  `json:"at"`
	Status         string                 `json:"status"`
	RowsExtracted  int                    `json:"rowsExtracted"`
	SourcesOk      int                    `json:"sourcesOk"`
	SourcesBlocked int                    `json:"sourcesBlocked"`
	Country        string                 `json:"country,omitempty"`
	Summary        map[string]interface{} `json:"summary,omitempty"`
}

// --- store ------------------------------------------------------------------

type packageStoreT struct {
	mu     sync.Mutex
	path   string
	loaded bool
	seq    int64

	Packages    map[string]*TaskPackage        `json:"packages"`    // keyed by metadata.name
	Allocations map[string]*PackageAllocation  `json:"allocations"` // keyed by allocationId
	Runs        []*PackageRun                  `json:"runs"`
	Checks      map[string]*PackageCheckResult `json:"checks"` // last preflight, keyed by package name
}

var pkgStore = &packageStoreT{}

func (s *packageStoreT) ensureLoaded() {
	if s.loaded {
		return
	}
	if s.Packages == nil {
		s.Packages = map[string]*TaskPackage{}
	}
	if s.Allocations == nil {
		s.Allocations = map[string]*PackageAllocation{}
	}
	if s.Checks == nil {
		s.Checks = map[string]*PackageCheckResult{}
	}
	if s.path == "" {
		if dir, err := ConfigDir(); err == nil {
			pdir := filepath.Join(dir, "packages")
			if mkErr := os.MkdirAll(pdir, 0o700); mkErr == nil {
				s.path = filepath.Join(pdir, "store.json")
			}
		}
	}
	if s.path != "" {
		if data, err := os.ReadFile(s.path); err == nil {
			_ = json.Unmarshal(data, s)
		}
	}
	s.loaded = true
}

func (s *packageStoreT) save() {
	if s.path == "" {
		return
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(s.path, data, 0o600)
}

func (s *packageStoreT) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), s.seq)
}

func (s *packageStoreT) upsertPackage(p TaskPackage) *TaskPackage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	now := time.Now().UnixMilli()
	if existing, ok := s.Packages[p.Metadata.Name]; ok {
		p.CreatedAt = existing.CreatedAt
		if p.Metadata.Version <= existing.Metadata.Version {
			p.Metadata.Version = existing.Metadata.Version + 1
		}
	} else {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	s.Packages[p.Metadata.Name] = &p
	s.save()
	return &p
}

func (s *packageStoreT) getPackage(name string) (*TaskPackage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	p, ok := s.Packages[name]
	return p, ok
}

func (s *packageStoreT) listPackages() []*TaskPackage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	out := make([]*TaskPackage, 0, len(s.Packages))
	for _, p := range s.Packages {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metadata.Name < out[j].Metadata.Name })
	return out
}

func (s *packageStoreT) deletePackage(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	if _, ok := s.Packages[name]; !ok {
		return false
	}
	delete(s.Packages, name)
	s.save()
	return true
}

func (s *packageStoreT) upsertAllocation(a PackageAllocation) *PackageAllocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	now := time.Now().UnixMilli()
	if a.AllocationID == "" {
		a.AllocationID = s.nextID("alloc")
	}
	if a.Status == "" {
		a.Status = "proposed"
	}
	if existing, ok := s.Allocations[a.AllocationID]; ok {
		a.CreatedAt = existing.CreatedAt
		a.RunCount = existing.RunCount
		a.BlockCount = existing.BlockCount
	} else {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	s.Allocations[a.AllocationID] = &a
	s.save()
	return &a
}

func (s *packageStoreT) listAllocations(packageName string) []*PackageAllocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	out := make([]*PackageAllocation, 0)
	for _, a := range s.Allocations {
		if packageName != "" && a.PackageName != packageName {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AllocationID < out[j].AllocationID })
	return out
}

func (s *packageStoreT) setCheck(name string, r *PackageCheckResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	s.Checks[name] = r
	s.save()
}

func (s *packageStoreT) getCheck(name string) (*PackageCheckResult, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	r, ok := s.Checks[name]
	return r, ok
}

func (s *packageStoreT) recordRun(run PackageRun) *PackageRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	if run.RunID == "" {
		run.RunID = s.nextID("prun")
	}
	if run.At == 0 {
		run.At = time.Now().UnixMilli()
	}
	s.Runs = append(s.Runs, &run)
	if len(s.Runs) > 500 {
		s.Runs = s.Runs[len(s.Runs)-500:]
	}
	s.save()
	return &run
}

func (s *packageStoreT) recentRuns(packageName string, limit int) []*PackageRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoaded()
	out := make([]*PackageRun, 0)
	for i := len(s.Runs) - 1; i >= 0; i-- {
		r := s.Runs[i]
		if packageName != "" && r.PackageName != packageName {
			continue
		}
		out = append(out, r)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// resetPackageStoreForTest installs a fresh store. Test-only.
func resetPackageStoreForTest(dir string) {
	path := ""
	if dir != "" {
		path = filepath.Join(dir, "package-store.json")
	}
	pkgStore = &packageStoreT{
		path:        path,
		loaded:      true,
		Packages:    map[string]*TaskPackage{},
		Allocations: map[string]*PackageAllocation{},
		Checks:      map[string]*PackageCheckResult{},
	}
}
