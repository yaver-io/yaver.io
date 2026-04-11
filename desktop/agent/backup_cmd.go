package main

// backup_cmd.go — `yaver backup` — local snapshot + optional
// off-box sink for the dev's ~/.yaver/ state. Solo-dev
// alternative to Backblaze / rsync.net subscriptions for the
// handful of megabytes of config + blobs + sync state yaver
// accumulates. Everything stays P2P: backups are local tarballs
// by default, and the optional remote sinks (rsync, S3-compat,
// B2) are all dev-owned destinations — never a yaver-managed
// service and never Convex.
//
// Storage layout:
//
//   ~/.yaver/backups/<timestamp>.tar.gz
//   ~/.yaver/backups/manifest.json   {backups: [{path, size, sha256, createdAt}]}
//
// By design the tarball excludes ephemeral / rebuildable state
// (cache dirs, pid files, socket files, worktrees) so restoring
// on a fresh machine doesn't clobber in-progress work.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// BackupRecord is the manifest entry for one snapshot.
type BackupRecord struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"createdAt"`
	Note      string `json:"note,omitempty"`
}

var backupMu sync.Mutex

// excludedBackupPaths lists directories we skip when making a
// snapshot. These are either ephemeral (caches, tmp) or
// rebuildable (worktrees, compiled artifacts) so we don't waste
// bytes backing them up.
var excludedBackupPaths = []string{
	"loops",        // per-loop worktrees rebuild from git
	"releases",     // rebuilt from Hermes compile
	"voice-input",  // transient audio capture
	".origin",      // sync origin ID — regenerate on restore
	"agent.log",
	"blackbox",     // cross-device ring, rebuilt from SDK streams
}

func backupDir() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func backupManifestPath() (string, error) {
	dir, err := backupDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.json"), nil
}

func loadBackupManifest() ([]BackupRecord, error) {
	p, err := backupManifestPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupRecord{}, nil
		}
		return nil, err
	}
	var payload struct {
		Backups []BackupRecord `json:"backups"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Backups, nil
}

func saveBackupManifest(records []BackupRecord) error {
	p, err := backupManifestPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]interface{}{
		"backups": records,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

// createBackup tarballs ~/.yaver/ (minus exclusions) into the
// backups dir and appends a manifest entry. Returns the
// resulting BackupRecord.
func createBackup(note string) (*BackupRecord, error) {
	base, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	dir, err := backupDir()
	if err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	outPath := filepath.Join(dir, "yaver-"+stamp+".tar.gz")

	backupMu.Lock()
	defer backupMu.Unlock()

	out, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	excluded := map[string]bool{}
	for _, e := range excludedBackupPaths {
		excluded[filepath.Join(base, e)] = true
	}
	// Always exclude the backups dir itself so a snapshot
	// doesn't include yesterday's snapshots.
	excluded[dir] = true

	err = filepath.Walk(base, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		for excl := range excluded {
			if strings.HasPrefix(path, excl) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, cerr := io.Copy(tw, f)
			f.Close()
			if cerr != nil {
				return cerr
			}
		}
		return nil
	})
	if err != nil {
		tw.Close()
		gz.Close()
		_ = os.Remove(outPath)
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}

	// Size + sha256 for the manifest.
	info, err := os.Stat(outPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(outPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	rec := BackupRecord{
		Path:      outPath,
		Size:      info.Size(),
		SHA256:    hex.EncodeToString(h.Sum(nil)),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Note:      note,
	}
	records, _ := loadBackupManifest()
	records = append(records, rec)
	// Prune ancient backups past the 20 most recent — local
	// disks aren't infinite.
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt > records[j].CreatedAt })
	if len(records) > 20 {
		for _, old := range records[20:] {
			_ = os.Remove(old.Path)
		}
		records = records[:20]
	}
	if err := saveBackupManifest(records); err != nil {
		return nil, err
	}
	return &rec, nil
}

