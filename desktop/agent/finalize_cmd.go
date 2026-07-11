package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func runFinalize(args []string) {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printFinalizeUsage()
		return
	}
	switch args[0] {
	case "start":
		if err := runFinalizeStart(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "finalize start: %v\n", err)
			os.Exit(1)
		}
	case "list", "ls":
		if err := runFinalizeList(); err != nil {
			fmt.Fprintf(os.Stderr, "finalize list: %v\n", err)
			os.Exit(1)
		}
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver finalize show <id>")
			os.Exit(1)
		}
		if err := runFinalizeShow(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "finalize show: %v\n", err)
			os.Exit(1)
		}
	case "stop":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: yaver finalize stop <id>")
			os.Exit(1)
		}
		if err := runFinalizeStop(args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "finalize stop: %v\n", err)
			os.Exit(1)
		}
	case "tick":
		if err := finalizeLocalTick(); err != nil {
			fmt.Fprintf(os.Stderr, "finalize tick: %v\n", err)
			os.Exit(1)
		}
	default:
		if err := runFinalizeStart(args); err != nil {
			fmt.Fprintf(os.Stderr, "finalize: %v\n", err)
			os.Exit(1)
		}
	}
}

type repeatStringFlag []string

func (r *repeatStringFlag) String() string { return strings.Join(*r, ", ") }
func (r *repeatStringFlag) Set(v string) error {
	if strings.TrimSpace(v) != "" {
		*r = append(*r, strings.TrimSpace(v))
	}
	return nil
}

