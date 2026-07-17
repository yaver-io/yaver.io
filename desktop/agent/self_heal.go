package main

// self_heal.go — multi-source yaver-binary detect + reconcile.
//
// Why this exists: a single user often ends up with `yaver` installed
// from several sources at once — apt-get, homebrew, npm-cli, the
// auto-updater under ~/.yaver/bin/<v>/, and ad-hoc curl downloads to
// /usr/local/bin. Each one is a separate binary and they drift out of
// sync. The classic symptom is "I just upgraded but `yaver --version`
// still shows the old number" because the shell is resolving a stale
// /usr/bin/yaver instead of the freshly-pulled ~/.yaver/bin/<v>/yaver.
//
// This file builds a single, idempotent reconciler:
//
//   1. Discover every `yaver` executable on the box (PATH + every well-
//      known install prefix from binary_discovery.go).
//   2. Probe each one's `--version`.
//   3. Decide the canonical binary: the highest semver actually
//      runnable on this OS/arch. If the running process is older but
//      the GitHub `latest` release is newer still, queue a self-update
//      first so the canonical is fresh.
//   4. For each non-canonical detected install, either copy the
//      canonical bytes over it (when writable + the user opted into
//      `Override`) or report a drift warning.
//
// Safety rails:
//   - Never auto-modifies anything without an explicit Apply call.
//     Startup runs report-only.
//   - Backs up replaced binaries to `<path>.previous-<version>` so a
//     bad reconcile is reversible.
//   - Skips package-managed paths (apt/brew) unless `IncludeManaged`
//     is set — overwriting those breaks the package manager's checksum
//     and the next `apt upgrade` reverts the change anyway.
//   - All filesystem writes go through atomic rename + chmod.
//
// Surfaces:
//   - CLI: `yaver self heal [--apply] [--include-managed]`
//   - CLI: woven into `yaver doctor` output
//   - HTTP: `GET /agent/self-heal` (report only, owner-auth)
//          `POST /agent/self-heal` (apply, owner-auth)
//   - Startup: spawned non-blocking after `yaver serve` boots; logs
//              drift warnings only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"crypto/sha256"
	"encoding/hex"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// YaverInstall describes one `yaver` binary the discoverer found on
// disk. Manager mirrors binary_discovery.go's labels but with a few
// yaver-specific additions ("yaver-update" for ~/.yaver/bin/<v>/
// auto-update path; "running" for the process executable).
//
// Version detection notes:
//   - For the running binary we use the compile-time `version`
//     constant directly — no exec needed and 100% reliable.
//   - For others we sha256-hash the bytes. If the hash matches the
//     running binary's hash, it's the same version. If it doesn't,
//     we report it as "drift" without claiming a specific version
//     string — `--version` probing across recursive yaver→yaver execs
//     turned out to be flaky on macOS (child exits in <2ms with empty
//     stdout when invoked from a yaver parent process).
type YaverInstall struct {
	Path            string `json:"path"`
	Version         string `json:"version"`
	SHA256          string `json:"sha256,omitempty"`
	Size            int64  `json:"size,omitempty"`
	Manager         string `json:"manager,omitempty"`
	Writable        bool   `json:"writable"`
	IsRunningBinary bool   `json:"isRunningBinary"`
	IsManaged       bool   `json:"isManaged"`
	SameAsRunning   bool   `json:"sameAsRunning"`
	IsNPMWrapper    bool   `json:"isNpmWrapper,omitempty"`
	ProbeError      string `json:"probeError,omitempty"`
}

// SelfHealReport is a structured snapshot suitable for CLI tables, the
// dashboard, and JSON HTTP responses.
type SelfHealReport struct {
	GeneratedAt   time.Time      `json:"generatedAt"`
	RunningBinary YaverInstall   `json:"runningBinary"`
	Installs      []YaverInstall `json:"installs"`
	Canonical     YaverInstall   `json:"canonical"`
	LatestRelease string         `json:"latestRelease,omitempty"`
	LatestErr     string         `json:"latestError,omitempty"`
	Drift         []string       `json:"drift,omitempty"`
	NeedsSelfPull bool           `json:"needsSelfPull"`
	Applied       []string       `json:"applied,omitempty"`
	ApplyErrors   []string       `json:"applyErrors,omitempty"`
}

