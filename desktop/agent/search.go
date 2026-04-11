package main

// search.go — a tiny pure-Go inverted index for the
// `yaver search` command. Solo-dev alternative to Algolia /
// Meilisearch / Typesense for indexing whatever content the
// dev's own app produces (blog posts, product rows, docs).
//
// Why pure-Go instead of SQLite FTS5? CGo. The agent ships as
// a single static binary on every platform (`brew install
// yaver`, `scoop install yaver`, Docker alpine) and adding a
// CGo dependency doubles the build matrix + breaks on musl.
// At solo-dev scale (thousands of docs, not millions) a plain
// map[string]map[string]int inverted index is plenty fast and
// fits in memory in under a megabyte.
//
// Indexes live at ~/.yaver/search/<index>/segments.json.
// Segments are append-only JSON files that get rewritten on
// compaction. This is intentionally simple — if the dev
// outgrows it, they're large enough to warrant a proper
// search backend and the switch is an HTTP shape change.
//
// HTTP surface:
//
//   POST   /search/<index>/docs      — upsert a document
//   DELETE /search/<index>/docs/<id> — remove a document
//   GET    /search/<index>?q=...     — query
//   GET    /search                   — list indexes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// SearchDocument is the dev-supplied record. Fields get joined
// into a single indexed text blob; the tokenizer doesn't care
// about field boundaries at query time.
type SearchDocument struct {
	ID     string                 `json:"id"`
	Title  string                 `json:"title,omitempty"`
	Body   string                 `json:"body,omitempty"`
	Tags   []string               `json:"tags,omitempty"`
	Extra  map[string]interface{} `json:"extra,omitempty"`
	AddedAt string                `json:"addedAt,omitempty"`
}

// searchIndex is one on-disk index. Posting list keyed by
// token → set of doc IDs, with per-doc metadata kept alongside.
type searchIndex struct {
	mu       sync.Mutex
	name     string
	path     string
	tokens   map[string]map[string]int // token → docID → freq
	docs     map[string]SearchDocument
}

var (
	searchStoreMu sync.Mutex
	searchIndexes = map[string]*searchIndex{}
)

func searchDir() (string, error) {
	base, err := ConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "search")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

// openSearchIndex loads (or creates) one index by name.
func openSearchIndex(name string) (*searchIndex, error) {
	if strings.ContainsAny(name, "/\\") || name == "" {
		return nil, fmt.Errorf("invalid index name")
	}
	searchStoreMu.Lock()
	defer searchStoreMu.Unlock()
	if idx, ok := searchIndexes[name]; ok {
		return idx, nil
	}
	root, err := searchDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	idx := &searchIndex{
		name:   name,
		path:   filepath.Join(dir, "segments.json"),
		tokens: map[string]map[string]int{},
		docs:   map[string]SearchDocument{},
	}
	_ = idx.loadLocked()
	searchIndexes[name] = idx
	return idx, nil
}

