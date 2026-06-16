package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func resetExternalMCPCacheForTest() {
	extMCPCache.Range(func(key, _ any) bool {
		extMCPCache.Delete(key)
		return true
	})
}

func TestHandleMCPServersUpdatePreservesExistingAuthToken(t *testing.T) {
	resetExternalMCPCacheForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, configDirName), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	if err := SaveConfig(&Config{
		ExternalMCPServers: []ExternalMCPServer{{
			Name:      "bet",
			URL:       "https://mcp.example/old",
			AuthToken: "secret-token",
			Enabled:   true,
		}},
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	srv := &HTTPServer{}
	body, _ := json.Marshal(ExternalMCPServer{
		Name:    "bet",
		URL:     "https://mcp.example/new",
		Enabled: false,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/servers", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	srv.handleMCPServers(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /mcp/servers status = %d, body = %s", rec.Code, rec.Body.String())
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.ExternalMCPServers) != 1 {
		t.Fatalf("server count = %d, want 1", len(cfg.ExternalMCPServers))
	}
	got := cfg.ExternalMCPServers[0]
	if got.AuthToken != "secret-token" {
		t.Fatalf("auth token = %q, want preserved", got.AuthToken)
	}
	if got.URL != "https://mcp.example/new" {
		t.Fatalf("url = %q, want updated", got.URL)
	}
	if got.Enabled {
		t.Fatalf("enabled = true, want false")
	}
}

func TestExternalMCPListsAndDispatchesNamespacedRemoteTool(t *testing.T) {
	resetExternalMCPCacheForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, configDirName), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}

	var sawAuth string
	var sawTool string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		var rpc struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		switch rpc.Method {
		case "tools/list":
			writeJSON(w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]any{"tools": []map[string]any{{
					"name":        "place_bet",
					"description": "Place a test bet",
					"inputSchema": map[string]any{"type": "object"},
				}}},
			})
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(rpc.Params, &params); err != nil {
				t.Fatalf("decode params: %v", err)
			}
			sawTool = params.Name
			writeJSON(w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}},
			})
		default:
			t.Fatalf("unexpected method %q", rpc.Method)
		}
	}))
	defer remote.Close()

	if err := SaveConfig(&Config{
		ExternalMCPServers: []ExternalMCPServer{{
			Name:      "yaverbet",
			URL:       remote.URL,
			AuthToken: "remote-token",
			Enabled:   true,
		}},
	}); err != nil {
		t.Fatalf("save config: %v", err)
	}

	tools := externalMCPToolDefs()
	if len(tools) != 1 {
		t.Fatalf("tool count = %d, want 1: %#v", len(tools), tools)
	}
	if tools[0]["name"] != "yaverbet__place_bet" {
		t.Fatalf("tool name = %v, want yaverbet__place_bet", tools[0]["name"])
	}
	if sawAuth != "Bearer remote-token" {
		t.Fatalf("tools/list auth = %q, want bearer token", sawAuth)
	}

	handled, result := dispatchExternalMCP(&HTTPServer{}, "yaverbet__place_bet", json.RawMessage(`{"stake":10}`))
	if !handled {
		t.Fatal("external tool was not handled")
	}
	if sawTool != "place_bet" {
		t.Fatalf("remote tool = %q, want place_bet", sawTool)
	}
	if _, ok := result.(map[string]any); !ok {
		t.Fatalf("result type = %T, want map", result)
	}
}