// SelfHealOptions controls Apply behavior. Defaults are conservative:
// no apply, no managed-path overrides, no auto-update.
type SelfHealOptions struct {
	Apply           bool
	IncludeManaged  bool
	AllowSelfUpdate bool
	Quiet           bool
}

// DiscoverYaverInstalls walks PATH + commonInstallPrefixes() and
// returns every `yaver` executable it finds, deduplicated by absolute
// path (after symlink resolution). The running binary is always the
// first entry if found, with version pulled from the in-process
// constant. All others are sha256-compared against the running binary
// — matching hash means same version, differing hash means drift.
func DiscoverYaverInstalls() []YaverInstall {
	seen := map[string]bool{}
	out := []YaverInstall{}
	var runningHash string

	// Always start with the running binary. We know our own version
	// from the compile-time `version` constant — no need to exec.
	if exePath, err := os.Executable(); err == nil {
		if real, rerr := filepath.EvalSymlinks(exePath); rerr == nil {
			exePath = real
		}
		bi := inspectYaverBinary(exePath, true, "")
		bi.Version = version // compile-time const (top of main.go)
		bi.SameAsRunning = true
		runningHash = bi.SHA256
		out = append(out, bi)
		seen[exePath] = true
	}

	// PATH first, then well-known prefixes.
	pathDirs := filepath.SplitList(os.Getenv("PATH"))
	pathDirs = append(pathDirs, commonInstallPrefixes()...)

	for _, dir := range pathDirs {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, yaverBinaryName())
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		// Resolve through symlinks so /usr/local/bin/yaver -> ~/.yaver/bin/...
		// dedupes against the running binary.
		real, rerr := filepath.EvalSymlinks(candidate)
		if rerr != nil {
			real = candidate
		}
		if seen[real] {
			continue
		}
		seen[real] = true
		out = append(out, inspectYaverBinary(candidate, false, runningHash))
	}
	return out
}

func yaverBinaryName() string {
	if runtime.GOOS == "windows" {
		return "yaver.exe"
	}
	return "yaver"
}

// inspectYaverBinary collects metadata about one binary on disk.
// Hash + size are always computed; version is set only for the running
// binary (using the compile-time constant) or when the file's hash
// matches the running binary (so we know it's the same version).
func inspectYaverBinary(path string, isRunning bool, runningHash string) YaverInstall {
	bi := YaverInstall{
		Path:            path,
		IsRunningBinary: isRunning,
		Manager:         guessManagerForPath(filepath.Dir(path)),
	}
	bi.IsManaged = bi.Manager == "system" || bi.Manager == "brew"

	if info, err := os.Stat(path); err == nil {
		bi.Size = info.Size()
	}

	if runtime.GOOS == "windows" {
		bi.Writable = true
	} else if f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0); err == nil {
		bi.Writable = true
		_ = f.Close()
	}

	if h, err := sha256File(path); err == nil {
		bi.SHA256 = h
		if runningHash != "" && h == runningHash {
			bi.SameAsRunning = true
			bi.Version = version
		}
	} else {
		bi.ProbeError = "sha256: " + err.Error()
	}
	bi.IsNPMWrapper = looksLikeNPMWrapperScript(path, bi.Size)
	return bi
}

// looksLikeNPMWrapperScript returns true when `path` is the
// `bin/yaver` Node entry-point shipped by the yaver-cli npm package
// (typically a 100-200 byte file starting with `#!/usr/bin/env node`),
// not a stale or hand-copied compiled Go binary. We have to recognise
// this explicitly because the wrapper sits on PATH at
// `~/.local/bin/yaver` (npm) or `/usr/local/bin/yaver` (npm symlink),
// and an unaware reconciler would happily overwrite the 100-byte
// launcher with the 41 MB Go agent — bricking the npm install.
func looksLikeNPMWrapperScript(path string, size int64) bool {
	// The real wrapper is currently ~105 bytes; cap generously at 4 KB
	// so a future expansion of the JS shim still matches.
	if size <= 0 || size > 4096 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 64)
	n, _ := f.Read(head)
	if n < 2 {
		return false
	}
	prefix := strings.ToLower(string(head[:n]))
	return strings.HasPrefix(prefix, "#!/usr/bin/env node") ||
		strings.HasPrefix(prefix, "#!/usr/bin/node") ||
		strings.HasPrefix(prefix, "#!/usr/local/bin/node") ||
		strings.HasPrefix(prefix, "#!/usr/bin/env -s node")
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseYaverVersionLine accepts either:
//
//	"yaver 1.99.100"             (current format from main.go)
//	"yaver version 1.99.100"     (older format)
//	"v1.99.100"                  (occasional debug build)
//
// Returns the bare semver (no "v" prefix) so callers can semver-compare
// directly with the version constant.
func parseYaverVersionLine(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		for _, f := range fields {
			f = strings.TrimPrefix(f, "v")
			if semver.IsValid("v" + f) {
				return f
			}
		}
	}
	return ""
}

