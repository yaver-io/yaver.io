package main

import "testing"

func TestRelayURLToPathStyle(t *testing.T) {
	cases := map[string]string{
		// Canonical relay subdomain → rewrite to path-style
		"https://229aeb03-b877-41aa-ba60-2daf785cd4a5.yaver.io":  "https://public.yaver.io/d/229aeb03-b877-41aa-ba60-2daf785cd4a5",
		"https://2859819c-23cf-444f-ac7c-fc41b81c394e.yaver.io/": "https://public.yaver.io/d/2859819c-23cf-444f-ac7c-fc41b81c394e",
		"http://abc.yaver.io":                                    "http://public.yaver.io/d/abc",
		// Already path-style — leave unchanged
		"https://public.yaver.io/d/229aeb03": "https://public.yaver.io/d/229aeb03",
		// Non-yaver.io domain — user-configured tunnel/CNAME — leave alone
		"https://mybox.example.com":         "https://mybox.example.com",
		"https://yaver-via-tunnel.acme.org": "https://yaver-via-tunnel.acme.org",
		// Garbage / empty
		"":     "",
		"  ":   "",
		"not a url": "not a url",
	}
	for in, want := range cases {
		got := relayURLToPathStyle(in)
		if got != want {
			t.Errorf("relayURLToPathStyle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetAssignedRelayURL_RewritesOnStore(t *testing.T) {
	defer setAssignedRelayURL("") // tidy
	setAssignedRelayURL("https://229aeb03-b877-41aa-ba60-2daf785cd4a5.yaver.io")
	got := getAssignedRelayURL()
	want := "https://public.yaver.io/d/229aeb03-b877-41aa-ba60-2daf785cd4a5"
	if got != want {
		t.Fatalf("after setAssignedRelayURL(subdomain), got %q, want %q", got, want)
	}
}
