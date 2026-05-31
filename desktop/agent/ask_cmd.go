package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runAsk implements `yaver ask "<question>"` — a grounded, deep
// question-answer against this repo/machine, streamed to the terminal.
//
// Unlike `yaver code "<prompt>"` (which frames the run as work and tells the
// runner to act on defaults without asking), ask mode reframes the run as
// explain-first deep analysis: the agent reads the actual code, cites
// file:line, escalates from a shallow scan to a wider cross-checked read for
// broad questions, and only modifies the working tree / deploys / touches git
// after confirming via yaver_ask_user. See askModePreamble() for the contract.
//
// Usage:
//
//	yaver ask how do I test STT/TTS
//	yaver ask "where does auth get wired?" --runner codex
//	yaver ask "why does the relay fall back to QUIC?" --dir relay
func runAsk(args []string) {
	var runner, model, workDir string
	depth := "auto" // auto | single | deep
	var questionParts []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--runner", "-r":
			if i+1 < len(args) {
				i++
				runner = args[i]
			}
		case "--model", "-m":
			if i+1 < len(args) {
				i++
				model = args[i]
			}
		case "--dir", "--work-dir", "-C":
			if i+1 < len(args) {
				i++
				workDir = args[i]
			}
		case "--deep":
			depth = "deep"
		case "--shallow", "--single":
			depth = "single"
		case "-h", "--help":
			printAskUsage()
			return
		default:
			questionParts = append(questionParts, args[i])
		}
	}

	question := strings.TrimSpace(strings.Join(questionParts, " "))
	if question == "" {
		printAskUsage()
		os.Exit(2)
	}

	// Depth resolution: explicit --deep/--shallow win; auto escalates to the
	// multi-agent graph only when the question reads as broad/architectural.
	goDeep := depth == "deep" || (depth == "auto" && detectAskBreadth(question))

	if goDeep {
		runID, err := createAskGraph(question, runner, model, workDir)
		if err != nil {
			fmt.Printf("error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[ask] broad question — escalating to a deep investigate → answer → verify graph (%s)\n\n", runID)
		if err := streamCodeGraph(runID); err != nil {
			fmt.Printf("\nstream error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	taskID, err := createAskTask(question, runner, model, workDir)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	if err := streamCodeTask(taskID, "ask"); err != nil {
		fmt.Printf("\nstream error: %v\n", err)
		os.Exit(1)
	}
}

func printAskUsage() {
	fmt.Println(`usage: yaver ask <question> [--deep|--shallow] [--runner claude|codex|opencode] [--model <id>] [--dir <path>]

Ask a question about this repo/machine and get a deep, grounded answer
(reads the actual code, cites file:line). The agent explains first and only
acts after confirming with you.

By default a narrow question runs one read-only agent; a broad/architectural
question auto-escalates to a multi-agent graph (investigate → answer → verify).
  --deep      force the multi-agent graph
  --shallow   force a single agent

examples:
  yaver ask how do I test STT/TTS
  yaver ask "where does auth get wired?" --runner codex
  yaver ask "how does auth work end to end?" --deep
  yaver ask "why does the relay fall back to QUIC?" --dir relay`)
}

func createAskGraph(question, runner, model, workDir string) (string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return "", fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	resp, err := localAgentRequest("POST", "/agent/graphs", map[string]interface{}{
		"name":     "ask",
		"prompt":   question,
		"template": "ask",
		"runner":   runner,
		"model":    model,
		"workDir":  strings.TrimSpace(workDir),
	})
	if err != nil {
		return "", fmt.Errorf("is the agent running? start it with 'yaver serve' (%w)", err)
	}
	if run, ok := resp["run"].(map[string]interface{}); ok {
		if id, _ := run["id"].(string); id != "" {
			return id, nil
		}
	}
	if msg, _ := resp["error"].(string); msg != "" {
		return "", fmt.Errorf("%s", msg)
	}
	return "", fmt.Errorf("ask graph start returned no run id")
}

func createAskTask(question, runner, model, workDir string) (string, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return "", fmt.Errorf("not authenticated — run 'yaver auth'")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"title":   question,
		"runner":  runner,
		"model":   model,
		"workDir": strings.TrimSpace(workDir),
		"source":  "ask",
		"askMode": true,
	})
	req, _ := http.NewRequest("POST", "http://127.0.0.1:18080/tasks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Yaver-Source", "ask")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("is the agent running? start it with 'yaver serve' (%w)", err)
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool   `json:"ok"`
		TaskID string `json:"taskId"`
		Error  string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if !result.OK || result.TaskID == "" {
		if result.Error != "" {
			return "", fmt.Errorf("%s", result.Error)
		}
		return "", fmt.Errorf("ask failed (status %d)", resp.StatusCode)
	}
	return result.TaskID, nil
}
