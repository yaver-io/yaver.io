package main

// ops_env.go — verb "env": read project / process environment
// variables. Write support is intentionally out-of-scope for this
// verb — env writes happen via `ops secrets` (for cloud secrets
// providers) or `ops files` (for a dotenv file on disk).

import (
	"encoding/json"
	"os"
	"strings"
)

type opsEnvPayload struct {
	// Op: "get" | "list". Defaults to "list".
	Op string `json:"op,omitempty"`
	// Key: variable name; required for op=get.
	Key string `json:"key,omitempty"`
	// Prefix: optional filter for op=list (case-insensitive).
	Prefix string `json:"prefix,omitempty"`
	// IncludeSensitive: default false — strips variables whose names
	// look like secrets (TOKEN/KEY/PASSWORD/SECRET). Agents that need
	// the raw values pass true explicitly.
	IncludeSensitive bool `json:"includeSensitive,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "env",
		Description: "Read the agent process environment. op=list returns variable names (+ values when includeSensitive=true); op=get reads a specific key. Writes are out-of-scope — use ops secrets or ops files.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"op":               map[string]interface{}{"type": "string", "enum": []string{"list", "get"}, "default": "list"},
				"key":              map[string]interface{}{"type": "string"},
				"prefix":           map[string]interface{}{"type": "string"},
				"includeSensitive": map[string]interface{}{"type": "boolean", "default": false},
			},
			"additionalProperties": false,
		},
		Handler:    opsEnvHandler,
		Streaming:  false,
		AllowGuest: false, // env can leak host identity + tokens
	})
}

func opsEnvHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsEnvPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
		}
	}
	op := strings.ToLower(p.Op)
	if op == "" {
		op = "list"
	}

	isSecret := func(name string) bool {
		u := strings.ToUpper(name)
		for _, needle := range []string{"TOKEN", "SECRET", "KEY", "PASSWORD", "PASSWD", "AUTH", "COOKIE"} {
			if strings.Contains(u, needle) {
				return true
			}
		}
		return false
	}

	switch op {
	case "get":
		if p.Key == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "key is required for op=get"}
		}
		if isSecret(p.Key) && !p.IncludeSensitive {
			return OpsResult{OK: false, Code: "unauthorized", Error: "that name looks sensitive — set includeSensitive=true to read it"}
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"key": p.Key, "value": os.Getenv(p.Key)}}
	case "list":
		prefix := strings.ToUpper(strings.TrimSpace(p.Prefix))
		out := make(map[string]interface{})
		raw := os.Environ()
		for _, kv := range raw {
			i := strings.IndexByte(kv, '=')
			if i <= 0 {
				continue
			}
			k, v := kv[:i], kv[i+1:]
			if prefix != "" && !strings.HasPrefix(strings.ToUpper(k), prefix) {
				continue
			}
			if isSecret(k) && !p.IncludeSensitive {
				out[k] = "(redacted; set includeSensitive=true to reveal)"
				continue
			}
			out[k] = v
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"count": len(out), "vars": out}}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "op must be list or get"}
	}
}
