package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MobileProject represents a discovered mobile project on the dev machine.
type MobileProject struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Framework string `json:"framework"` // "flutter", "expo", "react-native"
	Branch    string `json:"branch,omitempty"`
	Remote    string `json:"remote,omitempty"`
	SizeHuman string `json:"size,omitempty"`
}

// ── Mobile project cache ──────────────────────────────────────────────

var mobileProjectCache struct {
	mu        sync.RWMutex
	projects  []MobileProject
	scannedAt time.Time
	scanning  bool
}

// scanMobileProjects walks the home directory looking for mobile projects.
// Detects: pubspec.yaml (Flutter), package.json with expo/react-native.
// Skips: node_modules, .git, build artifacts, system dirs, caches.
func scanMobileProjects() []MobileProject {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	skipDirs := map[string]bool{
		"node_modules": true, ".git": true, "build": true, "dist": true,
		".cache": true, ".local": true, ".cargo": true, ".rustup": true,
		"Library": true, "Applications": true, "Music": true, "Movies": true,
		"Pictures": true, "Documents": true, "Public": true, "Downloads": true,
		"Desktop": true, ".Trash": true, "Pods": true, ".cocoapods": true,
		".gradle": true, ".android": true, ".pub-cache": true,
		"android": true, "ios": true, ".dart_tool": true,
		".expo": true, ".next": true, "vendor": true,
		"homebrew": true, "Cellar": true, "Caskroom": true,
	}

	// Skip entire directories that are SDKs/tools (not user projects)
	skipPaths := []string{
		"/development/flutter/", // Flutter SDK
		"/flutter/bin/",
		"/.pub-cache/",
		"/sdk/",
	}

	var projects []MobileProject
	seen := map[string]bool{}

	filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}

		// Skip hidden dirs and known non-project dirs
		name := info.Name()
		if info.IsDir() {
			if strings.HasPrefix(name, ".") && name != "." {
				if name != ".config" {
					return filepath.SkipDir
				}
			}
			if skipDirs[name] {
				return filepath.SkipDir
			}
			// Skip SDK/tool paths
			for _, sp := range skipPaths {
				if strings.Contains(path, sp) {
					return filepath.SkipDir
				}
			}
			// Limit depth to ~8 levels from home
			rel, _ := filepath.Rel(home, path)
			if strings.Count(rel, string(os.PathSeparator)) > 7 {
				return filepath.SkipDir
			}
			return nil
		}

		dir := filepath.Dir(path)

		// Already found a project in this dir
		if seen[dir] {
			return nil
		}

		var framework string

		switch name {
		case "pubspec.yaml":
			framework = "flutter"
		case "package.json":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			content := string(data)
			if strings.Contains(content, `"expo"`) {
				framework = "expo"
			} else if strings.Contains(content, `"react-native"`) {
				framework = "react-native"
			}
		default:
			return nil
		}

		if framework == "" {
			return nil
		}

		seen[dir] = true

		// Skip if this is inside another project's subdirectory or an SDK/library
		parentName := filepath.Base(dir)
		if parentName == "example" || parentName == "test" || parentName == "e2e" {
			return nil
		}
		// Skip if it's a library/SDK (not a real app) — check for .git to confirm it's a standalone project
		// or if the parent has a .git (it's a subdir of a larger project, which is fine — like monorepo/mobile/)
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			// No .git — check if parent or grandparent has one (monorepo case)
			parent := filepath.Dir(dir)
			grandparent := filepath.Dir(parent)
			hasParentGit := false
			if _, err := os.Stat(filepath.Join(parent, ".git")); err == nil {
				hasParentGit = true
			}
			if _, err := os.Stat(filepath.Join(grandparent, ".git")); err == nil {
				hasParentGit = true
			}
			if !hasParentGit {
				return nil // orphan package.json/pubspec.yaml without any git context — skip
			}
		}

		proj := MobileProject{
			Name:      filepath.Base(dir),
			Path:      dir,
			Framework: framework,
		}

		// Get git info (fast — just reads local files)
		if branch, err := runGit(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			proj.Branch = branch
		}
		if remote, err := runGit(dir, "config", "--get", "remote.origin.url"); err == nil {
			// Sanitize credentials from URL
			if idx := strings.Index(remote, "@"); idx > 0 && strings.Contains(remote[:idx], "://") {
				remote = remote[:strings.Index(remote, "://")+3] + remote[idx+1:]
			}
			proj.Remote = remote
		}

		// Quick size estimate (du -sh, first line)
		proj.SizeHuman = dirSizeHuman(dir)

		projects = append(projects, proj)
		return nil
	})

	return projects
}

