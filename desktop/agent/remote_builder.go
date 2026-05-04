package main

// remote_builder.go — local registry of paired remote-mac builders.
// A "builder" is another yaver-agent box (typically a Mac mini or
// Mac VM) that the current Linux/WSL host can dispatch iOS / Swift
// build + run sessions to. The Linux agent stores only the alias,
// URL, and a per-builder auth token; the Mac side keeps the actual
// Xcode + simulator install.
//
// The registry is a plain JSON file under ~/.yaver/builders.json.
// We deliberately do NOT push this to Convex — hostnames + tokens
// are infra-sensitive and would violate the privacy contract from
// CLAUDE.md (`remoteBuilderHostname` and `remoteBuilderTunnelToken`
// are on the forbidden-keys list). Convex sees only counters via
// `remoteRuntimeSessionMetrics`.
//
// Atomic save: write to <path>.tmp then rename. A crash mid-write
// leaves the previous registry intact, never a half-truncated file.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// BuilderEntry is one paired remote builder. Token is opaque to the
// agent; it just gets forwarded on every dispatched HTTP call.
type BuilderEntry struct {
	Alias     string   `json:"alias"`
	URL       string   `json:"url"`
	Token     string   `json:"token,omitempty"`
	Platforms []string `json:"platforms"`
	AddedAt   string   `json:"addedAt"`
	Note      string   `json:"note,omitempty"`
}

// BuilderRegistry holds the on-disk state. Default is the alias the
// session manager picks when a Swift project asks for an iOS target
// on a non-darwin host. An empty Default with non-empty Builders
// means the user has paired a builder but not chosen one — the CLI
// nudges them with a clear message rather than guessing.
type BuilderRegistry struct {
	Default  string                  `json:"default,omitempty"`
	Builders map[string]BuilderEntry `json:"builders"`
}

var (
	builderRegistryMu sync.RWMutex

	// builderPlatformsMu guards the in-process record of "this box
	// is acting as a builder for these platforms". Set once at
	// `yaver serve` parse time and read on every `/info` request.
	builderPlatformsMu sync.RWMutex
	builderPlatformsV  []string
)

