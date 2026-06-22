package main

// store_copy.go — AI marketing-copy drafter for the store listing.
//
// The model drafts subtitle/description/keywords/what's-new, but GROUNDED on
// the DerivationContext (the detected capabilities + SDKs) so it can't invent
// features the app doesn't have — Apple rejects copy describing functionality
// that isn't there, and a normie won't catch it. Output is strict JSON we
// parse + clamp (Apple's keyword field is one comma-string ≤100 chars).
//
// Inference goes through the Yaver gateway (the wallet IS the key). With no
// gateway configured we print the grounded prompt so the user/their agent can
// run it. The pure parts (prompt build, parse, keyword cap) are unit-tested.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type CopyDraft struct {
	Subtitle    string   `json:"subtitle"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	WhatsNew    string   `json:"whatsNew"`
}

func buildCopyPrompt(l StoreListing) string {
	caps := strings.Join(l.Derivation.DetectedCapabilities, ", ")
	if caps == "" {
		caps = "(none detected — keep claims generic; do NOT invent features)"
	}
	var b strings.Builder
	b.WriteString("You are writing App Store / Google Play listing copy. Be concrete, honest, and concise.\n")
	b.WriteString("STRICT RULES:\n")
	b.WriteString("- Only describe functionality supported by the DETECTED CAPABILITIES below. Never invent features.\n")
	b.WriteString("- subtitle ≤ 30 chars. keywords: 5–12 short terms, no spaces inside a term, no duplicates, total ≤ 100 chars when comma-joined.\n")
	b.WriteString("- description: 2–4 short paragraphs, no emoji spam, no keyword stuffing (Apple rejects it).\n")
	b.WriteString("- Output ONLY a JSON object: {\"subtitle\":\"\",\"description\":\"\",\"keywords\":[],\"whatsNew\":\"\"}\n\n")
	fmt.Fprintf(&b, "APP NAME: %s\n", dashIfEmpty(l.AppName))
	fmt.Fprintf(&b, "DETECTED CAPABILITIES: %s\n", caps)
	if len(l.Derivation.SDKs) > 0 {
		fmt.Fprintf(&b, "SDKs: %s\n", strings.Join(l.Derivation.SDKs, ", "))
	}
	return b.String()
}

// parseCopyDraft extracts the JSON object from a model response (tolerating
// ```json fences / prose around it) and clamps to store limits.
func parseCopyDraft(s string) (CopyDraft, error) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return CopyDraft{}, fmt.Errorf("no JSON object in model response")
	}
	var d CopyDraft
	if err := json.Unmarshal([]byte(s[start:end+1]), &d); err != nil {
		return CopyDraft{}, fmt.Errorf("parse draft JSON: %w", err)
	}
	d.Keywords = clampKeywords(d.Keywords, 100)
	return d, nil
}

// clampKeywords dedupes and trims the keyword list so the comma-joined string
// stays within Apple's limit (default 100 chars).
func clampKeywords(in []string, maxLen int) []string {
	seen := map[string]bool{}
	var out []string
	total := 0
	for _, k := range in {
		k = strings.TrimSpace(k)
		if k == "" || seen[strings.ToLower(k)] {
			continue
		}
		add := len(k)
		if len(out) > 0 {
			add++ // comma separator
		}
		if total+add > maxLen {
			break
		}
		seen[strings.ToLower(k)] = true
		out = append(out, k)
		total += add
	}
	return out
}

func applyCopyDraft(l *StoreListing, d CopyDraft) {
	if d.Subtitle != "" {
		l.Subtitle = d.Subtitle
	}
	if d.Description != "" {
		l.Description = d.Description
	}
	if len(d.Keywords) > 0 {
		l.Keywords = d.Keywords
	}
	if d.WhatsNew != "" {
		l.WhatsNew = d.WhatsNew
	}
}

// gatewayChat calls the Yaver gateway's OpenAI-compatible endpoint with the
// user's auth token (the wallet is the key). Returns the assistant text.
func gatewayChat(prompt string) (string, error) {
	base := gatewayBaseURL()
	if base == "" {
		return "", fmt.Errorf("no gateway configured (set YAVER_GATEWAY_URL)")
	}
	convexURL, token, err := loadAuthedConfig()
	_ = convexURL
	if err != nil || token == "" {
		return "", fmt.Errorf("not authenticated (run `yaver auth`)")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":    "auto",
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequest("POST", base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("unexpected gateway response")
	}
	return out.Choices[0].Message.Content, nil
}

func runListingDraft(args []string) {
	path := "."
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--path":
			if i+1 < len(args) {
				path = args[i+1]
				i++
			}
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Println("Usage: yaver listing draft [--path DIR] [--json]")
			fmt.Println("  Drafts subtitle/description/keywords/what's-new via the gateway, grounded")
			fmt.Println("  on your detected capabilities. No gateway → prints the prompt to run.")
			return
		}
	}
	listing := BuildStoreListing(path)
	prompt := buildCopyPrompt(listing)

	resp, err := gatewayChat(prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "AI draft unavailable (%v). Grounded prompt to run yourself:\n\n%s\n", err, prompt)
		return
	}
	draft, err := parseCopyDraft(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Couldn't parse the model output (%v). Raw:\n%s\n", err, resp)
		return
	}
	applyCopyDraft(&listing, draft)
	if jsonOut {
		b, _ := json.MarshalIndent(draft, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Println("AI-drafted listing copy (grounded on your capabilities):")
	fmt.Printf("\n  Subtitle: %s\n", draft.Subtitle)
	fmt.Printf("  Keywords: %s\n", strings.Join(draft.Keywords, ", "))
	fmt.Printf("\n  Description:\n%s\n", draft.Description)
	if draft.WhatsNew != "" {
		fmt.Printf("\n  What's new:\n%s\n", draft.WhatsNew)
	}
	fmt.Println("\n  Review + edit, then push with: yaver listing push --store apple --live")
}
