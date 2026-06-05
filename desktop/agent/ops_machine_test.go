package main

// ops_machine_test.go — the AI-understand + Talos-sync legs of the Talos-IoT
// machine hijack, tested against mock servers (no real LLM, no real Talos).
//   - machineUnderstandLLM: posts the observed schematic to an OpenAI-compatible
//     /chat/completions and merges the inferred names/units/scales back onto the
//     deterministic register observations.
//   - mergeUnderstanding: the pure merge (markdown-fence tolerant, keyed by
//     addr+func) — the bit that must never corrupt the observed stats.
//   - machinePost: the edge→Talos POST (Bearer org secret, status handling).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yaver-io/agent/machine"
)

func testSchematic() machine.Schematic {
	return machine.Schematic{
		Driver: "modbus_tcp",
		Source: "scan",
		Registers: []machine.RegisterObs{
			{Addr: 0, Func: 3, Last: 5000, Kind: machine.KindSetpoint},
			{Addr: 2, Func: 3, Last: 42, Kind: machine.KindCounter},
		},
	}
}

func TestMergeUnderstanding(t *testing.T) {
	// Markdown-fenced JSON (LLMs love fences) labelling only register 0.
	content := "```json\n" +
		`{"registers":[{"addr":0,"func":3,"name":"cut_length","unit2":"mm","scale":0.25,"kind":"setpoint","confidence":0.9}],"notes":"inferred"}` +
		"\n```"
	out := mergeUnderstanding(testSchematic(), content)
	if out.Registers[0].Name != "cut_length" || out.Registers[0].Unit2 != "mm" || out.Registers[0].Scale != 0.25 {
		t.Fatalf("register 0 not labelled: %+v", out.Registers[0])
	}
	// The deterministic observation must be preserved, not overwritten.
	if out.Registers[0].Last != 5000 || out.Registers[0].Kind != machine.KindSetpoint {
		t.Errorf("merge corrupted observed stats: %+v", out.Registers[0])
	}
	// Register 2 wasn't in the LLM output → must stay untouched.
	if out.Registers[1].Name != "" {
		t.Errorf("register 2 should be untouched, got name %q", out.Registers[1].Name)
	}
}

func TestMergeUnderstanding_badJSONIsSafe(t *testing.T) {
	out := mergeUnderstanding(testSchematic(), "not json at all")
	if !strings.Contains(out.Notes, "parse failed") {
		t.Errorf("bad LLM output should set a parse-failed note, got %q", out.Notes)
	}
	if len(out.Registers) != 2 || out.Registers[0].Last != 5000 {
		t.Error("bad LLM output must not corrupt the schematic")
	}
}

func TestMachineUnderstandLLM_mockLLM(t *testing.T) {
	var sawSystemPrompt bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		sawSystemPrompt = strings.Contains(string(body), "reverse-engineering")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"registers\":[{\"addr\":0,\"func\":3,\"name\":\"cut_length\",\"unit2\":\"mm\",\"scale\":0.25,\"kind\":\"setpoint\",\"confidence\":0.92}]}"}}]}`))
	}))
	defer srv.Close()

	out, err := machineUnderstandLLM(context.Background(), srv.URL, "test-key", "test-model",
		testSchematic(), map[string]any{"lengthMm": 1250})
	if err != nil {
		t.Fatalf("understand: %v", err)
	}
	if !sawSystemPrompt {
		t.Error("LLM request missing the understand system prompt")
	}
	if out.Registers[0].Name != "cut_length" || out.Registers[0].Scale != 0.25 {
		t.Fatalf("understand did not apply labels: %+v", out.Registers[0])
	}
}

func TestMachineUnderstandLLM_errorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"model overloaded"}}`))
	}))
	defer srv.Close()
	if _, err := machineUnderstandLLM(context.Background(), srv.URL, "k", "m", testSchematic(), nil); err == nil {
		t.Error("expected error when the LLM returns an error object")
	}
}

func TestMachinePost_bearerAndStatus(t *testing.T) {
	const secret = "test-org-secret"
	var gotAuth string
	var gotDevice string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotDevice, _ = body["deviceId"].(string)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	code, err := machinePost(context.Background(), srv.URL+"/machine-edge/heartbeat", secret,
		map[string]any{"deviceId": "pi-edge-001"})
	if err != nil || code != 200 {
		t.Fatalf("machinePost: code=%d err=%v", code, err)
	}
	if gotAuth != "Bearer "+secret {
		t.Errorf("Talos did not receive the Bearer org secret, got %q", gotAuth)
	}
	if gotDevice != "pi-edge-001" {
		t.Errorf("Talos did not receive the body, deviceId=%q", gotDevice)
	}
}

func TestMachinePost_nonOKIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if code, err := machinePost(context.Background(), srv.URL, "s", map[string]any{}); err == nil || code != 500 {
		t.Errorf("expected 500 error, got code=%d err=%v", code, err)
	}
}
