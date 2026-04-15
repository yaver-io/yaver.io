package main

// loop_exec_claude_stream.go — parser for `claude --output-format
// stream-json`. Each line of stdout is a JSON event; we print a
// short human-readable line per event so the user tailing the run
// sees Claude's actual work — tool calls, assistant chatter, file
// edits — instead of staring at silence for minutes.
//
// Permissive by design: anything we don't recognise gets dumped
// verbatim with a dim prefix, never dropped. The final "result"
// event carries the AIResponse contract.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// parseClaudeStream reads stream-json events from r, prints a live
// progress line per event to os.Stderr, and returns the AIResponse
// extracted from the final "result" event (if any), plus the
// session_id Claude assigned to this turn so the next kick can
// resume the same conversation via `claude --resume <id>`.
func parseClaudeStream(r io.Reader) (*AIResponse, string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	var lastResultText, sessionID string
	for scanner.Scan() {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" {
			continue
		}

		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			fmt.Fprintln(os.Stderr, raw)
			continue
		}

		printClaudeEvent(ev)

		// system/init carries the session_id we resume against next time.
		if ev["type"] == "system" {
			if sub, _ := ev["subtype"].(string); sub == "init" {
				if sid, ok := ev["session_id"].(string); ok && sid != "" {
					sessionID = sid
				}
			}
		}

		// The "result" event ends the turn.
		if ev["type"] == "result" {
			if r, ok := ev["result"].(string); ok && r != "" {
				lastResultText = r
			} else if t, ok := ev["text"].(string); ok && t != "" {
				lastResultText = t
			}
			// Some schemas echo session_id on the result event too.
			if sid, ok := ev["session_id"].(string); ok && sid != "" && sessionID == "" {
				sessionID = sid
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, sessionID, fmt.Errorf("read claude stream: %w", err)
	}
	if lastResultText == "" {
		return nil, sessionID, fmt.Errorf("claude stream ended with no result event")
	}
	resp, err := parseAIResponse(lastResultText)
	return resp, sessionID, err
}

// printClaudeEvent renders a single event as a one-line progress
// note. We try to surface the most useful field for each event
// type; unknown shapes fall back to a short JSON dump so nothing
// is silently swallowed.
func printClaudeEvent(ev map[string]interface{}) {
	t, _ := ev["type"].(string)
	switch t {
	case "system":
		// init / model info — skip the noisy ones, surface the rest.
		if sub, _ := ev["subtype"].(string); sub == "init" {
			model, _ := ev["model"].(string)
			tools, _ := ev["tools"].([]interface{})
			fmt.Fprintf(os.Stderr, "[claude] session init — model=%s tools=%d\n", model, len(tools))
			return
		}
	case "assistant":
		// Walk the content blocks: text => print snippet, tool_use
		// => print "tool: <name> <args summary>".
		msg, _ := ev["message"].(map[string]interface{})
		if msg == nil {
			return
		}
		blocks, _ := msg["content"].([]interface{})
		for _, b := range blocks {
			block, _ := b.(map[string]interface{})
			if block == nil {
				continue
			}
			switch block["type"] {
			case "text":
				txt, _ := block["text"].(string)
				if s := claudeStreamLine(txt, 200); s != "" {
					fmt.Fprintln(os.Stderr, s)
				}
			case "tool_use":
				name, _ := block["name"].(string)
				input, _ := block["input"].(map[string]interface{})
				fmt.Fprintf(os.Stderr, "[claude] %s %s\n", name, summariseToolInput(name, input))
			}
		}
		return
	case "user":
		// Tool result delivered back to Claude. Show a short note
		// so the user knows the tool finished.
		msg, _ := ev["message"].(map[string]interface{})
		if msg == nil {
			return
		}
		blocks, _ := msg["content"].([]interface{})
		for _, b := range blocks {
			block, _ := b.(map[string]interface{})
			if block == nil || block["type"] != "tool_result" {
				continue
			}
			isErr, _ := block["is_error"].(bool)
			tag := "ok"
			if isErr {
				tag = "ERR"
			}
			fmt.Fprintf(os.Stderr, "[claude]   → %s\n", tag)
		}
		return
	case "result":
		// Final summary line so the user sees the kick wrapped up.
		st, _ := ev["subtype"].(string)
		dur, _ := ev["duration_ms"].(float64)
		cost, _ := ev["total_cost_usd"].(float64)
		fmt.Fprintf(os.Stderr, "[claude] result: %s (%.1fs, $%.4f)\n",
			st, dur/1000.0, cost)
		return
	}
	// Unknown event — dump compact JSON so the user can see *something*.
	if b, err := json.Marshal(ev); err == nil {
		fmt.Fprintf(os.Stderr, "[claude] %s\n", claudeStreamLine(string(b), 240))
	}
}

func summariseToolInput(name string, input map[string]interface{}) string {
	if input == nil {
		return ""
	}
	// Common fields across the most-used built-in tools.
	for _, k := range []string{"file_path", "path", "command", "url", "pattern", "description"} {
		if v, ok := input[k].(string); ok && v != "" {
			return claudeStreamLine(v, 200)
		}
	}
	// Fallback: small JSON blob of the input.
	b, _ := json.Marshal(input)
	return claudeStreamLine(string(b), 200)
}

func claudeStreamLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}
