package main

// storage_reclaim.go — "my box is full and I'm on my phone" disk reclaim.
//
// The failure this exists for: you're driving a remote box from the phone,
// a build dies on ENOSPC, and the only thing standing between you and a
// working machine is 40 GB of Xcode DerivedData for a project you shipped
// last month. You should be able to see that, approve it, and free it —
// from the phone, without SSH.
//
// Scope discipline: every target in this catalog is REBUILDABLE — a cache
// that the toolchain regenerates on its own. Worst case for deleting any
// of it is a slow next build. We deliberately do NOT touch node_modules,
// Rust target/, Pods, or anything else whose recovery needs the network or
// a lockfile, because "slow next build" and "your flight-mode laptop can't
// build at all" are different promises. If that tier ever ships it goes
// behind its own confirm.
//
// Safety model, in order:
//
//  1. Reclaim takes IDs, never paths. An ID is a hash of the path, so a
//     caller cannot name a path that a scan did not first discover.
//  2. Every ID is re-resolved against a FRESH scan at reclaim time. The
//     catalog is the allowlist; there is no second way in.
//  3. reclaimPathAllowed is a belt-and-braces check on the resolved path:
//     symlinks resolved, must live under home, never home/root itself,
//     and — the one that matters — never a directory that is or contains
//     a git repo. A cache dir has no .git. Source does.
//
// Paths and project names are LOCAL ONLY. The scan result never goes to
// Convex (privacy contract: no absolute paths, they leak the home-dir
// username). Only aggregate byte counts are allowed to leave the box.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReclaimTarget is one reclaimable cache directory (or command-backed
// cache, e.g. Docker) that the user can approve individually.
type ReclaimTarget struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Label is the human string the phone shows.
	Label string `json:"label"`
	// Path is absolute and LOCAL ONLY — never forward this to Convex.
	Path string `json:"path,omitempty"`
	// Project attributes the target to a project when we can tell
	// (DerivedData encodes it in the dir name; per-repo caches live
	// inside the repo). Empty means a shared/system cache.
	Project   string `json:"project,omitempty"`
	SizeBytes int64  `json:"sizeBytes"`
	// LastUsedMs is the dir mtime — lets the UI say "untouched for 3
	// months", which is what makes an approval decision easy.
	LastUsedMs int64 `json:"lastUsedMs,omitempty"`
	// Action: how this target is reclaimed. "delete" removes Path;
	// the others shell out to a tool that owns its own cache.
	Action string `json:"action"`
	// Rebuild states what regenerating it costs, so the approval is informed.
	Rebuild string `json:"rebuild"`
}

// ReclaimGroup buckets targets by project so the phone can show
// "sfmg — 4.2 GB" rather than twelve anonymous cache paths.
type ReclaimGroup struct {
	// Project is "" for shared/system caches; the UI titles that group.
	Project   string          `json:"project"`
	SizeBytes int64           `json:"sizeBytes"`
	Targets   []ReclaimTarget `json:"targets"`
}

// StorageScan is the full plan: where the disk stands, and what could be freed.
type StorageScan struct {
	Hostname    string           `json:"hostname"`
	OS          string           `json:"os"`
	ScannedAt   string           `json:"scannedAt"`
	Filesystems []DiskSpaceEntry `json:"filesystems"`
	Groups      []ReclaimGroup   `json:"groups"`
	// TotalReclaimableBytes is the sum across every group.
	TotalReclaimableBytes int64 `json:"totalReclaimableBytes"`
	// Partial is true when the scan hit its deadline; the numbers are a
	// floor, not a total. Say so rather than implying we saw everything.
	Partial bool `json:"partial,omitempty"`
}

