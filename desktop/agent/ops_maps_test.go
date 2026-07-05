package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMapsOpenRegistered(t *testing.T) {
	for _, v := range listOpsVerbs() {
		if v.Name == "maps_open" {
			return
		}
	}
	t.Fatalf("missing ops verb maps_open")
}

func TestMapsOpenBuildsGoogleDirections(t *testing.T) {
	payload, _ := json.Marshal(mapsOpenPayload{Provider: "google", Origin: "Kadikoy", Destination: "Taksim", Traffic: true, Surface: "car"})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "maps_open",
		Payload: payload,
	})
	if !res.OK {
		t.Fatalf("maps_open failed: %#v", res)
	}
	plan := res.Initial.(MapsOpenPlan)
	if plan.Provider != "google" || plan.Surface != "car" || !strings.Contains(plan.OpenURL, "google.com/maps/dir") {
		t.Fatalf("unexpected Google plan: %#v", plan)
	}
}

func TestMapsOpenBuildsYandexTrafficSearch(t *testing.T) {
	plan, err := buildMapsOpenPlan(mapsOpenPayload{Provider: "yandex", Query: "15 July Bridge", Traffic: true, OpenMode: "selenium"})
	if err != nil {
		t.Fatalf("buildMapsOpenPlan: %v", err)
	}
	if plan.Provider != "yandex" || plan.OpenMode != "browser" || !strings.Contains(plan.OpenURL, "yandex.com/maps") || !strings.Contains(plan.OpenURL, "l=map%2Ctrf") {
		t.Fatalf("unexpected Yandex plan: %#v", plan)
	}
}

func TestMapsOpenRequiresTarget(t *testing.T) {
	payload, _ := json.Marshal(mapsOpenPayload{Provider: "waze"})
	res := dispatchOps(OpsContext{Ctx: context.Background(), Caller: "owner"}, OpsRequest{
		Machine: "local",
		Verb:    "maps_open",
		Payload: payload,
	})
	if res.OK || res.Code != "bad_payload" {
		t.Fatalf("expected bad_payload, got %#v", res)
	}
}
