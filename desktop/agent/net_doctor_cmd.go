package main

// net_doctor_cmd.go — CLI + HTTP + MCP surfaces for RunNetDoctor.
//
//	yaver net doctor                 # full layered diagnosis (terminal)
//	yaver net doctor --throughput    # also measure download speed
//	yaver net doctor --target X      # also verify a specific host end-to-end
//	yaver net doctor --json          # machine-readable report
//
// HTTP (owner-auth):  POST /net/doctor  { throughput?, target?, skip_yaver? }
// MCP tool:           net_doctor        (so the AI can self-diagnose its runner)

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// runNet dispatches `yaver net <subcommand>`.
func runNet(args []string) {
	if len(args) == 0 {
		fmt.Println("usage: yaver net <doctor>")
		fmt.Println("  doctor   deep internet-connectivity troubleshooting (link/dhcp/gateway/dns/captive/https/quality)")
		os.Exit(1)
	}
	switch args[0] {
	case "doctor", "diagnose", "troubleshoot":
		runNetDoctorCLI(args[1:])
	default:
		fmt.Printf("unknown net subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

func runNetDoctorCLI(args []string) {
	fs := flag.NewFlagSet("net doctor", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Emit the report as JSON")
	thru := fs.Bool("throughput", false, "Also run a small download throughput test")
	target := fs.String("target", "", "Also verify a specific host end-to-end (e.g. github.com)")
	skipYaver := fs.Bool("skip-yaver", false, "Skip the Yaver-reachability layer")
	fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	rep := RunNetDoctor(ctx, NetDoctorOptions{
		Throughput: *thru,
		Target:     *target,
		SkipYaver:  *skipYaver,
	})

	if *jsonOut {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(b))
		if rep.Status == NetFail {
			os.Exit(2)
		}
		return
	}

	renderNetDoctorText(os.Stdout, rep)
	if rep.Status == NetFail {
		os.Exit(2)
	}
}

func netGlyph(s NetLayerStatus) string {
	switch s {
	case NetOK:
		return "✓"
	case NetWarn:
		return "!"
	case NetFail:
		return "✗"
	default:
		return "·"
	}
}

func renderNetDoctorText(w *os.File, rep NetDoctorReport) {
	fmt.Fprintf(w, "yaver net doctor — %s · %s", rep.Host, rep.Platform)
	if rep.Medium != "" && rep.Medium != "unknown" {
		fmt.Fprintf(w, " · %s", rep.Medium)
	}
	if rep.SSID != "" {
		fmt.Fprintf(w, " (%s)", rep.SSID)
	}
	fmt.Fprintf(w, "\n\n")

	for _, l := range rep.Layers {
		fmt.Fprintf(w, "  %s %-26s %s\n", netGlyph(l.Status), l.Title, l.Detail)
		if l.Hint != "" && (l.Status == NetFail || l.Status == NetWarn) {
			fmt.Fprintf(w, "      → %s\n", l.Hint)
		}
	}

	fmt.Fprintf(w, "\n")
	banner := "DIAGNOSIS"
	switch rep.Status {
	case NetOK:
		banner = "✓ ONLINE"
	case NetWarn:
		banner = "! DEGRADED"
	case NetFail:
		banner = "✗ PROBLEM"
	}
	fmt.Fprintf(w, "%s: %s\n", banner, rep.Verdict)
	if rep.RootCause != "" {
		fmt.Fprintf(w, "Root cause layer: %s\n", rep.RootCause)
	}
	if len(rep.Remediation) > 0 {
		fmt.Fprintf(w, "\nWhat to do:\n")
		for _, r := range rep.Remediation {
			fmt.Fprintf(w, "  • %s\n", r)
		}
	}
	fmt.Fprintf(w, "\n(%d ms)\n", rep.DurationMs)
}

// ─── HTTP ────────────────────────────────────────────────────────────

// handleNetDoctor (POST /net/doctor) runs the diagnosis and returns the report.
func (s *HTTPServer) handleNetDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Throughput bool   `json:"throughput,omitempty"`
		Target     string `json:"target,omitempty"`
		SkipYaver  bool   `json:"skip_yaver,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	rep := RunNetDoctor(ctx, NetDoctorOptions{
		Throughput: req.Throughput,
		Target:     strings.TrimSpace(req.Target),
		SkipYaver:  req.SkipYaver,
	})
	jsonReply(w, http.StatusOK, rep)
}

// mcpNetDoctor is the MCP handler — returns the full structured report so the AI
// can reason about *where* connectivity broke, not just raw tool output.
func mcpNetDoctor(throughput bool, target string) interface{} {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rep := RunNetDoctor(ctx, NetDoctorOptions{
		Throughput: throughput,
		Target:     strings.TrimSpace(target),
	})
	return rep
}