// SetBuilderPlatforms records the platform list this agent serves
// as a builder for. Called from runServe() after flag parsing. An
// empty slice (the default) means "not a builder".
func SetBuilderPlatforms(platforms []string) {
	builderPlatformsMu.Lock()
	defer builderPlatformsMu.Unlock()
	out := make([]string, 0, len(platforms))
	seen := map[string]bool{}
	for _, p := range platforms {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	builderPlatformsV = out
}

// builderPlatforms returns the current builder platform list. Used
// by the /info handler to advertise builder mode + by the
// remote_runtime capability surface.
func builderPlatforms() []string {
	builderPlatformsMu.RLock()
	defer builderPlatformsMu.RUnlock()
	out := make([]string, len(builderPlatformsV))
	copy(out, builderPlatformsV)
	return out
}

// builderRegistryPath returns the on-disk path. Falls back to
// $HOME/.yaver/builders.json so a missing $YAVER_HOME doesn't break
// things on a fresh box.
func builderRegistryPath() string {
	if h := strings.TrimSpace(os.Getenv("YAVER_HOME")); h != "" {
		return filepath.Join(h, "builders.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Last-ditch fallback: cwd. Better than crashing on a host
		// with no $HOME (rare but possible inside a Docker sandbox).
		return ".yaver-builders.json"
	}
	return filepath.Join(home, ".yaver", "builders.json")
}

// LoadBuilders reads the registry from disk. Returns an empty
// registry (not nil) when the file doesn't exist so callers can
// always range over .Builders without a nil check.
func LoadBuilders() (*BuilderRegistry, error) {
	builderRegistryMu.RLock()
	defer builderRegistryMu.RUnlock()
	return loadBuildersFrom(builderRegistryPath())
}

func loadBuildersFrom(path string) (*BuilderRegistry, error) {
	reg := &BuilderRegistry{Builders: map[string]BuilderEntry{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) == 0 {
		return reg, nil
	}
	if err := json.Unmarshal(raw, reg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if reg.Builders == nil {
		reg.Builders = map[string]BuilderEntry{}
	}
	return reg, nil
}

// SaveBuilders writes the registry atomically: tmp file + rename.
// Caller passes the registry by value-or-ptr; mutation here doesn't
// leak back. Uses 0600 so a token doesn't end up world-readable.
func SaveBuilders(reg *BuilderRegistry) error {
	builderRegistryMu.Lock()
	defer builderRegistryMu.Unlock()
	return saveBuildersTo(builderRegistryPath(), reg)
}

func saveBuildersTo(path string, reg *BuilderRegistry) error {
	if reg == nil {
		return fmt.Errorf("nil registry")
	}
	if reg.Builders == nil {
		reg.Builders = map[string]BuilderEntry{}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	body, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".builders-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}

// AddBuilder upserts the entry by alias and writes the file. If
// reg.Default is empty the new alias becomes the default — first
// pairing wins so a single-Mac user is configured after one
// command.
func (r *BuilderRegistry) AddBuilder(entry BuilderEntry) error {
	if strings.TrimSpace(entry.Alias) == "" {
		return fmt.Errorf("alias required")
	}
	if strings.TrimSpace(entry.URL) == "" {
		return fmt.Errorf("url required")
	}
	if entry.AddedAt == "" {
		entry.AddedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if entry.Platforms == nil {
		entry.Platforms = []string{"ios"}
	}
	if r.Builders == nil {
		r.Builders = map[string]BuilderEntry{}
	}
	r.Builders[entry.Alias] = entry
	if r.Default == "" {
		r.Default = entry.Alias
	}
	return nil
}

// Forget removes an entry. Clearing the current default falls back
// to "the alphabetically first remaining alias" so the user is
// never left with a stale default pointing at nothing.
func (r *BuilderRegistry) Forget(alias string) bool {
	alias = strings.TrimSpace(alias)
	if _, ok := r.Builders[alias]; !ok {
		return false
	}
	delete(r.Builders, alias)
	if r.Default == alias {
		r.Default = ""
		var aliases []string
		for k := range r.Builders {
			aliases = append(aliases, k)
		}
		sort.Strings(aliases)
		if len(aliases) > 0 {
			r.Default = aliases[0]
		}
	}
	return true
}

// SetDefault marks alias as the default builder. Returns an error
// (not silent) if the alias isn't paired — saves a confused-user
// 5-minute debug session later.
func (r *BuilderRegistry) SetDefault(alias string) error {
	alias = strings.TrimSpace(alias)
	if _, ok := r.Builders[alias]; !ok {
		return fmt.Errorf("no paired builder with alias %q (run `yaver builder list` for the registered set)", alias)
	}
	r.Default = alias
	return nil
}

// SortedAliases returns the alias keys in stable order — the CLI
// uses it for `yaver builder list` so output is deterministic.
func (r *BuilderRegistry) SortedAliases() []string {
	out := make([]string, 0, len(r.Builders))
	for k := range r.Builders {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PingBuilder issues a GET <url>/info with the entry's token (if
// any) and decodes the response. Used by `yaver builder ping` and
// by `yaver builder list` to gate the ✓ / ✗ marker. Surfaces both
// transport-level errors (timeout, refused) and HTTP-level errors
// (non-200) so the user knows whether the box is unreachable or
// just refusing the request.
type BuilderInfo struct {
	OK        bool     `json:"ok"`
	Version   string   `json:"version,omitempty"`
	Hostname  string   `json:"hostname,omitempty"`
	Platforms []string `json:"platforms,omitempty"`
	IsBuilder bool     `json:"isBuilder"`
	Note      string   `json:"note,omitempty"`
}

func PingBuilder(client *http.Client, entry BuilderEntry) (*BuilderInfo, error) {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	url := strings.TrimRight(entry.URL, "/") + "/info"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if entry.Token != "" {
		req.Header.Set("Authorization", "Bearer "+entry.Token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	var info BuilderInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("parse /info: %w", err)
	}
	info.OK = true
	return &info, nil
}
