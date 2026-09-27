package main

import (
	"strings"
	"testing"
)

func TestPublisherPlanNamesHumanGatesAndBrowserHandoff(t *testing.T) {
	profile := PublisherProfile{EntityType: "organization", LegalName: "Example Studio", PublisherName: "Example", CountryCode: "US", AddressLine1: "1 Main St", City: "Seattle", PostalCode: "98101", Website: "https://example.com", BusinessEmail: "dev@example.com", Phone: "+1 555 0100", DUNSNumber: "123456789"}
	plan, err := publisherPlan(profile, "ios")
	if err != nil {
		t.Fatal(err)
	}
	if plan["platform"] != "apple" || plan["profileReady"] != true {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	next := plan["nextTool"].(map[string]any)
	if next["name"] != "browser_interactive_start" {
		t.Fatalf("missing human browser handoff: %+v", next)
	}
}

func TestPublisherPlanRequiresDUNSForOrganizationStoreEnrollment(t *testing.T) {
	profile := PublisherProfile{EntityType: "organization"}
	missing := publisherMissingFields(profile, "apple")
	found := false
	for _, field := range missing {
		if field == "dunsNumber" {
			found = true
		}
	}
	if !found {
		t.Fatalf("organization plan did not require D-U-N-S: %v", missing)
	}
}

func TestPublisherPlatformAliases(t *testing.T) {
	for input, want := range map[string]string{"ios": "apple", "android": "google-play", "windows-store": "microsoft-store", "ps5": "playstation"} {
		if got := normalizePublisherPlatform(input); got != want {
			t.Fatalf("%s => %s, want %s", input, got, want)
		}
	}
}

func TestPublisherSetupRejectsCredentialShapedUnknownFields(t *testing.T) {
	_, err := decodePublisherSetupRequest([]byte(`{"op":"save","profile":{"legalName":"Example","privateKey":"must-not-be-accepted"}}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown credential field to fail closed, got %v", err)
	}
}