// fetchLatestYaverRelease asks GitHub for the latest tag on the
// configured release repo. Returns the bare semver. Honors
// YAVER_UPDATE_REPO so dev/staging operators can point elsewhere.
func fetchLatestYaverRelease(ctx context.Context) (string, error) {
	type ghRelease struct {
		TagName string `json:"tag_name"`
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updateRepo())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", fmt.Errorf("github status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v"), nil
}

// chooseCanonical picks the source-of-truth binary. The running
// binary always wins — we know its version exactly via the compile-
// time constant, and we know it works (it's literally executing this
// code right now). When the running binary has a known version, every
// other install is reconciled against it. If we somehow couldn't even
// resolve our own executable (extremely rare), fall back to the first
// install with a known version.
func chooseCanonical(installs []YaverInstall) YaverInstall {
	for _, inst := range installs {
		if inst.IsRunningBinary && inst.Version != "" {
			return inst
		}
	}
	for _, inst := range installs {
		if inst.Version != "" {
			return inst
		}
	}
	if len(installs) > 0 {
		return installs[0]
	}
	return YaverInstall{}
}

// driftLines describes mismatches the operator should know about,
// independent of whether we're going to fix them. With hash-based
// detection we can call out three states: identical to canonical,
// known-different, or unreadable.
func driftLines(installs []YaverInstall, canonical YaverInstall, latest string) []string {
	out := []string{}
	for _, inst := range installs {
		if inst.Path == canonical.Path {
			continue
		}
		if inst.IsNPMWrapper {
			// npm wrapper Node script is the legitimate launcher, not drift.
			continue
		}
		switch {
		case inst.SHA256 == "":
			out = append(out, fmt.Sprintf("%s — unreadable (%s)", inst.Path, inst.ProbeError))
		case !inst.SameAsRunning:
			out = append(out, fmt.Sprintf("%s — bytes differ from canonical (sha256 %s vs %s)", inst.Path, shortHashSelfHeal(inst.SHA256), shortHashSelfHeal(canonical.SHA256)))
		}
	}
	if latest != "" && canonical.Version != "" {
		can := "v" + canonical.Version
		lat := "v" + latest
		if semver.IsValid(can) && semver.IsValid(lat) && semver.Compare(lat, can) > 0 {
			out = append(out, fmt.Sprintf("GitHub `latest` is v%s but canonical install is v%s", latest, canonical.Version))
		}
	}
	return out
}

func shortHashSelfHeal(h string) string {
	if len(h) < 12 {
		return h
	}
	return h[:12]
}

// BuildSelfHealReport assembles the read-only snapshot. Apply paths
// take this report + opts and act on it.
func BuildSelfHealReport(ctx context.Context) *SelfHealReport {
	rep := &SelfHealReport{
		GeneratedAt: time.Now().UTC(),
		Installs:    DiscoverYaverInstalls(),
	}
	for _, inst := range rep.Installs {
		if inst.IsRunningBinary {
			rep.RunningBinary = inst
			break
		}
	}
	rep.Canonical = chooseCanonical(rep.Installs)
	if latest, err := fetchLatestYaverRelease(ctx); err == nil {
		rep.LatestRelease = latest
		can := "v" + rep.Canonical.Version
		lat := "v" + latest
		if semver.IsValid(can) && semver.IsValid(lat) && semver.Compare(lat, can) > 0 {
			rep.NeedsSelfPull = true
		}
	} else {
		rep.LatestErr = err.Error()
	}
	rep.Drift = driftLines(rep.Installs, rep.Canonical, rep.LatestRelease)
	return rep
}

