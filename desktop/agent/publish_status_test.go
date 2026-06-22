package main

import (
	"strings"
	"testing"
)

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
	r := assemblePublishReadiness([]string{"NSCameraUsageDescription"}, nil, l, nil, true, true, nil)
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
	r := assemblePublishReadiness(nil, nil, l, []string{"iPhone 6.7\""}, true, true, nil)
	if r.Ready {
		t.Error("missing required assets must block")
	}
}

func TestPublishReadinessBlocksOnMissingIdentity(t *testing.T) {
	l := StoreListing{AppName: "X"} // no bundle/package
	r := assemblePublishReadiness(nil, nil, l, nil, true, true, nil)
	if r.Ready {
		t.Error("missing bundle/package id must block")
	}
}

func TestPublishReadinessReadyWhenComplete(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x", Privacy: []DataCollection{{Category: "Location"}}}
	r := assemblePublishReadiness(nil, nil, l, nil, true, true, nil)
	if !r.Ready {
		t.Errorf("complete app should be ready, blockers=%v", r.Blockers)
	}
}

func TestPublishReadinessBlocksOnMissingPermissionVideo(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x"}
	pv := &permVideoCheck{Needed: true, Perm: "FOREGROUND_SERVICE_SPECIAL_USE"}
	r := assemblePublishReadiness(nil, nil, l, nil, true, true, pv)
	if r.Ready {
		t.Error("a declared FGS special-use permission with no video must block")
	}
	c, ok := findCheck(r, "permission-video")
	if !ok || c.OK || !c.Blocker {
		t.Errorf("permission-video should be a failed blocker, got %+v ok=%v", c, ok)
	}
	if !strings.Contains(c.Detail, "yaver studio permission-video") {
		t.Errorf("detail should name the generate command: %s", c.Detail)
	}
}

func TestPublishReadinessReadyWhenPermissionVideoPresent(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x"}
	pv := &permVideoCheck{Needed: true, Perm: "FOREGROUND_SERVICE_SPECIAL_USE", HaveVideo: true, VideoPath: "/tmp/v.mp4"}
	r := assemblePublishReadiness(nil, nil, l, nil, true, true, pv)
	if !r.Ready {
		t.Errorf("with the video present it should be ready, blockers=%v", r.Blockers)
	}
	if c, _ := findCheck(r, "permission-video"); !c.OK {
		t.Error("permission-video check should pass when video present")
	}
}

func TestPublishReadinessAuthAndAndroidAreWarnings(t *testing.T) {
	l := StoreListing{AppName: "X", BundleID: "com.x", PackageName: "com.x"}
	// Missing store auth + missing Android perms = NOT blockers (warnings).
	r := assemblePublishReadiness(nil, []string{"android.permission.CAMERA"}, l, nil, false, false, nil)
	if !r.Ready {
		t.Errorf("auth + android-perm warnings should not block, blockers=%v", r.Blockers)
	}
	if c, _ := findCheck(r, "store-auth-apple"); c.OK {
		t.Error("apple auth should report not-OK when creds absent")
	}
}
