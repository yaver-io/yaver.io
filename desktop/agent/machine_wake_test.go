package main

import "testing"

func TestMachineWakeDecision(t *testing.T) {
	cases := []struct {
		name      string
		row       *byoMachineRow
		wantReady bool
		wantOK    bool
		wantCode  string
	}{
		{"nil row", nil, false, false, "not_found"},
		{"already active", &byoMachineRow{Name: "box", State: "active", ServerIP: "1.2.3.4"}, false, true, ""},
		{"permanently deleted", &byoMachineRow{Name: "box", State: "deleted"}, false, false, "deleted"},
		{"stopped no snapshot", &byoMachineRow{Name: "box", State: "stopped"}, false, false, "no_snapshot"},
		{"stopped with snapshot", &byoMachineRow{Name: "box", State: "stopped", SnapshotImageID: "img-9"}, true, false, ""},
		{"unknown state", &byoMachineRow{Name: "box", State: "provisioning"}, false, false, "bad_state"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ready, early := machineWakeDecision(c.row)
			if ready != c.wantReady {
				t.Fatalf("ready=%v want %v", ready, c.wantReady)
			}
			if ready {
				if early != nil {
					t.Fatalf("ready path must not return an early result")
				}
				return
			}
			if early == nil {
				t.Fatal("non-ready path must return an early result")
			}
			if early.OK != c.wantOK {
				t.Fatalf("OK=%v want %v", early.OK, c.wantOK)
			}
			if c.wantCode != "" && early.Code != c.wantCode {
				t.Fatalf("code=%q want %q", early.Code, c.wantCode)
			}
		})
	}
}

func TestMachineWake_Registered(t *testing.T) {
	opsRegistryMu.RLock()
	_, ok := opsRegistry["machine_wake"]
	opsRegistryMu.RUnlock()
	if !ok {
		t.Fatal("machine_wake verb not registered")
	}
}
