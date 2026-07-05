package main

import (
	"strings"
	"testing"
)

func TestSeleniumSearchURL(t *testing.T) {
	got := seleniumSearchURL("google", "hasan arda kasikci live")
	if got != "https://www.google.com/search?q=hasan+arda+kasikci+live" {
		t.Fatalf("google search URL = %q", got)
	}
	got = seleniumSearchURL("ddg", "teams meeting")
	if got != "https://duckduckgo.com/?q=teams+meeting" {
		t.Fatalf("ddg search URL = %q", got)
	}
}

func TestSeleniumMCPToolsRegistered(t *testing.T) {
	wrapper, ok := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	if !ok {
		t.Fatal("getMCPToolsList did not return wrapper")
	}
	tools, ok := wrapper["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("tools has unexpected type %T", wrapper["tools"])
	}
	want := map[string]bool{
		"selenium_status":     false,
		"selenium_start":      false,
		"selenium_search":     false,
		"selenium_text":       false,
		"selenium_screenshot": false,
		"selenium_close":      false,
	}
	for _, tool := range tools {
		if _, exists := want[tool["name"].(string)]; exists {
			want[tool["name"].(string)] = true
			desc, _ := tool["description"].(string)
			if tool["name"] == "selenium_search" && !strings.Contains(strings.ToLower(desc), "google") {
				t.Fatalf("selenium_search description should mention Google, got %q", desc)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("missing selenium MCP tool %s", name)
		}
	}
}