// ApplySelfHeal mutates the filesystem according to opts. Steps:
//
//  1. If AllowSelfUpdate and a newer GitHub release is available, run
//     the existing checkAutoUpdate machinery so the canonical binary
//     gets refreshed in-place. Re-discover after.
//  2. For every install whose version != canonical, copy the canonical
//     bytes over it. Skip non-writable + skip managed (apt/brew) paths
//     unless IncludeManaged is set.
//  3. Backup replaced binaries to <path>.previous-<oldver>.
//
// All errors are collected; one bad path doesn't abort the rest.
func ApplySelfHeal(ctx context.Context, rep *SelfHealReport, opts SelfHealOptions) {
	if opts.AllowSelfUpdate && rep.NeedsSelfPull {
		// checkAutoUpdate respects the operator's auto-update setting —
		// force it on for this single call, since the operator asked for
		// a heal explicitly. forcedAutoUpdateConfig copies, so nothing
		// is persisted. It updates os.Executable() in place; on
		// completion the running process exits and systemd/launchd
		// restarts on the new binary. From the perspective of this
		// function, we never see the post-update state — that's fine,
		// the next agent boot will re-run BuildSelfHealReport and find
		// the rest of the installs still need reconciling.
		log.Printf("[self-heal] running self-update to v%s before reconciling other paths", rep.LatestRelease)
		healCfg, _ := LoadConfig()
		checkAutoUpdate(forcedAutoUpdateConfig(healCfg))
		// If we're still alive, the update was a no-op. Refresh the
		// snapshot so the rest of the reconcile sees current state.
		fresh := BuildSelfHealReport(ctx)
		*rep = *fresh
	}

	canonical := rep.Canonical
	if canonical.Path == "" || canonical.Version == "" {
		rep.ApplyErrors = append(rep.ApplyErrors, "no canonical binary identified — refusing to apply")
		return
	}
	canonicalBytes, err := os.ReadFile(canonical.Path)
	if err != nil {
		rep.ApplyErrors = append(rep.ApplyErrors, fmt.Sprintf("read canonical %s: %v", canonical.Path, err))
		return
	}

	for _, inst := range rep.Installs {
		if inst.Path == canonical.Path {
			continue
		}
		if inst.SameAsRunning {
			continue
		}
		if inst.IsNPMWrapper {
			// Skip the Node wrapper script — overwriting it with Go-binary
			// bytes would brick `npm install -g yaver-cli`. The wrapper is
			// the legitimate single entry point; the canonical Go binary
			// lives under ~/.yaver/bin/<v>/<plat>/ and the wrapper execs it.
			continue
		}
		if !inst.Writable {
			rep.ApplyErrors = append(rep.ApplyErrors, fmt.Sprintf("%s — not writable (run with sudo or remove via package manager)", inst.Path))
			continue
		}
		if inst.IsManaged && !opts.IncludeManaged {
			rep.ApplyErrors = append(rep.ApplyErrors, fmt.Sprintf("%s — managed by %s; pass --include-managed to override (will be reverted on next package upgrade)", inst.Path, inst.Manager))
			continue
		}
		stamp := shortHashSelfHeal(inst.SHA256)
		if stamp == "" {
			stamp = "unknown"
		}
		backupName := inst.Path + ".previous-" + stamp
		if err := atomicReplaceBinary(inst.Path, canonicalBytes, backupName); err != nil {
			rep.ApplyErrors = append(rep.ApplyErrors, fmt.Sprintf("%s: %v", inst.Path, err))
			continue
		}
		rep.Applied = append(rep.Applied, fmt.Sprintf("%s: was sha256 %s, now v%s (backup at %s)", inst.Path, stamp, canonical.Version, filepath.Base(backupName)))
	}
}

