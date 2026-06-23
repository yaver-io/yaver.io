package main

// publish_rollout_hook.go — the "build → upload → reach testers in one pass"
// wiring on top of the store_* verbs (appstoreconnect.go / playpublish_api.go).
//
// After a publish target uploads a binary, OPTIONALLY promote it to testers:
//   testflight → assign the latest build to a beta group
//   playstore  → roll the track's draft release out (status=completed)
//
// Opt-in via the target's env (rolloutAfterPublish=true) so default behaviour
// is unchanged. Best-effort: the binary already uploaded, so a promote failure
// is recorded in the run message, never fails the publish (e.g. Play's Console
// Foreground-Service declaration block returns a 403 here — surfaced, not fatal).

import (
	"fmt"
	"log"
)

// rolloutAfterPublish runs the optional post-upload promote. Returns a short
// human note (appended to the completion message) describing what happened.
func (pm *PublishManager) rolloutAfterPublish(run *PublishRun, target PublishTarget) string {
	env := target.Env
	if env == nil || !envTruthy(env["rolloutAfterPublish"]) {
		return ""
	}
	project := env["project"]
	switch target.Kind {
	case "testflight":
		bundleID := firstNonEmptyString(env["bundleId"], env["APP_BUNDLE_ID"])
		if bundleID == "" {
			log.Printf("[publish] rolloutAfterPublish: testflight needs bundleId in target env — skipping")
			return ""
		}
		cl, err := newASCClient(project)
		if err != nil {
			return rolloutNote("testflight", err)
		}
		app, err := cl.AppByBundleID(bundleID)
		if err != nil {
			return rolloutNote("testflight", err)
		}
		builds, err := cl.ListBuilds(app.ID)
		if err != nil || len(builds) == 0 {
			return rolloutNote("testflight", fmt.Errorf("no builds (%v)", err))
		}
		gid, err := resolveAppleGroupID(cl, app.ID, env["rolloutGroup"])
		if err != nil {
			return rolloutNote("testflight", err)
		}
		if err := cl.AssignBuildToGroup(gid, builds[0].ID); err != nil {
			return rolloutNote("testflight", err)
		}
		return fmt.Sprintf("; assigned build %s to beta group", builds[0].Version)
	case "playstore":
		pkg := firstNonEmptyString(env["packageName"], env["PLAY_PACKAGE_NAME"])
		if pkg == "" {
			log.Printf("[publish] rolloutAfterPublish: playstore needs packageName in target env — skipping")
			return ""
		}
		track := firstNonEmptyString(env["rolloutTrack"], "internal")
		status := firstNonEmptyString(env["rolloutStatus"], "completed")
		cl, err := newPlayClient(project, pkg)
		if err != nil {
			return rolloutNote("playstore", err)
		}
		if _, err := cl.PromoteRelease(track, status, 0); err != nil {
			return rolloutNote("playstore", err)
		}
		return fmt.Sprintf("; rolled %s track out (%s)", track, status)
	}
	return ""
}

func rolloutNote(store string, err error) string {
	log.Printf("[publish] rolloutAfterPublish %s: %v", store, err)
	return fmt.Sprintf("; rollout skipped (%v)", err)
}
