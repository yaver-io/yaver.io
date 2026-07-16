package main

// Disk guard — artifact-aware disk hygiene for local and remote boxes.
//
// Why this exists: on 2026-07-16 a runner loop filled a remote box to 100%
// (75G/75G, zero free). Nothing announced it. What surfaced instead was a set
// of unrelated-looking failures — `npm install -g` dying with nospc, two
// systemd units sitting in `failed`, an agent upgrade that silently refused.
// Yaver could already SEE the disk (`df`, `du`, `find_large_files` are all
// read-only) but had no way to act, so a box that Yaver was actively using
// became unusable while Yaver watched.
//
// The safety contract, in order of importance:
//
//  1. ALLOWLIST ONLY. A class names an exact, narrow path pattern it fully
//     understands. There is deliberately no "delete the big files" scanner:
//     a heuristic GC eventually guesses wrong, and the cost of guessing wrong
//     once (someone's only copy of something) dwarfs the value of every
//     correct guess. If a class cannot explain how the bytes regenerate, it
//     does not belong here.
//  2. Every candidate is re-checked by diskGuardPathAllowed() at delete time,
//     not just at scan time. A class is a suggestion; the guard is the
//     authority. Secrets, keys and git work trees are refused there even if a
//     class mistakenly proposes them.
//  3. dryRun defaults to TRUE. Callers opt in to deletion.
//  4. Sweep (the autorun entry point) reclaims only when the filesystem is at
//     or above a threshold. Below it, sweep reports and does nothing — a disk
//     at 40% has no problem worth taking any risk to solve.
//
// Verbs: diskguard_scan (read-only), diskguard_clear (explicit),
// diskguard_sweep (autorun-safe: threshold-gated).
//
// Remote targeting, cron routines, ops_verbs discovery, panic recovery and the
// scheduler's circuit breaker all come free from registerOpsVerb.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// diskGuardDefaultThreshold is the used-percent at or above which sweep is
// allowed to reclaim. 85 leaves real headroom: by the time a box reports 95%
// the build that would have freed it can no longer run.
const diskGuardDefaultThreshold = 85

// diskGuardDefaultMinAge keeps the guard away from files still being written.
// The opencode artifact appears at process start, so anything younger than
// this may belong to a live invocation.
const diskGuardDefaultMinAge = 60 * time.Minute

// diskGuardKeepAgentVersions is how many agent versions to retain besides the
// one currently symlinked. Keeping a spare makes rollback possible without a
// network round trip.
const diskGuardKeepAgentVersions = 1

// opencodeArtifactRe matches the temp shared object opencode extracts on every
// invocation and never removes: /tmp/.<16 hex>-00000000.so
//
// Proven regenerable on 2026-07-16: counting the files, running
// `opencode --version`, and counting again yields exactly one more, at exactly
// the same byte size. It is opencode's own Bun/Zig runtime, re-extracted per
// run. Deleting them costs nothing; the next invocation writes a fresh one.
var opencodeArtifactRe = regexp.MustCompile(`^\.[0-9a-f]{8,32}-0{8}\.so$`)

// semverDirRe matches a ~/.yaver/bin/<version> directory.
var semverDirRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// diskGuardProtectedNames are refused anywhere, at any depth. These are the
// things whose loss is unrecoverable — an auth key, a signing identity, a
// keystore. None of them are ever large enough to be worth reclaiming, so
// there is no tension between safety and purpose here.
var diskGuardProtectedNames = []string{
	".ssh", ".gnupg", ".aws", ".yaver/vault", "vault.json", "config.json",
	".appstoreconnect", "keys", "keystore.properties", "google-play-service-account.json",
}

// diskGuardProtectedSuffixes are refused by extension — signing material and
// env files that carry secrets.
var diskGuardProtectedSuffixes = []string{
	".p8", ".p12", ".pem", ".key", ".keystore", ".jks", ".mobileprovision",
	".env", ".env.local", ".env.test", ".env.production",
}

type diskGuardCandidate struct {
	Path    string    `json:"path"`
	Bytes   int64     `json:"bytes"`
	ModTime time.Time `json:"modTime"`
}

