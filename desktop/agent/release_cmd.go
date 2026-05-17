package main

// release_cmd.go — `yaver release` CLI. Self-hosted OTA lane for
// React Native apps that reuses the existing Hermes compile +
// bundle-check + P2P relay stack.
//
// The whole mechanism is:
//
//  1. Dev runs `yaver release publish --channel production` in a
//     React Native repo. We invoke `react-native bundle` → get a
//     raw JS bundle, hand it to `compileHermesBundle` → Hermes
//     bytecode, then run `ValidateHBC` to confirm the BC version
//     matches what the container app ships.
//  2. The resulting .hbc is copied into
//     `~/.yaver/releases/<channel>/<semver>/bundle.hbc` and a
//     sibling `metadata.json` records size/md5/hbcVersion/
//     publishedAt. The channel's top-level `manifest.json` is
//     atomically updated so `latest` points at the new semver.
//  3. The existing agent HTTP server exposes `/releases/*`
//     endpoints (see release_http.go) so end-user apps can poll
//     through the P2P relay. Rollback is one manifest write.
//
// Storage layout under ~/.yaver/releases/:
//
//   <channel>/
//     manifest.json          { latest, rolloutPercent, releases[] }
//     <semver>/
//       bundle.hbc           Hermes bytecode
//       metadata.json        BundleMetadata + publishedAt + commit
//
// Rollout percentage is pure-local bucketing on a deviceID hash —
// no server, no cross-device coordination. See releaseRolloutHit.

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReleaseManifest is the per-channel index file.
type ReleaseManifest struct {
	Channel         string            `json:"channel"`
	Latest          string            `json:"latest,omitempty"` // semver of the current "latest" release
	RolloutPercent  int               `json:"rolloutPercent"`   // 0..100; 0 = nobody, 100 = everyone
	Releases        []ReleaseEntry    `json:"releases"`         // newest first
	UpdatedAt       string            `json:"updatedAt"`
	LegacyByVersion map[string]string `json:"-"` // not serialized
}

// ReleaseEntry is one published release inside a channel.
type ReleaseEntry struct {
	Semver          string `json:"semver"`
	Size            int64  `json:"size"`
	MD5             string `json:"md5"`
	HermesBCVersion int    `json:"hermesBcVersion"`
	PublishedAt     string `json:"publishedAt"`
	Commit          string `json:"commit,omitempty"`
	Notes           string `json:"notes,omitempty"`
}

var releaseMu sync.Mutex

// releasesRoot is `~/.yaver/releases` (or the equivalent under
// ConfigDir). Created lazily.
func releasesRoot() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "releases")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func channelDir(channel string) (string, error) {
	root, err := releasesRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, sanitizeChannelName(channel)), nil
}

func sanitizeChannelName(c string) string {
	// Channels are filesystem paths — keep them to a known
	// alphabet so a malicious channel value can't escape the
	// releases dir.
	out := strings.Builder{}
	for _, r := range c {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			out.WriteRune(r)
		}
	}
	res := out.String()
	if res == "" {
		return "production"
	}
	return res
}

// loadManifest reads a channel's manifest, returning an empty
// manifest if none exists yet.
func loadManifest(channel string) (*ReleaseManifest, error) {
	dir, err := channelDir(channel)
	if err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &ReleaseManifest{
				Channel:        channel,
				RolloutPercent: 100,
				Releases:       []ReleaseEntry{},
			}, nil
		}
		return nil, err
	}
	var m ReleaseManifest
	if jerr := json.Unmarshal(data, &m); jerr != nil {
		return nil, jerr
	}
	if m.Channel == "" {
		m.Channel = channel
	}
	if m.Releases == nil {
		m.Releases = []ReleaseEntry{}
	}
	return &m, nil
}

