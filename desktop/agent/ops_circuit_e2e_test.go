package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yaver-io/agent/circuit"
)

// End-to-end exercise of the circuit service primitives through the REAL
// registered ops handlers: design slots persist + stay isolated, the new
// circuit_designs / circuit_health / circuit_design_delete verbs behave, and a
// simulation actually runs on the pure-Go MNA engine.
//
// HOME is redirected to a temp dir so vault open fails fast ("not authenticated"
// before any keychain access) and the cell falls back to ~/.yaver files under
// the temp HOME — no keychain prompt, no pollution of the real config.
func TestCircuitServicePrimitivesE2E(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// isolate the process-global controller cache from other tests
	circuitMu.Lock()
	circuitCtrls = map[string]*circuit.Controller{}
	circuitMu.Unlock()

	octx := OpsContext{Ctx: context.Background(), Server: &HTTPServer{}, Caller: "owner"}
	call := func(verb string, payload map[string]any) OpsResult {
		t.Helper()
		spec, ok := opsRegistry[verb]
		if !ok {
			t.Fatalf("verb %q not registered", verb)
		}
		raw, _ := json.Marshal(payload)
		return spec.Handler(octx, raw)
	}
	out := func(r OpsResult) map[string]any {
		m, _ := r.Initial.(map[string]any)
		return m
	}

	const rcDeck = "* RC\nV1 in 0 DC 0 AC 1 SIN(0 5 1k)\nR1 in out 1k\nC1 out 0 100n\n.end"
	// a deliberately different design so slot isolation is observable
	const divDeck = "* divider\nV1 in 0 DC 10\nR1 in out 2k\nR2 out 0 1k\n.end"

	// health on the empty default slot
	if r := call("circuit_health", map[string]any{}); !r.OK {
		t.Fatalf("circuit_health failed: %v", r.Error)
	} else if dc, _ := out(r)["designCount"].(int); dc < 1 {
		t.Fatalf("designCount = %v, want >= 1", out(r)["designCount"])
	}

	// import into a named slot
	r := call("circuit_import", map[string]any{"design": "panel-a", "format": "spice", "text": rcDeck})
	if !r.OK {
		t.Fatalf("import panel-a failed: %v", r.Error)
	}
	info, ok := out(r)["info"].(circuit.CircuitInfo)
	if !ok {
		t.Fatalf("import returned no CircuitInfo: %#v", out(r)["info"])
	}
	if info.ElementCount != 3 || !info.HasGround || !info.Simulatable {
		t.Fatalf("panel-a info = {el:%d gnd:%v sim:%v}, want {3 true true}", info.ElementCount, info.HasGround, info.Simulatable)
	}

	// a second, different design in its own slot
	if r := call("circuit_import", map[string]any{"design": "panel-b", "format": "spice", "text": divDeck}); !r.OK {
		t.Fatalf("import panel-b failed: %v", r.Error)
	}

	// circuit_designs lists default + both named slots
	dr := call("circuit_designs", map[string]any{})
	if !dr.OK {
		t.Fatalf("circuit_designs failed: %v", dr.Error)
	}
	designs, _ := out(dr)["designs"].([]map[string]any)
	got := map[string]bool{}
	for _, d := range designs {
		got[d["design"].(string)] = true
	}
	for _, want := range []string{"default", "panel-a", "panel-b"} {
		if !got[want] {
			t.Fatalf("circuit_designs missing %q; got %v", want, got)
		}
	}

	// simulate panel-a — the MNA engine actually runs and yields samples
	sr := call("circuit_simulate", map[string]any{"design": "panel-a", "type": "tran", "tstop": 5e-3})
	if !sr.OK {
		t.Fatalf("simulate panel-a failed: %v", sr.Error)
	}
	res, ok := out(sr)["result"].(circuit.SimResult)
	if !ok || len(res.Samples) == 0 {
		t.Fatalf("simulate produced no samples: %#v", out(sr)["result"])
	}

	// slot isolation: panel-a is untouched by panel-b's import
	desc := call("circuit_describe", map[string]any{"design": "panel-a"})
	di, _ := out(desc)["info"].(circuit.CircuitInfo)
	if di.ElementCount != 3 {
		t.Fatalf("panel-a clobbered: elementCount = %d, want 3", di.ElementCount)
	}

	// delete a named slot — it drops out of the listing
	if r := call("circuit_design_delete", map[string]any{"design": "panel-a"}); !r.OK {
		t.Fatalf("delete panel-a failed: %v", r.Error)
	}
	after := call("circuit_designs", map[string]any{})
	ad, _ := out(after)["designs"].([]map[string]any)
	for _, d := range ad {
		if d["design"] == "panel-a" {
			t.Fatalf("panel-a still listed after delete")
		}
	}

	// the default slot is protected from deletion
	if r := call("circuit_design_delete", map[string]any{"design": ""}); r.OK {
		t.Fatalf("deleting the default slot should be refused")
	}
}