func runFinalizeStart(args []string) error {
	if err := ensureDaemonAlive(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("finalize start", flag.ExitOnError)
	machine := fs.String("machine", "", "remote Yaver machine to own the finalize run")
	runner := fs.String("runner", "", "runner override: claude, codex, opencode, glm")
	model := fs.String("model", "", "model override")
	mode := fs.String("mode", "", "runner mode override")
	workDir := fs.String("work-dir", "", "working directory")
	taskID := fs.String("task", "", "existing task id to finalize")
	testkitRoot := fs.String("testkit-root", "", "directory containing yaver-test-sdk specs (web, iOS sim, Android emu, Redroid)")
	inferTest := fs.Bool("infer-test", false, "infer a test command from the project")
	maxIterations := fs.Int("max-iterations", 20, "maximum continue/kick iterations")
	maxWallClock := fs.Int("max-minutes", 360, "maximum wall-clock minutes")
	kickEvery := fs.Int("kick-every", 90, "minimum seconds between kicks")
	var testCmds repeatStringFlag
	fs.Var(&testCmds, "test-cmd", "validation command; may be repeated")
	_ = fs.Parse(args)
	objective := strings.TrimSpace(strings.Join(fs.Args(), " "))

	body, _ := json.Marshal(FinalizeStartRequest{
		Objective:       objective,
		Runner:          *runner,
		Model:           *model,
		Mode:            *mode,
		WorkDir:         *workDir,
		TaskID:          *taskID,
		MaxIterations:   *maxIterations,
		MaxWallClockMin: *maxWallClock,
		KickIntervalSec: *kickEvery,
		TestCommands:    []string(testCmds),
		TestkitRoot:     *testkitRoot,
		InferTest:       *inferTest,
	})
	if strings.TrimSpace(*machine) != "" {
		var out struct {
			OK    bool         `json:"ok"`
			Run   *FinalizeRun `json:"run"`
			Error string       `json:"error"`
		}
		var remoteBody FinalizeStartRequest
		_ = json.Unmarshal(body, &remoteBody)
		if err := remoteAgentJSONForDevice(context.Background(), *machine, http.MethodPost, "/finalize", remoteBody, &out); err != nil {
			return err
		}
		if !out.OK || out.Run == nil {
			if out.Error != "" {
				return fmt.Errorf("%s", out.Error)
			}
			return fmt.Errorf("remote agent returned no finalize run")
		}
		fmt.Printf("Started finalize run %s on %s\n", out.Run.ID, *machine)
		if out.Run.TaskID != "" {
			fmt.Printf("Task: %s\n", out.Run.TaskID)
		}
		fmt.Printf("Status: %s\n", out.Run.Status)
		return nil
	}

	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	req, _ := http.NewRequest(http.MethodPost, localAgentBaseURL()+"/finalize", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		OK    bool         `json:"ok"`
		Run   *FinalizeRun `json:"run"`
		Error string       `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !out.OK || out.Run == nil {
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	fmt.Printf("Started finalize run %s\n", out.Run.ID)
	if out.Run.TaskID != "" {
		fmt.Printf("Task: %s\n", out.Run.TaskID)
	}
	fmt.Printf("Status: %s\n", out.Run.Status)
	return nil
}

func runFinalizeList() error {
	resp, err := localAgentRequest("GET", "/finalize", nil)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(data))
	return nil
}

func runFinalizeShow(id string) error {
	resp, err := localAgentRequest("GET", "/finalize/"+strings.TrimSpace(id), nil)
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println(string(data))
	return nil
}

func runFinalizeStop(id string) error {
	_, err := localAgentRequest("POST", "/finalize/"+strings.TrimSpace(id)+"/stop", map[string]any{})
	if err != nil {
		return err
	}
	fmt.Println("stopped " + strings.TrimSpace(id))
	return nil
}

func runFinalizeFromRunnerPassthrough(runnerID string, opts runnerPassthroughOpts, runnerArgs []string) error {
	objective := strings.TrimSpace(strings.Join(runnerArgs, " "))
	if objective == "" {
		objective = "Continue the current work until validation passes."
	}
	req := FinalizeStartRequest{
		Objective:       objective,
		Runner:          runnerID,
		WorkDir:         opts.cwd,
		MaxIterations:   20,
		MaxWallClockMin: 360,
		KickIntervalSec: 90,
		InferTest:       true,
	}
	if strings.TrimSpace(req.WorkDir) == "" && strings.TrimSpace(opts.machine) == "" {
		if cwd, err := os.Getwd(); err == nil {
			req.WorkDir = cwd
		}
	}
	if strings.TrimSpace(opts.machine) == "" {
		return startFinalizeLocal(req)
	}
	var out struct {
		OK    bool         `json:"ok"`
		Run   *FinalizeRun `json:"run"`
		Error string       `json:"error"`
	}
	if err := remoteAgentJSONForDevice(context.Background(), opts.machine, http.MethodPost, "/finalize", req, &out); err != nil {
		return err
	}
	if !out.OK || out.Run == nil {
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		return fmt.Errorf("remote agent returned no finalize run")
	}
	fmt.Printf("Started finalize run %s on %s\n", out.Run.ID, opts.machine)
	if out.Run.TaskID != "" {
		fmt.Printf("Task: %s\n", out.Run.TaskID)
	}
	fmt.Printf("Status: %s\n", out.Run.Status)
	return nil
}

func startFinalizeLocal(reqBody FinalizeStartRequest) error {
	if err := ensureDaemonAlive(); err != nil {
		return err
	}
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest(http.MethodPost, localAgentBaseURL()+"/finalize", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var out struct {
		OK    bool         `json:"ok"`
		Run   *FinalizeRun `json:"run"`
		Error string       `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !out.OK || out.Run == nil {
		if out.Error != "" {
			return fmt.Errorf("%s", out.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	fmt.Printf("Started finalize run %s\n", out.Run.ID)
	if out.Run.TaskID != "" {
		fmt.Printf("Task: %s\n", out.Run.TaskID)
	}
	fmt.Printf("Status: %s\n", out.Run.Status)
	return nil
}

func printFinalizeUsage() {
	fmt.Print(`yaver finalize — closed-loop coding finalizer

Usage:
  yaver finalize start [flags] "objective"
  yaver finalize list
  yaver finalize show <id>
  yaver finalize stop <id>
  yaver finalize tick

Examples:
  yaver finalize start --runner codex --infer-test "finish the checkout flow"
  yaver finalize start --runner codex --test-cmd "go test ./..." "make tests pass"
  yaver finalize start --runner codex --testkit-root yaver-tests "validate the UI in browser/Redroid"

Linux installs a user systemd timer (yaver-finalize.timer) so active finalize
runs keep getting kicked even after the launching terminal exits.
`)
}