type diskGuardClassReport struct {
	Class string `json:"class"`
	// Why documents how the bytes regenerate. An agent reading a scan should
	// be able to justify the deletion from this field alone.
	Why        string   `json:"why"`
	Count      int      `json:"count"`
	Bytes      int64    `json:"bytes"`
	Human      string   `json:"human"`
	Sample     []string `json:"sample,omitempty"`
	Applicable bool     `json:"applicable"`
	Skipped    string   `json:"skipped,omitempty"`
}

type diskGuardFS struct {
	Path        string `json:"path"`
	TotalBytes  int64  `json:"totalBytes"`
	UsedBytes   int64  `json:"usedBytes"`
	FreeBytes   int64  `json:"freeBytes"`
	UsedPercent int    `json:"usedPercent"`
	Human       string `json:"human"`
}

type diskGuardScanResult struct {
	Filesystem  diskGuardFS            `json:"filesystem"`
	Classes     []diskGuardClassReport `json:"classes"`
	TotalBytes  int64                  `json:"reclaimableBytes"`
	TotalHuman  string                 `json:"reclaimableHuman"`
	Threshold   int                    `json:"thresholdPercent"`
	OverThresh  bool                   `json:"overThreshold"`
	Recommended string                 `json:"recommendation"`
}

type diskGuardClearResult struct {
	DryRun       bool                   `json:"dryRun"`
	Classes      []diskGuardClassReport `json:"classes"`
	DeletedFiles int                    `json:"deletedFiles"`
	FreedBytes   int64                  `json:"freedBytes"`
	FreedHuman   string                 `json:"freedHuman"`
	Refused      []string               `json:"refused,omitempty"`
	Errors       []string               `json:"errors,omitempty"`
	Before       diskGuardFS            `json:"before"`
	After        diskGuardFS            `json:"after"`
}

type diskGuardSweepResult struct {
	Acted     bool                  `json:"acted"`
	Reason    string                `json:"reason"`
	Threshold int                   `json:"thresholdPercent"`
	Scan      *diskGuardScanResult  `json:"scan,omitempty"`
	Clear     *diskGuardClearResult `json:"clear,omitempty"`
}

type diskGuardScanPayload struct {
	Path      string `json:"path"`
	Threshold int    `json:"thresholdPercent"`
}

type diskGuardClearPayload struct {
	Path      string   `json:"path"`
	Classes   []string `json:"classes"`
	DryRun    *bool    `json:"dryRun"`
	MinAgeMin int      `json:"minAgeMinutes"`
}