// ReclaimOutcome is the per-target result of an approved reclaim.
type ReclaimOutcome struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Path       string `json:"path,omitempty"`
	FreedBytes int64  `json:"freedBytes"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// ReclaimResult is what the user sees after approving.
type ReclaimResult struct {
	DryRun     bool             `json:"dryRun"`
	FreedBytes int64            `json:"freedBytes"`
	Freed      string           `json:"freed"`
	Outcomes   []ReclaimOutcome `json:"outcomes"`
	// Free space on the root filesystem before/after, so the user sees the
	// thing they actually care about move.
	RootFreeGBBefore float64 `json:"rootFreeGbBefore"`
	RootFreeGBAfter  float64 `json:"rootFreeGbAfter"`
}

const (
	reclaimActionDelete      = "delete"
	reclaimActionDockerPrune = "docker_prune"
	reclaimActionYaverClean  = "yaver_clean"
)

// scanDeadline bounds a scan. Sizing a 60 GB DerivedData tree is IO-bound;
// we'd rather return a partial plan fast than make the phone hang.
const scanDeadline = 45 * time.Second

var (
	scanCacheMu   sync.Mutex
	scanCache     *StorageScan
	scanCacheAt   time.Time
	scanCacheTTL  = 60 * time.Second
	scanInFlightM sync.Mutex // serialises scans; two concurrent du storms help nobody
)

// scanStorage returns the reclaim plan, reusing a recent scan unless forced.
func scanStorage(force bool) StorageScan {
	scanCacheMu.Lock()
	if !force && scanCache != nil && time.Since(scanCacheAt) < scanCacheTTL {
		s := *scanCache
		scanCacheMu.Unlock()
		return s
	}
	scanCacheMu.Unlock()

	scanInFlightM.Lock()
	defer scanInFlightM.Unlock()

	// Someone may have completed a scan while we waited on the lock.
	scanCacheMu.Lock()
	if !force && scanCache != nil && time.Since(scanCacheAt) < scanCacheTTL {
		s := *scanCache
		scanCacheMu.Unlock()
		return s
	}
	scanCacheMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), scanDeadline)
	defer cancel()

	s := buildStorageScan(ctx)

	scanCacheMu.Lock()
	scanCache = &s
	scanCacheAt = time.Now()
	scanCacheMu.Unlock()
	return s
}

func buildStorageScan(ctx context.Context) StorageScan {
	hostname, _ := os.Hostname()
	scan := StorageScan{
		Hostname:    hostname,
		OS:          runtime.GOOS,
		ScannedAt:   time.Now().UTC().Format(time.RFC3339),
		Filesystems: userVisibleFilesystems(collectDiskSpace()),
	}

	candidates := append(systemCacheCandidates(), projectCacheCandidates(ctx)...)

	// Size every candidate in parallel — each is an independent `du`.
	var wg sync.WaitGroup
	sized := make([]ReclaimTarget, len(candidates))
	sem := make(chan struct{}, 6)
	for i, c := range candidates {
		wg.Add(1)
		go func(i int, t ReclaimTarget) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			sized[i] = sizeTarget(ctx, t)
		}(i, c)
	}
	wg.Wait()

	if ctx.Err() != nil {
		scan.Partial = true
	}

	// Group by project. Anything under ~1 MB is noise — it costs the user
	// a decision and buys back nothing.
	const minInteresting = 1 << 20
	byProject := map[string]*ReclaimGroup{}
	for _, t := range sized {
		if t.ID == "" || t.SizeBytes < minInteresting {
			continue
		}
		g, ok := byProject[t.Project]
		if !ok {
			g = &ReclaimGroup{Project: t.Project}
			byProject[t.Project] = g
		}
		g.Targets = append(g.Targets, t)
		g.SizeBytes += t.SizeBytes
		scan.TotalReclaimableBytes += t.SizeBytes
	}

	for _, g := range byProject {
		sort.Slice(g.Targets, func(i, j int) bool { return g.Targets[i].SizeBytes > g.Targets[j].SizeBytes })
		scan.Groups = append(scan.Groups, *g)
	}
	// Biggest group first; shared caches ("") sink to the bottom so the
	// user's own projects lead.
	sort.Slice(scan.Groups, func(i, j int) bool {
		a, b := scan.Groups[i], scan.Groups[j]
		if (a.Project == "") != (b.Project == "") {
			return a.Project != ""
		}
		return a.SizeBytes > b.SizeBytes
	})
	return scan
}

// systemMountPrefixes are mounts a human never reasons about when their disk
// is full. On macOS, APFS reports ~15 synthetic volumes (/System/Volumes/VM,
// …/Preboot, …/Update, one per simulator runtime) that all echo the SAME
// underlying container's free space — rendering them is 15 rows of noise
// saying the same thing once.
var systemMountPrefixes = []string{
	"/System/Volumes/",
	"/Library/Developer/CoreSimulator/",
	"/private/var/vm",
	"/dev",
	"/proc",
	"/sys",
	"/run",
	"/snap/",
	"/boot/efi",
}

// userVisibleFilesystems collapses the mount list to the volumes a person
// actually acts on: drop the synthetic system mounts, then dedupe volumes
// that report identical capacity AND free space (the APFS-container siblings).
// The mount holding $HOME always survives — it's the one that matters.
func userVisibleFilesystems(all []DiskSpaceEntry) []DiskSpaceEntry {
	home, _ := os.UserHomeDir()

	// Which mount holds home? Longest matching prefix wins.
	homeMount := ""
	for _, fs := range all {
		if home != "" && strings.HasPrefix(home, fs.Mount) && len(fs.Mount) > len(homeMount) {
			homeMount = fs.Mount
		}
	}

	type sig struct {
		total, free float64
	}
	seen := map[sig]bool{}
	var out []DiskSpaceEntry

	for _, fs := range all {
		isHome := fs.Mount == homeMount
		if !isHome {
			skip := false
			for _, p := range systemMountPrefixes {
				if strings.HasPrefix(fs.Mount, p) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		s := sig{fs.TotalGB, fs.FreeGB}
		if seen[s] && !isHome {
			continue // an APFS sibling of a volume we already listed
		}
		seen[s] = true
		out = append(out, fs)
	}
	return out
}

// sizeTarget fills in SizeBytes/LastUsedMs, or zeroes the ID when the target
// doesn't exist on this box (the catalog is cross-platform by design).
func sizeTarget(ctx context.Context, t ReclaimTarget) ReclaimTarget {
	if t.Action == reclaimActionDockerPrune {
		t.SizeBytes = dockerReclaimableBytes(ctx)
		if t.SizeBytes <= 0 {
			t.ID = ""
		}
		return t
	}
	if t.Path == "" {
		t.ID = ""
		return t
	}
	fi, err := os.Stat(t.Path)
	if err != nil || !fi.IsDir() {
		t.ID = "" // absent — drop it silently, this box just doesn't use that tool
		return t
	}
	t.SizeBytes = fastDirSize(ctx, t.Path)
	t.LastUsedMs = fi.ModTime().UnixMilli()
	return t
}

// fastDirSize prefers `du`, which is an order of magnitude faster than a Go
// walk on the trees that matter here (DerivedData routinely holds >100k files).
// Windows has no du, so it walks — slower, but correct.
func fastDirSize(ctx context.Context, path string) int64 {
	if _, err := exec.LookPath("du"); runtime.GOOS != "windows" && err == nil {
		// -s summarise, -k KB units, -x stay on one filesystem (never
		// follow a mount into a network share and hang).
		cmd := exec.CommandContext(ctx, "du", "-sk", "-x", path)
		out, err := cmd.Output()
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) > 0 {
				if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
					return kb * 1024
				}
			}
		}
		// du exits non-zero on unreadable subdirs but still prints a total;
		// if we got nothing usable, fall through to the walk.
		if ctx.Err() != nil {
			return 0
		}
	}
	return dirSize(path)
}

// dockerReclaimableBytes asks Docker what a prune would actually free.
func dockerReclaimableBytes(ctx context.Context) int64 {
	if _, err := exec.LookPath("docker"); err != nil {
		return 0
	}
	cmd := exec.CommandContext(ctx, "docker", "system", "df", "--format", "{{.Reclaimable}}")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		// Lines look like "1.2GB (52%)" or "0B".
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "("); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		total += parseHumanBytes(line)
	}
	return total
}

func parseHumanBytes(s string) int64 {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0
	}
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "TB"):
		mult, s = 1<<40, strings.TrimSuffix(s, "TB")
	case strings.HasSuffix(s, "GB"):
		mult, s = 1<<30, strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "MB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "KB"):
		mult, s = 1<<10, strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(f * float64(mult))
}

// targetID is a stable hash of the path (or kind, for command-backed
// targets). Callers approve IDs, never paths — see the safety model above.
func targetID(kind, path string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + path))
	return hex.EncodeToString(h[:8])
}

func newTarget(kind, label, path, project, rebuild string) ReclaimTarget {
	return ReclaimTarget{
		ID:      targetID(kind, path),
		Kind:    kind,
		Label:   label,
		Path:    path,
		Project: project,
		Action:  reclaimActionDelete,
		Rebuild: rebuild,
	}
}

// --- catalog: shared / system caches --------------------------------------

// systemCacheCandidates proposes the shared/system caches for THIS OS.
//
// Cross-platform posture: macOS and Linux are first-class (that's where the
// boxes are). Windows is supported but second-class — the catalog is smaller
// (no Xcode, obviously) and the scan walks in Go instead of shelling out to
// du/find, because Windows' `find.exe` is a text-search tool with completely
// different semantics and would silently return garbage. Targets that don't
// exist on a given box are dropped by sizeTarget, so listing a path that only
// makes sense elsewhere is harmless — but we still branch, to keep the catalog
// honest about what each OS actually has.
func systemCacheCandidates() []ReclaimTarget {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	j := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }

	// Toolchain caches that live in the same place on every OS.
	out := []ReclaimTarget{
		newTarget("gradle_cache", "Gradle caches", j(".gradle", "caches"), "",
			"Re-downloaded on next Gradle build (needs network)."),
		newTarget("android_build_cache", "Android build cache", j(".android", "build-cache"), "",
			"Regenerated by the next Android build."),
		newTarget("expo_cache", "Expo cache", j(".expo"), "",
			"Regenerated by the next Expo start."),
		newTarget("go_build_cache", "Go build cache", goBuildCacheDir(), "",
			"Regenerated by the next go build (slower first build)."),
	}

	switch runtime.GOOS {
	case "darwin":
		out = append(out, darwinCacheCandidates(j)...)
	case "windows":
		out = append(out, windowsCacheCandidates(j)...)
	default:
		out = append(out, linuxCacheCandidates(j)...)
	}

	// Docker is command-backed, not a path we delete ourselves. Present on
	// all three; sizeTarget drops it when the binary isn't there.
	out = append(out, ReclaimTarget{
		ID:      targetID(reclaimActionDockerPrune, "docker"),
		Kind:    "docker",
		Label:   "Docker dangling images, stopped containers, build cache",
		Action:  reclaimActionDockerPrune,
		Rebuild: "Images re-pulled / re-built on next use (needs network).",
	})

	// Yaver's own state — reuse the existing cleaner rather than reaching
	// into ~/.yaver from here.
	out = append(out, ReclaimTarget{
		ID:      targetID(reclaimActionYaverClean, "yaver"),
		Kind:    "yaver",
		Label:   "Yaver task history, screenshots and logs",
		Action:  reclaimActionYaverClean,
		Rebuild: "Past task records and their screenshots are dropped. Nothing running is affected.",
	})

	return out
}

func darwinCacheCandidates(j func(...string) string) []ReclaimTarget {
	// Xcode DerivedData is special: each SUBDIR is one project, so we can
	// attribute it. That's what turns "12 GB of DerivedData" into "sfmg:
	// 4.2 GB, yaver: 3.1 GB" — the thing that makes the approval obvious.
	out := xcodeDerivedDataTargets(j("Library", "Developer", "Xcode", "DerivedData"))
	return append(out,
		newTarget("xcode_archives", "Xcode archives", j("Library", "Developer", "Xcode", "Archives"), "",
			"Re-archive to upload again. Delete only if your builds are already on TestFlight."),
		newTarget("ios_device_support", "iOS DeviceSupport symbols", j("Library", "Developer", "Xcode", "iOS DeviceSupport"), "",
			"Xcode re-downloads on next device attach."),
		newTarget("xcode_device_logs", "Xcode device logs", j("Library", "Developer", "Xcode", "iOS Device Logs"), "",
			"Crash logs from attached devices; regenerated on attach."),
		newTarget("simulator_caches", "CoreSimulator caches", j("Library", "Developer", "CoreSimulator", "Caches"), "",
			"Regenerated by the simulator."),
		newTarget("cocoapods_cache", "CocoaPods cache", j("Library", "Caches", "CocoaPods"), "",
			"Re-downloaded on next pod install (needs network)."),
		newTarget("npm_cache", "npm cache", j(".npm", "_cacache"), "",
			"Re-downloaded on next install (needs network)."),
		newTarget("yarn_cache", "Yarn cache", j("Library", "Caches", "Yarn"), "",
			"Re-downloaded on next install (needs network)."),
		newTarget("homebrew_cache", "Homebrew downloads", j("Library", "Caches", "Homebrew"), "",
			"Re-downloaded on next brew install (needs network)."),
	)
}

func linuxCacheCandidates(j func(...string) string) []ReclaimTarget {
	return []ReclaimTarget{
		newTarget("npm_cache", "npm cache", j(".npm", "_cacache"), "",
			"Re-downloaded on next install (needs network)."),
		newTarget("yarn_cache", "Yarn cache", j(".cache", "yarn"), "",
			"Re-downloaded on next install (needs network)."),
		newTarget("go_build_cache_xdg", "Go build cache", j(".cache", "go-build"), "",
			"Regenerated by the next go build (slower first build)."),
		newTarget("pip_cache", "pip cache", j(".cache", "pip"), "",
			"Re-downloaded on next pip install (needs network)."),
	}
}

// windowsCacheCandidates covers the toolchains that actually run on Windows.
// No Xcode, no CocoaPods. LOCALAPPDATA is where the JS/Go toolchains put
// their caches; fall back to the conventional path when the var is unset.
func windowsCacheCandidates(j func(...string) string) []ReclaimTarget {
	local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if local == "" {
		local = j("AppData", "Local")
	}
	l := func(parts ...string) string { return filepath.Join(append([]string{local}, parts...)...) }

	return []ReclaimTarget{
		newTarget("npm_cache_win", "npm cache", l("npm-cache"), "",
			"Re-downloaded on next install (needs network)."),
		newTarget("yarn_cache_win", "Yarn cache", l("Yarn", "Cache"), "",
			"Re-downloaded on next install (needs network)."),
		newTarget("pnpm_store_win", "pnpm store", l("pnpm", "store"), "",
			"Re-downloaded on next install (needs network)."),
		newTarget("go_build_cache_win", "Go build cache", l("go-build"), "",
			"Regenerated by the next go build (slower first build)."),
		newTarget("nuget_cache_win", "NuGet packages", j(".nuget", "packages"), "",
			"Re-downloaded on next restore (needs network)."),
		newTarget("pip_cache_win", "pip cache", l("pip", "Cache"), "",
			"Re-downloaded on next pip install (needs network)."),
	}
}

// goBuildCacheDir asks the toolchain rather than guessing, since GOCACHE is
// commonly relocated on CI-ish boxes.
func goBuildCacheDir() string {
	if v := strings.TrimSpace(os.Getenv("GOCACHE")); v != "" {
		return v
	}
	if _, err := exec.LookPath("go"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "env", "GOCACHE").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// xcodeDerivedDataTargets turns each DerivedData subdir into its own target.
// Dir names are "<ProjectName>-<hash>", which is the only place in the whole
// cache landscape where the toolchain hands us project attribution for free.
func xcodeDerivedDataTargets(root string) []ReclaimTarget {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []ReclaimTarget
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "ModuleCache.noindex" {
			// Shared across projects — keep it, but as its own target.
			out = append(out, newTarget("xcode_module_cache", "Xcode module cache",
				filepath.Join(root, name), "", "Regenerated by the next build."))
			continue
		}
		project := name
		if i := strings.LastIndex(name, "-"); i > 0 {
			project = name[:i]
		}
		out = append(out, newTarget("xcode_derived_data",
			"Xcode DerivedData", filepath.Join(root, name), project,
			"Regenerated by the next Xcode build (slower first build)."))
	}
	return out
}

// --- catalog: per-project caches ------------------------------------------

// projectCacheSubdirs are the rebuildable build dirs we look for inside every
// git repo. Deliberately excludes node_modules / Pods / target — see the file
// header on why the network-dependent tier is not in this cut.
var projectCacheSubdirs = []struct {
	rel     string
	kind    string
	label   string
	rebuild string
}{
	{"android/build", "android_build", "Android build output", "Regenerated by the next Android build."},
	{"android/.gradle", "android_gradle", "Android Gradle state", "Regenerated by the next Android build."},
	{"android/app/build", "android_app_build", "Android app build output", "Regenerated by the next Android build."},
	{"ios/build", "ios_build", "iOS build output", "Regenerated by the next iOS build."},
	{".gradle", "project_gradle", "Gradle project state", "Regenerated by the next Gradle build."},
	{"build", "gradle_build", "Build output", "Regenerated by the next build."},
	{".next/cache", "next_cache", "Next.js build cache", "Regenerated by the next next build."},
	{".turbo", "turbo_cache", "Turborepo cache", "Regenerated by the next turbo run."},
	{".parcel-cache", "parcel_cache", "Parcel cache", "Regenerated by the next build."},
	{".dart_tool", "dart_tool", "Flutter/Dart tool cache", "Regenerated by the next flutter build."},
	{".expo", "project_expo", "Expo project cache", "Regenerated by the next expo start."},
}

// projectCacheCandidates walks the user's git repos and proposes each repo's
// rebuildable build dirs, attributed to the repo name.
func projectCacheCandidates(ctx context.Context) []ReclaimTarget {
	var out []ReclaimTarget
	for _, repo := range findGitRepos(ctx) {
		project := filepath.Base(repo)
		for _, sub := range projectCacheSubdirs {
			p := filepath.Join(repo, filepath.FromSlash(sub.rel))
			if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
				continue
			}
			out = append(out, newTarget(sub.kind, sub.label, p, project, sub.rebuild))
		}
	}
	return out
}

// findGitRepos reuses the discovery roots the agent already scans for
// PROJECTS.md, so "what is a project" means the same thing everywhere.
//
// Unix shells out to find (an order of magnitude faster than a Go walk on a
// real home dir). Windows MUST NOT: `find.exe` on Windows is a text-search
// tool, unrelated to POSIX find, and LookPath would happily resolve it and
// hand us garbage. Windows walks in Go instead.
func findGitRepos(ctx context.Context) []string {
	roots := projectDiscoveryRoots()
	if len(roots) == 0 {
		return nil
	}
	if runtime.GOOS == "windows" {
		return walkGitRepos(ctx, roots)
	}
	if _, err := exec.LookPath("find"); err != nil {
		return walkGitRepos(ctx, roots)
	}
	args := append([]string{}, roots...)
	args = append(args,
		"-name", ".git", "-maxdepth", "6", "-type", "d",
		"-not", "-path", "*/node_modules/*",
		"-not", "-path", "*/.cache/*",
		"-not", "-path", "*/Library/*",
		"-not", "-path", "*/Pods/*",
		"-not", "-path", "*/.Trash/*",
	)
	out, _ := exec.CommandContext(ctx, "find", args...).Output() // partial output is fine
	var repos []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		repo := filepath.Dir(line)
		if seen[repo] {
			continue
		}
		seen[repo] = true
		repos = append(repos, repo)
	}
	return repos
}

// repoWalkSkipDirs are directories a git repo is never usefully found under,
// and which are expensive to descend into.
var repoWalkSkipDirs = map[string]bool{
	"node_modules": true, ".cache": true, "Library": true, "Pods": true,
	".Trash": true, "AppData": true, "vendor": true, "target": true,
}

// walkGitRepos is the portable equivalent of the find command above: bounded
// depth, same skip list, stops at a .git (no point descending into a repo to
// look for more repos).
func walkGitRepos(ctx context.Context, roots []string) []string {
	const maxDepth = 6
	var repos []string
	seen := map[string]bool{}

	for _, root := range roots {
		rootDepth := strings.Count(filepath.Clean(root), string(filepath.Separator))

		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable dir — skip it, don't abort the walk
			}
			if ctx.Err() != nil {
				return filepath.SkipAll
			}
			if !d.IsDir() {
				return nil
			}
			name := d.Name()
			if name == ".git" {
				repo := filepath.Dir(path)
				if !seen[repo] {
					seen[repo] = true
					repos = append(repos, repo)
				}
				return filepath.SkipDir
			}
			if repoWalkSkipDirs[name] {
				return filepath.SkipDir
			}
			if strings.Count(path, string(filepath.Separator))-rootDepth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		})
	}
	return repos
}

// --- safety guard ---------------------------------------------------------

// reclaimPathAllowed is the last line of defence before an os.RemoveAll.
// It assumes the path already came out of a scan (so it is in the catalog)
// and asks the independent question: could deleting this destroy work?
func reclaimPathAllowed(path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute")
	}
	clean := filepath.Clean(path)

	// Resolve symlinks: a cache dir that is a symlink to / would otherwise
	// walk straight past every check below.
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	if isFilesystemRoot(resolved) {
		return fmt.Errorf("refusing to delete filesystem root")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return fmt.Errorf("cannot determine home directory")
	}
	homeResolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		homeResolved = home
	}
	if resolved == homeResolved {
		return fmt.Errorf("refusing to delete home directory")
	}
	// Everything we reclaim lives under the user's home. A target that
	// escaped it is a bug or an attack; either way, stop.
	rel, err := filepath.Rel(homeResolved, resolved)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path is outside the home directory")
	}

	// The one that actually matters: a build cache never contains a git
	// repo. Source does. If there's a .git here, we are about to delete
	// someone's work — refuse regardless of what the catalog said.
	if fi, err := os.Stat(filepath.Join(resolved, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
		return fmt.Errorf("refusing to delete a git repository")
	}
	return nil
}

// --- reclaim --------------------------------------------------------------

// performStorageReclaim frees the approved targets. IDs are re-resolved
// against a fresh scan — the catalog is the allowlist, so an ID that no
// longer corresponds to a real reclaimable target simply is not found.
func performStorageReclaim(ids []string, dryRun bool) ReclaimResult {
	res := ReclaimResult{DryRun: dryRun}
	res.RootFreeGBBefore = rootFreeGB()

	scan := scanStorage(true) // force: never delete against a stale catalog
	byID := map[string]ReclaimTarget{}
	for _, g := range scan.Groups {
		for _, t := range g.Targets {
			byID[t.ID] = t
		}
	}

	for _, id := range ids {
		t, ok := byID[id]
		if !ok {
			res.Outcomes = append(res.Outcomes, ReclaimOutcome{
				ID:    id,
				OK:    false,
				Error: "unknown target (rescan and retry)",
			})
			continue
		}
		res.Outcomes = append(res.Outcomes, reclaimOne(t, dryRun))
	}

	for _, o := range res.Outcomes {
		if o.OK {
			res.FreedBytes += o.FreedBytes
		}
	}
	res.Freed = formatBytes(res.FreedBytes)

	if !dryRun && res.FreedBytes > 0 {
		// The catalog we just deleted from is now wrong.
		scanCacheMu.Lock()
		scanCache = nil
		scanCacheMu.Unlock()
	}
	res.RootFreeGBAfter = rootFreeGB()
	return res
}

func reclaimOne(t ReclaimTarget, dryRun bool) ReclaimOutcome {
	out := ReclaimOutcome{ID: t.ID, Label: t.Label, Path: t.Path, FreedBytes: t.SizeBytes}

	switch t.Action {
	case reclaimActionDelete:
		if err := reclaimPathAllowed(t.Path); err != nil {
			out.OK = false
			out.FreedBytes = 0
			out.Error = err.Error()
			return out
		}
		if dryRun {
			out.OK = true
			return out
		}
		if err := os.RemoveAll(t.Path); err != nil {
			out.OK = false
			out.FreedBytes = 0
			out.Error = err.Error()
			return out
		}
		out.OK = true
		return out

	case reclaimActionDockerPrune:
		if dryRun {
			out.OK = true
			return out
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		// Not --volumes: a named volume is data, not a cache, and this
		// catalog only promises to delete things that rebuild themselves.
		cmd := exec.CommandContext(ctx, "docker", "system", "prune", "-af")
		if b, err := cmd.CombinedOutput(); err != nil {
			out.OK = false
			out.FreedBytes = 0
			out.Error = strings.TrimSpace(string(b))
			return out
		}
		out.OK = true
		return out

	case reclaimActionYaverClean:
		r := performClean(0, true, dryRun)
		out.OK = true
		out.FreedBytes = r.BytesFreed
		return out
	}

	out.OK = false
	out.FreedBytes = 0
	out.Error = "unknown action " + t.Action
	return out
}

// --- heartbeat gauge ------------------------------------------------------

// storageSnapshotForHeartbeat is the small numbers-only disk gauge that rides
// every heartbeat to Convex.
//
// It reads CACHED state only — the diskhealth loop's 10-minute snapshot, and
// the reclaim scan's cache if one happens to be warm. It never triggers a
// scan: heartbeats fire every 30s, and kicking off a `du` storm on each one
// would turn a status ping into a disk-thrashing loop.
//
// Numbers only, deliberately: the agent knows exactly which project's caches
// are fat, but paths and project names are forbidden in Convex (they leak the
// home-dir username). The gauge answers "how full", not "of what".
func storageSnapshotForHeartbeat() map[string]interface{} {
	machineHealthMu.RLock()
	fs := append([]DiskSpaceEntry(nil), machineHealth.Filesystems...)
	machineHealthMu.RUnlock()
	if len(fs) == 0 {
		return nil // first scan hasn't landed yet
	}

	home, _ := os.UserHomeDir()
	var pick *DiskSpaceEntry
	bestLen := -1
	for i := range fs {
		if home != "" && strings.HasPrefix(home, fs[i].Mount) && len(fs[i].Mount) > bestLen {
			pick, bestLen = &fs[i], len(fs[i].Mount)
		}
	}
	if pick == nil {
		for i := range fs {
			if fs[i].Mount == "/" || isFilesystemRoot(fs[i].Mount) {
				pick = &fs[i]
				break
			}
		}
	}
	if pick == nil {
		return nil
	}

	out := map[string]interface{}{
		"totalGb": pick.TotalGB,
		"usedGb":  pick.UsedGB,
		"freeGb":  pick.FreeGB,
		"usedPct": pick.UsedPct,
	}

	// Only report reclaimable when a scan is already warm — this is the
	// number that turns "92% full" from alarming into actionable, but it is
	// not worth a scan on the heartbeat path.
	scanCacheMu.Lock()
	if scanCache != nil && time.Since(scanCacheAt) < 30*time.Minute {
		out["reclaimableGb"] = round1(float64(scanCache.TotalReclaimableBytes) / float64(1<<30))
	}
	scanCacheMu.Unlock()

	return out
}

// homeVolumeTotalGB is the capacity of the volume holding $HOME — the disk
// the user means when they say "my disk". Statfs directly rather than reading
// the diskhealth cache, since the hardware profile is built at boot before the
// first health scan has landed.
func homeVolumeTotalGB() float64 {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return 0
	}
	total, _, ok := statfsGB(home)
	if !ok {
		return 0
	}
	return round1(total)
}

// isFilesystemRoot is the portable "is this the top of a volume" check.
// On unix that's "/". On Windows it's "C:\" — comparing against
// filepath.Separator would never match, which is exactly the kind of guard
// that silently doesn't guard.
func isFilesystemRoot(p string) bool {
	if p == string(filepath.Separator) {
		return true
	}
	if vol := filepath.VolumeName(p); vol != "" {
		return p == vol || p == vol+string(filepath.Separator)
	}
	return false
}

// rootFreeGB reports free space on the filesystem that actually holds the
// caches — i.e. the mount containing $HOME, which is the number the user is
// watching. Falls back to the longest-prefix mount so this stays right on
// boxes where /home or C:\Users is its own volume.
func rootFreeGB() float64 {
	home, _ := os.UserHomeDir()
	best := 0.0
	bestLen := -1
	for _, fs := range collectDiskSpace() {
		if home != "" && strings.HasPrefix(home, fs.Mount) && len(fs.Mount) > bestLen {
			best, bestLen = fs.FreeGB, len(fs.Mount)
		}
	}
	if bestLen >= 0 {
		return best
	}
	for _, fs := range collectDiskSpace() {
		if fs.FreeGB > best {
			best = fs.FreeGB
		}
	}
	return best
}
