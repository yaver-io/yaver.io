// Command machinetest is the edge-agent side of the Talos-IoT machine-hijack
// e2e: it stands in for the Yaver agent on a Pi wired to a machine's Modbus bus.
// Against a live Modbus-TCP slave (cmd/modbus-emu) it exercises the full flow —
//
//	READ plane:  ScanTCP (absorb the register map) + repeated ReadTCP (observe
//	             the live counter advancing + measurement jitter).
//	WRITE plane: WriteTCP a setpoint, verified by read-back (the safe-write gate).
//	SYNC:        POST the discovered schematic to a mock Talos /machine-edge
//	             endpoint with a Bearer org secret (the edge→commander leg).
//
// PASS = registers absorbed + counter advanced + write read-back verified +
// Talos sync accepted. No hardware, no cloud — runs in a plain Linux container.
//
//	machinetest flow <modbusAddr>
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/yaver-io/agent/machine"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "flow" {
		fmt.Fprintln(os.Stderr, "usage: machinetest flow <modbusAddr>")
		os.Exit(2)
	}
	addr := os.Args[2]
	const timeout = 3 * time.Second

	eng, _ := machine.New()

	// READ plane — absorb the register map.
	sch, err := eng.ScanTCP(addr, 1, 3, 0, 5, timeout)
	if err != nil {
		fail("ScanTCP", err)
	}
	fmt.Printf("[edge] absorbed %d registers from PLC %s (driver=%s)\n", len(sch.Registers), addr, sch.Driver)
	if len(sch.Registers) < 5 {
		fail("scan", fmt.Errorf("expected 5 registers, got %d", len(sch.Registers)))
	}

	// READ plane — observe the live counter (reg 2) advancing over time.
	first, err := eng.ReadTCP(addr, 1, 3, 0, 5, timeout)
	if err != nil {
		fail("ReadTCP", err)
	}
	fmt.Printf("[edge] t0 registers: %v\n", first)
	time.Sleep(1 * time.Second)
	later, err := eng.ReadTCP(addr, 1, 3, 0, 5, timeout)
	if err != nil {
		fail("ReadTCP#2", err)
	}
	fmt.Printf("[edge] t1 registers: %v\n", later)
	if later[2] <= first[2] {
		fail("counter", fmt.Errorf("piece counter did not advance (%d -> %d)", first[2], later[2]))
	}
	fmt.Printf("[edge] piece counter advanced %d -> %d ✓ (live READ plane)\n", first[2], later[2])

	// WRITE plane — set the cut-length setpoint, verified by read-back.
	const newSetpoint = 1300
	rb, err := eng.WriteTCP(addr, 1, 0, newSetpoint, timeout)
	if err != nil {
		fail("WriteTCP", err)
	}
	if rb != newSetpoint {
		fail("write-readback", fmt.Errorf("read-back %d != written %d", rb, newSetpoint))
	}
	fmt.Printf("[edge] wrote setpoint=%d, verified read-back=%d ✓ (WRITE plane)\n", newSetpoint, rb)

	// SYNC — push the schematic to a mock Talos commander (edge→Talos leg).
	if err := syncToMockTalos(sch); err != nil {
		fail("talos-sync", err)
	}
	fmt.Println("[edge] schematic synced to Talos /machine-edge ✓")

	fmt.Println()
	fmt.Println("RESULT: PASS — Talos-IoT edge flow (absorb → observe → verified write → sync) ✓")
}

// syncToMockTalos mirrors machine_sync: POST the schematic to /machine-edge with
// a Bearer org secret; a mock commander must accept it (200) and see the data.
func syncToMockTalos(sch machine.Schematic) error {
	const orgSecret = "test-org-secret"
	var gotAuth, gotRegisters bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization") == "Bearer "+orgSecret
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if regs, ok := body["schematic"].(map[string]any); ok {
			if rs, ok := regs["registers"].([]any); ok && len(rs) > 0 {
				gotRegisters = true
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	payload, _ := json.Marshal(map[string]any{
		"deviceId":  "pi-edge-001",
		"machineId": "wire-machine-1",
		"schematic": sch,
	})
	req, _ := http.NewRequest("POST", srv.URL+"/machine-edge/manual", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+orgSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("talos returned %d", resp.StatusCode)
	}
	if !gotAuth {
		return fmt.Errorf("talos did not receive the Bearer org secret")
	}
	if !gotRegisters {
		return fmt.Errorf("talos did not receive the schematic registers")
	}
	return nil
}

func fail(stage string, err error) {
	fmt.Printf("\nRESULT: FAIL at %s: %v\n", stage, err)
	os.Exit(1)
}
