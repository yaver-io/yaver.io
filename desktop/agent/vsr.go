package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	vsrFrameWidth   = 96
	vsrFrameHeight  = 96
	vsrMaxFrames    = 250
	vsrMaxBatch     = 8
	vsrSessionTTL   = 2 * time.Minute
	vsrDecodeWindow = 90 * time.Second
)

type vsrFrame struct {
	Timestamp int64  `json:"timestamp"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Format    string `json:"format"`
	Data      string `json:"data"`
}

type vsrSession struct {
	ID              string
	Language        string
	ContextualTerms []string
	MaxAlternatives int
	Frames          [][]byte
	Timestamps      []int64
	NextSequence    int
	CreatedAt       time.Time
}

type vsrSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*vsrSession
}

func (s *HTTPServer) ensureVSRStore() *vsrSessionStore {
	s.vsrOnce.Do(func() {
		if s.vsrSessions == nil {
			s.vsrSessions = &vsrSessionStore{sessions: make(map[string]*vsrSession)}
		}
	})
	return s.vsrSessions
}

func vsrCommand() []string {
	return strings.Fields(strings.TrimSpace(os.Getenv("YAVER_VSR_COMMAND")))
}

func (s *HTTPServer) handleVSRCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	command := vsrCommand()
	available := len(command) > 0
	reason := ""
	if !available {
		reason = "Install a local VSR runtime and set YAVER_VSR_COMMAND; no camera data was sent."
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available":     available,
		"backend":       "user-machine",
		"language":      "en",
		"mouthCropOnly": true,
		"frame":         map[string]any{"width": vsrFrameWidth, "height": vsrFrameHeight, "format": "gray8", "fps": 25, "maxFrames": vsrMaxFrames},
		"reason":        reason,
	})
}

func (s *HTTPServer) handleVSRSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(vsrCommand()) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Local VSR runtime is not configured."})
		return
	}
	var body struct {
		Language        string   `json:"language"`
		ContextualTerms []string `json:"contextualTerms"`
		MaxAlternatives int      `json:"maxAlternatives"`
		Width           int      `json:"width"`
		Height          int      `json:"height"`
		Format          string   `json:"format"`
	}
	if err := decodeVSRJSONBody(r, &body, 32<<10); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.Language != "en" || body.Width != vsrFrameWidth || body.Height != vsrFrameHeight || body.Format != "gray8" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Expected English 96x96 gray8 mouth crops."})
		return
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not create VSR session."})
		return
	}
	id := "vsr_" + hex.EncodeToString(idBytes)
	if body.MaxAlternatives < 1 || body.MaxAlternatives > 5 {
		body.MaxAlternatives = 3
	}
	terms := sanitizeVSRTerms(body.ContextualTerms)
	store := s.ensureVSRStore()
	store.mu.Lock()
	for key, session := range store.sessions {
		if time.Since(session.CreatedAt) > vsrSessionTTL {
			delete(store.sessions, key)
		}
	}
	if len(store.sessions) >= 4 {
		store.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many active Silent Input sessions; finish or retry after two minutes."})
		return
	}
	store.sessions[id] = &vsrSession{ID: id, Language: "en", ContextualTerms: terms, MaxAlternatives: body.MaxAlternatives, CreatedAt: time.Now()}
	store.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{"sessionId": id})
}

func (s *HTTPServer) handleVSRSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/vsr/session/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 1 && parts[0] != "" && r.Method == http.MethodDelete {
		store := s.ensureVSRStore()
		store.mu.Lock()
		_, existed := store.sessions[parts[0]]
		delete(store.sessions, parts[0])
		store.mu.Unlock()
		if !existed {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "VSR session not found or expired."})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"discarded": true})
		return
	}
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "frames":
		s.handleVSRFrames(w, r, parts[0])
	case "stop":
		s.handleVSRStop(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

func (s *HTTPServer) handleVSRFrames(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		SessionID string     `json:"sessionId"`
		Sequence  int        `json:"sequence"`
		Frames    []vsrFrame `json:"frames"`
	}
	if err := decodeVSRJSONBody(r, &body, 128<<10); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if body.SessionID != id || len(body.Frames) == 0 || len(body.Frames) > vsrMaxBatch {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid VSR frame batch."})
		return
	}
	decoded := make([][]byte, 0, len(body.Frames))
	timestamps := make([]int64, 0, len(body.Frames))
	for _, frame := range body.Frames {
		if frame.Width != vsrFrameWidth || frame.Height != vsrFrameHeight || frame.Format != "gray8" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Only 96x96 gray8 mouth crops are accepted."})
			return
		}
		pixels, err := base64.StdEncoding.DecodeString(frame.Data)
		if err != nil || len(pixels) != vsrFrameWidth*vsrFrameHeight {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid mouth crop payload."})
			return
		}
		decoded = append(decoded, pixels)
		timestamps = append(timestamps, frame.Timestamp)
	}
	store := s.ensureVSRStore()
	store.mu.Lock()
	defer store.mu.Unlock()
	session := store.sessions[id]
	if session == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "VSR session not found or expired."})
		return
	}
	if body.Sequence != session.NextSequence {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "VSR batches must be ordered and sent exactly once."})
		return
	}
	if len(session.Frames)+len(decoded) > vsrMaxFrames {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "Silent Input is limited to 10 seconds."})
		return
	}
	if len(session.Timestamps) > 0 && timestamps[0] <= session.Timestamps[len(session.Timestamps)-1] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Frame timestamps must increase."})
		return
	}
	for index := 1; index < len(timestamps); index++ {
		if timestamps[index] <= timestamps[index-1] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Frame timestamps must increase."})
			return
		}
	}
	session.Frames = append(session.Frames, decoded...)
	session.Timestamps = append(session.Timestamps, timestamps...)
	session.NextSequence++
	writeJSON(w, http.StatusOK, map[string]any{"accepted": len(decoded), "total": len(session.Frames)})
}

func (s *HTTPServer) handleVSRStop(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := s.ensureVSRStore()
	store.mu.Lock()
	session := store.sessions[id]
	delete(store.sessions, id)
	store.mu.Unlock()
	if session == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "VSR session not found or expired."})
		return
	}
	if len(session.Frames) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Not enough stable mouth frames were received."})
		return
	}
	started := time.Now()
	result, err := runVSR(r.Context(), session)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(result.Text) == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "The visual speech model returned no text."})
		return
	}
	result = biasVSRResult(result, session.ContextualTerms)
	result.DurationMS = int(time.Since(started).Milliseconds())
	result.Metrics = map[string]int{"inferenceMs": result.DurationMS}
	writeJSON(w, http.StatusOK, result)
}

type vsrAlternative struct {
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence,omitempty"`
}

