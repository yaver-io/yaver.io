package main

// Generic external-MCP client: let a user register their own remote MCP servers
// (e.g. a private yaver-bet on Hetzner) and use them THROUGH Yaver. The agent
// becomes an MCP client — it lists each server's tools (namespaced
// "<server>__<tool>"), merges them into this agent's tools/list, and forwards
// tools/call to the right server. Nothing about the remote server is special-cased
// here; any JSON-RPC-over-HTTP MCP server works.
//
// Wiring (all additive):
//   - Config.ExternalMCPServers      (config.go)            persistence
//   - getMCPToolsList appends        externalMCPToolDefs()  (mcp_tools.go)
//   - tools/call default chain calls dispatchExternalMCP()  (httpserver.go)
//   - route /mcp/servers ->          handleMCPServers        (httpserver.go)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ExternalMCPServer is one user-registered remote MCP server.
type ExternalMCPServer struct {
	Name      string `json:"name"`                 // unique; the tool namespace
	URL       string `json:"url"`                  // JSON-RPC-over-HTTP MCP endpoint
	AuthToken string `json:"auth_token,omitempty"` // optional bearer
	Enabled   bool   `json:"enabled"`
}

const extMCPNamespaceSep = "__"

var (
	extMCPMu       sync.Mutex // guards config add/remove
	extMCPCache    sync.Map   // name -> *extToolEntry
	extMCPClient   = &http.Client{Timeout: 8 * time.Second}
	extMCPCacheTTL = 60 * time.Second
)

type extToolEntry struct {
	at    time.Time
	tools []map[string]interface{}
}

func enabledExternalServers() []ExternalMCPServer {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return nil
	}
	var out []ExternalMCPServer
	for _, s := range cfg.ExternalMCPServers {
		if s.Enabled && s.URL != "" && s.Name != "" {
			out = append(out, s)
		}
	}
	return out
}

func findExternalServer(name string) (ExternalMCPServer, bool) {
	cfg, err := LoadConfig()
	if err != nil || cfg == nil {
		return ExternalMCPServer{}, false
	}
	for _, s := range cfg.ExternalMCPServers {
		if s.Name == name {
			return s, true
		}
	}
	return ExternalMCPServer{}, false
}

// callExternalMCP does one JSON-RPC POST to a server and returns the `result`.
func callExternalMCP(srv ExternalMCPServer, method string, params interface{}) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if srv.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+srv.AuthToken)
	}
	resp, err := extMCPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return nil, err
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("%s", rpc.Error.Message)
	}
	return rpc.Result, nil
}

// fetchExternalTools returns a server's tools (cached with a short TTL).
func fetchExternalTools(srv ExternalMCPServer) ([]map[string]interface{}, error) {
	if v, ok := extMCPCache.Load(srv.Name); ok {
		if e := v.(*extToolEntry); time.Since(e.at) < extMCPCacheTTL {
			return e.tools, nil
		}
	}
	res, err := callExternalMCP(srv, "tools/list", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var wrap struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(res, &wrap); err != nil {
		return nil, err
	}
	extMCPCache.Store(srv.Name, &extToolEntry{at: time.Now(), tools: wrap.Tools})
	return wrap.Tools, nil
}

// externalMCPToolDefs returns every enabled server's tools, namespaced, ready to
// be merged into this agent's tools/list. Failures are skipped, never fatal.
func externalMCPToolDefs() []map[string]interface{} {
	var defs []map[string]interface{}
	for _, srv := range enabledExternalServers() {
		tools, err := fetchExternalTools(srv)
		if err != nil {
			continue
		}
		for _, t := range tools {
			name, _ := t["name"].(string)
			if name == "" {
				continue
			}
			nt := map[string]interface{}{
				"name":        srv.Name + extMCPNamespaceSep + name,
				"description": "[" + srv.Name + "] " + asString(t["description"]),
			}
			if sch, ok := t["inputSchema"]; ok {
				nt["inputSchema"] = sch
			}
			defs = append(defs, nt)
		}
	}
	return defs
}

// dispatchExternalMCP forwards a namespaced "<server>__<tool>" call to its server.
// Returns handled=false for anything that isn't an external tool.
func dispatchExternalMCP(s *HTTPServer, name string, args json.RawMessage) (bool, interface{}) {
	i := strings.Index(name, extMCPNamespaceSep)
	if i < 0 {
		return false, nil
	}
	srvName, tool := name[:i], name[i+len(extMCPNamespaceSep):]
	srv, ok := findExternalServer(srvName)
	if !ok || !srv.Enabled {
		return false, nil
	}
	var arguments interface{}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &arguments)
	}
	res, err := callExternalMCP(srv, "tools/call", map[string]interface{}{"name": tool, "arguments": arguments})
	if err != nil {
		return true, mcpToolError("external mcp '" + srvName + "': " + err.Error())
	}
	var out interface{}
	if err := json.Unmarshal(res, &out); err != nil {
		return true, mcpToolError("external mcp '" + srvName + "': bad response")
	}
	return true, out
}

