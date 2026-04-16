package main

// autoideas_http.go — HTTP + MCP surface for `yaver autoideas`.
// Same shape as /autodev/start: caller POSTs a small JSON spec,
// daemon spawns `yaver autoideas <project> <flags...>` as a
// detached subprocess (which immediately re-detaches itself via
// the autodev_detach machinery), returns the loop name + stream
// name so the caller can subscribe to live progress and read the
// generated ideas file.

import (
	"encoding/json"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// AutoIdeasStart describes one autoideas invocation. Mirrors the
// CLI flag set so any client (mobile, web, MCP, peer Yaver agent)
// can start a generation run with the same knobs the user would
// have on the terminal.
type AutoIdeasStart struct {
	Project    string `json:"project"`
	WorkDir    string `json:"work_dir"`
	Hours      string `json:"hours"`
	Load       string `json:"load"`
	Prompt     string `json:"prompt"`
	Harden     string `json:"harden"`
	Engine     string `json:"engine"`
	Runner     string `json:"runner"`
	Output     string `json:"output"`
	MaxBatches int    `json:"max_batches"`
	Tick       int    `json:"tick"`
}

func (s *HTTPServer) handleAutoIdeasStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body AutoIdeasStart
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

	args := autoIdeasBuildArgs(project, body)
	loopName := project + "-autoideas"
	streamName := "autodev:" + loopName

	exe, err := os.Executable()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "find yaver binary: "+err.Error())
		return
	}
	cmd := osexec.Command(exe, append([]string{"autoideas"}, args...)...)
	cmd.Dir = body.WorkDir
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	// The CLI re-fork-execs with YAVER_AUTODEV_DETACHED=1, so this
	// parent exec is short-lived (just enough to spawn the child).
	if err := cmd.Start(); err != nil {
		jsonError(w, http.StatusInternalServerError, "spawn autoideas: "+err.Error())
		return
	}
	go func() { _ = cmd.Wait() }()

	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":          true,
		"loop_name":   loopName,
		"stream_name": streamName,
		"output":      autoIdeasOutputPath(body),
		"work_dir":    body.WorkDir,
	})
}

// autoIdeasBuildArgs converts the JSON spec into argv flags so the
// CLI / HTTP / MCP / peer-agent surfaces all hand-roll the same
// command line (no logic skew).
func autoIdeasBuildArgs(project string, body AutoIdeasStart) []string {
	args := []string{project}
	if body.Hours != "" {
		args = append(args, "--hours", body.Hours)
	}
	switch body.Load {
	case "high", "burst", "heavy":
		args = append(args, "--heavy")
	case "lite", "low":
		args = append(args, "--lite")
	}
	if body.Prompt != "" {
		args = append(args, "--prompt", body.Prompt)
	}
	if body.Harden != "" {
		args = append(args, "--harden", body.Harden)
	}
	if body.Engine != "" {
		args = append(args, "--engine", body.Engine)
	}
	if body.Runner != "" {
		args = append(args, "--runner", body.Runner)
	}
	if body.Output != "" {
		args = append(args, "--output", body.Output)
	}
	if body.MaxBatches > 0 {
		args = append(args, "--max-batches", strconv.Itoa(body.MaxBatches))
	}
	if body.Tick > 0 {
		args = append(args, "--tick", strconv.Itoa(body.Tick))
	}
	return args
}

func autoIdeasOutputPath(body AutoIdeasStart) string {
	out := body.Output
	if out == "" {
		out = "ideas.md"
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(body.WorkDir, out)
	}
	return out
}

// handleAutoIdeasFile reads the generated ideas file and returns
// it as a list of {checked, title, lineNumber} records so the
// mobile UI can render checkboxes + map check-toggles back to the
// right line. Plus the raw file for "View source" mode.
//
// GET /autoideas/file?work_dir=…&output=ideas.md
func (s *HTTPServer) handleAutoIdeasFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	wd := r.URL.Query().Get("work_dir")
	if wd == "" {
		jsonError(w, http.StatusBadRequest, "work_dir required")
		return
	}
	output := r.URL.Query().Get("output")
	if output == "" {
		output = "ideas.md"
	}
	path := output
	if !filepath.IsAbs(path) {
		path = filepath.Join(wd, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"items": []interface{}{},
			"raw":   "",
			"path":  path,
		})
		return
	}
	type item struct {
		Line    int    `json:"line"`
		Checked bool   `json:"checked"`
		Title   string `json:"title"`
	}
	items := []item{}
	for i, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(t, "- [ ]"):
			items = append(items, item{Line: i + 1, Checked: false, Title: strings.TrimSpace(strings.TrimPrefix(t, "- [ ]"))})
		case strings.HasPrefix(t, "- [x]"), strings.HasPrefix(t, "- [X]"):
			items = append(items, item{Line: i + 1, Checked: true, Title: strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(t, "- [x]"), "- [X]"), ""))})
		case strings.HasPrefix(t, "* [ ]"):
			items = append(items, item{Line: i + 1, Checked: false, Title: strings.TrimSpace(strings.TrimPrefix(t, "* [ ]"))})
		}
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"items": items,
		"raw":   string(data),
		"path":  path,
	})
}

