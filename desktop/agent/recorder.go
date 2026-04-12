package main

// recorder.go — screen recording + sharing. Replaces Loom / Tella /
// Vidyard for the solo dev who already has ffmpeg and a Mac mini.
//
// Two roles per recording session:
//
//   1. agent-side capture — the Mac mini (or Linux box) captures
//      its own screen + mic via ffmpeg. On macOS we use
//      `avfoundation`, on Linux `x11grab`. The dev triggers this
//      from the mobile app, walks through whatever they're
//      showing, taps stop.
//
//   2. mobile-side capture (future) — the phone records its own
//      camera / screen and uploads alongside. The session ID is
//      shared so both streams land in the same ClipSession
//      object, allowing a picture-in-picture composition later.
//
// For the MVP only (1) is wired. Mobile controls the recording
// remotely over HTTP, lists sessions, and plays back the mp4.
//
// Files land in ~/.yaver/clips/<sessionID>/ with a metadata.json
// and agent-screen.mp4 (plus mobile-camera.mp4 when mobile adds
// its own stream). Each session gets a public share URL via the
// shortener module: /clips/:id serves the metadata + download
// links, /clips/:id/agent-screen.mp4 streams the file.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ClipSession is one recording. Persisted as metadata.json in
// the session directory so the listing survives restarts.
type ClipSession struct {
	ID          string    `json:"id"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	StoppedAt   time.Time `json:"stoppedAt,omitempty"`
	DurationSec int       `json:"durationSec,omitempty"`
	// Targets requested for this session (e.g. ["agent-screen","mobile-screen"]).
	// Default is ["agent-screen"] when absent for backward compatibility.
	Targets []string `json:"targets,omitempty"`
	// Streams available in this session. Extendable so mobile
	// can join with its own capture later without migrations.
	Streams []ClipStream `json:"streams"`
}

// ClipStream is one uploaded track. Agent-screen always exists;
// mobile-camera / mobile-screen slots are optional.
type ClipStream struct {
	Kind     string `json:"kind"`     // "agent-screen" | "mobile-camera" | "mobile-screen" | "mic"
	File     string `json:"file"`     // filename inside the session dir
	Bytes    int64  `json:"bytes"`
	Mime     string `json:"mime"`
	Uploaded bool   `json:"uploaded"` // false until the stop signal arrives
}

// activeSession tracks the currently-recording ffmpeg process
// (agent side) so /clips/stop can kill it gracefully.
type activeSession struct {
	session *ClipSession
	cmd     *exec.Cmd
	stopCh  chan struct{}
}

var (
	clipMu      sync.Mutex
	clipActive  *activeSession // only one at a time per agent
	clipBaseDir string
)

func clipDir() (string, error) {
	clipMu.Lock()
	defer clipMu.Unlock()
	if clipBaseDir != "" {
		return clipBaseDir, nil
	}
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "clips")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	clipBaseDir = p
	return p, nil
}

func sessionDir(id string) (string, error) {
	base, err := clipDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(base, id)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}

func loadClipSession(id string) (*ClipSession, error) {
	dir, err := sessionDir(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return nil, err
	}
	var s ClipSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveClipSession(s *ClipSession) error {
	dir, err := sessionDir(s.ID)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0o600)
}

func listClipSessions() ([]ClipSession, error) {
	base, err := clipDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	out := make([]ClipSession, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := loadClipSession(e.Name()); err == nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

// --- ffmpeg capture --------------------------------------------------------

// startAgentCapture launches ffmpeg with the right platform
// flags for the current OS. The output file lives inside the
// session directory; the caller stores the *exec.Cmd in
// activeSession so /clips/stop can kill it.
func startAgentCapture(s *ClipSession) (*exec.Cmd, error) {
	dir, err := sessionDir(s.ID)
	if err != nil {
		return nil, err
	}
	out := filepath.Join(dir, "agent-screen.mp4")

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not installed — brew install ffmpeg (or apt install ffmpeg)")
	}

	var args []string
	switch runtime.GOOS {
	case "darwin":
		// avfoundation video input index 1 is usually the main
		// display on macOS; audio input 0 is the default mic.
		// The dev can override via `yaver config clip.device`
		// later if they use a second monitor.
		args = []string{
			"-f", "avfoundation",
			"-framerate", "30",
			"-i", "1:0",
			"-vcodec", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
			"-acodec", "aac",
			"-y", out,
		}
	case "linux":
		display := os.Getenv("DISPLAY")
		if display == "" {
			display = ":0.0"
		}
		args = []string{
			"-f", "x11grab",
			"-framerate", "30",
			"-i", display,
			"-f", "pulse", "-i", "default",
			"-vcodec", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
			"-acodec", "aac",
			"-y", out,
		}
	default:
		return nil, fmt.Errorf("unsupported OS %q", runtime.GOOS)
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// --- HTTP ------------------------------------------------------------------

// handleClipStart kicks off an agent-side capture and returns
// the session ID so the mobile client can poll progress / later
// attach its own stream.
func (s *HTTPServer) handleClipStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Title       string   `json:"title,omitempty"`
		Description string   `json:"description,omitempty"`
		Targets     []string `json:"targets,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Default to agent-screen only for backward compatibility.
	targets := body.Targets
	if len(targets) == 0 {
		targets = []string{"agent-screen"}
	}

	clipMu.Lock()
	if clipActive != nil {
		clipMu.Unlock()
		jsonError(w, http.StatusConflict, "a recording is already running — stop it first")
		return
	}
	clipMu.Unlock()

	// Build initial streams list from targets.
	var streams []ClipStream
	wantAgent := false
	for _, t := range targets {
		switch t {
		case "agent-screen":
			streams = append(streams, ClipStream{Kind: "agent-screen", File: "agent-screen.mp4", Mime: "video/mp4"})
			wantAgent = true
		case "mobile-screen":
			streams = append(streams, ClipStream{Kind: "mobile-screen", File: "mobile-screen.mp4", Mime: "video/mp4"})
		}
	}
	if len(streams) == 0 {
		streams = []ClipStream{{Kind: "agent-screen", File: "agent-screen.mp4", Mime: "video/mp4"}}
		wantAgent = true
	}

	session := &ClipSession{
		ID:          "clip-" + randomFormID(),
		Title:       body.Title,
		Description: body.Description,
		StartedAt:   time.Now().UTC(),
		Targets:     targets,
		Streams:     streams,
	}
	if err := saveClipSession(session); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var cmd *exec.Cmd
	if wantAgent {
		var err error
		cmd, err = startAgentCapture(session)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	clipMu.Lock()
	clipActive = &activeSession{session: session, cmd: cmd, stopCh: make(chan struct{})}
	clipMu.Unlock()

	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"session":  session,
		"shareUrl": "/clips/" + session.ID,
	})
}

