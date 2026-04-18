package main

// Dev-server dependency-installation shared helpers.
//
// Lives in its own file so the streaming install machinery doesn't
// get tangled with the main dev-server lifecycle in devserver.go.
// Every line of install output (Node tarball download, npm / yarn
// progress, pub get, etc.) goes through an emitterLogWriter so it
// fans out to /dev/events SSE subscribers — the mobile Hot Reload
// card and the `yaver emu start` CLI both consume those events and
// render them live, so a stalled install is never invisible.

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// emitterLogWriter splits incoming bytes into lines, logs them
// locally with a prefix, and fans each line out to the SSE emitter
// as a DevServerEvent{Type: "log", LogLine: …}. Distinct from the
// devLogWriter used by subprocess stdout/stderr: this one doesn't
// touch baseDevServer.history (that's reserved for the dev server's
// own output, used for Tail() on start-failure diagnostics).
type emitterLogWriter struct {
	prefix    string
	framework string
	emit      func(DevServerEvent)
	buf       []byte
}

func newEmitterLogWriter(emit func(DevServerEvent), prefix, framework string) *emitterLogWriter {
	if emit == nil {
		return nil
	}
	return &emitterLogWriter{prefix: prefix, framework: framework, emit: emit}
}

func (w *emitterLogWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:idx]))
		w.buf = w.buf[idx+1:]
		if line == "" {
			continue
		}
		log.Printf("%s %s", w.prefix, line)
		if w.emit != nil {
			w.emit(DevServerEvent{
				Type:      "log",
				Framework: w.framework,
				LogLine:   line,
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
	return len(p), nil
}

// isOnlyNodeMissing reports whether every missing tool in missing
// is something installNodeRuntime provides (node/npm/npx). If bun /
// pnpm / yarn / any non-Node tool is also missing, we bail — those
// need separate installers we don't ship yet.
func isOnlyNodeMissing(missing []string) bool {
	if len(missing) == 0 {
		return false
	}
	for _, m := range missing {
		switch m {
		case "node", "npm", "npx":
			continue
		default:
			return false
		}
	}
	return true
}

// ensureNodeDepsStreamed is the shared preflight for every JS-based
// dev server (Expo, React Native, Vite, Next). It:
//
//  1. Returns immediately if node_modules exists.
//  2. Detects the project's package manager and what's installed.
//  3. If only Node itself is missing, downloads Node LTS sudo-free
//     into ~/.yaver/runtimes/node via installNodeRuntime.
//  4. Runs the project's package-manager install with stdout/stderr
//     tee'd to the SSE emitter so the mobile card shows every line
//     live instead of a silent spinner.
func ensureNodeDepsStreamed(ctx context.Context, workDir string, emit func(DevServerEvent), framework string) error {
	if _, err := os.Stat(filepath.Join(workDir, "node_modules")); err == nil {
		return nil
	}
	installWriter := newEmitterLogWriter(emit, "[install]", framework)
	manifest, _ := readProjectPackageManifest(workDir)
	prep := detectProjectPreparation(workDir, manifest)
	if !prep.CanAutoInstallDependencies && isOnlyNodeMissing(prep.MissingTools) {
		if installWriter != nil {
			fmt.Fprintln(installWriter, "Node missing — installing LTS into ~/.yaver/runtimes/node (sudo-free)...")
		}
		if _, nerr := installNodeRuntime(ctx, func(line string) {
			if installWriter != nil {
				fmt.Fprintln(installWriter, line)
			}
		}); nerr != nil {
			return fmt.Errorf("auto-install Node failed: %w", nerr)
		}
		prep = detectProjectPreparation(workDir, manifest)
	}
	if !prep.CanAutoInstallDependencies {
		missing := strings.Join(prep.MissingTools, ", ")
		if missing == "" {
			missing = "package manager"
		}
		return fmt.Errorf("cannot install dependencies (%s missing on this machine). Install Node from the phone (POST /install/node) or run `yaver install node`", missing)
	}
	log.Printf("[dev] Installing dependencies in %s with %s...", workDir, prep.PackageManager)
	if installWriter != nil {
		fmt.Fprintf(installWriter, "running `%s install` in %s ...\n", prep.PackageManager, workDir)
	}
	if err := installProjectDependenciesTo(workDir, prep, installWriter); err != nil {
		return fmt.Errorf("%s install failed: %w", prep.PackageManager, err)
	}
	return nil
}