// handleAutoIdeasSelect takes a list of line numbers from the
// ideas file and starts an autodev run with only those items as
// the checklist. Used by mobile/web "Implement selected" buttons.
//
// POST /autoideas/select
//
//	{work_dir, output (optional), lines:[12,17,33], engine, hours, load, ...}
//
// We materialise a temp checklist file with just the selected
// titles, then call the existing /autodev/start path so the
// implementation loop is identical.
func (s *HTTPServer) handleAutoIdeasSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		WorkDir    string `json:"work_dir"`
		Output     string `json:"output"`
		Lines      []int  `json:"lines"`
		Project    string `json:"project"`
		Engine     string `json:"engine"`
		Hours      string `json:"hours"`
		Load       string `json:"load"`
		AutoBranch bool   `json:"auto_branch"`
		Deploy     string `json:"deploy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.WorkDir == "" || len(body.Lines) == 0 {
		jsonError(w, http.StatusBadRequest, "work_dir + lines required")
		return
	}
	output := body.Output
	if output == "" {
		output = "ideas.md"
	}
	srcPath := output
	if !filepath.IsAbs(srcPath) {
		srcPath = filepath.Join(body.WorkDir, srcPath)
	}
	src, err := os.ReadFile(srcPath)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "read source: "+err.Error())
		return
	}
	allLines := strings.Split(string(src), "\n")
	want := map[int]bool{}
	for _, n := range body.Lines {
		want[n] = true
	}

	// Compose a fresh checklist with only the picks, preserving the
	// original wording.
	var picked []string
	for i, l := range allLines {
		if !want[i+1] {
			continue
		}
		t := strings.TrimSpace(l)
		title := ""
		switch {
		case strings.HasPrefix(t, "- [ ]"):
			title = strings.TrimSpace(strings.TrimPrefix(t, "- [ ]"))
		case strings.HasPrefix(t, "* [ ]"):
			title = strings.TrimSpace(strings.TrimPrefix(t, "* [ ]"))
		case strings.HasPrefix(t, "- [x]"), strings.HasPrefix(t, "- [X]"):
			title = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "- [x]"), "- [X]"))
		}
		if title == "" {
			continue
		}
		picked = append(picked, "- [ ] "+title)
	}
	if len(picked) == 0 {
		jsonError(w, http.StatusBadRequest, "no parseable items at the given line numbers")
		return
	}

	// Mark the picked lines as checked in the source file so the
	// mobile UI sees them progress.
	for i, l := range allLines {
		if !want[i+1] {
			continue
		}
		allLines[i] = strings.Replace(l, "- [ ]", "- [x]", 1)
	}
	_ = os.WriteFile(srcPath, []byte(strings.Join(allLines, "\n")), 0o644)

	// Materialise the autodev checklist. We use a stable name so
	// re-clicking "Implement selected" later overwrites rather than
	// growing.
	autodevList := filepath.Join(body.WorkDir, ".autoideas-selected.md")
	header := "# Selected from " + filepath.Base(srcPath) + " — generated by yaver autoideas\n\n"
	_ = os.WriteFile(autodevList, []byte(header+strings.Join(picked, "\n")+"\n"), 0o644)

	// Now start an autodev run with --remained pointed at the
	// selected list, mirroring /autodev/start under the hood.
	exe, err := os.Executable()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "find yaver binary: "+err.Error())
		return
	}
	args := []string{"autodev"}
	project := body.Project
	if project == "" {
		project = filepath.Base(body.WorkDir)
	}
	args = append(args, project)
	args = append(args, "--remained", autodevList)
	if body.Engine != "" {
		args = append(args, "--engine", body.Engine)
	}
	if body.Hours != "" {
		args = append(args, "--hours", body.Hours)
	}
	switch body.Load {
	case "high", "burst", "heavy":
		args = append(args, "--heavy")
	case "lite", "low":
		args = append(args, "--lite")
	}
	if body.AutoBranch {
		args = append(args, "--auto-branch")
	}
	if body.Deploy != "" {
		args = append(args, "--deploy", body.Deploy)
	}
	cmd := osexec.Command(exe, args...)
	cmd.Dir = body.WorkDir
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		jsonError(w, http.StatusInternalServerError, "spawn autodev: "+err.Error())
		return
	}
	go func() { _ = cmd.Wait() }()

	jsonReply(w, http.StatusAccepted, map[string]interface{}{
		"ok":          true,
		"loop_name":   project + "-autodev",
		"stream_name": "autodev:" + project + "-autodev",
		"selected":    len(picked),
		"checklist":   autodevList,
		"work_dir":    body.WorkDir,
	})
}
