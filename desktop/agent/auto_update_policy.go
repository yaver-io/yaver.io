package main

import (
	"math/rand"
	"time"
)

// Auto-update policy — one place that answers "should this agent keep
// itself current, and when does it next look?".
//
// Historically `auto_update` was a plain bool that zero-valued to
// false, and nothing ever set it true. Both the boot check and the
// periodic ticker were therefore dead code on every box whose operator
// never ran `yaver auto-update enable` — which is how a fleet drifts to
// v1.99.297 while the release is v1.99.309, and why the dashboard's
// only recourse was a hand-driven "Update agent" modal per box.
//
// The field is now tri-state (*bool), matching the HeadlessKeepAwake
// idiom next to it in Config: nil means "operator never said", and the
// answer to that is ON. Explicit false still wins.
//
// Migration wart worth knowing: the old `auto_update` bool carried
// `omitempty`, so `yaver auto-update disable` wrote *nothing* to
// config.json — "off" and "never asked" were the same byte pattern on
// disk. There is no way to tell those apart retroactively, so a box
// that opted out before this change reads as nil and will now
// auto-update again. Opting out post-change persists `false` properly
// (a non-nil pointer is not empty) and sticks.

// shouldAutoUpdate reports whether the agent keeps itself on the latest
// release. Unset (nil) means yes — see the default-on rationale above.
func shouldAutoUpdate(cfg *Config) bool {
	if cfg != nil && cfg.AutoUpdate != nil {
		return *cfg.AutoUpdate
	}
	return true
}

// applyDefaultAutoUpdate pins the default into cfg so `yaver
// auto-update status` and the config HTTP surface report a concrete
// value instead of an implicit one. Returns true when it wrote, so the
// caller can decide whether a SaveConfig is warranted.
func applyDefaultAutoUpdate(cfg *Config) bool {
	if cfg == nil || cfg.AutoUpdate != nil {
		return false
	}
	cfg.AutoUpdate = boolPtr(true)
	return true
}

// forcedAutoUpdateConfig returns a copy of cfg with auto-update forced
// on, for the call sites that mean "check right now regardless of what
// the operator configured" (`yaver update`, POST /agent/update, self
// heal). Copying rather than mutating keeps the caller's cfg — and
// therefore what a later SaveConfig would persist — untouched.
func forcedAutoUpdateConfig(cfg *Config) *Config {
	forced := &Config{}
	if cfg != nil {
		*forced = *cfg
	}
	forced.AutoUpdate = boolPtr(true)
	return forced
}

// autoUpdateCheckInterval is the delay until the next periodic check:
// a uniform random point in [6h, 12h).
//
// The jitter is not cosmetic. Every agent in the fleet polls the same
// GitHub `releases/latest` endpoint, and a fixed 6h period phase-locks
// them to their own boot times — which cluster, because a release is
// exactly when a lot of boxes restart at once. Unauthenticated GitHub
// API calls are rate-limited per source IP, so a datacenter full of
// agents on a shared egress IP can 403 each other out of updating
// entirely. Spreading each agent's next look across a 6h window keeps
// the aggregate request rate flat and is what every well-behaved
// updater (apt, Chrome, Sparkle) does for the same reason.
func autoUpdateCheckInterval() time.Duration {
	const min = 6 * time.Hour
	const spread = 6 * time.Hour
	return min + time.Duration(rand.Int63n(int64(spread)))
}
