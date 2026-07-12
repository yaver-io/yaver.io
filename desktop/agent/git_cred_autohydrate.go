package main

import (
	"context"
	"log"
	"time"
)

// autoHydrateGitCredentialsOnManagedBox — "first-class instant provisioning"
// for git. On a Yaver-MANAGED cloud box that boots with NO git credentials
// (a fresh provision, or a resume from a snapshot taken before the user
// authed git), pull the owner's git creds from their PRIMARY device over the
// relay and write them locally — so private `git clone`/`push` works INSTANTLY
// with zero manual per-box setup. This is the payoff of registering the
// gh/glab Device-Flow OAuth apps: the user auths ONCE on any device, and every
// box they spin up inherits it automatically.
//
// Privacy-safe: creds move device→device via the toolchain-sync peer-proxy
// (proxyToDevice → direct endpoint or password-gated relay tunnel). Convex
// only brokers the primary's endpoint for discovery — it never sees the token
// (the vault/git-credentials privacy contract holds).
//
// Idempotent + best-effort: no-ops if creds already exist, if this box IS the
// primary, or if the primary is unreachable; retries briefly on boot since the
// relay tunnel + the primary may not be reachable the instant we come up.
func autoHydrateGitCredentialsOnManagedBox(cfg *Config) {
	// Only managed cloud boxes have /etc/yaver/machine.json. A user's own
	// Mac/laptop is the SOURCE of creds, never a hydration target.
	if cfg == nil || loadMachineIdentity() == nil {
		return
	}
	// Already have git creds? Idempotent no-op across restarts.
	if creds, _ := loadGitCredentials(); len(creds) > 0 {
		return
	}
	selfID := cfg.DeviceID

	for attempt := 1; attempt <= 6; attempt++ {
		// Back off across boot so the relay tunnel + peers have time to be
		// reachable: 10s, 20s, … 60s.
		time.Sleep(time.Duration(attempt*10) * time.Second)

		// A concurrent OAuth / machine-onboarding may have populated creds
		// while we waited — re-check so we never clobber a fresher source.
		if creds, _ := loadGitCredentials(); len(creds) > 0 {
			return
		}

		// Candidate sources, in preference order: the PRIMARY first (usually
		// the user's main machine), then ANY other online owner device that
		// might hold creds. Pulling only from the primary meant a fresh box got
		// NO git creds whenever there was no primary set (e.g. right after the
		// old primary was retired) — friction the "auth once, works everywhere"
		// promise is supposed to eliminate.
		var candidates []string
		seen := map[string]bool{selfID: true}
		if pid, err := resolvePrimaryDeviceIDForMCP(); err == nil && pid != "" && !seen[pid] {
			candidates = append(candidates, pid)
			seen[pid] = true
		}
		if devs, err := listDevices(cfg.ConvexSiteURL, cfg.AuthToken); err == nil {
			for _, d := range filterOnlineDevices(devs) {
				if d.DeviceID != "" && !seen[d.DeviceID] {
					candidates = append(candidates, d.DeviceID)
					seen[d.DeviceID] = true
				}
			}
		}

		for _, srcID := range candidates {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			creds, err := toolchainGitCredentialsFromPeer(ctx, srcID)
			cancel()
			if err != nil || len(creds) == 0 {
				continue
			}
			imported, _, _ := importToolchainGitCredentials(creds, false /*removeMissing*/, false /*dryRun*/)
			if len(imported) > 0 {
				log.Printf("[git-autohydrate] pulled %d git credential(s) from %s — private clone/push ready", len(imported), srcID)
				return
			}
		}
	}
}