// restoreBackup extracts a tarball back into ~/.yaver/. It
// runs as an overlay — existing files get overwritten, missing
// files stay missing. The dev's running agent should be stopped
// first (we warn rather than kill it).
func restoreBackup(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	base, err := ConfigDir()
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Contain the target — reject any entry that escapes
		// the config dir.
		abs := filepath.Join(base, hdr.Name)
		if !strings.HasPrefix(abs, base) {
			return fmt.Errorf("refusing to write outside config dir: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(abs, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
				return err
			}
			out, err := os.OpenFile(abs, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

// pushBackupToSink shells out to rsync / scp / a custom command.
// The dev chooses the sink — yaver never owns it, which keeps
// the "no vendor" promise intact. The --sink flag's value is
// treated as a template with `{file}` replaced by the tarball
// path.
func pushBackupToSink(tarballPath, sinkTemplate string) error {
	if sinkTemplate == "" {
		return nil
	}
	cmd := strings.ReplaceAll(sinkTemplate, "{file}", tarballPath)
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty sink command")
	}
	c := exec.Command(parts[0], parts[1:]...)
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	return c.Run()
}

// --- CLI ------------------------------------------------------------------

func runBackup(args []string) {
	if len(args) == 0 {
		printBackupUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "create":
		backupCreateCmd(args[1:])
	case "list", "ls":
		backupListCmd()
	case "restore":
		backupRestoreCmd(args[1:])
	case "prune":
		backupPruneCmd(args[1:])
	case "help", "--help", "-h":
		printBackupUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown backup subcommand: %s\n\n", args[0])
		printBackupUsage()
		os.Exit(1)
	}
}

func printBackupUsage() {
	fmt.Print(`Yaver backup — local snapshots of ~/.yaver/.

Usage:
  yaver backup create [--note "..."] [--sink "<cmd with {file}>"]
  yaver backup list
  yaver backup restore <tarball>
  yaver backup prune --keep N

Examples:
  yaver backup create
  yaver backup create --sink "rsync {file} backup-server:/backups/"
  yaver backup create --sink "aws s3 cp {file} s3://mybucket/yaver/"

Every snapshot lives under ~/.yaver/backups/ with a manifest.
No vendor account required — the optional --sink just shells
out to whatever command you own. Excludes caches, worktrees,
and transient ring buffers so restoring is idempotent.
`)
}

func backupCreateCmd(args []string) {
	fs := flag.NewFlagSet("backup create", flag.ExitOnError)
	note := fs.String("note", "", "optional note for the manifest")
	sink := fs.String("sink", "", "optional shell command (use {file} as the tarball path)")
	fs.Parse(args)
	rec, err := createBackup(*note)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s  %d bytes  %s\n", rec.Path, rec.Size, rec.SHA256[:12])
	if *sink != "" {
		if err := pushBackupToSink(rec.Path, *sink); err != nil {
			fmt.Fprintf(os.Stderr, "sink: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✓ pushed to sink\n")
	}
}

func backupListCmd() {
	records, _ := loadBackupManifest()
	if len(records) == 0 {
		fmt.Println("No backups yet. `yaver backup create` to create one.")
		return
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt > records[j].CreatedAt })
	for _, r := range records {
		fmt.Printf("  %s  %d KB  %s\n", r.CreatedAt, r.Size/1024, filepath.Base(r.Path))
		if r.Note != "" {
			fmt.Printf("    note: %s\n", r.Note)
		}
	}
}

func backupRestoreCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver backup restore <tarball>")
		os.Exit(1)
	}
	path := args[0]
	if !filepath.IsAbs(path) {
		// Resolve relative to the backups dir so
		// `yaver backup restore yaver-20260411-120000.tar.gz` works.
		dir, _ := backupDir()
		path = filepath.Join(dir, path)
	}
	if err := restoreBackup(path); err != nil {
		fmt.Fprintf(os.Stderr, "restore: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ restored — restart the agent to pick up the new state")
}

func backupPruneCmd(args []string) {
	fs := flag.NewFlagSet("backup prune", flag.ExitOnError)
	keep := fs.Int("keep", 10, "number of most-recent backups to keep")
	fs.Parse(args)
	records, _ := loadBackupManifest()
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt > records[j].CreatedAt })
	if len(records) <= *keep {
		fmt.Printf("Only %d backup(s) — nothing to prune.\n", len(records))
		return
	}
	pruned := records[*keep:]
	for _, r := range pruned {
		_ = os.Remove(r.Path)
	}
	records = records[:*keep]
	_ = saveBackupManifest(records)
	fmt.Printf("✓ pruned %d old backup(s)\n", len(pruned))
}
