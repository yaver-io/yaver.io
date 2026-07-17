package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// shipLastTag is the durable marker for "what did we already ship?".
//
// It has to be durable and it has to live in git, because the question spans
// agent restarts, machines, and weeks. In-memory state cannot answer it and
// Convex must not (the privacy contract keeps work-derived data off it, and a
// deploy watermark is work-derived). A tag is the cheapest thing that is already
// replicated everywhere the repo is.
const shipLastTag = "ship/last"

// shipTargetRule maps a repo path prefix to the deploy steps it affects.
//
// The mapping is deliberately conservative in one direction: a path that matches
// nothing deploys nothing. "Deploy all platforms" must not mean "burn a
// TestFlight upload because a Go comment changed" — TestFlight is ~15-20 uploads
// per day with no rollback, only supersede.
type shipTargetRule struct {
	Prefix  string
	Targets []string
	Why     string
}

// shipTargetRules is ordered most-specific-first; the first match wins.
var shipTargetRules = []shipTargetRule{
	{Prefix: "backend/convex/", Targets: []string{"convex"}, Why: "convex functions/schema"},
	{Prefix: "backend/", Targets: []string{"convex"}, Why: "backend"},
	{Prefix: "web/", Targets: []string{"web-cloudflare"}, Why: "web app"},
	{Prefix: "cli/", Targets: []string{"cli-npm"}, Why: "cli package"},
	// The agent binary is distributed BY the npm package (npm install -g
	// yaver-cli downloads it), so an agent change ships through the npm target
	// even though nothing under cli/ was touched.
	{Prefix: "desktop/agent/", Targets: []string{"cli-npm"}, Why: "agent binary ships via the npm package"},
	{Prefix: "mobile/", Targets: []string{"testflight-ios", "playstore-android"}, Why: "mobile app"},
	{Prefix: "sdk/", Targets: []string{"cli-npm"}, Why: "sdk ships with the package"},
}

// shipTargetsForPath returns the deploy steps one changed path implies.
func shipTargetsForPath(path string) []string {
	p := strings.TrimPrefix(strings.TrimSpace(path), "./")
	for _, rule := range shipTargetRules {
		if strings.HasPrefix(p, rule.Prefix) {
			return rule.Targets
		}
	}
	return nil
}

// shipTargetPlan is the answer to "what does this diff need deployed?".
type shipTargetPlan struct {
	// Since is the ref the diff was taken from. Empty means there was no prior
	// ship marker and the plan covers everything reachable — see detectShipTargets.
	Since string `json:"since"`
	Head  string `json:"head"`
	// Targets are deploy step names, ordered by shipDeployOrder.
	Targets []string `json:"targets"`
	// Changed is the raw changed-path list, kept so a human can see WHY a target
	// was chosen. A plan that says "testflight" without saying which file caused
	// it is a plan you cannot argue with.
	Changed []string `json:"changed"`
	// Reasons maps each target to the paths that selected it.
	Reasons map[string][]string `json:"reasons,omitempty"`
	// Unmapped are changed paths that imply no deploy (docs, tasks, tests).
	// Reported rather than dropped: a path everyone assumed was covered and
	// silently was not is exactly how a target goes missing for months.
	Unmapped []string `json:"unmapped,omitempty"`
}

// shipDeployOrder is the order targets deploy in, and it encodes a real
// dependency plus a real trade.
//
// Convex first: web and mobile may read a schema that has to exist before they
// ship. Mobile last: it is the slowest (a ~45m archive) and the most
// rate-limited, so it should not run before the cheap targets have proven the
// gate. The cost of that choice is that a mobile failure leaves the cheaper
// targets already shipped — accepted deliberately, because the inverse (shipping
// a mobile build against a backend that never landed) is worse and less
// recoverable.
var shipDeployOrder = []string{"convex", "web-cloudflare", "cli-npm", "testflight-ios", "playstore-android"}

func sortShipTargets(targets []string) []string {
	rank := map[string]int{}
	for i, t := range shipDeployOrder {
		rank[t] = i
	}
	out := append([]string(nil), targets...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, oki := rank[out[i]]
		rj, okj := rank[out[j]]
		if !oki || !okj {
			return out[i] < out[j]
		}
		return ri < rj
	})
	return out
}

// shipGitExec is the git seam, so the plan is testable without a repo.
var shipGitExec = func(ctx context.Context, workDir string, args ...string) (string, error) {
	res := autorunExec(ctx, "git", args, workDir)
	if res.Err != nil {
		return res.Output, res.Err
	}
	return res.Output, nil
}

// shipResolveSince returns the ref to diff from: the last ship marker, or empty
// when none exists.
func shipResolveSince(ctx context.Context, workDir string) string {
	if _, err := shipGitExec(ctx, workDir, "rev-parse", "--verify", shipLastTag); err != nil {
		return ""
	}
	return shipLastTag
}

// detectShipTargets computes what to deploy for the diff between the last ship
// and head.
//
// The first-ship case is deliberately NOT "deploy everything". With no marker
// there is no honest diff, and inventing one by deploying every target would
// make the very first ship the most expensive and most dangerous one — a full
// mobile release nobody asked for. Instead it returns an empty target set and
// says why, leaving the operator to pass explicit targets once and thereby
// create the marker.
func detectShipTargets(ctx context.Context, workDir, head string) (shipTargetPlan, error) {
	plan := shipTargetPlan{Head: head, Reasons: map[string][]string{}}
	since := shipResolveSince(ctx, workDir)
	plan.Since = since
	if since == "" {
		return plan, nil
	}
	out, err := shipGitExec(ctx, workDir, "diff", "--name-only", since+".."+head)
	if err != nil {
		return plan, fmt.Errorf("diff %s..%s: %w: %s", since, head, err, strings.TrimSpace(out))
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		plan.Changed = append(plan.Changed, path)
		targets := shipTargetsForPath(path)
		if len(targets) == 0 {
			plan.Unmapped = append(plan.Unmapped, path)
			continue
		}
		for _, t := range targets {
			if !seen[t] {
				seen[t] = true
				plan.Targets = append(plan.Targets, t)
			}
			plan.Reasons[t] = append(plan.Reasons[t], path)
		}
	}
	plan.Targets = sortShipTargets(plan.Targets)
	return plan, nil
}

// markShipped moves the ship marker to the deployed SHA.
//
// Force-moves the tag on purpose: it is a watermark, not history. It is written
// only after a successful deploy, so a failed ship leaves the previous marker
// standing and the next attempt re-detects the same targets — which is the
// behavior you want from a retry.
func markShipped(ctx context.Context, workDir, sha string) error {
	if out, err := shipGitExec(ctx, workDir, "tag", "-f", shipLastTag, sha); err != nil {
		return fmt.Errorf("tag %s: %w: %s", shipLastTag, err, strings.TrimSpace(out))
	}
	return nil
}
