package main

// ops_netcapture.go — wire-observe & deep-analysis verbs. These expose the
// netcapture package as ops verbs so a remote Commander (or an on-box Claude
// Code runner — e.g. a phone plugged into a machine via USB-RS485) can start a
// capture, tail decoded frames, and get a structured + LLM-narrated diagnosis of
// a PLC / robot / ERP link.
//
// Security posture mirrors ops_machine.go:
//   - Opt-in only: verbs refuse unless started with --netcapture.
//   - Owner-only: AllowGuest is false on every verb.
//   - Privacy: pcap + tty logs stay local (~/.yaver/netcapture); TDS/SQL payloads
//     are redacted by default. Nothing here is synced to Convex.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/yaver-io/agent/netcapture"
)

func netcaptureEngineForOps(c OpsContext) (*netcapture.Engine, *OpsResult) {
	if c.Server == nil {
		return nil, &OpsResult{OK: false, Code: "unavailable", Error: "no server context"}
	}
	if !c.Server.netcaptureEnabled {
		return nil, &OpsResult{OK: false, Code: "unauthorized", Error: "netcapture is disabled on this agent; start it with `yaver serve --netcapture`"}
	}
	eng, err := c.Server.ensureNetcapture()
	if err != nil {
		return nil, &OpsResult{OK: false, Code: "unsupported", Error: "netcapture engine unavailable: " + err.Error()}
	}
	return eng, nil
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "netcapture_status",
		Description: "List active wire-capture sessions (network + serial) and whether netcapture is enabled.",
		Schema:      ghostJSONSchema(map[string]interface{}{}),
		Handler:     netcaptureStatusHandler,
		AllowGuest:  false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "netcapture_start",
		Description: "Start a capture. kind=net taps a NIC with tcpdump (decoded in pure Go: Modbus-TCP, S7/LOGO!, OPC-UA, MS-SQL/TDS, HTTP, DNS, TCP health). kind=serial taps an RS232/RS485 tty (device=/dev/ttyUSB0, decoder=modbus_rtu|marlin|ascii|auto) — or omit device for a manual session fed via netcapture_feed (phone USB-serial / connector box / replay). Live events stream on /streams/netcapture:<session>.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"kind":           map[string]interface{}{"type": "string", "enum": []string{"net", "serial"}, "description": "net (default) or serial"},
			"iface":          map[string]interface{}{"type": "string", "description": "network interface for kind=net (default: any)"},
			"filter":         map[string]interface{}{"type": "string", "description": "BPF filter, e.g. 'tcp port 502' or 'host 10.0.0.50'"},
			"device":         map[string]interface{}{"type": "string", "description": "serial device for kind=serial, e.g. /dev/ttyUSB0 (omit for manual/fed session)"},
			"baud":           map[string]interface{}{"type": "integer", "description": "serial baud (default 9600)"},
			"decoder":        map[string]interface{}{"type": "string", "enum": []string{"modbus_rtu", "marlin", "ascii", "auto"}, "description": "serial decoder (default auto = modbus_rtu framing)"},
			"capturePayload": map[string]interface{}{"type": "boolean", "description": "high-risk: retain raw payloads (TDS/SQL bodies otherwise redacted)"},
		}),
		Handler:    netcaptureStartHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "netcapture_feed",
		Description: "Inject raw bytes (hex) into a serial capture session — the phone USB-serial / IoT-connector-box / replay path when there is no /dev/tty to open.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
			"hex":     map[string]interface{}{"type": "string", "description": "hex-encoded bytes"},
		}, "session", "hex"),
		Handler:    netcaptureFeedHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "netcapture_tail",
		Description: "Return the last N decoded events of a session (default 50) so the AI can read what's on the wire right now.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
			"n":       map[string]interface{}{"type": "integer"},
		}, "session"),
		Handler:    netcaptureTailHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "netcapture_analyze",
		Description: "Return the full structured deep-analysis (flows, per-protocol stats, disconnect timeline, deterministic findings) for a session, plus — unless diagnose=false — an LLM root-cause narrative with next steps. The primary AI-piping verb.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session":  map[string]interface{}{"type": "string"},
			"diagnose": map[string]interface{}{"type": "boolean", "description": "add an LLM narrative (default true)"},
			"baseUrl":  map[string]interface{}{"type": "string"},
			"apiKey":   map[string]interface{}{"type": "string"},
			"model":    map[string]interface{}{"type": "string"},
		}, "session"),
		Handler:    netcaptureAnalyzeHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "netcapture_stop",
		Description: "Stop a capture session and return its final structured analysis.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"session": map[string]interface{}{"type": "string"},
		}, "session"),
		Handler:    netcaptureStopHandler,
		AllowGuest: false,
	})
	registerOpsVerb(opsVerbSpec{
		Name:        "netcapture_pcap_decode",
		Description: "Decode an existing .pcap file with the pure-Go decoders (no tshark needed) and return the structured deep-analysis. Useful for captures pulled off another box.",
		Schema: ghostJSONSchema(map[string]interface{}{
			"file":       map[string]interface{}{"type": "string", "description": "path to a .pcap file"},
			"maxPackets": map[string]interface{}{"type": "integer", "description": "cap packets decoded (0 = all)"},
		}, "file"),
		Handler:    netcapturePcapDecodeHandler,
		AllowGuest: false,
	})
}

func netcaptureStatusHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := netcaptureEngineForOps(c)
	if deny != nil {
		return *deny
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"enabled": true, "sessions": eng.Sessions()}}
}

func netcaptureStartHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if _, deny := netcaptureEngineForOps(c); deny != nil {
		return *deny
	}
	var p struct {
		Kind           string `json:"kind"`
		Iface          string `json:"iface"`
		Filter         string `json:"filter"`
		Device         string `json:"device"`
		Baud           int    `json:"baud"`
		Decoder        string `json:"decoder"`
		CapturePayload bool   `json:"capturePayload"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	id, err := c.Server.startNetcapture(p.Kind, p.Iface, p.Filter, p.Device, p.Decoder, p.Baud, p.CapturePayload)
	out := map[string]interface{}{"session": id, "stream": "netcapture:" + id, "kind": firstNonEmptyStr(p.Kind, "net")}
	if err != nil {
		out["warning"] = err.Error()
	}
	return OpsResult{OK: id != "", Initial: out}
}

func netcaptureFeedHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := netcaptureEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
		Hex     string `json:"hex"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(p.Hex), " ", ""))
	if err != nil {
		return OpsResult{OK: false, Code: "bad_payload", Error: "invalid hex: " + err.Error()}
	}
	if err := eng.Feed(p.Session, raw); err != nil {
		return OpsResult{OK: false, Code: "not_found", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"fed": len(raw)}}
}

func netcaptureTailHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := netcaptureEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
		N       int    `json:"n"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if p.N <= 0 {
		p.N = 50
	}
	evs, ok := eng.Tail(p.Session, p.N)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"events": evs, "count": len(evs)}}
}

func netcaptureAnalyzeHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := netcaptureEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session  string `json:"session"`
		Diagnose *bool  `json:"diagnose"`
		BaseURL  string `json:"baseUrl"`
		APIKey   string `json:"apiKey"`
		Model    string `json:"model"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	an, ok := eng.Analyze(p.Session, 50)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
	}
	out := map[string]interface{}{"analysis": an}
	if p.Diagnose == nil || *p.Diagnose {
		if narrative, err := netcaptureDiagnoseLLM(c.Ctx, p.BaseURL, p.APIKey, p.Model, an); err == nil {
			out["diagnosis"] = narrative
		} else {
			out["diagnosisError"] = err.Error()
		}
	}
	return OpsResult{OK: true, Initial: out}
}

func netcaptureStopHandler(c OpsContext, payload json.RawMessage) OpsResult {
	eng, deny := netcaptureEngineForOps(c)
	if deny != nil {
		return *deny
	}
	var p struct {
		Session string `json:"session"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	an, ok := eng.Stop(p.Session)
	if !ok {
		return OpsResult{OK: false, Code: "not_found", Error: "unknown session"}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"analysis": an}}
}

func netcapturePcapDecodeHandler(c OpsContext, payload json.RawMessage) OpsResult {
	if _, deny := netcaptureEngineForOps(c); deny != nil {
		return *deny
	}
	var p struct {
		File       string `json:"file"`
		MaxPackets int    `json:"maxPackets"`
	}
	if r := ghostUnmarshal(payload, &p); r != nil {
		return *r
	}
	if strings.TrimSpace(p.File) == "" {
		return OpsResult{OK: false, Code: "bad_payload", Error: "file is required"}
	}
	an, err := netcapture.DecodePcapFile(p.File, p.MaxPackets)
	if err != nil {
		return OpsResult{OK: false, Code: "decode_failed", Error: err.Error()}
	}
	return OpsResult{OK: true, Initial: map[string]interface{}{"analysis": an}}
}

// ── LLM root-cause narrative (OpenAI-compatible; mirrors machineUnderstandLLM) ──

const netcaptureDiagnoseSystemPrompt = `You are an industrial troubleshooting expert for PLC / robotics / ERP links (Modbus TCP & RTU, Siemens S7/LOGO!, OPC-UA, MS-SQL/TDS, HTTP, DNS, and raw TCP/serial health). You are given a STRUCTURED capture analysis: per-protocol stats, top flows, a disconnect timeline, and deterministic findings. Write a concise root-cause diagnosis and concrete next steps. Be specific: name the offending device/flow/protocol and tie each conclusion to evidence in the data (a finding, a reset, an exception code, a latency, a CRC error). If the data is healthy, say so plainly. Plain text, no markdown headers, under ~200 words.`

func netcaptureDiagnoseLLM(ctx context.Context, baseURL, apiKey, model string, an netcapture.Analysis) (string, error) {
	baseURL = firstNonEmptyStr(baseURL, os.Getenv("GHOST_VISION_BASE_URL"), os.Getenv("OPENAI_BASE_URL"), localOllamaV1)
	apiKey = firstNonEmptyStr(apiKey, os.Getenv("GHOST_VISION_API_KEY"), os.Getenv("OPENAI_API_KEY"))
	if model == "" {
		model = firstNonEmptyStr(os.Getenv("GHOST_VISION_MODEL"), os.Getenv("OPENAI_MODEL"))
		if model == "" {
			if strings.Contains(baseURL, "11434") {
				model = "llama3.1"
			} else {
				model = "gpt-4o-mini"
			}
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	aj, _ := json.Marshal(an)
	body := map[string]any{
		"model":       model,
		"temperature": 0,
		"messages": []any{
			map[string]any{"role": "system", "content": netcaptureDiagnoseSystemPrompt},
			map[string]any{"role": "user", "content": "Capture analysis:\n" + string(aj)},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	cl := &http.Client{Timeout: 90 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != nil {
		return "", fmt.Errorf("diagnose: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("diagnose: empty response")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
