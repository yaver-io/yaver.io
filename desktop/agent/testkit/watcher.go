package testkit

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watch watches the spec directory and re-runs every spec whose file
// changes (mtime + content hash). It is the "vibe coding" loop: edit a
// YAML in the editor, see the result on the next save.
//
// We deliberately do NOT pull in fsnotify or inotify here. The agent
// already runs as a long-lived Go process; a 500ms poll loop is
// imperceptible to humans, costs ~0 CPU, and works identically on
// macOS and Linux without cgo.
func Watch(ctx context.Context, root string, opts RunOptions, onResult func(*Result)) error {
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}

	knownHash := map[string]string{}

	// Initial sweep so we have a baseline of every spec's hash.
	if err := walkSpecs(abs, func(p string) error {
		h, err := fileHash(p)
		if err == nil {
			knownHash[p] = h
		}
		return nil
	}); err != nil {
		return err
	}

	// Run everything once on startup.
	specs, err := DiscoverSpecs(abs)
	if err == nil {
		for _, s := range specs {
			r := Run(ctx, s, opts)
			if onResult != nil {
				onResult(r)
			}
		}
	}

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			changed := []string{}
			seen := map[string]bool{}
			_ = walkSpecs(abs, func(p string) error {
				seen[p] = true
				h, err := fileHash(p)
				if err != nil {
					return nil
				}
				if knownHash[p] != h {
					knownHash[p] = h
					changed = append(changed, p)
				}
				return nil
			})
			// Detect deletions so we don't keep stale hashes.
			for k := range knownHash {
				if !seen[k] {
					delete(knownHash, k)
				}
			}
			for _, p := range changed {
				s, err := LoadSpec(p)
				if err != nil {
					if onResult != nil {
						onResult(&Result{
							Spec:       &Spec{Name: filepath.Base(p), Path: p},
							StartedAt:  time.Now(),
							FinishedAt: time.Now(),
							Err:        err,
						})
					}
					continue
				}
				r := Run(ctx, s, opts)
				if onResult != nil {
					onResult(r)
				}
			}
		}
	}
}

func walkSpecs(root string, visit func(string) error) error {
	return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".test.yaml") && !strings.HasSuffix(name, ".test.yml") {
			return nil
		}
		return visit(p)
	})
}

func fileHash(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FormatTimestamp returns a short HH:MM:SS prefix used by the watcher
// log lines.
func FormatTimestamp(t time.Time) string {
	return fmt.Sprintf("%02d:%02d:%02d", t.Hour(), t.Minute(), t.Second())
}
