package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMediaOpenRegistered(t *testing.T) {
	for _, v := range listOpsVerbs() {
		if v.Name == "media_open" {
			return
		}
	}
	t.Fatalf("missing ops verb media_open")
}

func TestMediaOpenBuildsYouTubeLiveSearch(t *testing.T) {
	payload, _ := json.Marshal(mediaOpenPayload{Provider: "youtube", Query: "Hasan Arda Kasikci", Live: true, Surface: "car", Open: false})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "media_open",
		Payload: payload,
	})
	if !res.OK {
		t.Fatalf("media_open failed: %#v", res)
	}
	plan := res.Initial.(MediaOpenPlan)
	if plan.Provider != "youtube" || plan.Surface != "car" || !plan.Live {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if !strings.Contains(plan.OpenURL, "youtube.com/results") || !strings.Contains(plan.OpenURL, "Hasan+Arda+Kasikci+live") {
		t.Fatalf("unexpected YouTube search URL: %s", plan.OpenURL)
	}
}

func TestMediaOpenBuildsTwitchSearch(t *testing.T) {
	plan, err := buildMediaOpenPlan(mediaOpenPayload{Provider: "twitch", Query: "hasanabi", OpenMode: "selenium"})
	if err != nil {
		t.Fatalf("buildMediaOpenPlan: %v", err)
	}
	if plan.Provider != "twitch" || plan.OpenMode != "browser" || !strings.Contains(plan.OpenURL, "twitch.tv/search") {
		t.Fatalf("unexpected Twitch plan: %#v", plan)
	}
}

func TestMediaOpenRejectsUnsafeURL(t *testing.T) {
	payload, _ := json.Marshal(mediaOpenPayload{URL: "javascript:alert(1)"})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "media_open",
		Payload: payload,
	})
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected bad_payload, got %#v", res)
	}
}
