package main

// asciinema.go — self-hosted terminal recording. Replaces
// asciinema.org for the solo dev who wants to share terminal
// sessions without depending on a third-party host.
//
// Format: asciicast v2
//   https://docs.asciinema.org/manual/asciicast/v2/
//
//   Line 1: JSON header { version, width, height, timestamp,
//                        env, title }
//   Line N: JSON triple [ elapsed, "o", chunkText ]
//
// Recording works two ways:
//
//   1. Import — the dev records locally with `asciinema rec`
//      and POSTs the raw file to /asciinema/import.
//   2. Live   — the agent itself wraps a command via `script -q`
//      (macOS) or `script -fq` (Linux), translates the raw
//      output to asciicast v2, and streams it back. Covered by
//      /asciinema/start + /asciinema/stop so the mobile app can
//      trigger a recording on the dev's Mac mini remotely.
//
// Playback: /asciinema/:id renders a minimal HTML page that
// uses the asciinema-player CDN (optional — if the dev is fully
// offline they can download the JSON and replay locally).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AsciiCast struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	CreatedAt time.Time `json:"createdAt"`
	Duration  float64   `json:"duration"`
	File      string    `json:"file"` // path inside asciinema dir
}

var (
	castMu      sync.Mutex
	castIndex   []AsciiCast
	activeCast  *exec.Cmd
	activeCastInfo *AsciiCast
)

func asciinemaDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "asciinema")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func asciinemaIndexFile() (string, error) {
	dir, err := asciinemaDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "index.json"), nil
}

func loadCasts() []AsciiCast {
	castMu.Lock()
	defer castMu.Unlock()
	if castIndex != nil {
		return castIndex
	}
	p, _ := asciinemaIndexFile()
	data, err := os.ReadFile(p)
	if err != nil {
		castIndex = []AsciiCast{}
		return castIndex
	}
	_ = json.Unmarshal(data, &castIndex)
	return castIndex
}

func saveCasts() error {
	p, _ := asciinemaIndexFile()
	data, _ := json.MarshalIndent(castIndex, "", "  ")
	return os.WriteFile(p, data, 0o600)
}

// --- HTTP ------------------------------------------------------------------

func (s *HTTPServer) handleAsciinemaList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "casts": loadCasts()})
}

// handleAsciinemaImport ingests a raw asciicast file the dev
// recorded locally via the asciinema CLI. Validates the header
// line is legal JSON and appends the meta to the index.
func (s *HTTPServer) handleAsciinemaImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() {
		jsonError(w, http.StatusBadRequest, "empty upload")
		return
	}
	var header struct {
		Version int     `json:"version"`
		Width   int     `json:"width"`
		Height  int     `json:"height"`
		Title   string  `json:"title"`
		Duration float64 `json:"duration,omitempty"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		jsonError(w, http.StatusBadRequest, "header is not valid JSON")
		return
	}
	if header.Version != 2 {
		jsonError(w, http.StatusBadRequest, "only asciicast v2 supported")
		return
	}

	id := "cast-" + randomFormID()
	dir, _ := asciinemaDir()
	file := filepath.Join(dir, id+".cast")
	if err := os.WriteFile(file, body, 0o600); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cast := AsciiCast{
		ID:        id,
		Title:     header.Title,
		Width:     header.Width,
		Height:    header.Height,
		CreatedAt: time.Now().UTC(),
		Duration:  header.Duration,
		File:      id + ".cast",
	}
	castMu.Lock()
	castIndex = append(loadCasts(), cast)
	_ = saveCasts()
	castMu.Unlock()
	jsonReply(w, http.StatusCreated, map[string]interface{}{"ok": true, "cast": cast, "playUrl": "/asciinema/" + cast.ID})
}

// handleAsciinemaDetail serves either the raw cast JSON or a
// tiny HTML player page that pulls asciinema-player from CDN.
// Public — so share links work from any browser.
func (s *HTTPServer) handleAsciinemaDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/asciinema/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	var cast *AsciiCast
	for i, c := range loadCasts() {
		if c.ID == id {
			cast = &castIndex[i]
			break
		}
	}
	if cast == nil {
		jsonError(w, http.StatusNotFound, "cast not found")
		return
	}
	dir, _ := asciinemaDir()
	full := filepath.Join(dir, cast.File)
	if len(parts) == 2 && parts[1] == "raw" {
		http.ServeFile(w, r, full)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>
<link rel="stylesheet" type="text/css" href="https://cdn.jsdelivr.net/npm/asciinema-player@3.7.0/dist/bundle/asciinema-player.css">
</head><body style="background:#111;color:#eee;font-family:system-ui;margin:0;padding:32px">
<h1>%s</h1>
<div id="player"></div>
<script src="https://cdn.jsdelivr.net/npm/asciinema-player@3.7.0/dist/bundle/asciinema-player.min.js"></script>
<script>AsciinemaPlayer.create('/asciinema/%s/raw', document.getElementById('player'), { fit: "width", autoPlay: true, theme: "monokai" });</script>
</body></html>`, cast.Title, cast.Title, cast.ID)
}

// handleAsciinemaStart begins a local recording wrapped around
// a dev-specified shell command. Only one active recording at
// a time per agent — matches the Loom model.
func (s *HTTPServer) handleAsciinemaStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Title   string `json:"title"`
		Command string `json:"command"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Command == "" {
		body.Command = os.Getenv("SHELL")
	}
	if body.Command == "" {
		body.Command = "/bin/bash"
	}
	if _, err := exec.LookPath("asciinema"); err != nil {
		jsonError(w, http.StatusPreconditionFailed, "asciinema not installed — brew install asciinema")
		return
	}
	castMu.Lock()
	if activeCast != nil {
		castMu.Unlock()
		jsonError(w, http.StatusConflict, "a recording is already running")
		return
	}
	id := "cast-" + randomFormID()
	dir, _ := asciinemaDir()
	file := filepath.Join(dir, id+".cast")
	cmd := exec.Command("asciinema", "rec", "-q", "--title", body.Title, "-c", body.Command, file)
	cmd.Stdin = os.Stdin
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		castMu.Unlock()
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	activeCast = cmd
	activeCastInfo = &AsciiCast{ID: id, Title: body.Title, File: id + ".cast", CreatedAt: time.Now().UTC()}
	castMu.Unlock()
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "id": id})
}

func (s *HTTPServer) handleAsciinemaStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	castMu.Lock()
	defer castMu.Unlock()
	if activeCast == nil || activeCastInfo == nil {
		jsonError(w, http.StatusNotFound, "no active recording")
		return
	}
	_ = activeCast.Process.Signal(os.Interrupt)
	_ = activeCast.Wait()
	cast := *activeCastInfo
	castIndex = append(loadCasts(), cast)
	_ = saveCasts()
	activeCast = nil
	activeCastInfo = nil
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "cast": cast, "playUrl": "/asciinema/" + cast.ID})
}