// saveManifest writes the manifest atomically (tmp + rename).
func saveManifest(channel string, m *ReleaseManifest) error {
	dir, err := channelDir(channel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	m.Channel = channel
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, jerr := json.MarshalIndent(m, "", "  ")
	if jerr != nil {
		return jerr
	}
	p := filepath.Join(dir, "manifest.json")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// runRelease is the `yaver release ...` entry point.
func runRelease(args []string) {
	if len(args) == 0 {
		printReleaseUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "publish":
		releasePublish(args[1:])
	case "list", "ls":
		releaseList(args[1:])
	case "rollback":
		releaseRollback(args[1:])
	case "rollout":
		releaseRollout(args[1:])
	case "delete", "rm":
		releaseDelete(args[1:])
	// Mobile store release — friendly alias for the native
	// build+upload path (iOS → TestFlight, Android → Play). Same
	// pipeline as `yaver ios-native`/`android-native`; `yaver build
	// ios|android` stays artifact-only (.ipa/.aab, no upload).
	case "ios":
		runNativeIOS(args[1:])
	case "android":
		runNativeAndroid(args[1:])
	case "flutter":
		runNativeFlutter(args[1:])
	case "help", "--help", "-h":
		printReleaseUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown release subcommand: %s\n\n", args[0])
		printReleaseUsage()
		os.Exit(1)
	}
}

func printReleaseUsage() {
	fmt.Print(`Yaver self-hosted OTA — publish React Native JS bundles through your own relay.

Usage:
  yaver release publish [flags]            Compile + validate + store a new release
  yaver release list [--channel <ch>]      Show every release in a channel (newest first)
  yaver release rollback <channel> <semver>   Flip the channel's "latest" back to <semver>
  yaver release rollout <channel> <pct>    Set the rollout percentage (0..100)
  yaver release delete <channel> <semver>  Remove a specific release

Native store release (build + upload; alias of ios-native/android-native):
  yaver release ios     [repo-or-project-dir]   Build + ship to TestFlight
  yaver release android [repo-or-project-dir]   Build + ship to Play
  yaver release flutter [repo-or-project-dir]   Flutter → store for the detected platform
  (artifact-only? use 'yaver build ios|android' → .ipa/.aab, no upload)

Publish flags:
  --channel, -c <name>     Channel to publish into (default: production)
  --entry, -e <file>       Metro entry file (default: index.js)
  --platform <ios|android> Platform to bundle for (default: ios)
  --semver, -v <semver>    Release version (default: read from package.json)
  --notes <text>           Optional changelog notes stored in metadata
  --dry-run                Run the bundle + validate step but don't stash

The bundle is compiled with Yaver's embedded hermesc (BC version
matches the Yaver app), validated by ValidateHBC, and copied into
~/.yaver/releases/<channel>/<semver>/bundle.hbc. End-user apps poll
/releases/latest?channel=<name> through the P2P relay.
`)
}

// releasePublish compiles + stores a new release.
func releasePublish(args []string) {
	fs := flag.NewFlagSet("release publish", flag.ExitOnError)
	channel := fs.String("channel", "production", "release channel")
	channelShort := fs.String("c", "", "release channel (short)")
	entry := fs.String("entry", "index.js", "Metro entry file")
	entryShort := fs.String("e", "", "Metro entry file (short)")
	platform := fs.String("platform", "ios", "platform (ios|android)")
	semver := fs.String("semver", "", "release version (default: package.json version)")
	semverShort := fs.String("v", "", "release version (short)")
	notes := fs.String("notes", "", "optional release notes")
	dryRun := fs.Bool("dry-run", false, "compile + validate but don't stash")
	fs.Parse(args)

	if *channelShort != "" {
		*channel = *channelShort
	}
	if *entryShort != "" {
		*entry = *entryShort
	}
	if *semverShort != "" {
		*semver = *semverShort
	}

	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cwd: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stat(filepath.Join(workDir, "package.json")); err != nil {
		fmt.Fprintln(os.Stderr, "no package.json in cwd — run `yaver release publish` from your RN project root")
		os.Exit(2)
	}

	// Derive semver from package.json if not passed.
	if strings.TrimSpace(*semver) == "" {
		v, verr := readPackageJSONVersion(workDir)
		if verr != nil || v == "" {
			fmt.Fprintf(os.Stderr, "could not read version from package.json: %v\n", verr)
			fmt.Fprintln(os.Stderr, "pass --semver explicitly")
			os.Exit(2)
		}
		*semver = v
	}

	fmt.Printf("=> channel=%s semver=%s platform=%s entry=%s\n",
		*channel, *semver, *platform, *entry)

	// Step 1: metro bundle to a temp file.
	tmp, tmpErr := os.CreateTemp("", "yaver-release-*.jsbundle")
	if tmpErr != nil {
		fmt.Fprintf(os.Stderr, "temp: %v\n", tmpErr)
		os.Exit(1)
	}
	tmp.Close()
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	fmt.Println("   running `npx react-native bundle` (this may take ~30s)...")
	metroCmd := exec.Command("npx", "react-native", "bundle",
		"--platform", *platform,
		"--entry-file", *entry,
		"--bundle-output", tmpPath,
		"--dev", "false",
		"--minify", "true",
		"--reset-cache",
	)
	metroCmd.Dir = workDir
	metroCmd.Stdout = os.Stderr
	metroCmd.Stderr = os.Stderr
	if err := metroCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "react-native bundle failed: %v\n", err)
		os.Exit(1)
	}

	// Step 2: Hermes compile in place.
	fmt.Println("   compiling to Hermes bytecode...")
	if err := compileHermesBundle(tmpPath); err != nil {
		fmt.Fprintf(os.Stderr, "hermesc: %v\n", err)
		os.Exit(1)
	}

	// Step 3: validate HBC — version pinned to whatever the
	// embedded hermesc produces. Passing 0 accepts any version
	// since the dev could be targeting a container with a
	// different BC. The end-user app re-validates on its side.
	md, vErr := ValidateHBC(tmpPath, 0)
	if vErr != nil {
		fmt.Fprintf(os.Stderr, "validation failed: %v\n", vErr)
		os.Exit(1)
	}
	fmt.Printf("   bundle: %d bytes, md5=%s, hbcVersion=%d\n",
		md.Size, md.MD5, md.HermesBCVersion)

	if *dryRun {
		fmt.Println("   --dry-run: not stashing")
		return
	}

	// Step 4: copy into the release store + update the manifest.
	releaseMu.Lock()
	defer releaseMu.Unlock()

	dir, err := channelDir(*channel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel dir: %v\n", err)
		os.Exit(1)
	}
	relDir := filepath.Join(dir, *semver)
	if err := os.MkdirAll(relDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	finalBundle := filepath.Join(relDir, "bundle.hbc")
	if err := copyFile(tmpPath, finalBundle); err != nil {
		fmt.Fprintf(os.Stderr, "copy bundle: %v\n", err)
		os.Exit(1)
	}

	entryRec := ReleaseEntry{
		Semver:          *semver,
		Size:            md.Size,
		MD5:             md.MD5,
		HermesBCVersion: md.HermesBCVersion,
		PublishedAt:     time.Now().UTC().Format(time.RFC3339),
		Commit:          currentGitSHA(workDir),
		Notes:           strings.TrimSpace(*notes),
	}
	// Persist per-release metadata next to the bundle.
	metaBytes, _ := json.MarshalIndent(entryRec, "", "  ")
	_ = os.WriteFile(filepath.Join(relDir, "metadata.json"), metaBytes, 0600)

	// Update channel manifest — drop any prior record for the same
	// semver (re-publishing the same version), prepend the new
	// record, point latest at it.
	m, err := loadManifest(*channel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}
	filtered := m.Releases[:0]
	for _, r := range m.Releases {
		if r.Semver != *semver {
			filtered = append(filtered, r)
		}
	}
	m.Releases = append([]ReleaseEntry{entryRec}, filtered...)
	m.Latest = *semver
	if m.RolloutPercent == 0 {
		m.RolloutPercent = 100
	}
	if err := saveManifest(*channel, m); err != nil {
		fmt.Fprintf(os.Stderr, "save manifest: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✓ published %s @ %s\n", entryRec.Semver, entryRec.PublishedAt)
	fmt.Printf("  bundle: %s\n", finalBundle)
	fmt.Printf("  channel manifest: %s\n", filepath.Join(dir, "manifest.json"))
	fmt.Printf("  rollout: %d%% of devices will pull this release\n", m.RolloutPercent)
}

// releaseList prints every release in a channel, newest first.
func releaseList(args []string) {
	fs := flag.NewFlagSet("release list", flag.ExitOnError)
	channel := fs.String("channel", "production", "channel to list")
	fs.Parse(args)

	m, err := loadManifest(*channel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}
	if len(m.Releases) == 0 {
		fmt.Printf("No releases in channel %q yet. Run `yaver release publish --channel %s`.\n",
			*channel, *channel)
		return
	}
	fmt.Printf("=== channel %s (latest=%s, rollout=%d%%) ===\n",
		*channel, m.Latest, m.RolloutPercent)
	for _, r := range m.Releases {
		marker := " "
		if r.Semver == m.Latest {
			marker = "*"
		}
		fmt.Printf("  %s %s  %s  %d bytes  bc%d  %s\n",
			marker, r.Semver, r.PublishedAt, r.Size, r.HermesBCVersion, shortCommit(r.Commit))
		if r.Notes != "" {
			fmt.Printf("      %s\n", r.Notes)
		}
	}
}

// releaseRollback flips `latest` to a previously published semver.
func releaseRollback(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver release rollback <channel> <semver>")
		os.Exit(1)
	}
	channel := args[0]
	target := args[1]

	releaseMu.Lock()
	defer releaseMu.Unlock()

	m, err := loadManifest(channel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}
	found := false
	for _, r := range m.Releases {
		if r.Semver == target {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "release %q not found in channel %q — `yaver release list --channel %s`\n",
			target, channel, channel)
		os.Exit(2)
	}
	m.Latest = target
	if err := saveManifest(channel, m); err != nil {
		fmt.Fprintf(os.Stderr, "save manifest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ channel %s latest → %s\n", channel, target)
}

// releaseRollout sets the rollout percentage for a channel.
func releaseRollout(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver release rollout <channel> <percent>")
		os.Exit(1)
	}
	channel := args[0]
	pct, err := strconv.Atoi(args[1])
	if err != nil || pct < 0 || pct > 100 {
		fmt.Fprintln(os.Stderr, "percent must be an integer 0..100")
		os.Exit(2)
	}

	releaseMu.Lock()
	defer releaseMu.Unlock()

	m, err := loadManifest(channel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}
	m.RolloutPercent = pct
	if err := saveManifest(channel, m); err != nil {
		fmt.Fprintf(os.Stderr, "save manifest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ channel %s rollout → %d%%\n", channel, pct)
}

// releaseDelete drops a specific release from both the manifest
// and the on-disk store. If it was `latest`, latest reverts to
// the next newest remaining release (or empty string).
func releaseDelete(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver release delete <channel> <semver>")
		os.Exit(1)
	}
	channel := args[0]
	target := args[1]

	releaseMu.Lock()
	defer releaseMu.Unlock()

	m, err := loadManifest(channel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load manifest: %v\n", err)
		os.Exit(1)
	}
	filtered := m.Releases[:0]
	removed := false
	for _, r := range m.Releases {
		if r.Semver == target {
			removed = true
			continue
		}
		filtered = append(filtered, r)
	}
	if !removed {
		fmt.Fprintf(os.Stderr, "release %q not in channel %q\n", target, channel)
		os.Exit(2)
	}
	m.Releases = filtered
	if m.Latest == target {
		if len(m.Releases) > 0 {
			m.Latest = m.Releases[0].Semver
		} else {
			m.Latest = ""
		}
	}
	if err := saveManifest(channel, m); err != nil {
		fmt.Fprintf(os.Stderr, "save manifest: %v\n", err)
		os.Exit(1)
	}
	// Remove the on-disk bundle too.
	dir, _ := channelDir(channel)
	_ = os.RemoveAll(filepath.Join(dir, target))
	fmt.Printf("✓ removed %s from channel %s (latest=%s)\n", target, channel, m.Latest)
}

// releaseRolloutHit computes whether a given deviceID falls inside
// the current rollout bucket for a manifest. Pure-local SHA256
// bucketing — no server coordination needed. The deviceID is
// stable per end-user device, so the same device consistently
// gets either "in" or "out" until the rollout percentage changes
// or a new release is published.
func releaseRolloutHit(deviceID string, pct int) bool {
	if pct >= 100 {
		return true
	}
	if pct <= 0 {
		return false
	}
	sum := sha256.Sum256([]byte(deviceID))
	// Use the first 4 bytes as a uint32, mod 100.
	n := uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
	return int(n%100) < pct
}

// --- helpers -------------------------------------------------------------

func readPackageJSONVersion(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", err
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", err
	}
	return pkg.Version, nil
}

func currentGitSHA(workDir string) string {
	out, err := exec.Command("git", "-C", workDir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func shortCommit(c string) string {
	if c == "" {
		return ""
	}
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// sortedReleases returns a semver-lexicographically sorted copy of
// the manifest's releases. Used by listing + by the rollback UX
// when showing "the candidate versions to roll back to".
func sortedReleases(m *ReleaseManifest) []ReleaseEntry {
	out := make([]ReleaseEntry, len(m.Releases))
	copy(out, m.Releases)
	sort.Slice(out, func(i, j int) bool {
		return out[i].PublishedAt > out[j].PublishedAt
	})
	return out
}
