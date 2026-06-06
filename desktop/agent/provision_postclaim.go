package main

// provision_postclaim.go — the "box does its job automatically" half of
// zero-touch. Once a provisioned device has self-credentialed (claimed +
// attested → token → authenticated serve), it runs its baked
// yaver.provision.yaml `setup` steps exactly once, bringing up the
// builder's workload (typically `yaver companion up`, package installs,
// unit enables). This is what turns "a Pi that just connected to my
// account" into "a Talos edge node already running its Modbus loop".
//
// Idempotent via a marker file so reboots and re-execs don't re-run it.
// Gated on the box actually being provisioned (a seed exists) so a normal
// install that happens to have a stray manifest never auto-executes shell.

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const provisionPostClaimMarker = "provision-postclaim.done"

// maybeRunProvisionPostClaim runs the post-claim setup once, in the
// background, on authenticated serve startup. Safe to call unconditionally:
// it no-ops unless this is a provisioned box with an un-applied manifest.
func maybeRunProvisionPostClaim(workDir string) {
	// Only provisioned boxes (with a seed) auto-run setup. A normal install
	// must never execute shell from a manifest it didn't provision under.
	seed, _ := LoadProvisionSeed()
	if seed == nil {
		return
	}

	cfgDir, err := ConfigDir()
	if err != nil {
		return
	}
	markerPath := filepath.Join(cfgDir, provisionPostClaimMarker)
	if _, statErr := os.Stat(markerPath); statErr == nil {
		return // already applied
	}

	manifest, err := FindProvisionManifest(workDir)
	if err != nil {
		log.Printf("[provision] post-claim: manifest unreadable: %v", err)
		return
	}
	if manifest == nil || len(manifest.Setup) == 0 {
		// Nothing to do — still drop the marker so we don't re-scan every boot.
		_ = os.WriteFile(markerPath, []byte("no-setup\n"), 0o600)
		return
	}

	ok := runProvisionSetup(manifest)
	if ok {
		// Record completion (timestamp-free body — the mtime is enough and
		// stamping a clock here would be noise).
		_ = os.WriteFile(markerPath, []byte(fmt.Sprintf("product=%s steps=%d\n", manifest.Product, len(manifest.Setup))), 0o600)
		log.Printf("[provision] post-claim setup complete for product %q (%d steps)", manifest.Product, len(manifest.Setup))
	} else {
		// Leave the marker absent so the next boot retries — a failed
		// bring-up should self-heal on restart rather than wedge forever.
		log.Printf("[provision] post-claim setup did not complete; will retry on next start")
	}
}

// runProvisionSetup executes each setup step in order. Returns false if a
// required step failed (which aborts the rest). Each step gets a generous
// but bounded timeout so a hung command can't block startup forever.
func runProvisionSetup(m *ProvisionManifest) bool {
	log.Printf("[provision] running post-claim setup for product %q (%d steps)…", m.Product, len(m.Setup))
	for i, step := range m.Setup {
		label := step.Name
		if label == "" {
			label = fmt.Sprintf("step %d", i+1)
		}
		if step.Run == "" {
			continue
		}
		log.Printf("[provision]   → %s: %s", label, step.Run)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		cmd := exec.CommandContext(ctx, "sh", "-c", step.Run)
		if m.dir != "" {
			cmd.Dir = m.dir
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		cancel()
		if err != nil {
			if step.AllowFailure {
				log.Printf("[provision]   %s failed (allow_failure): %v — continuing", label, err)
				continue
			}
			log.Printf("[provision]   %s failed: %v — aborting setup", label, err)
			return false
		}
	}
	return true
}