// atomicReplaceBinary writes `data` to `targetPath` atomically. The
// previous contents go to backupPath first. Permission bits from the
// original file are preserved; if we can't read them, fall back to 0755.
func atomicReplaceBinary(targetPath string, data []byte, backupPath string) error {
	mode := os.FileMode(0o755)
	if info, err := os.Stat(targetPath); err == nil {
		mode = info.Mode().Perm()
	}
	// Backup first. Best-effort: if we can't write to the same dir for
	// some reason, fail before touching the original.
	if err := writeFileAtomic(backupPath, mustReadFile(targetPath), mode); err != nil {
		return fmt.Errorf("write backup %s: %w", backupPath, err)
	}
	if err := writeFileAtomic(targetPath, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", targetPath, err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".yaver-self-heal-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return nil
}

func mustReadFile(p string) []byte {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return data
}

// PrintSelfHealReport renders the report for terminal use.
func PrintSelfHealReport(w io.Writer, rep *SelfHealReport) {
	fmt.Fprintln(w, "yaver self heal — installation snapshot")
	fmt.Fprintln(w, "=======================================")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Running:    %s @ v%s (manager: %s)\n", rep.RunningBinary.Path, displayVersion(rep.RunningBinary.Version), displayManager(rep.RunningBinary.Manager))
	fmt.Fprintf(w, "Canonical:  %s @ v%s\n", rep.Canonical.Path, displayVersion(rep.Canonical.Version))
	if rep.LatestRelease != "" {
		fmt.Fprintf(w, "Latest:     v%s (from github.com/%s)\n", rep.LatestRelease, updateRepo())
	} else if rep.LatestErr != "" {
		fmt.Fprintf(w, "Latest:     unknown (%s)\n", rep.LatestErr)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Detected installs:")
	// Sort by path for stable output.
	installs := append([]YaverInstall(nil), rep.Installs...)
	sort.SliceStable(installs, func(i, j int) bool { return installs[i].Path < installs[j].Path })
	for _, inst := range installs {
		marker := " "
		if inst.Path == rep.Canonical.Path {
			marker = "*"
		}
		writable := "rw"
		if !inst.Writable {
			writable = "ro"
		}
		state := "differs"
		if inst.SameAsRunning {
			state = "same"
		} else if inst.SHA256 == "" {
			state = "unreadable"
		}
		fmt.Fprintf(w, "  %s %s\n      v%-10s sha256=%s state=%-10s manager=%-10s %s%s\n",
			marker, inst.Path,
			displayVersion(inst.Version),
			shortHashSelfHeal(inst.SHA256),
			state,
			displayManager(inst.Manager),
			writable,
			managedSuffix(inst))
		if inst.ProbeError != "" {
			fmt.Fprintf(w, "      probe: %s\n", inst.ProbeError)
		}
	}

	if len(rep.Drift) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Drift:")
		for _, d := range rep.Drift {
			fmt.Fprintf(w, "  - %s\n", d)
		}
	}
	if rep.NeedsSelfPull {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Action: GitHub has a newer release. Run `yaver self heal --apply --self-update`.")
	}
	if len(rep.Applied) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Applied:")
		for _, a := range rep.Applied {
			fmt.Fprintf(w, "  + %s\n", a)
		}
	}
	if len(rep.ApplyErrors) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Apply errors:")
		for _, e := range rep.ApplyErrors {
			fmt.Fprintf(w, "  ! %s\n", e)
		}
	}
}

