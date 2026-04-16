package main

// autoinit_http.go — HTTP + MCP surface for `yaver autoinit`.
// Mirrors the autoideas / autodev pattern so mobile, web, MCP,
// and peer yaver-go agents can all kick off project initialisation
// and check whether init.md is in place.

import (
	"encoding/json"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
)

// AutoInitStart is the request body for POST /autoinit/start.
type AutoInitStart struct {
	Project string `json:"project"`
	WorkDir string `json:"work_dir"`
	Prompt  string `json:"prompt"`
	Engine  string `json:"engine"`
	Output  string `json:"output"`
	Force   bool   `json:"force"`
}

func (s *HTTPServer) handleAutoInitStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body AutoInitStart
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.WorkDir == "" {
		jsonError(w, http.StatusBadRequest, "work_dir required")
		return
	}
	if _, err := os.Stat(body.WorkDir); err != nil {
		jsonError(w, http.StatusBadRequest, "work_dir does not exist: "+body.WorkDir)
		return
	}
	project := body.Project
	if project == "" {
		project = filepath.Base(body.WorkDir)
	}

	exe, err := os.Executable()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "find yaver binary: "+err.Error())
		return
	}
	args := []string{"autoinit", project}
	if body.Prompt != "" {
		args = append(args, "--prompt", body.Prompt)
	}
	if body.Engine != "" {
		args = append(args, "--engine", body.Engine)
	}
	if body.Output != "" {
		args = append(args, "--output", body.Output)
	}
	if body.Force {
		args = append(args, "--force")
	}
	cmd := osexec.Command(exe, args...)
	cmd.Dir = body.WorkDir
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		jsonError(w, http.StatusInternalServerError, "spawn autoinit: "+err.Error())
		return
	}
	go func() { _ = cmd.Wait() }()

	streamName := "autodev:" + project + "-autoinit"
	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":          true,
		"loop_name":   project + "-autoinit",
		"stream_name": streamName,
		"output":      autoinitOutputPath(body),
		"work_dir":    body.WorkDir,
	})
}

func autoinitOutputPath(body AutoInitStart) string {
	out := body.Output
	if out == "" {
		out = autoinitFile
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(body.WorkDir, out)
	}
	return out
}

// handleAutoInitStatus answers GET /autoinit/status?work_dir=…
// so any UI can show "init done ✓" or "init not yet ↓ start it".
func (s *HTTPServer) handleAutoInitStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	wd := r.URL.Query().Get("work_dir")
	if wd == "" {
		jsonError(w, http.StatusBadRequest, "work_dir required")
		return
	}
	jsonReply(w, http.StatusOK, computeAutoInitStatus(wd))
}
