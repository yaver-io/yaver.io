package main

import "testing"

func findCheck(r PublishReadiness, name string) (CheckStatus, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return CheckStatus{}, false
}

func TestPublishReadinessBlocksOnIosGaps(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x"}
	r := assemblePublishReadiness([]string{"NSCameraUsageDescription"}, nil, l, nil, true, true)
	if r.Ready {
		t.Error("missing iOS usage string must block readiness")
	}
	c, _ := findCheck(r, "permissions")
	if c.OK || !c.Blocker {
		t.Error("permissions check should be a failed blocker")
	}
}

func TestPublishReadinessBlocksOnMissingAssets(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x"}
	r := assemblePublishReadiness(nil, nil, l, []string{"iPhone 6.7\""}, true, true)
	if r.Ready {
		t.Error("missing required assets must block")
	}
}

func TestPublishReadinessBlocksOnMissingIdentity(t *testing.T) {
	l := StoreListing{AppName: "X"} // no bundle/package
	r := assemblePublishReadiness(nil, nil, l, nil, true, true)
	if r.Ready {
		t.Error("missing bundle/package id must block")
	}
}

func TestPublishReadinessReadyWhenComplete(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x", Privacy: []DataCollection{{Category: "Location"}}}
	r := assemblePublishReadiness(nil, nil, l, nil, true, true)
	if !r.Ready {
		t.Errorf("complete app should be ready, blockers=%v", r.Blockers)
	}
}

func TestPublishReadinessAuthAndAndroidAreWarnings(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x"}
	// Missing store auth + missing Android perms = NOT blockers (warnings).
	r := assemblePublishReadiness(nil, []string{"android.permission.CAMERA"}, l, nil, false, false)
	if !r.Ready {
		t.Errorf("auth + android-perm warnings should not block, blockers=%v", r.Blockers)
	}
	if c, _ := findCheck(r, "store-auth-apple"); c.OK {
		t.Error("apple auth should report not-OK when creds absent")
	}
}
