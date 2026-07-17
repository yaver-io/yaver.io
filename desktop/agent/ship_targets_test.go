package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestShipTargetsForPath(t *testing.T) {
	cases := []struct {
		path string
		want []string
		why  string
	}{
		{"backend/convex/schema.ts", []string{"convex"}, ""},
		{"web/app/page.tsx", []string{"web-cloudflare"}, ""},
		{"cli/src/bundler.js", []string{"cli-npm"}, ""},
		{"desktop/agent/ship.go", []string{"cli-npm"}, "the agent binary is distributed by the npm package"},
		{"mobile/app/index.tsx", []string{"testflight-ios", "playstore-android"}, ""},
		// The expensive negatives. TestFlight is ~15-20 uploads/day with no
		// rollback, so a docs change must never reach it.
		{"docs/architecture/SHIP_BARRIER.md", nil, "docs deploy nothing"},
		{"README.md", nil, "root docs deploy nothing"},
		{"tasks/whatever.md", nil, "task files deploy nothing"},
		{"relay/main.go", nil, "relay is not a deploy step"},
	}
	for _, c := range cases {
		got := shipTargetsForPath(c.path)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("shipTargetsForPath(%q) = %v, want %v  %s", c.path, got, c.want, c.why)
		}
	}
}

// Convex before mobile: web and mobile may read a schema that has to land first.
// Mobile last: slowest and most rate-limited.
func TestSortShipTargetsFollowsDependencyOrder(t *testing.T) {
	got := sortShipTargets([]string{"testflight-ios", "cli-npm", "convex", "web-cloudflare"})
	want := []string{"convex", "web-cloudflare", "cli-npm", "testflight-ios"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deploy order = %v, want %v", got, want)
	}
}

func withStubGit(t *testing.T, fn func(ctx context.Context, workDir string, args ...string) (string, error)) {
	t.Helper()
	orig := shipGitExec
	shipGitExec = fn
	t.Cleanup(func() { shipGitExec = orig })
}

func TestDetectShipTargetsMapsDiffToTargets(t *testing.T) {
	withStubGit(t, func(_ context.Context, _ string, args ...string) (string, error) {
		switch {
		case args[0] == "rev-parse":
			return "tagsha\n", nil
		case args[0] == "diff":
			return "backend/convex/schema.ts\nmobile/app/index.tsx\ndocs/x.md\n", nil
		}
		return "", nil
	})
	plan, err := detectShipTargets(context.Background(), "/repo", "headsha")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"convex", "testflight-ios", "playstore-android"}
	if !reflect.DeepEqual(plan.Targets, want) {
		t.Fatalf("targets = %v, want %v", plan.Targets, want)
	}
	// A plan that says "testflight" without saying which file caused it is a plan
	// you cannot argue with.
	if got := plan.Reasons["convex"]; !reflect.DeepEqual(got, []string{"backend/convex/schema.ts"}) {
		t.Fatalf("reasons[convex] = %v", got)
	}
	if !reflect.DeepEqual(plan.Unmapped, []string{"docs/x.md"}) {
		t.Fatalf("unmapped = %v; a path that deploys nothing must be reported, not silently dropped", plan.Unmapped)
	}
}

// A docs-only diff must deploy nothing at all. This is the test that stands
// between a comment change and a burned TestFlight upload.
func TestDetectShipTargetsDocsOnlyDeploysNothing(t *testing.T) {
	withStubGit(t, func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "rev-parse" {
			return "tagsha\n", nil
		}
		return "README.md\ndocs/a.md\ntasks/b.md\n", nil
	})
	plan, err := detectShipTargets(context.Background(), "/repo", "headsha")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 0 {
		t.Fatalf("a docs-only diff selected %v; it must deploy nothing", plan.Targets)
	}
}

// With no ship/last marker there is no honest diff. Inventing one by deploying
// everything would make the very first ship the most expensive and most
// dangerous one — a full mobile release nobody asked for.
func TestDetectShipTargetsRefusesToInferAFirstShip(t *testing.T) {
	withStubGit(t, func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "rev-parse" {
			return "", errStubNoTag{}
		}
		t.Fatalf("must not diff when there is no marker; called with %v", args)
		return "", nil
	})
	plan, err := detectShipTargets(context.Background(), "/repo", "headsha")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Since != "" {
		t.Fatalf("since = %q, want empty to signal no marker", plan.Since)
	}
	if len(plan.Targets) != 0 {
		t.Fatalf("first ship selected %v; it must refuse to infer and let the operator choose once", plan.Targets)
	}
}

type errStubNoTag struct{}

func (errStubNoTag) Error() string { return "fatal: Needed a single revision" }

// The watermark must move only on success, so a failed ship re-detects the same
// targets on retry.
func TestMarkShippedForceMovesTheTag(t *testing.T) {
	var got []string
	withStubGit(t, func(_ context.Context, _ string, args ...string) (string, error) {
		got = args
		return "", nil
	})
	if err := markShipped(context.Background(), "/repo", "deadbeef"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "tag -f "+shipLastTag+" deadbeef" {
		t.Fatalf("markShipped ran %v", got)
	}
}