// handleMCPServers is the REST API the web/mobile UI uses to manage servers:
//
//	GET    /mcp/servers              list (tokens redacted)
//	POST   /mcp/servers              add/update {name,url,auth_token?,enabled?}
//	POST   /mcp/servers?test=1       test a {url,auth_token?} without saving
//	DELETE /mcp/servers?name=<name>  remove
func (s *HTTPServer) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, _ := LoadConfig()
		var list []map[string]interface{}
		if cfg != nil {
			for _, srv := range cfg.ExternalMCPServers {
				n := 0
				if t, err := fetchExternalTools(srv); err == nil {
					n = len(t)
				}
				list = append(list, map[string]interface{}{
					"name": srv.Name, "url": srv.URL, "enabled": srv.Enabled,
					"hasAuth": srv.AuthToken != "", "toolCount": n,
				})
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"servers": list})

	case http.MethodPost:
		var in ExternalMCPServer
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.URL == "" {
			jsonError(w, http.StatusBadRequest, "need {name,url}")
			return
		}
		if r.URL.Query().Get("test") != "" {
			tools, err := fetchExternalTools(in)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "toolCount": len(tools)})
			return
		}
		if in.Name == "" {
			jsonError(w, http.StatusBadRequest, "need a name")
			return
		}
		extMCPMu.Lock()
		defer extMCPMu.Unlock()
		cfg, err := LoadConfig()
		if err != nil || cfg == nil {
			jsonError(w, http.StatusInternalServerError, "config load failed")
			return
		}
		found := false
		for i := range cfg.ExternalMCPServers {
			if cfg.ExternalMCPServers[i].Name == in.Name {
				if in.AuthToken == "" {
					in.AuthToken = cfg.ExternalMCPServers[i].AuthToken
				}
				cfg.ExternalMCPServers[i] = in
				found = true
				break
			}
		}
		if !found {
			cfg.ExternalMCPServers = append(cfg.ExternalMCPServers, in)
		}
		if err := SaveConfig(cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, "config save failed")
			return
		}
		extMCPCache.Delete(in.Name)
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})

	case http.MethodDelete:
		name := r.URL.Query().Get("name")
		if name == "" {
			jsonError(w, http.StatusBadRequest, "need ?name=")
			return
		}
		extMCPMu.Lock()
		defer extMCPMu.Unlock()
		cfg, err := LoadConfig()
		if err != nil || cfg == nil {
			jsonError(w, http.StatusInternalServerError, "config load failed")
			return
		}
		kept := cfg.ExternalMCPServers[:0]
		for _, srv := range cfg.ExternalMCPServers {
			if srv.Name != name {
				kept = append(kept, srv)
			}
		}
		cfg.ExternalMCPServers = kept
		if err := SaveConfig(cfg); err != nil {
			jsonError(w, http.StatusInternalServerError, "config save failed")
			return
		}
		extMCPCache.Delete(name)
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})

	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST/DELETE")
	}
}

func asString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
