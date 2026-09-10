package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestACPMCPChildReceivesItsOwnTaskContext(t *testing.T) {
	for _, id := range []string{"task-one", "task-two"} {
		servers := acpMCPServersForTask("sh", nil, true, &Task{ID: id, Source: "mobile-code"})
		// Exercise the serialized descriptor, with no inherited runner env.
		wire, err := json.Marshal(servers)
		if err != nil {
			t.Fatal(err)
		}
		var decoded []acpMCPServer
		if err := json.Unmarshal(wire, &decoded); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(decoded[0].Command, "-c", `printf '%s/%s' "$YAVER_TASK_ID" "$YAVER_TASK_SOURCE"`)
		cmd.Env = []string{}
		for _, env := range decoded[0].Env {
			cmd.Env = append(cmd.Env, env.Name+"="+env.Value)
		}
		out, err := cmd.Output()
		if err != nil || string(out) != id+"/mobile-code" {
			t.Fatalf("MCP child context = %q, err=%v; want this task's context", out, err)
		}
	}
}

func TestACPMCPTaskContextRespectsToolScope(t *testing.T) {
	external := []ExternalMCPServer{{Name: "external", URL: "https://example.com/mcp"}}
	for _, include := range []bool{false, true} {
		servers := acpMCPServersForTask("yaver", external, include, &Task{ID: "private-task-id"})
		for _, server := range servers {
			if server.Name == "yaver" {
				if !include {
					t.Fatal("Yaver MCP appeared despite deselection")
				}
				continue
			}
			wire, _ := json.Marshal(server)
			if len(server.Env) != 0 || strings.Contains(string(wire), "private-task-id") {
				t.Fatal("task context leaked into an external MCP descriptor")
			}
		}
	}
}
