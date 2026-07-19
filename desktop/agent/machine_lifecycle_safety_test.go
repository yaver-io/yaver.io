package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMachineLifecycleDryRunsWithoutLiveGateEvenWhenConfirmed(t *testing.T) {
	t.Setenv("YAVER_CLOUD_STOPSTART_LIVE", "")

	cases := []struct {
		name    string
		handler func(json.RawMessage) OpsResult
		body    string
	}{
		{
			name: "create",
			handler: func(b json.RawMessage) OpsResult {
				return opsMachineCreateHandler(OpsContext{}, b)
			},
			body: `{"name":"box-a","confirm":true}`,
		},
		{
			name: "up",
			handler: func(b json.RawMessage) OpsResult {
				return opsMachineUpHandler(OpsContext{}, b)
			},
			body: `{"snapshotImageId":"99001","name":"box-a","confirm":true}`,
		},
		{
			name: "down",
			handler: func(b json.RawMessage) OpsResult {
				return opsMachineDownHandler(OpsContext{}, b)
			},
			body: `{"serverId":"123","confirm":true}`,
		},
		{
			name: "rm",
			handler: func(b json.RawMessage) OpsResult {
				return opsMachineRmHandler(OpsContext{}, b)
			},
			body: `{"serverId":"123","confirm":true,"purge":true}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.handler(json.RawMessage(tc.body))
			if !res.OK {
				t.Fatalf("expected dry-run OK, got %+v", res)
			}
			m, _ := res.Initial.(map[string]interface{})
			if m == nil || m["dryRun"] != true {
				t.Fatalf("expected dryRun:true, got %#v", res.Initial)
			}
			if why := strings.TrimSpace(m["why"].(string)); !strings.Contains(why, "YAVER_CLOUD_STOPSTART_LIVE") {
				t.Fatalf("why = %q, want live-gate reason", why)
			}
		})
	}
}

func TestMachineConfirmPlanRequiresConfirmAndLiveGate(t *testing.T) {
	t.Setenv("YAVER_CLOUD_STOPSTART_LIVE", "1")
	if gate := machineConfirmPlan(false, "would spend"); gate == nil {
		t.Fatal("missing confirm must dry-run even when live gate is set")
	}
	if gate := machineConfirmPlan(true, "would spend"); gate != nil {
		t.Fatalf("confirm + live gate should pass, got %+v", gate)
	}
}