// handleClipStop gracefully terminates the ffmpeg process
// (SIGINT so the moov atom gets flushed) and marks the stream
// uploaded. Mobile can then GET /clips/:id for metadata or
// /clips/:id/agent-screen.mp4 for playback.
func (s *HTTPServer) handleClipStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	clipMu.Lock()
	active := clipActive
	clipActive = nil
	clipMu.Unlock()
	if active == nil {
		jsonError(w, http.StatusNotFound, "no active recording")
		return
	}
	// Politely ask ffmpeg to finalise the moov atom (if agent capture was running).
	if active.cmd != nil {
		_ = active.cmd.Process.Signal(os.Interrupt)
		_ = active.cmd.Wait()
	}
	active.session.StoppedAt = time.Now().UTC()
	active.session.DurationSec = int(active.session.StoppedAt.Sub(active.session.StartedAt).Seconds())
	// Walk the session dir and flip the agent-screen stream to
	// uploaded with the final file size.
	for i := range active.session.Streams {
		if active.session.Streams[i].Kind == "agent-screen" {
			p, _ := sessionDir(active.session.ID)
			if info, err := os.Stat(filepath.Join(p, active.session.Streams[i].File)); err == nil {
				active.session.Streams[i].Bytes = info.Size()
				active.session.Streams[i].Uploaded = true
			}
		}
	}
	_ = saveClipSession(active.session)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": active.session})
}

// handleClipList returns every recorded session.
func (s *HTTPServer) handleClipList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	sessions, err := listClipSessions()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "sessions": sessions})
}

// handleClipDetail is the public playback endpoint — returns
// metadata when called at /clips/:id and streams the file when
// called at /clips/:id/<filename>. Auth-free so share links work
// from anywhere the agent is reachable.
func (s *HTTPServer) handleClipDetail(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/clips/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		jsonError(w, http.StatusNotFound, "session id required")
		return
	}
	id := parts[0]
	sess, err := loadClipSession(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}
	if len(parts) == 1 {
		// Metadata view. Show merged video if available, otherwise agent-screen.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		videoSrc := "agent-screen.mp4"
		for _, st := range sess.Streams {
			if st.Kind == "merged" && st.Uploaded {
				videoSrc = "merged.mp4"
				break
			}
		}
		// Build stream badges.
		var badges string
		for _, st := range sess.Streams {
			if st.Uploaded {
				badges += fmt.Sprintf(` <span style="background:#334;padding:2px 8px;border-radius:4px;font-size:12px;color:#aaa">%s</span>`, st.Kind)
			}
		}
		fmt.Fprintf(w, `<!doctype html><html><body style='font-family:system-ui;max-width:960px;margin:32px auto;background:#111;color:#eee'>
<h1>%s</h1><p>%s</p>
<video controls style="width:100%%;border-radius:8px" src="/clips/%s/%s"></video>
<p>Recorded %s · %d sec%s</p>
</body></html>`, sess.Title, sess.Description, sess.ID, videoSrc, sess.StartedAt.Format(time.RFC1123), sess.DurationSec, badges)
		return
	}
	// File streaming view.
	dir, _ := sessionDir(id)
	full := filepath.Join(dir, parts[1])
	if _, err := os.Stat(full); err != nil {
		jsonError(w, http.StatusNotFound, "file not found")
		return
	}
	http.ServeFile(w, r, full)
}

