package main

// package_registry.go — agent-side cache of the public Convex
// package catalogue. Lets new tools and new install commands ship
// without a CLI release: the agent pulls the catalogue on boot,
// refreshes every 6 hours, and merges it into the `/install/list`
// response so the phone/web Tools tab always shows the current set.
//
// The endpoint is intentionally public. Every payload field is
// non-sensitive (tool name, install commands, description, doc URL)
// so there's nothing to leak if a stranger hits it.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"time"
)

// PackageRegistryEntry mirrors the Convex `packages:list` row shape.
type PackageRegistryEntry struct {
	Name         string                 `json:"name"`
	Kind         string                 `json:"kind"`
	Description  string                 `json:"description"`
	Tags         []string               `json:"tags,omitempty"`
	Installs     []PackageRegistryStep  `json:"installs"`
	CheckCommand string                 `json:"checkCommand,omitempty"`
	DocURL       string                 `json:"docUrl,omitempty"`
	UpdatedAt    int64                  `json:"updatedAt"`
}

// PackageRegistryStep is one install route for a registry entry.
// An empty `Platform` matches any OS; an empty `PackageManager` means
// "run the command verbatim" (used by curl-based one-liners).
type PackageRegistryStep struct {
	Platform       string `json:"platform,omitempty"`
	PackageManager string `json:"packageManager"`
	Command        string `json:"command"`
}

var (
	registryMu        sync.RWMutex
	registryCache     []PackageRegistryEntry
	registryFetchedAt time.Time
	registryTTL       = 6 * time.Hour
)

// fetchPackageRegistry pulls the public `packages:list` query from
// Convex. Uses the ConvexSiteURL from the agent's config so self-
// hosted Yaver deployments just work.
func fetchPackageRegistry(ctx context.Context, convexSiteURL string) ([]PackageRegistryEntry, error) {
	if convexSiteURL == "" {
		return nil, fmt.Errorf("convex site URL not configured")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", convexSiteURL+"/packages", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("convex /packages returned %d", resp.StatusCode)
	}
	var out []PackageRegistryEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// RefreshPackageRegistry forces a fetch and caches the result. Safe
// to call from any goroutine.
func RefreshPackageRegistry(ctx context.Context, convexSiteURL string) error {
	entries, err := fetchPackageRegistry(ctx, convexSiteURL)
	if err != nil {
		return err
	}
	registryMu.Lock()
	registryCache = entries
	registryFetchedAt = time.Now()
	registryMu.Unlock()
	return nil
}

// PackageRegistry returns the cached list, refreshing in the
// background if TTL elapsed. Never blocks — callers always get
// whatever the last successful fetch returned, possibly empty.
func PackageRegistry(convexSiteURL string) []PackageRegistryEntry {
	registryMu.RLock()
	cached := registryCache
	stale := time.Since(registryFetchedAt) > registryTTL
	registryMu.RUnlock()
	if stale && convexSiteURL != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = RefreshPackageRegistry(ctx, convexSiteURL)
		}()
	}
	return cached
}

// ResolveInstallStep picks the best install route for `entry` given
// the package managers available on this host. Returns nil if no
// route matches. The caller is then responsible for running the
// chosen Command in a shell. Order matters: the first `Installs`
// entry whose (platform, packageManager) matches wins — so the
// Convex seed should list the preferred OS-native route first.
func ResolveInstallStep(entry PackageRegistryEntry, availablePMs map[string]bool) *PackageRegistryStep {
	for i := range entry.Installs {
		step := &entry.Installs[i]
		if step.Platform != "" && step.Platform != runtime.GOOS {
			continue
		}
		pm := step.PackageManager
		if pm == "" {
			// Empty packageManager == verbatim shell. Accept it only
			// as a last-resort fallback: keep iterating to give a
			// real package manager priority.
			continue
		}
		if availablePMs[pm] {
			return step
		}
	}
	// Second pass — accept verbatim-shell steps even if no manager
	// matched (typical for curl-based one-liners where we only need
	// `curl` on PATH, which almost every machine has).
	for i := range entry.Installs {
		step := &entry.Installs[i]
		if step.Platform != "" && step.Platform != runtime.GOOS {
			continue
		}
		if step.PackageManager == "" || step.PackageManager == "curl" {
			return step
		}
	}
	return nil
}

// AvailablePackageManagersSet is a convenience that turns the
// detectPackageManagers() slice into a map for O(1) ResolveInstallStep
// lookups.
func AvailablePackageManagersSet() map[string]bool {
	out := map[string]bool{}
	for _, pm := range detectPackageManagers() {
		out[pm] = true
	}
	return out
}
