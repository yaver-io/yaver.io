package main

import (
	"encoding/json"
	"testing"
)

// TestClaudeEventUsageParse pins the wire shape we rely on for the mobile
// "tokens used N" line. claude-code's stream-json result event carries
// usage as a sibling of `result` and `total_cost_usd`; the agent reads
// input_tokens + cache_creation_input_tokens + cache_read_input_tokens
// as "input" (the user's effective prompt size) and output_tokens as
// "output". If claude-code ever moves usage inside `message.usage` (as
// older partial-message events did) this test will fail and the agent
// will need a second parse path.
func TestClaudeEventUsageParse(t *testing.T) {
	const line = `{"type":"result","subtype":"success","result":"done","total_cost_usd":0.0234,"usage":{"input_tokens":120,"output_tokens":456,"cache_creation_input_tokens":10,"cache_read_input_tokens":20}}`

	var ev ClaudeEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev.Type != "result" {
		t.Fatalf("type = %q, want result", ev.Type)
	}
	if ev.Usage == nil {
		t.Fatalf("Usage is nil — claude-code stream shape changed; update the parser")
	}
	if ev.Usage.InputTokens != 120 || ev.Usage.OutputTokens != 456 {
		t.Errorf("input/output = %d/%d, want 120/456", ev.Usage.InputTokens, ev.Usage.OutputTokens)
	}
	if ev.Usage.CacheCreationInputTokens != 10 || ev.Usage.CacheReadInputTokens != 20 {
		t.Errorf("cache create/read = %d/%d, want 10/20", ev.Usage.CacheCreationInputTokens, ev.Usage.CacheReadInputTokens)
	}

	// The mobile-facing total includes cache reads + cache creation in
	// "input" since they all consume context. This matches what the
	// claude-code CLI footer prints to the terminal.
	gotInput := ev.Usage.InputTokens + ev.Usage.CacheCreationInputTokens + ev.Usage.CacheReadInputTokens
	if gotInput != 150 {
		t.Errorf("effective input = %d, want 150", gotInput)
	}
}

// TestTaskInfoTokensJSON pins the JSON tags so the mobile client (which
// reads `inputTokens` / `outputTokens`) keeps working as the Task struct
// grows. If someone renames these tags by accident, the mobile bubble
// silently shows "tokens used 0" — this test catches that at compile-time.
func TestTaskInfoTokensJSON(t *testing.T) {
	info := TaskInfo{InputTokens: 1234, OutputTokens: 567}
	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := got["inputTokens"].(float64); !ok || int(v) != 1234 {
		t.Errorf("inputTokens missing or wrong: %v", got["inputTokens"])
	}
	if v, ok := got["outputTokens"].(float64); !ok || int(v) != 567 {
		t.Errorf("outputTokens missing or wrong: %v", got["outputTokens"])
	}
}
