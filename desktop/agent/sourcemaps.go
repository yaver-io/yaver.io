package main

// sourcemaps.go — source-map upload + symbolication for the
// Errors dashboard. Solo-dev alternative to Sentry's paid symbol-
// server. Turns obfuscated stack frames like
//
//   at anonymous (index.android.bundle:1234:5678)
//
// into legible references like
//
//   at PurchaseScreen.purchase (src/screens/PurchaseScreen.tsx:42:10)
//
// Storage: ~/.yaver/sourcemaps/<app>/<version>/bundle.map
//
// Every upload carries an `app` (bundle name) + `version` (semver
// string or git SHA). On error ingest, we look up the map that
// matches the bundle filename referenced in the frame, parse the
// V3 JSON source-map VLQ mappings, and resolve frames to
// `<source>:<line>:<col>` entries.
//
// V3 spec: https://tc39.es/source-map-spec/

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// SourceMap is a trimmed V3 source-map record. Only the fields we
// need for symbolication are decoded — we skip sourceContent /
// names etc to keep memory small.
type SourceMap struct {
	Version  int      `json:"version"`
	Sources  []string `json:"sources"`
	Mappings string   `json:"mappings"`
	Names    []string `json:"names"`
}

// SourceMapEntry is one (line, col) -> (source, line, col, name)
// resolution record from the decoded mappings.
type SourceMapEntry struct {
	GenCol   int
	SrcIndex int
	SrcLine  int
	SrcCol   int
	NameIdx  int
}

// SourceMapTable is the parsed-once representation used by
// symbolicate. Lines[i] is a sorted list of entries for generated
// line i (1-indexed).
type SourceMapTable struct {
	Raw     *SourceMap
	Lines   [][]SourceMapEntry
	Sources []string
	Names   []string
}

// SourceMapStore keeps a file-backed index of uploaded maps.
// Concurrent safe.
type SourceMapStore struct {
	mu    sync.RWMutex
	cache map[string]*SourceMapTable // key: "<app>/<version>"
}

var (
	sourceMapStoreOnce sync.Once
	sourceMapStoreInst *SourceMapStore
)

func GlobalSourceMapStore() *SourceMapStore {
	sourceMapStoreOnce.Do(func() {
		sourceMapStoreInst = &SourceMapStore{cache: map[string]*SourceMapTable{}}
	})
	return sourceMapStoreInst
}

// sanitizeSourceMapName strips path separators + question marks
// so an untrusted app/version pair can't escape the sourcemaps
// dir or clobber anything else.
func sanitizeSourceMapName(s string) string {
	out := strings.Builder{}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			out.WriteRune(r)
		}
	}
	return out.String()
}

func sourceMapDir() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "sourcemaps")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// Upload stores a raw source-map on disk. Validates it's valid
// JSON + has mappings before writing so a corrupt upload doesn't
// corrupt the store.
func (s *SourceMapStore) Upload(app, version string, data []byte) error {
	var m SourceMap
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("invalid source-map JSON: %w", err)
	}
	if m.Mappings == "" {
		return fmt.Errorf("source-map has no `mappings` field")
	}
	dir, err := sourceMapDir()
	if err != nil {
		return err
	}
	app = sanitizeSourceMapName(app)
	version = sanitizeSourceMapName(version)
	if app == "" || version == "" {
		return fmt.Errorf("app and version are required")
	}
	target := filepath.Join(dir, app, version)
	if err := os.MkdirAll(target, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(target, "bundle.map"), data, 0600); err != nil {
		return err
	}

	// Invalidate cache for this key — next symbolicate() will
	// re-parse from disk.
	s.mu.Lock()
	delete(s.cache, app+"/"+version)
	s.mu.Unlock()
	return nil
}

// List returns every uploaded map grouped by app.
func (s *SourceMapStore) List() map[string][]string {
	out := map[string][]string{}
	dir, err := sourceMapDir()
	if err != nil {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		app := e.Name()
		versions := []string{}
		subs, _ := os.ReadDir(filepath.Join(dir, app))
		for _, v := range subs {
			if v.IsDir() {
				versions = append(versions, v.Name())
			}
		}
		out[app] = versions
	}
	return out
}

// Delete removes one uploaded map.
func (s *SourceMapStore) Delete(app, version string) error {
	dir, err := sourceMapDir()
	if err != nil {
		return err
	}
	app = sanitizeSourceMapName(app)
	version = sanitizeSourceMapName(version)
	path := filepath.Join(dir, app, version)
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.cache, app+"/"+version)
	s.mu.Unlock()
	return nil
}

// load resolves and parses a map from disk. Cached on first hit.
func (s *SourceMapStore) load(app, version string) (*SourceMapTable, error) {
	key := app + "/" + version
	s.mu.RLock()
	if t := s.cache[key]; t != nil {
		s.mu.RUnlock()
		return t, nil
	}
	s.mu.RUnlock()

	dir, err := sourceMapDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, app, version, "bundle.map")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw SourceMap
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	table := parseSourceMap(&raw)

	s.mu.Lock()
	s.cache[key] = table
	s.mu.Unlock()
	return table, nil
}

