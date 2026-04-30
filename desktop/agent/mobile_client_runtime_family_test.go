package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMobileClientBuildNativeBundleWithContractSendsRuntimeFamilies(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/dev/build-native" {
			t.Fatalf("path = %s, want /dev/build-native", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status":"ok",
			"bundleUrl":"/dev/native-bundle?build=test",
			"size":1024,
			"md5":"0123456789abcdef0123456789abcdef",
			"bcVersion":96,
			"platform":"ios",
			"moduleName":"main",
			"runtimeFamilySelection":{
				"guest":{"packageName":"sfmg","expoVersion":"54.0.33","reactNativeVersion":"0.81.5","reactVersion":"19.2.5"},
				"selected":{"id":"family-b","label":"Family B","expoVersion":"54.0.33","reactNativeVersion":"0.81.5","reactVersion":"19.2.5","hermesBCVersion":96},
				"exactMatch":true,
				"matchKind":"exact"
			}
		}`))
	}))
	defer srv.Close()

	client := NewMobileClient(srv.URL, "test-token", srv.Client())
	contract := &NativeBuildConsumerContract{
		ConsumerVersion:              "1.18.15",
		ConsumerBuild:                "264",
		ConsumerSDKVersion:           "1.0.0",
		ConsumerHermesBCVersion:      96,
		ConsumerCurrentRuntimeFamily: "family-a",
		ConsumerDefaultRuntimeFamily: "family-a",
		ConsumerRuntimeFamilies: []RuntimeFamily{
			{ID: "family-a", Label: "Family A", CompiledIn: true},
			{ID: "family-b", Label: "Family B", CompiledIn: true, PreferredPackageNames: []string{"sfmg"}},
		},
	}

	res, err := client.BuildNativeBundleWithContract(context.Background(), "ios", contract)
	if err != nil {
		t.Fatalf("BuildNativeBundleWithContract: %v", err)
	}
	if res == nil || res.RuntimeFamilySelection == nil {
		t.Fatalf("expected runtime family selection in response")
	}
	if res.RuntimeFamilySelection.Selected.ID != "family-b" {
		t.Fatalf("selected family = %q, want family-b", res.RuntimeFamilySelection.Selected.ID)
	}
	if got["consumerCurrentRuntimeFamilyId"] != "family-a" {
		t.Fatalf("consumerCurrentRuntimeFamilyId = %#v, want family-a", got["consumerCurrentRuntimeFamilyId"])
	}
	if got["consumerDefaultRuntimeFamilyId"] != "family-a" {
		t.Fatalf("consumerDefaultRuntimeFamilyId = %#v, want family-a", got["consumerDefaultRuntimeFamilyId"])
	}
	families, ok := got["consumerRuntimeFamilies"].([]any)
	if !ok || len(families) != 2 {
		t.Fatalf("consumerRuntimeFamilies = %#v, want 2 entries", got["consumerRuntimeFamilies"])
	}
	second, ok := families[1].(map[string]any)
	if !ok {
		t.Fatalf("second runtime family = %#v", families[1])
	}
	if second["id"] != "family-b" {
		t.Fatalf("second family id = %#v, want family-b", second["id"])
	}
	preferred, ok := second["preferredPackageNames"].([]any)
	if !ok || len(preferred) != 1 || preferred[0] != "sfmg" {
		t.Fatalf("preferredPackageNames = %#v, want [sfmg]", second["preferredPackageNames"])
	}
}
