package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func runAgentMeshSmoke(args []string) {
	fs := flag.NewFlagSet("agent mesh-smoke", flag.ExitOnError)
	device := fs.String("device", "", "target device id or hostname (default: first online remote machine)")
	command := fs.String("command", "printf MESH_SMOKE_OK", "custom command to run on the remote machine")
	timeout := fs.Duration("timeout", 45*time.Second, "how long to wait for the remote task")
	_ = fs.Parse(args)

	fmt.Println("Yaver mesh smoke")
	fmt.Println("----------------")

	machinesResp, err := localAgentRequest("GET", "/console/machines", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mesh smoke: inventory: %v\n", err)
		os.Exit(1)
	}
	rawMachines, _ := json.Marshal(machinesResp["machines"])
	var machines []MachineInfo
	_ = json.Unmarshal(rawMachines, &machines)
	if len(machines) == 0 {
		fmt.Fprintln(os.Stderr, "mesh smoke: no machines in inventory")
		os.Exit(1)
	}
	fmt.Printf("inventory: %d machine(s)\n", len(machines))
	for _, m := range machines {
		online := "offline"
		if m.IsOnline {
			online = "online"
		}
		fmt.Printf("  - %s (%s, %s, provider=%s)\n", m.Name, m.DeviceID, online, strings.TrimSpace(m.Provider))
	}

	statusResp, err := localAgentRequest("GET", "/agent/status", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mesh smoke: local status: %v\n", err)
		os.Exit(1)
	}
	rawStatus, _ := json.Marshal(statusResp["status"])
	var localStatus AgentStatus
	_ = json.Unmarshal(rawStatus, &localStatus)
	fmt.Printf("local agent: %s/%s, runner=%s installed=%v\n", localStatus.System.OS, localStatus.System.Arch, localStatus.Runner.ID, localStatus.Runner.Installed)

	target := pickMeshSmokeTarget(machines, *device)
	if target == nil {
		fmt.Println("remote test: skipped (no online remote machine found)")
		return
	}
	fmt.Printf("target: %s (%s)\n", target.Name, target.DeviceID)

	base, token, err := remoteAgentBaseAndToken(target.DeviceID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mesh smoke: resolve target: %v\n", err)
		os.Exit(1)
	}

	var capResp struct {
		OK      bool        `json:"ok"`
		Machine MachineInfo `json:"machine"`
	}
	if err := remoteAgentJSON(timeoutContext(*timeout), base, token, "GET", "/agent/capabilities", nil, &capResp); err != nil {
		fmt.Fprintf(os.Stderr, "mesh smoke: remote capabilities: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("remote capabilities: os=%s arch=%s runners=%d\n", capResp.Machine.OS, capResp.Machine.Arch, len(capResp.Machine.Capabilities.Runners))

	var createResp struct {
		OK     bool   `json:"ok"`
		TaskID string `json:"taskId"`
	}
	if err := remoteAgentJSON(timeoutContext(*timeout), base, token, "POST", "/tasks", map[string]interface{}{
		"title":         "mesh smoke",
		"description":   "verify remote task execution over mesh",
		"customCommand": *command,
		"source":        "agent-mesh-smoke",
	}, &createResp); err != nil {
		fmt.Fprintf(os.Stderr, "mesh smoke: remote task create: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("remote task created: %s\n", createResp.TaskID)

	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		var taskResp struct {
			OK   bool     `json:"ok"`
			Task TaskInfo `json:"task"`
		}
		if err := remoteAgentJSON(timeoutContext(15*time.Second), base, token, "GET", "/tasks/"+createResp.TaskID, nil, &taskResp); err != nil {
			fmt.Fprintf(os.Stderr, "mesh smoke: poll task: %v\n", err)
			os.Exit(1)
		}
		switch taskResp.Task.Status {
		case TaskStatusFinished:
			fmt.Printf("remote task completed: %s\n", strings.TrimSpace(taskResp.Task.ResultText))
			if !strings.Contains(taskResp.Task.Output, "MESH_SMOKE_OK") && !strings.Contains(taskResp.Task.ResultText, "MESH_SMOKE_OK") {
				fmt.Fprintln(os.Stderr, "mesh smoke: remote task finished but expected marker not found")
				os.Exit(1)
			}
			fmt.Println("mesh smoke: PASS")
			return
		case TaskStatusFailed, TaskStatusStopped:
			fmt.Fprintf(os.Stderr, "mesh smoke: remote task ended as %s: %s\n", taskResp.Task.Status, strings.TrimSpace(taskResp.Task.ResultText))
			os.Exit(1)
		}
		time.Sleep(1500 * time.Millisecond)
	}

	_ = remoteAgentJSON(timeoutContext(10*time.Second), base, token, "POST", "/tasks/"+createResp.TaskID+"/stop", map[string]interface{}{}, nil)
	fmt.Fprintln(os.Stderr, "mesh smoke: timed out waiting for remote task")
	os.Exit(1)
}

func pickMeshSmokeTarget(machines []MachineInfo, want string) *MachineInfo {
	want = strings.TrimSpace(want)
	for i := range machines {
		m := &machines[i]
		if m.IsLocal || !m.IsOnline {
			continue
		}
		if want == "" {
			return m
		}
		if m.DeviceID == want || strings.EqualFold(m.Name, want) || strings.HasPrefix(m.DeviceID, want) {
			return m
		}
	}
	return nil
}

func timeoutContext(d time.Duration) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}
