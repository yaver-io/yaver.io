package main

// ops_artifact.go — the artifact-return channel.
//
// THE GAP (docs/yaver-remote-pc-operation-audit.md §Gap 6, still open):
// a compiled PDF, a .dwg, a screenshot, or a build output had no clean way home.
// `/files/read` caps at 2 MB and returns text; `/files/raw` streams inline but
// only beneath a DISCOVERED PROJECT ROOT; the only byte-returning verb
// (`cad_get`) was hard-scoped to ~/.yaver/cad/. So "build the report and send it
// to me" dead-ended.
//
// THE CHANNEL: `artifact_fetch` returns a bounded file's bytes (base64) plus
// mime/size/sha256. It is confined to an explicit allowlist of roots — the
// discovered project roots, a dedicated ~/.yaver/artifacts dir, and any
// operator-added YAVER_ARTIFACT_ROOTS. Symlinks are resolved on BOTH sides, so a
// link that lives inside a root but points outside it is refused. It deliberately
// does NOT expose all of ~/.yaver: that directory holds the vault, master key
// and local-only screenlog frames, and a remote `ops(machine=B)` call must not
// be able to read another machine's secrets/screens.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// artifactMaxBytes caps a single fetch. 12 MB fits a PDF/screenshot/asset
// without turning an MCP text result into a memory event.
const artifactMaxBytes = 12 << 20

func init() {
	registerOpsVerb(opsVerbSpec{
		Name: "artifact_fetch",
		Description: "Return a file's bytes from an allowed location so a compiled PDF / .dwg / screenshot / build " +
			"output can reach the caller. Confined to discovered project roots, ~/.yaver/artifacts, and " +
			"YAVER_ARTIFACT_ROOTS. Returns base64 + mime + size + sha256, bounded at 12 MB.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"path":     map[string]interface{}{"type": "string", "description": "Absolute path (or ~/…) inside an allowed root."},
			"maxBytes": map[string]interface{}{"type": "integer", "description": "Optional lower cap (default and hard max: 12 MB)."},
		}, "path"),
		Handler:        artifactFetchHandler,
		AllowCompanion: false,
	})
}

// artifactDir is the dedicated write-then-fetch staging dir. Agents that produce
// an out-of-tree artifact copy it here; the owner can also drop files in.
func artifactDir() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "artifacts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// artifactRoots returns the resolved allowlist. Symlinks are resolved so a
// comparison against the resolved target is meaningful.
func artifactRoots() []string {
	var raw []string
	if dir, err := artifactDir(); err == nil && dir != "" {
		raw = append(raw, dir)
	}
	for _, p := range listDiscoveredProjects() {
		if p.Path != "" {
			raw = append(raw, p.Path)
		}
	}
	for _, r := range strings.Split(os.Getenv("YAVER_ARTIFACT_ROOTS"), string(os.PathListSeparator)) {
		if r = strings.TrimSpace(r); r != "" {
			raw = append(raw, r)
		}
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, r := range raw {
		if abs, err := filepath.Abs(r); err == nil {
			r = abs
		}
		if rr, err := filepath.EvalSymlinks(r); err == nil {
			r = rr
		}
		if r == "" || seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

var errArtifactForbidden = errors.New("path is outside the allowed artifact roots")

// resolveArtifactPath confines p to the allowlist. Pure apart from the
// filesystem checks, so it is unit-tested directly.
func resolveArtifactPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("path is required")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved := abs
	if rr, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = rr
	}
	for _, root := range artifactRoots() {
		if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return resolved, nil
		}
	}
	return "", errArtifactForbidden
}

func artifactFetchHandler(_ OpsContext, payload json.RawMessage) OpsResult {
	var p struct {
		Path     string `json:"path"`
		MaxBytes int    `json:"maxBytes"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	abs, err := resolveArtifactPath(p.Path)
	if err != nil {
		if errors.Is(err, errArtifactForbidden) {
			return OpsResult{OK: false, Code: "forbidden", Error: err.Error() +
				" — allowed roots are discovered project roots, ~/.yaver/artifacts, and YAVER_ARTIFACT_ROOTS"}
		}
		return OpsResult{OK: false, Code: "bad_payload", Error: err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	if info.IsDir() {
		return OpsResult{OK: false, Code: "bad_payload", Error: "path is a directory"}
	}
	if !info.Mode().IsRegular() {
		return OpsResult{OK: false, Code: "bad_payload", Error: "not a regular file"}
	}
	maxBytes := p.MaxBytes
	if maxBytes <= 0 || maxBytes > artifactMaxBytes {
		maxBytes = artifactMaxBytes
	}
	if info.Size() > int64(maxBytes) {
		return OpsResult{OK: false, Code: "too_large", Error: fmt.Sprintf(
			"file is %d bytes; cap is %d (raise maxBytes up to %d)", info.Size(), maxBytes, artifactMaxBytes)}
	}
	buf, err := os.ReadFile(abs)
	if err != nil {
		return OpsResult{OK: false, Code: "read_failed", Error: err.Error()}
	}
	sum := sha256.Sum256(buf)
	return OpsResult{OK: true, Initial: map[string]interface{}{
		"path":   abs,
		"name":   filepath.Base(abs),
		"size":   len(buf),
		"mime":   mimeForExt(strings.ToLower(filepath.Ext(abs))),
		"sha256": fmt.Sprintf("%x", sum[:]),
		"base64": base64.StdEncoding.EncodeToString(buf),
	}}
}

// mimeForExt maps a file extension to a content type. Extracted so the artifact
// channel and the files browser agree.
func mimeForExt(ext string) string {
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	case ".pdf":
		return "application/pdf"
	case ".json":
		return "application/json"
	case ".txt", ".md", ".log":
		return "text/plain"
	case ".mp4":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	}
	return "application/octet-stream"
}