// dirSizeHuman returns a human-readable size of a directory (e.g. "42M").
// Skips heavy dirs (node_modules, .git, build) for speed.
func dirSizeHuman(dir string) string {
	var total int64
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if info.IsDir() {
			n := info.Name()
			if n == "node_modules" || n == ".git" || n == "build" || n == "Pods" || n == ".dart_tool" || n == ".gradle" {
				return filepath.SkipDir
			}
		} else {
			total += info.Size()
		}
		count++
		if count > 5000 {
			return filepath.SkipAll
		}
		return nil
	})

	switch {
	case total < 1024:
		return "<1K"
	case total < 1024*1024:
		return fmt.Sprintf("%dK", total/1024)
	case total < 1024*1024*1024:
		return fmt.Sprintf("%dM", total/(1024*1024))
	default:
		return fmt.Sprintf("%.1fG", float64(total)/(1024*1024*1024))
	}
}

// ── HTTP handler ──────────────────────────────────────────────────────

// handleMobileProjects returns all mobile projects found on the machine.
// GET /projects/mobile — scans home directory for Flutter, Expo, React Native projects.
// Results are cached for 10 minutes; POST forces a re-scan.
func (s *HTTPServer) handleMobileProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// Force re-scan
		go func() {
			mobileProjectCache.mu.Lock()
			mobileProjectCache.scanning = true
			mobileProjectCache.mu.Unlock()

			projects := scanMobileProjects()

			mobileProjectCache.mu.Lock()
			mobileProjectCache.projects = projects
			mobileProjectCache.scannedAt = time.Now()
			mobileProjectCache.scanning = false
			mobileProjectCache.mu.Unlock()

			log.Printf("[mobile-scan] Found %d mobile projects", len(projects))
		}()

		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"message": "scan started",
		})
		return
	}

	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
		return
	}

	// Check cache (10 min TTL)
	mobileProjectCache.mu.RLock()
	projects := mobileProjectCache.projects
	scannedAt := mobileProjectCache.scannedAt
	scanning := mobileProjectCache.scanning
	mobileProjectCache.mu.RUnlock()

	if projects != nil && time.Since(scannedAt) < 10*time.Minute {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"projects":  projects,
			"scannedAt": scannedAt.UTC().Format(time.RFC3339),
			"scanning":  scanning,
		})
		return
	}

	// No cache or stale — scan synchronously (first time), then cache
	if projects == nil {
		mobileProjectCache.mu.Lock()
		mobileProjectCache.scanning = true
		mobileProjectCache.mu.Unlock()

		scanned := scanMobileProjects()

		mobileProjectCache.mu.Lock()
		mobileProjectCache.projects = scanned
		mobileProjectCache.scannedAt = time.Now()
		mobileProjectCache.scanning = false
		mobileProjectCache.mu.Unlock()

		projects = scanned
		scannedAt = time.Now()
		log.Printf("[mobile-scan] Initial scan: found %d mobile projects", len(projects))
	} else {
		// Stale cache — return stale data but trigger background refresh
		go func() {
			mobileProjectCache.mu.Lock()
			mobileProjectCache.scanning = true
			mobileProjectCache.mu.Unlock()

			scanned := scanMobileProjects()

			mobileProjectCache.mu.Lock()
			mobileProjectCache.projects = scanned
			mobileProjectCache.scannedAt = time.Now()
			mobileProjectCache.scanning = false
			mobileProjectCache.mu.Unlock()

			log.Printf("[mobile-scan] Background refresh: found %d mobile projects", len(scanned))
		}()
	}

	if projects == nil {
		projects = []MobileProject{}
	}

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"projects":  projects,
		"scannedAt": scannedAt.UTC().Format(time.RFC3339),
		"scanning":  scanning,
	})
}