type vsrResult struct {
	Text          string           `json:"text"`
	Confidence    *float64         `json:"confidence,omitempty"`
	Alternatives  []vsrAlternative `json:"alternatives,omitempty"`
	CorrectedFrom string           `json:"correctedFrom,omitempty"`
	DurationMS    int              `json:"durationMs"`
	Metrics       map[string]int   `json:"metrics,omitempty"`
}

// Only tiny edit-distance corrections toward caller-supplied terms are
// allowed. General English passes through unchanged, and mobile still requires
// an explicit Send after showing the result.
func biasVSRResult(result vsrResult, terms []string) vsrResult {
	raw := strings.ToLower(strings.Join(strings.Fields(result.Text), " "))
	if raw == "" {
		return result
	}
	best, bestDistance := raw, len([]rune(raw))+1
	for _, term := range terms {
		candidate := strings.ToLower(strings.Join(strings.Fields(term), " "))
		if candidate == "" || len(candidate) > 64 {
			continue
		}
		distance := editDistance(raw, candidate)
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	denominator := len([]rune(best))
	if len([]rune(raw)) > denominator {
		denominator = len([]rune(raw))
	}
	if best == raw || denominator == 0 || float64(bestDistance)/float64(denominator) > 0.23 {
		return result
	}
	result.CorrectedFrom = result.Text
	result.Alternatives = append([]vsrAlternative{{Text: result.Text, Confidence: result.Confidence}}, result.Alternatives...)
	result.Text = best
	return result
}

func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)
	previous := make([]int, len(br)+1)
	for i := range previous {
		previous[i] = i
	}
	for ai, av := range ar {
		current := make([]int, len(br)+1)
		current[0] = ai + 1
		for bi, bv := range br {
			cost := 0
			if av != bv {
				cost = 1
			}
			current[bi+1] = previous[bi+1] + 1
			if insertion := current[bi] + 1; insertion < current[bi+1] {
				current[bi+1] = insertion
			}
			if substitution := previous[bi] + cost; substitution < current[bi+1] {
				current[bi+1] = substitution
			}
		}
		previous = current
	}
	return previous[len(br)]
}

func runVSR(parent context.Context, session *vsrSession) (vsrResult, error) {
	command := vsrCommand()
	if len(command) == 0 {
		return vsrResult{}, errors.New("local VSR runtime is not configured")
	}
	ctx, cancel := context.WithTimeout(parent, vsrDecodeWindow)
	defer cancel()
	input := struct {
		Language        string   `json:"language"`
		Width           int      `json:"width"`
		Height          int      `json:"height"`
		FPS             int      `json:"fps"`
		Frames          []string `json:"frames"`
		ContextualTerms []string `json:"contextualTerms"`
		MaxAlternatives int      `json:"maxAlternatives"`
	}{Language: "en", Width: vsrFrameWidth, Height: vsrFrameHeight, FPS: 25, ContextualTerms: session.ContextualTerms, MaxAlternatives: session.MaxAlternatives}
	for _, frame := range session.Frames {
		input.Frames = append(input.Frames, base64.StdEncoding.EncodeToString(frame))
	}
	payload, _ := json.Marshal(input)
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdin = strings.NewReader(string(payload))
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return vsrResult{}, errors.New("visual speech inference timed out")
	}
	if err != nil {
		return vsrResult{}, fmt.Errorf("visual speech inference failed: %s", strings.TrimSpace(string(output)))
	}
	var result vsrResult
	if err := json.Unmarshal(output, &result); err != nil {
		return vsrResult{}, errors.New("visual speech runtime returned an invalid result")
	}
	return result, nil
}

func sanitizeVSRTerms(input []string) []string {
	out := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, term := range input {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || len(term) > 64 || seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
		if len(out) == 80 {
			break
		}
	}
	return out
}

func decodeVSRJSONBody(r *http.Request, dst any, limit int64) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}