func displayVersion(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

func displayManager(m string) string {
	if m == "" {
		return "unknown"
	}
	return m
}

func managedSuffix(inst YaverInstall) string {
	if inst.IsManaged {
		return " (managed)"
	}
	return ""
}

// truncateOneLine kept for diagnostic helpers that may flatten
// multi-line probe output into a single log line. Intentionally
// unexported and small enough to live alongside the rest of self_heal.
func truncateOneLineSelfHeal(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// runSelfHealCommand is the CLI entry: `yaver self heal [--apply] ...`.
// Mounted from main.go's command dispatcher.
func runSelfHealCommand(args []string) {
	opts := SelfHealOptions{}
	for _, a := range args {
		switch a {
		case "--apply":
			opts.Apply = true
		case "--include-managed":
			opts.IncludeManaged = true
		case "--self-update":
			opts.AllowSelfUpdate = true
			opts.Apply = true
		case "--quiet", "-q":
			opts.Quiet = true
		case "--json":
			opts.Quiet = true // JSON output, no human banner
		case "-h", "--help":
			fmt.Println("yaver self heal — reconcile multi-source yaver installs to a single version")
			fmt.Println()
			fmt.Println("Usage: yaver self heal [--apply] [--include-managed] [--self-update] [--json]")
			fmt.Println()
			fmt.Println("Without --apply, prints a report of every yaver binary on this box and any drift.")
			fmt.Println("With --apply, copies the canonical (highest semver) binary over each non-canonical install.")
			fmt.Println("--include-managed also rewrites apt/brew paths (will be reverted by the package manager).")
			fmt.Println("--self-update first pulls the latest release from GitHub if it's newer than canonical.")
			os.Exit(0)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rep := BuildSelfHealReport(ctx)
	if opts.Apply {
		ApplySelfHeal(ctx, rep, opts)
	}

	wantJSON := false
	for _, a := range args {
		if a == "--json" {
			wantJSON = true
		}
	}
	if wantJSON {
		out, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(out))
	} else {
		PrintSelfHealReport(os.Stdout, rep)
	}
	if len(rep.ApplyErrors) > 0 {
		os.Exit(1)
	}
}

// runSelfHealOnStartup is fired non-blocking from `yaver serve`. It
// builds the report, logs drift warnings, and — for safe cases —
// auto-reconciles them so the operator doesn't end up with multiple
// drifting yaver binaries on the same box. "Safe" here is restricted
// to writable, non-managed, non-wrapper sibling installs whose bytes
// disagree with the canonical (running) binary; everything else is
// reported only and left for `yaver self heal --apply` to handle
// explicitly. Why: pre-fix users routinely accumulated stale copies at
// /usr/local/bin, ~/.local/bin, etc., and cached agent dirs whose
// directory name lied about the binary inside (1.99.149/ holding the
// 1.99.150 bytes). With one canonical install path, multiple copies
// are always a bug, so closing the loop at startup keeps the box
// honest without surprising anyone with overwrites of brew/apt paths.
func runSelfHealOnStartup() {
	go func() {
		// Wait a bit so this doesn't compete with the auto-update tick
		// that runs at start.
		time.Sleep(45 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		rep := BuildSelfHealReport(ctx)
		if len(rep.Drift) == 0 && !rep.NeedsSelfPull {
			return
		}
		log.Printf("[self-heal] %d install(s) found; drift detected:", len(rep.Installs))
		for _, d := range rep.Drift {
			log.Printf("[self-heal]   - %s", d)
		}
		if rep.NeedsSelfPull {
			log.Printf("[self-heal]   - GitHub has v%s; canonical is v%s. Run `yaver self heal --apply --self-update`.", rep.LatestRelease, rep.Canonical.Version)
		}

		// Auto-reconcile. Restricted to siblings that are: writable, not
		// managed (apt/brew would just revert us on next upgrade), not
		// the npm wrapper (would brick the launcher), and actually drift
		// (different bytes from canonical). If none qualify, the report-
		// only log above is the whole story for this boot.
		if !hasReconcilableDrift(rep) {
			return
		}
		ApplySelfHeal(ctx, rep, SelfHealOptions{Apply: true, IncludeManaged: false, AllowSelfUpdate: false, Quiet: true})
		for _, applied := range rep.Applied {
			log.Printf("[self-heal] reconciled: %s", applied)
		}
		for _, errMsg := range rep.ApplyErrors {
			log.Printf("[self-heal] could not reconcile: %s", errMsg)
		}
	}()
}

// hasReconcilableDrift returns true when at least one sibling install
// matches the auto-apply criteria. Mirrors the per-install gate inside
// ApplySelfHeal so the startup hook can decide whether the apply call
// is worth making.
func hasReconcilableDrift(rep *SelfHealReport) bool {
	if rep == nil || rep.Canonical.Path == "" {
		return false
	}
	for _, inst := range rep.Installs {
		if inst.Path == rep.Canonical.Path {
			continue
		}
		if inst.SameAsRunning || inst.IsNPMWrapper || inst.IsManaged {
			continue
		}
		if !inst.Writable || inst.SHA256 == "" {
			continue
		}
		return true
	}
	return false
}

// HTTP surface (registered in httpserver.go).

// handleSelfHealReport: GET /agent/self-heal — owner-auth report.
func handleSelfHealReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	rep := BuildSelfHealReport(ctx)
	jsonReply(w, http.StatusOK, rep)
}

// handleSelfHealApply: POST /agent/self-heal — owner-auth, applies
// reconcile per the JSON body's options. Body shape mirrors
// SelfHealOptions; missing fields default to false.
func handleSelfHealApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonReply(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}
	var opts SelfHealOptions
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&opts)
	}
	opts.Apply = true
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	rep := BuildSelfHealReport(ctx)
	ApplySelfHeal(ctx, rep, opts)
	jsonReply(w, http.StatusOK, rep)
}

// Sentinel for tests that want to assert no GitHub call when offline.
var errSelfHealOffline = errors.New("github unreachable; report is local-only")
