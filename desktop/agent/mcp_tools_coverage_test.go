package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPToolsList_hasNoUnknownDispatcherEntries(t *testing.T) {
	wrapper, ok := (&HTTPServer{}).getMCPToolsList().(map[string]interface{})
	if !ok {
		t.Fatal("getMCPToolsList did not return a map wrapper")
	}
	tools, ok := wrapper["tools"].([]map[string]interface{})
	if !ok {
		t.Fatal("tools key is not []map[string]interface{}")
	}
	srv := &HTTPServer{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		raw, err := json.Marshal(map[string]interface{}{
			"name":      name,
			"arguments": map[string]interface{}{},
		})
		if err != nil {
			t.Fatalf("marshal %q: %v", name, err)
		}
		text := billingToolText(t, srv.handleMCPToolCall(raw))
		if strings.Contains(strings.ToLower(text), "unknown tool: "+strings.ToLower(name)) {
			t.Fatalf("tools/list advertises %q but dispatcher returned unknown tool", name)
		}
	}
}
