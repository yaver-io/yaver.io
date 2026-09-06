package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestVSRRejectsFullOrMalformedFrames(t *testing.T) {
	t.Setenv("YAVER_VSR_COMMAND", os.Args[0]+" -test.run=TestVSRHelper")
	s := &HTTPServer{}
	start := httptest.NewRequest(http.MethodPost, "/vsr/session/start", strings.NewReader(`{"language":"en","width":96,"height":96,"format":"gray8","maxAlternatives":3}`))
	w := httptest.NewRecorder()
	s.handleVSRSessionStart(w, start)
	if w.Code != http.StatusCreated {
		t.Fatalf("start status=%d body=%s", w.Code, w.Body)
	}
	var opened map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &opened)
	body := map[string]any{"sessionId": opened["sessionId"], "sequence": 0, "frames": []map[string]any{{"timestamp": 1, "width": 128, "height": 128, "format": "jpeg", "data": "full-face-like"}}}
	payload, _ := json.Marshal(body)
	w = httptest.NewRecorder()
	s.handleVSRSession(w, httptest.NewRequest(http.MethodPost, "/vsr/session/"+opened["sessionId"]+"/frames", bytes.NewReader(payload)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed frame status=%d body=%s", w.Code, w.Body)
	}
}

func TestVSRSessionDecodesAndDiscardsFrames(t *testing.T) {
	t.Setenv("YAVER_VSR_COMMAND", os.Args[0]+" -test.run=TestVSRHelper")
	t.Setenv("GO_WANT_VSR_HELPER", "1")
	s := &HTTPServer{}
	start := httptest.NewRequest(http.MethodPost, "/vsr/session/start", strings.NewReader(`{"language":"en","contextualTerms":["Run Tests","run tests"],"width":96,"height":96,"format":"gray8","maxAlternatives":3}`))
	w := httptest.NewRecorder()
	s.handleVSRSessionStart(w, start)
	var opened map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &opened)
	id := opened["sessionId"]
	encoded := base64.StdEncoding.EncodeToString(make([]byte, 96*96))
	frames := make([]map[string]any, 8)
	for i := range frames {
		frames[i] = map[string]any{"timestamp": i + 1, "width": 96, "height": 96, "format": "gray8", "data": encoded}
	}
	payload, _ := json.Marshal(map[string]any{"sessionId": id, "sequence": 0, "frames": frames})
	w = httptest.NewRecorder()
	s.handleVSRSession(w, httptest.NewRequest(http.MethodPost, "/vsr/session/"+id+"/frames", bytes.NewReader(payload)))
	if w.Code != http.StatusOK {
		t.Fatalf("frames status=%d body=%s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.handleVSRSession(w, httptest.NewRequest(http.MethodPost, "/vsr/session/"+id+"/stop", strings.NewReader(`{}`)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"run tests"`) {
		t.Fatalf("stop status=%d body=%s", w.Code, w.Body)
	}
	s.vsrSessions.mu.Lock()
	_, retained := s.vsrSessions.sessions[id]
	s.vsrSessions.mu.Unlock()
	if retained {
		t.Fatal("mouth frames retained after inference")
	}
}

func TestVSRHelper(t *testing.T) {
	if os.Getenv("GO_WANT_VSR_HELPER") != "1" {
		return
	}
	var input struct {
		Frames          []string `json:"frames"`
		ContextualTerms []string `json:"contextualTerms"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil || len(input.Frames) != 8 {
		os.Exit(3)
	}
	_, _ = os.Stdout.WriteString(`{"text":"run tests","confidence":0.91,"durationMs":0}`)
	os.Exit(0)
}

func TestVSRCommandBiasIsConservative(t *testing.T) {
	biased := biasVSRResult(vsrResult{Text: "ron tess"}, []string{"run tests", "deploy", "open terminal"})
	if biased.Text != "run tests" || biased.CorrectedFrom != "ron tess" {
		t.Fatalf("unexpected correction: %+v", biased)
	}
	general := biasVSRResult(vsrResult{Text: "please inspect the authentication boundary"}, []string{"run tests", "deploy"})
	if general.Text != "please inspect the authentication boundary" || general.CorrectedFrom != "" {
		t.Fatalf("general English must survive: %+v", general)
	}
}

func TestVSRDeleteDiscardsAbandonedSession(t *testing.T) {
	s := &HTTPServer{vsrSessions: &vsrSessionStore{sessions: map[string]*vsrSession{"vsr_abandoned": {ID: "vsr_abandoned", Frames: [][]byte{make([]byte, 96*96)}}}}}
	w := httptest.NewRecorder()
	s.handleVSRSession(w, httptest.NewRequest(http.MethodDelete, "/vsr/session/vsr_abandoned", nil))
	if w.Code != http.StatusOK || len(s.vsrSessions.sessions) != 0 {
		t.Fatalf("delete status=%d sessions=%d", w.Code, len(s.vsrSessions.sessions))
	}
}
