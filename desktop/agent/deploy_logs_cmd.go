package main

// deploy_logs_cmd.go — `yaver deploy logs <run-id>` CLI. Streams the
// full on-disk log of a previous /deploy/ship run via the agent
// HTTP API (/deploy/runs/{id}/output). Falls back to the in-memory
// tail if disk persistence was off for that run.
//
// Also `yaver deploy runs` (list) for discoverability.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

func runDeployLogsCmd(args []string) {
	fs := flag.NewFlagSet("deploy logs", flag.ExitOnError)
	machine := fs.String("machine", "", "Remote deviceId (default: local agent)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: yaver deploy logs <run-id> [--machine <deviceId>]")
		os.Exit(1)
	}
	id := strings.TrimSpace(fs.Arg(0))
	if id == "" {
		fmt.Fprintln(os.Stderr, "Error: missing run id")
		os.Exit(1)
	}

	resp, err := deployAgentGET("/deploy/runs/"+id+"/output", *machine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		os.Exit(1)
	}
	reader := bufio.NewReaderSize(resp.Body, 64*1024)
	_, err = io.Copy(os.Stdout, reader)
	if err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
}

func runDeployRunsCmd(args []string) {
	fs := flag.NewFlagSet("deploy runs", flag.ExitOnError)
	limit := fs.Int("limit", 20, "Number of recent runs to show")
	machine := fs.String("machine", "", "Remote deviceId (default: local agent)")
	asJSON := fs.Bool("json", false, "Emit JSON")
	fs.Parse(args)

	path := "/deploy/runs?limit=" + strconv.Itoa(*limit)
	resp, err := deployAgentGET(path, *machine)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "Error: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		os.Exit(1)
	}
	body, _ := io.ReadAll(resp.Body)
	if *asJSON {
		fmt.Print(string(body))
		return
	}
	var decoded struct {
		Runs []struct {
			ID          string `json:"id"`
			App         string `json:"app"`
			Target      string `json:"target"`
			StartedAt   int64  `json:"started_at"`
			DurationMs  int64  `json:"duration_ms"`
			ExitCode    int    `json:"exit_code"`
			OK          bool   `json:"ok"`
			InProgress  bool   `json:"in_progress"`
			ErrorClass  string `json:"error_class"`
			RequestedBy string `json:"requested_by"`
			IsGuest     bool   `json:"is_guest"`
		} `json:"runs"`
	}
	_ = json.Unmarshal(body, &decoded)
	if len(decoded.Runs) == 0 {
		fmt.Println("No deploy runs on this agent yet.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tAPP\tTARGET\tSTATUS\tDURATION\tCLASS\tBY\tSTARTED")
	for _, r := range decoded.Runs {
		status := "ok"
		if r.InProgress {
			status = "running"
		} else if !r.OK {
			status = fmt.Sprintf("fail(%d)", r.ExitCode)
		}
		dur := "-"
		if r.DurationMs > 0 {
			dur = time.Duration(r.DurationMs * int64(time.Millisecond)).Round(time.Second).String()
		}
		by := "owner"
		if r.IsGuest {
			if r.RequestedBy != "" {
				by = "guest:" + r.RequestedBy
			} else {
				by = "guest"
			}
		}
		class := r.ErrorClass
		if class == "" {
			class = "-"
		}
		started := time.UnixMilli(r.StartedAt).Format("2006-01-02 15:04:05")
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.App, r.Target, status, dur, class, by, started)
	}
	w.Flush()
}

// deployAgentGET is a tiny wrapper that issues a GET against the
// local agent or (when deviceID is set) against a peer of the same
// user. Returns the response for streaming — caller must Close Body.
func deployAgentGET(path, deviceID string) (*http.Response, error) {
	cfg, err := LoadConfig()
	if err != nil || cfg.AuthToken == "" {
		return nil, fmt.Errorf("not authenticated — run `yaver auth`")
	}
	var req *http.Request
	if d := strings.TrimSpace(deviceID); d != "" {
		candidates, token, err := resolveRemoteAgentCandidates(d)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("no reachable endpoint for device %s", d)
		}
		first := candidates[0]
		req, err = http.NewRequest("GET", first.BaseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		for k, v := range first.Headers {
			req.Header.Set(k, v)
		}
	} else {
		base := localAgentBaseURL()
		req, err = http.NewRequest("GET", base+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	req.Header.Set("Accept", "*/*")
	return http.DefaultClient.Do(req)
}
