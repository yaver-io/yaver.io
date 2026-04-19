package main

// ops_files.go — verb "files": read / list / write / search files on the
// target machine. Thin wrapper around the existing file tools so
// agents have a single verb with one payload-discriminator instead of
// learning five specific MCP tool shapes.

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type opsFilesPayload struct {
	// Op: "read" | "list" | "write" | "delete" | "search".
	Op string `json:"op"`
	// Path: target for read/list/write/delete. Search uses Path as root.
	Path string `json:"path,omitempty"`
	// Content: UTF-8 content for write.
	Content string `json:"content,omitempty"`
	// Pattern: search pattern (regex). Fed to filepath.Match when IsGlob.
	Pattern string `json:"pattern,omitempty"`
	// IsGlob: if true, Pattern is a shell glob. Otherwise plain substring.
	IsGlob bool `json:"isGlob,omitempty"`
	// MaxBytes: truncate read at this many bytes (0 = no cap up to 1 MiB default).
	MaxBytes int64 `json:"maxBytes,omitempty"`
	// MaxResults: cap search/list entries.
	MaxResults int `json:"maxResults,omitempty"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "files",
		Description: "Read / list / write / delete / search files on the target machine. Op-discriminated: {op:\"read\",path:\"/foo\"} / {op:\"list\",path:\"/foo\"} / {op:\"write\",path,content} / {op:\"delete\",path} / {op:\"search\",path,pattern,isGlob?}.",
		Schema: map[string]interface{}{
			"type":     "object",
			"required": []string{"op"},
			"properties": map[string]interface{}{
				"op":         map[string]interface{}{"type": "string", "enum": []string{"read", "list", "write", "delete", "search"}},
				"path":       map[string]interface{}{"type": "string"},
				"content":    map[string]interface{}{"type": "string"},
				"pattern":    map[string]interface{}{"type": "string"},
				"isGlob":     map[string]interface{}{"type": "boolean"},
				"maxBytes":   map[string]interface{}{"type": "integer"},
				"maxResults": map[string]interface{}{"type": "integer"},
			},
			"additionalProperties": false,
		},
		Handler:    opsFilesHandler,
		Streaming:  false,
		AllowGuest: false, // filesystem access is owner-only
	})
}

func opsFilesHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p opsFilesPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	if p.Op == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "op is required"}
	}

	switch p.Op {
	case "read":
		if p.Path == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "path required"}
		}
		limit := p.MaxBytes
		if limit <= 0 {
			limit = 1 << 20 // 1 MiB
		}
		f, err := os.Open(p.Path)
		if err != nil {
			return fileErr(err)
		}
		defer f.Close()
		buf := make([]byte, limit+1)
		n, _ := f.Read(buf)
		truncated := int64(n) > limit
		if truncated {
			n = int(limit)
		}
		st, _ := f.Stat()
		out := map[string]interface{}{
			"path":      p.Path,
			"bytes":     n,
			"content":   string(buf[:n]),
			"truncated": truncated,
		}
		if st != nil {
			out["size"] = st.Size()
			out["mode"] = st.Mode().String()
			out["modTime"] = st.ModTime()
		}
		return OpsResult{OK: true, Initial: out}

	case "list":
		if p.Path == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "path required"}
		}
		entries, err := os.ReadDir(p.Path)
		if err != nil {
			return fileErr(err)
		}
		out := make([]map[string]interface{}, 0, len(entries))
		max := p.MaxResults
		if max <= 0 {
			max = 500
		}
		for i, e := range entries {
			if i >= max {
				break
			}
			info, _ := e.Info()
			var size int64
			if info != nil {
				size = info.Size()
			}
			out = append(out, map[string]interface{}{
				"name":  e.Name(),
				"isDir": e.IsDir(),
				"size":  size,
			})
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"path":       p.Path,
			"count":      len(out),
			"truncated":  len(entries) > max,
			"entries":    out,
		}}

	case "write":
		if p.Path == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "path required"}
		}
		// Create parent dirs the caller implicitly expects. No-op if
		// they already exist.
		if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
			return fileErr(err)
		}
		if err := os.WriteFile(p.Path, []byte(p.Content), 0o644); err != nil {
			return fileErr(err)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"path": p.Path, "bytes": len(p.Content)}}

	case "delete":
		if p.Path == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "path required"}
		}
		if err := os.Remove(p.Path); err != nil {
			return fileErr(err)
		}
		return OpsResult{OK: true, Initial: map[string]interface{}{"path": p.Path, "deleted": true}}

	case "search":
		root := p.Path
		if root == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "path required"}
		}
		if p.Pattern == "" {
			return OpsResult{OK: false, Code: "bad_payload", Error: "pattern required for op=search"}
		}
		max := p.MaxResults
		if max <= 0 {
			max = 200
		}
		hits := []map[string]interface{}{}
		walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // best effort — skip unreadable entries
			}
			if d.IsDir() {
				// Skip obvious noise dirs that cost a lot and rarely help.
				base := d.Name()
				if base == ".git" || base == "node_modules" || base == ".next" || base == "dist" || base == "target" {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			match := false
			if p.IsGlob {
				m, _ := filepath.Match(p.Pattern, name)
				match = m
			} else {
				match = strings.Contains(name, p.Pattern)
			}
			if match {
				hits = append(hits, map[string]interface{}{"path": path})
				if len(hits) >= max {
					return errHaltWalk
				}
			}
			return nil
		})
		truncated := errors.Is(walkErr, errHaltWalk)
		return OpsResult{OK: true, Initial: map[string]interface{}{
			"root":      root,
			"count":     len(hits),
			"truncated": truncated,
			"hits":      hits,
		}}
	default:
		return OpsResult{OK: false, Code: "bad_payload", Error: "unknown op: " + p.Op}
	}
}

var errHaltWalk = errors.New("halt")

func fileErr(err error) OpsResult {
	switch {
	case os.IsNotExist(err):
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	case os.IsPermission(err):
		return OpsResult{OK: false, Code: "unauthorized", Error: err.Error()}
	default:
		return OpsResult{OK: false, Code: "io_error", Error: err.Error()}
	}
}