// Resolve returns the (source, line, col, name) for a generated
// position, or empty strings if no mapping. Line/col are
// 1-indexed on the way in.
func (s *SourceMapStore) Resolve(app, version string, line, col int) (src string, sline int, scol int, name string, ok bool) {
	t, err := s.load(app, version)
	if err != nil || t == nil {
		return "", 0, 0, "", false
	}
	if line < 1 || line > len(t.Lines) {
		return "", 0, 0, "", false
	}
	row := t.Lines[line-1]
	if len(row) == 0 {
		return "", 0, 0, "", false
	}
	// Binary-search for the largest entry.GenCol <= col.
	lo, hi := 0, len(row)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		if row[mid].GenCol <= col-1 {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if hi < 0 {
		return "", 0, 0, "", false
	}
	e := row[hi]
	if e.SrcIndex < 0 || e.SrcIndex >= len(t.Sources) {
		return "", 0, 0, "", false
	}
	src = t.Sources[e.SrcIndex]
	if e.NameIdx >= 0 && e.NameIdx < len(t.Names) {
		name = t.Names[e.NameIdx]
	}
	return src, e.SrcLine + 1, e.SrcCol + 1, name, true
}

// parseSourceMap decodes a V3 map's VLQ mappings field into a
// line-indexed table.
func parseSourceMap(raw *SourceMap) *SourceMapTable {
	t := &SourceMapTable{
		Raw:     raw,
		Sources: append([]string{}, raw.Sources...),
		Names:   append([]string{}, raw.Names...),
		Lines:   [][]SourceMapEntry{},
	}
	var srcIdx, srcLine, srcCol, nameIdx int
	lines := strings.Split(raw.Mappings, ";")
	for _, line := range lines {
		row := []SourceMapEntry{}
		var genCol int
		if line == "" {
			t.Lines = append(t.Lines, row)
			continue
		}
		for _, seg := range strings.Split(line, ",") {
			if seg == "" {
				continue
			}
			vals, ok := decodeVLQs(seg)
			if !ok {
				continue
			}
			genCol += vals[0]
			entry := SourceMapEntry{GenCol: genCol, NameIdx: -1}
			if len(vals) >= 4 {
				srcIdx += vals[1]
				srcLine += vals[2]
				srcCol += vals[3]
				entry.SrcIndex = srcIdx
				entry.SrcLine = srcLine
				entry.SrcCol = srcCol
			}
			if len(vals) >= 5 {
				nameIdx += vals[4]
				entry.NameIdx = nameIdx
			}
			row = append(row, entry)
		}
		t.Lines = append(t.Lines, row)
	}
	return t
}

// Base64 VLQ charset for V3 source maps.
var base64VLQAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// decodeVLQs decodes a string of base64 VLQ characters into a
// slice of signed ints (the values between the commas in the
// mappings field).
func decodeVLQs(s string) ([]int, bool) {
	var out []int
	var value, shift int
	for i := 0; i < len(s); i++ {
		idx := strings.IndexByte(base64VLQAlphabet, s[i])
		if idx < 0 {
			return nil, false
		}
		cont := (idx & 32) != 0
		value |= (idx & 31) << shift
		if cont {
			shift += 5
			continue
		}
		neg := (value & 1) == 1
		value >>= 1
		if neg {
			if value == 0 {
				value = -(1 << 30) // "negative zero" sentinel; safe for our offsets
			} else {
				value = -value
			}
		}
		out = append(out, value)
		value, shift = 0, 0
	}
	return out, true
}

// Symbolicate walks an error stack trace and rewrites each frame
// whose bundle filename matches one of the uploaded maps. Frames
// we can't resolve pass through unchanged.
//
// Recognised frame shapes:
//
//	"  at SomeFunc (index.android.bundle:1234:5678)"
//	"at anonymous (http://localhost/index.bundle:1234:5678)"
//	"index.android.bundle:1234:5678"
//
// The app/version pair defaults to the file's basename + "latest"
// when none is attached to the record.
func Symbolicate(app, version string, stack []string) []string {
	if len(stack) == 0 {
		return stack
	}
	store := GlobalSourceMapStore()
	out := make([]string, len(stack))
	for i, frame := range stack {
		out[i] = symbolicateFrame(store, app, version, frame)
	}
	return out
}

var frameRe = regexp.MustCompile(`\(?([^\s():]+):(\d+):(\d+)\)?`)

func symbolicateFrame(store *SourceMapStore, app, version, frame string) string {
	m := frameRe.FindStringSubmatch(frame)
	if m == nil {
		return frame
	}
	bundle := m[1]
	line, err := strconv.Atoi(m[2])
	if err != nil {
		return frame
	}
	col, err := strconv.Atoi(m[3])
	if err != nil {
		return frame
	}
	// Default app/version lookup — try the explicit pair first,
	// then a filename-derived key.
	tryApp := app
	tryVer := version
	if tryApp == "" {
		tryApp = bundleBasename(bundle)
	}
	if tryVer == "" {
		tryVer = "latest"
	}
	src, sline, scol, name, ok := store.Resolve(tryApp, tryVer, line, col)
	if !ok {
		return frame
	}
	if name == "" {
		return fmt.Sprintf("%s (%s:%d:%d)", frame, src, sline, scol)
	}
	return fmt.Sprintf("%s → %s:%d:%d (was %s)", name, src, sline, scol, frame)
}

func bundleBasename(p string) string {
	// Strip query strings and path segments so
	// "https://host/index.android.bundle?foo=bar" becomes
	// "index.android.bundle".
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// --- HTTP ----------------------------------------------------------

func (s *HTTPServer) handleSourceMaps(w http.ResponseWriter, r *http.Request) {
	store := GlobalSourceMapStore()
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":   true,
			"maps": store.List(),
		})
	case http.MethodPost:
		// multipart would be ideal, but solo-dev CLIs can't always
		// build multipart bodies easily — accept raw JSON body with
		// app + version + map fields.
		var body struct {
			App     string          `json:"app"`
			Version string          `json:"version"`
			Map     json.RawMessage `json:"map"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if len(body.Map) == 0 || body.App == "" || body.Version == "" {
			jsonError(w, http.StatusBadRequest, "app, version, map required")
			return
		}
		if err := store.Upload(body.App, body.Version, body.Map); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true})
	case http.MethodDelete:
		q := r.URL.Query()
		if err := store.Delete(q.Get("app"), q.Get("version")); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST/DELETE")
	}
}

// --- CLI -----------------------------------------------------------

func runSourceMaps(args []string) {
	if len(args) == 0 {
		printSourceMapsUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "upload":
		sourceMapUploadCmd(args[1:])
	case "list", "ls":
		sourceMapListCmd()
	case "delete", "rm":
		sourceMapDeleteCmd(args[1:])
	case "resolve":
		sourceMapResolveCmd(args[1:])
	case "help", "--help", "-h":
		printSourceMapsUsage()
	default:
		os.Stderr.WriteString("unknown sourcemaps subcommand: " + args[0] + "\n\n")
		printSourceMapsUsage()
		os.Exit(1)
	}
}

func printSourceMapsUsage() {
	os.Stdout.WriteString(`Yaver source maps — local symbolication for the Errors dashboard.

Usage:
  yaver sourcemaps upload <file> --app <name> --version <ver>
  yaver sourcemaps list
  yaver sourcemaps delete <app> <version>
  yaver sourcemaps resolve <app> <version> <line> <col>

Maps are stored under ~/.yaver/sourcemaps/<app>/<version>/bundle.map
and kept entirely local. On error ingest, the agent resolves
stack frames referencing <app>:<line>:<col> into source files.
`)
}

func sourceMapUploadCmd(args []string) {
	var path, app, version string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--app" && i+1 < len(args):
			app = args[i+1]
			i++
		case a == "--version" && i+1 < len(args):
			version = args[i+1]
			i++
		default:
			if !strings.HasPrefix(a, "--") && path == "" {
				path = a
			}
		}
	}
	if path == "" || app == "" || version == "" {
		os.Stderr.WriteString("usage: yaver sourcemaps upload <file> --app <name> --version <ver>\n")
		os.Exit(1)
	}
	f, err := os.Open(path)
	if err != nil {
		os.Stderr.WriteString("open: " + err.Error() + "\n")
		os.Exit(1)
	}
	defer f.Close()
	var buf strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		buf.WriteString(sc.Text())
		buf.WriteByte('\n')
	}
	if err := GlobalSourceMapStore().Upload(app, version, []byte(buf.String())); err != nil {
		os.Stderr.WriteString("upload: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("✓ uploaded " + app + "@" + version + "\n")
}

func sourceMapListCmd() {
	maps := GlobalSourceMapStore().List()
	if len(maps) == 0 {
		os.Stdout.WriteString("No source maps uploaded yet.\n")
		return
	}
	for app, versions := range maps {
		os.Stdout.WriteString(app + "\n")
		for _, v := range versions {
			os.Stdout.WriteString("  - " + v + "\n")
		}
	}
}

func sourceMapDeleteCmd(args []string) {
	if len(args) < 2 {
		os.Stderr.WriteString("usage: yaver sourcemaps delete <app> <version>\n")
		os.Exit(1)
	}
	if err := GlobalSourceMapStore().Delete(args[0], args[1]); err != nil {
		os.Stderr.WriteString("delete: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("✓ removed\n")
}

func sourceMapResolveCmd(args []string) {
	if len(args) < 4 {
		os.Stderr.WriteString("usage: yaver sourcemaps resolve <app> <version> <line> <col>\n")
		os.Exit(1)
	}
	line, _ := strconv.Atoi(args[2])
	col, _ := strconv.Atoi(args[3])
	src, sl, sc, name, ok := GlobalSourceMapStore().Resolve(args[0], args[1], line, col)
	if !ok {
		os.Stdout.WriteString("(no mapping)\n")
		return
	}
	if name != "" {
		os.Stdout.WriteString(name + " → ")
	}
	fmt.Fprintf(os.Stdout, "%s:%d:%d\n", src, sl, sc)
}