// handleClipUpload accepts a streamed upload from the mobile
// app's future native recorder. The kind query param tags the
// stream so multiple devices can contribute to the same session.
//
// Example: POST /clips/<id>/upload?kind=mobile-camera with the
// mp4 payload as the request body.
func (s *HTTPServer) handleClipUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	// Path: /clips/upload/<id>?kind=...
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		jsonError(w, http.StatusBadRequest, "expected /clips/upload/<id>")
		return
	}
	id := parts[2]
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "mobile-camera"
	}
	sess, err := loadClipSession(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}
	dir, _ := sessionDir(id)
	filename := kind + ".mp4"
	out := filepath.Join(dir, filename)
	f, err := os.Create(out)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	n, err := io.Copy(f, r.Body)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Record/extend the stream list in metadata.
	found := false
	for i := range sess.Streams {
		if sess.Streams[i].Kind == kind {
			sess.Streams[i].File = filename
			sess.Streams[i].Bytes = n
			sess.Streams[i].Mime = "video/mp4"
			sess.Streams[i].Uploaded = true
			found = true
			break
		}
	}
	if !found {
		sess.Streams = append(sess.Streams, ClipStream{
			Kind: kind, File: filename, Bytes: n, Mime: "video/mp4", Uploaded: true,
		})
	}
	_ = saveClipSession(sess)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": sess})
}

// handleClipMerge composes multiple streams into a single
// side-by-side video using ffmpeg's hstack filter. Typically
// used to merge agent-screen (landscape) + mobile-screen
// (portrait) for marketing / demo videos.
//
// POST /clips/merge/<id>
func (s *HTTPServer) handleClipMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		jsonError(w, http.StatusBadRequest, "expected /clips/merge/<id>")
		return
	}
	id := parts[2]
	sess, err := loadClipSession(id)
	if err != nil {
		jsonError(w, http.StatusNotFound, "session not found")
		return
	}

	dir, _ := sessionDir(id)

	// Find the streams that have been uploaded.
	var inputs []string
	for _, st := range sess.Streams {
		if st.Uploaded && st.Kind != "merged" && st.Kind != "mic" {
			p := filepath.Join(dir, st.File)
			if _, err := os.Stat(p); err == nil {
				inputs = append(inputs, p)
			}
		}
	}
	if len(inputs) < 2 {
		jsonError(w, http.StatusConflict, "need at least 2 uploaded streams to merge")
		return
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		jsonError(w, http.StatusInternalServerError, "ffmpeg not installed")
		return
	}

	outPath := filepath.Join(dir, "merged.mp4")

	// Build ffmpeg filter: scale each input to 720px height,
	// then hstack them side-by-side. No audio for marketing
	// videos (cleaner). Supports 2+ inputs.
	var ffArgs []string
	for _, inp := range inputs {
		ffArgs = append(ffArgs, "-i", inp)
	}
	var filterParts []string
	var stackInputs []string
	for i := range inputs {
		label := fmt.Sprintf("s%d", i)
		filterParts = append(filterParts, fmt.Sprintf("[%d:v]scale=-2:720,setsar=1[%s]", i, label))
		stackInputs = append(stackInputs, fmt.Sprintf("[%s]", label))
	}
	filter := strings.Join(filterParts, ";") + ";" +
		strings.Join(stackInputs, "") + fmt.Sprintf("hstack=inputs=%d[v]", len(inputs))

	ffArgs = append(ffArgs,
		"-filter_complex", filter,
		"-map", "[v]",
		"-vcodec", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
		"-shortest", "-an",
		"-y", outPath,
	)

	cmd := exec.Command("ffmpeg", ffArgs...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("ffmpeg merge failed: %v", err))
		return
	}

	// Record the merged stream in metadata.
	info, _ := os.Stat(outPath)
	var mergedBytes int64
	if info != nil {
		mergedBytes = info.Size()
	}
	// Remove any previous merged stream, then add the new one.
	var kept []ClipStream
	for _, st := range sess.Streams {
		if st.Kind != "merged" {
			kept = append(kept, st)
		}
	}
	sess.Streams = append(kept, ClipStream{
		Kind: "merged", File: "merged.mp4", Bytes: mergedBytes,
		Mime: "video/mp4", Uploaded: true,
	})
	_ = saveClipSession(sess)
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true, "session": sess})
}