func (idx *searchIndex) loadLocked() error {
	data, err := os.ReadFile(idx.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		Docs   map[string]SearchDocument   `json:"docs"`
		Tokens map[string]map[string]int   `json:"tokens"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.Docs != nil {
		idx.docs = payload.Docs
	}
	if payload.Tokens != nil {
		idx.tokens = payload.Tokens
	}
	return nil
}

func (idx *searchIndex) saveLocked() error {
	data, err := json.MarshalIndent(map[string]interface{}{
		"docs":      idx.docs,
		"tokens":    idx.tokens,
		"updatedAt": time.Now().UnixMilli(),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := idx.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, idx.path)
}

// Upsert adds or replaces a document. Re-indexes tokens
// against the new body.
func (idx *searchIndex) Upsert(doc SearchDocument) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if doc.ID == "" {
		return fmt.Errorf("doc.id required")
	}
	// Remove old postings for this ID.
	if _, existed := idx.docs[doc.ID]; existed {
		idx.removePostingsLocked(doc.ID)
	}
	if doc.AddedAt == "" {
		doc.AddedAt = time.Now().UTC().Format(time.RFC3339)
	}
	idx.docs[doc.ID] = doc

	// Tokenize the combined field blob.
	blob := doc.Title + " " + doc.Body + " " + strings.Join(doc.Tags, " ")
	for _, v := range doc.Extra {
		if sv, ok := v.(string); ok {
			blob += " " + sv
		}
	}
	for _, tok := range tokenize(blob) {
		if idx.tokens[tok] == nil {
			idx.tokens[tok] = map[string]int{}
		}
		idx.tokens[tok][doc.ID]++
	}
	return idx.saveLocked()
}

// Delete removes a document and its postings.
func (idx *searchIndex) Delete(id string) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if _, ok := idx.docs[id]; !ok {
		return nil
	}
	idx.removePostingsLocked(id)
	delete(idx.docs, id)
	return idx.saveLocked()
}

// removePostingsLocked cleans every token→doc entry for a
// given doc ID. Called before Upsert (to replace an existing
// doc) and on Delete.
func (idx *searchIndex) removePostingsLocked(id string) {
	for tok, postings := range idx.tokens {
		if _, ok := postings[id]; ok {
			delete(postings, id)
			if len(postings) == 0 {
				delete(idx.tokens, tok)
			}
		}
	}
}

// Query does a naive TF-based scoring: sum frequencies across
// matching tokens, order by score desc. Returns the top `limit`
// docs with their match snippet.
func (idx *searchIndex) Query(q string, limit int) []SearchHit {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	tokens := tokenize(q)
	if len(tokens) == 0 {
		return nil
	}
	score := map[string]int{}
	for _, tok := range tokens {
		postings := idx.tokens[tok]
		if postings == nil {
			// Prefix fallback — lets "ya" match "yaver".
			for t, p := range idx.tokens {
				if strings.HasPrefix(t, tok) {
					for id, f := range p {
						score[id] += f
					}
				}
			}
			continue
		}
		for id, f := range postings {
			score[id] += f * 2 // exact matches weighted higher
		}
	}
	hits := make([]SearchHit, 0, len(score))
	for id, s := range score {
		hits = append(hits, SearchHit{ID: id, Score: s, Doc: idx.docs[id]})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Doc.AddedAt > hits[j].Doc.AddedAt
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// SearchHit is one query result row.
type SearchHit struct {
	ID    string         `json:"id"`
	Score int            `json:"score"`
	Doc   SearchDocument `json:"doc"`
}

// tokenize lowercases + splits on non-letter/digit + filters
// tiny tokens. Good enough for the "find that blog post I
// wrote" use case; a follow-up can land stemming (snowball) if
// the solo dev asks for it.
func tokenize(s string) []string {
	tokens := []string{}
	var buf strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(unicode.ToLower(r))
			continue
		}
		if buf.Len() > 1 {
			tokens = append(tokens, buf.String())
		}
		buf.Reset()
	}
	if buf.Len() > 1 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

// listIndexes returns every named index on disk.
func listIndexes() []string {
	root, err := searchDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// --- HTTP -----------------------------------------------------------------

func (s *HTTPServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/search")
	path = strings.TrimPrefix(path, "/")

	// GET /search → list indexes
	if path == "" {
		if r.Method != http.MethodGet {
			jsonError(w, http.StatusMethodNotAllowed, "use GET")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"indexes": listIndexes(),
		})
		return
	}

	parts := strings.SplitN(path, "/", 3)
	indexName := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}
	idx, err := openSearchIndex(indexName)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch {
	case rest == "" && r.Method == http.MethodGet:
		// Query
		q := r.URL.Query().Get("q")
		limitParam := r.URL.Query().Get("limit")
		limit := 20
		if limitParam != "" {
			fmt.Sscanf(limitParam, "%d", &limit)
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"q":     q,
			"hits":  idx.Query(q, limit),
		})
	case rest == "docs" && r.Method == http.MethodPost:
		var doc SearchDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if err := idx.Upsert(doc); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true})
	case strings.HasPrefix(rest, "docs/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(rest, "docs/")
		if err := idx.Delete(id); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusNotFound, "unknown search route")
	}
}