type diskGuardSweepPayload struct {
	Path      string `json:"path"`
	Threshold int    `json:"thresholdPercent"`
	DryRun    *bool  `json:"dryRun"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "diskguard_scan",
		Description: "Report reclaimable disk artifacts (read-only, never deletes). " +
			"Returns filesystem usage plus a per-class breakdown, each with a `why` explaining how the bytes regenerate. " +
			"Classes: opencode-tmp-so (opencode leaks a ~4.5MB /tmp .so per invocation), yaver-old-agents (superseded ~/.yaver/bin/<version> trees). " +
			"Pass machine:<deviceId|alias|primary> to scan a remote box.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"path":             map[string]interface{}{"type": "string", "description": "Filesystem to report on (default \"/\")."},
			"thresholdPercent": map[string]interface{}{"type": "integer", "description": "Used-percent considered unhealthy (default 85)."},
		}),
		Handler:    diskGuardScanHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "diskguard_clear",
		Description: "Reclaim known-safe regenerable artifacts. dryRun defaults to TRUE — pass dryRun:false to actually delete. " +
			"Only allowlisted classes are ever touched; secrets, keys and git work trees are refused by a guard that runs at delete time. " +
			"Pass machine:<deviceId|alias|primary> to clear a remote box.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"path":          map[string]interface{}{"type": "string", "description": "Filesystem to report usage against (default \"/\")."},
			"classes":       map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Limit to these classes; empty = all safe classes."},
			"dryRun":        map[string]interface{}{"type": "boolean", "description": "Default true. Set false to delete."},
			"minAgeMinutes": map[string]interface{}{"type": "integer", "description": "Only touch artifacts older than this (default 60) so live invocations are never raced."},
		}),
		Handler:    diskGuardClearHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name: "diskguard_sweep",
		Description: "Autorun entry point: scan, and reclaim ONLY if the filesystem is at or above thresholdPercent (default 85). " +
			"Below the threshold it reports and does nothing. Safe to attach to a routine/cron via routine_create. " +
			"Pass machine:<deviceId|alias|primary> to sweep a remote box.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"path":             map[string]interface{}{"type": "string", "description": "Filesystem to guard (default \"/\")."},
			"thresholdPercent": map[string]interface{}{"type": "integer", "description": "Reclaim at or above this used-percent (default 85)."},
			"dryRun":           map[string]interface{}{"type": "boolean", "description": "Default false for sweep — a threshold breach is the opt-in. Set true to observe only."},
		}),
		Handler:    diskGuardSweepHandler,
		AllowGuest: false,
	})
}

// diskGuardPathAllowed is the authority on whether a path may be deleted.
// Classes propose; this disposes. It runs again at delete time rather than
// trusting the scan, because a scan result can be minutes old and a class can
// have a bug — this is the single place that has to be right.
//
// Returns (false, reason) for anything protected.
func diskGuardPathAllowed(path string) (bool, string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, "unresolvable path"
	}
	// Refuse filesystem root and other absurdly shallow targets outright.
	if abs == "/" || abs == "" || filepath.Dir(abs) == abs {
		return false, "refusing filesystem root"
	}
	if strings.Count(strings.Trim(abs, string(filepath.Separator)), string(filepath.Separator)) < 1 {
		return false, "refusing top-level path"
	}

	lower := strings.ToLower(abs)
	base := strings.ToLower(filepath.Base(abs))

	for _, suf := range diskGuardProtectedSuffixes {
		// Match ".env" and ".env.local" alike, plus signing material.
		if strings.HasSuffix(base, suf) || base == strings.TrimPrefix(suf, ".") {
			return false, "protected file type: " + suf
		}
	}
	for _, name := range diskGuardProtectedNames {
		n := strings.ToLower(name)
		if base == n || strings.Contains(lower, string(filepath.Separator)+n+string(filepath.Separator)) ||
			strings.HasSuffix(lower, string(filepath.Separator)+n) {
			return false, "protected path component: " + name
		}
	}
	// Never delete anything inside a git work tree. Source, uncommitted work
	// and history all live there, and no reclaimable class legitimately does.
	if root, ok := diskGuardInGitWorkTree(abs); ok {
		return false, "inside git work tree: " + root
	}
	return true, ""
}

// diskGuardInGitWorkTree walks up looking for a .git entry. Bounded to 40
// levels so a pathological path can't spin.
func diskGuardInGitWorkTree(path string) (string, bool) {
	dir := path
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
	return "", false
}

// diskGuardStat reads filesystem usage via `df -Pk`, whose POSIX output format
// is identical on macOS and Linux. Statfs would avoid the fork but its struct
// field types differ across those platforms, which would cost a build tag for
// no behavioural gain.
func diskGuardStat(path string) (diskGuardFS, error) {
	if strings.TrimSpace(path) == "" {
		path = "/"
	}
	out, err := runCmd("df", "-Pk", path)
	if err != nil {
		return diskGuardFS{}, fmt.Errorf("df failed: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return diskGuardFS{}, fmt.Errorf("df returned no data for %s", path)
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 5 {
		return diskGuardFS{}, fmt.Errorf("unparseable df output: %q", lines[len(lines)-1])
	}
	total, _ := strconv.ParseInt(f[1], 10, 64)
	used, _ := strconv.ParseInt(f[2], 10, 64)
	free, _ := strconv.ParseInt(f[3], 10, 64)
	pct, _ := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
	fs := diskGuardFS{
		Path:        path,
		TotalBytes:  total * 1024,
		UsedBytes:   used * 1024,
		FreeBytes:   free * 1024,
		UsedPercent: pct,
	}
	fs.Human = fmt.Sprintf("%s used of %s (%d%%), %s free",
		humanBytesDG(fs.UsedBytes), humanBytesDG(fs.TotalBytes), fs.UsedPercent, humanBytesDG(fs.FreeBytes))
	return fs, nil
}

func humanBytesDG(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTP"[exp])
}

// --- classes -------------------------------------------------------------

// diskGuardCollectOpencodeSo finds opencode's leaked runtime .so files.
func diskGuardCollectOpencodeSo(minAge time.Duration) ([]diskGuardCandidate, error) {
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().Add(-minAge)
	var out []diskGuardCandidate
	for _, e := range entries {
		if e.IsDir() || !opencodeArtifactRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		out = append(out, diskGuardCandidate{
			Path:    filepath.Join("/tmp", e.Name()),
			Bytes:   info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}

// diskGuardCollectOldAgents finds superseded ~/.yaver/bin/<version> trees,
// keeping whatever `current` resolves to plus the newest
// diskGuardKeepAgentVersions others (rollback without a network round trip).
func diskGuardCollectOldAgents() ([]diskGuardCandidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	binDir := filepath.Join(home, ".yaver", "bin")
	entries, err := os.ReadDir(binDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// Resolve `current` so the live version is never a candidate.
	keep := map[string]bool{}
	if resolved, err := filepath.EvalSymlinks(filepath.Join(binDir, "current")); err == nil {
		keep[filepath.Base(resolved)] = true
	}

	var versions []string
	for _, e := range entries {
		if e.IsDir() && semverDirRe.MatchString(e.Name()) {
			versions = append(versions, e.Name())
		}
	}
	sort.Slice(versions, func(i, j int) bool { return compareSemverDG(versions[i], versions[j]) > 0 })
	for i := 0; i < len(versions) && i < diskGuardKeepAgentVersions; i++ {
		keep[versions[i]] = true
	}

	var out []diskGuardCandidate
	for _, v := range versions {
		if keep[v] {
			continue
		}
		p := filepath.Join(binDir, v)
		size, err := dirSizeDG(p)
		if err != nil {
			continue
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		out = append(out, diskGuardCandidate{Path: p, Bytes: size, ModTime: info.ModTime()})
	}
	return out, nil
}

func compareSemverDG(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		ai, _ := strconv.Atoi(as[i])
		bi, _ := strconv.Atoi(bs[i])
		if ai != bi {
			if ai > bi {
				return 1
			}
			return -1
		}
	}
	return 0
}

func dirSizeDG(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry — skip, don't fail the whole scan
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

type diskGuardClass struct {
	Name    string
	Why     string
	Collect func(minAge time.Duration) ([]diskGuardCandidate, error)
	// KeepNewest retains the N most recent artifacts (FIFO: oldest evicted
	// first). A leaking producer may be mid-write or holding its most recent
	// artifact open, and the newest copies are the ones most likely to be
	// live — so age, not size, decides who goes.
	KeepNewest int
	// AlwaysEnforce marks a class as pure garbage whose FIFO cap is applied on
	// every sweep, not only past the disk threshold. This is what stops a slow
	// leak from ever reaching the threshold: waiting for 85% to dump 20GB is
	// crisis management, while holding a bounded ring is hygiene. Only set it
	// where the bytes are provably regenerable and provably unwanted.
	AlwaysEnforce bool
}

func diskGuardClasses() []diskGuardClass {
	return []diskGuardClass{
		{
			Name: "opencode-tmp-so",
			Why:  "opencode (a Bun/Zig single-file binary) extracts its ~4.5MB runtime to /tmp on every invocation and never removes it. Verified regenerable: the next invocation writes a fresh copy. Left alone this is ~2GB/day forever.",
			Collect: func(minAge time.Duration) ([]diskGuardCandidate, error) {
				return diskGuardCollectOpencodeSo(minAge)
			},
			KeepNewest:    3,
			AlwaysEnforce: true,
		},
		{
			Name: "yaver-old-agents",
			Why:  "superseded ~/.yaver/bin/<version> trees. The version `current` points to, plus the newest spare, are always kept; any other is re-downloadable by the npm launcher.",
			Collect: func(time.Duration) ([]diskGuardCandidate, error) {
				return diskGuardCollectOldAgents()
			},
			// Collect already applies its own keep policy (current + newest
			// spare), which is version-aware rather than mtime-aware — an
			// mtime FIFO here would fight it.
			KeepNewest: 0,
		},
	}
}

// diskGuardApplyFIFO drops the KeepNewest most recent candidates from the
// reclaim set, returning the rest oldest-first so deletion order is
// deterministic and the survivors are always the freshest.
func diskGuardApplyFIFO(cands []diskGuardCandidate, keepNewest int) []diskGuardCandidate {
	sort.Slice(cands, func(i, j int) bool { return cands[i].ModTime.After(cands[j].ModTime) })
	if keepNewest > 0 {
		if len(cands) <= keepNewest {
			return nil
		}
		cands = cands[keepNewest:]
	}
	// Oldest-first: if a delete pass is interrupted, the oldest bytes are
	// already gone rather than a random scatter.
	for i, j := 0, len(cands)-1; i < j; i, j = i+1, j-1 {
		cands[i], cands[j] = cands[j], cands[i]
	}
	return cands
}

// diskGuardCollect runs the requested classes and applies the safety guard,
// returning per-class reports plus the surviving candidates.
func diskGuardCollect(only []string, minAge time.Duration, enforceOnly bool) ([]diskGuardClassReport, map[string][]diskGuardCandidate, []string) {
	wanted := map[string]bool{}
	for _, c := range only {
		wanted[strings.TrimSpace(c)] = true
	}
	var reports []diskGuardClassReport
	kept := map[string][]diskGuardCandidate{}
	var refused []string

	for _, class := range diskGuardClasses() {
		if len(wanted) > 0 && !wanted[class.Name] {
			continue
		}
		// enforceOnly: a below-threshold sweep touches only the classes whose
		// FIFO cap is safe to hold continuously.
		if enforceOnly && !class.AlwaysEnforce {
			continue
		}
		rep := diskGuardClassReport{Class: class.Name, Why: class.Why, Applicable: true}
		cands, err := class.Collect(minAge)
		if err != nil {
			rep.Applicable = false
			rep.Skipped = err.Error()
			reports = append(reports, rep)
			continue
		}
		cands = diskGuardApplyFIFO(cands, class.KeepNewest)
		for _, cand := range cands {
			if ok, reason := diskGuardPathAllowed(cand.Path); !ok {
				refused = append(refused, cand.Path+" ("+reason+")")
				continue
			}
			kept[class.Name] = append(kept[class.Name], cand)
			rep.Count++
			rep.Bytes += cand.Bytes
			if len(rep.Sample) < 3 {
				rep.Sample = append(rep.Sample, cand.Path)
			}
		}
		rep.Human = humanBytesDG(rep.Bytes)
		reports = append(reports, rep)
	}
	return reports, kept, refused
}

// --- handlers ------------------------------------------------------------

func diskGuardScanHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p diskGuardScanPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	threshold := p.Threshold
	if threshold <= 0 || threshold > 100 {
		threshold = diskGuardDefaultThreshold
	}
	fs, err := diskGuardStat(p.Path)
	if err != nil {
		return OpsResult{OK: false, Code: "internal", Error: err.Error()}
	}
	reports, _, _ := diskGuardCollect(nil, diskGuardDefaultMinAge, false)

	var total int64
	for _, r := range reports {
		total += r.Bytes
	}
	res := diskGuardScanResult{
		Filesystem: fs,
		Classes:    reports,
		TotalBytes: total,
		TotalHuman: humanBytesDG(total),
		Threshold:  threshold,
		OverThresh: fs.UsedPercent >= threshold,
	}
	switch {
	case res.OverThresh && total > 0:
		res.Recommended = fmt.Sprintf("disk at %d%% (>= %d%%): diskguard_clear would reclaim %s", fs.UsedPercent, threshold, res.TotalHuman)
	case res.OverThresh:
		res.Recommended = fmt.Sprintf("disk at %d%% (>= %d%%) but no known-safe artifacts found — investigate manually (find_large_files)", fs.UsedPercent, threshold)
	default:
		res.Recommended = fmt.Sprintf("disk at %d%% (< %d%%): healthy, no action", fs.UsedPercent, threshold)
	}
	return OpsResult{OK: true, Initial: res}
}

func diskGuardClearHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p diskGuardClearPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	dryRun := true // deletion is always opt-in
	if p.DryRun != nil {
		dryRun = *p.DryRun
	}
	minAge := diskGuardDefaultMinAge
	if p.MinAgeMin > 0 {
		minAge = time.Duration(p.MinAgeMin) * time.Minute
	}
	res := diskGuardRunClear(p.Path, p.Classes, dryRun, minAge, false)
	return OpsResult{OK: true, Initial: res}
}

func diskGuardRunClear(path string, classes []string, dryRun bool, minAge time.Duration, enforceOnly bool) diskGuardClearResult {
	before, _ := diskGuardStat(path)
	reports, kept, refused := diskGuardCollect(classes, minAge, enforceOnly)

	out := diskGuardClearResult{
		DryRun:  dryRun,
		Classes: reports,
		Refused: refused,
		Before:  before,
	}
	for _, cands := range kept {
		for _, cand := range cands {
			// Re-check at delete time: the scan may be stale and a class may
			// be wrong. The guard is the authority, not the class.
			if ok, reason := diskGuardPathAllowed(cand.Path); !ok {
				out.Refused = append(out.Refused, cand.Path+" ("+reason+")")
				continue
			}
			if dryRun {
				out.DeletedFiles++
				out.FreedBytes += cand.Bytes
				continue
			}
			if err := os.RemoveAll(cand.Path); err != nil {
				out.Errors = append(out.Errors, cand.Path+": "+err.Error())
				continue
			}
			out.DeletedFiles++
			out.FreedBytes += cand.Bytes
		}
	}
	out.FreedHuman = humanBytesDG(out.FreedBytes)
	after, _ := diskGuardStat(path)
	out.After = after
	return out
}

func diskGuardSweepHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p diskGuardSweepPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	threshold := p.Threshold
	if threshold <= 0 || threshold > 100 {
		threshold = diskGuardDefaultThreshold
	}
	// Sweep deletes by default: crossing the threshold IS the opt-in, and a
	// sweep that never acts is just a scan on a timer.
	dryRun := false
	if p.DryRun != nil {
		dryRun = *p.DryRun
	}

	fs, err := diskGuardStat(p.Path)
	if err != nil {
		return OpsResult{OK: false, Code: "internal", Error: err.Error()}
	}
	if fs.UsedPercent < threshold {
		// Below threshold we still hold the FIFO ring on AlwaysEnforce classes.
		// A pure "report until 85%" guard lets a 2GB/day leak run for five
		// weeks and then asks us to reclaim 20GB in a panic; capping the ring
		// every sweep means the threshold is never reached in the first place.
		// Nothing else is touched.
		clear := diskGuardRunClear(p.Path, nil, dryRun, diskGuardDefaultMinAge, true)
		reason := fmt.Sprintf("disk at %d%% is below the %d%% threshold — held the FIFO cap on always-enforce classes only (%s across %d artifacts); nothing else touched",
			fs.UsedPercent, threshold, clear.FreedHuman, clear.DeletedFiles)
		if clear.DeletedFiles == 0 {
			reason = fmt.Sprintf("disk at %d%% is below the %d%% threshold and the FIFO cap is already satisfied — no action", fs.UsedPercent, threshold)
		}
		return OpsResult{OK: true, Initial: diskGuardSweepResult{
			Acted:     !dryRun && clear.DeletedFiles > 0,
			Threshold: threshold,
			Reason:    reason,
			Clear:     &clear,
		}}
	}

	clear := diskGuardRunClear(p.Path, nil, dryRun, diskGuardDefaultMinAge, false)
	return OpsResult{OK: true, Initial: diskGuardSweepResult{
		Acted:     !dryRun,
		Threshold: threshold,
		Reason: fmt.Sprintf("disk at %d%% (>= %d%%) — reclaimed %s across %d artifacts",
			fs.UsedPercent, threshold, clear.FreedHuman, clear.DeletedFiles),
		Clear: &clear,
	}}
}
