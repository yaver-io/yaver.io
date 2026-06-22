package main

import (
	"strings"
	"testing"
)

func TestBuildCopyPromptGrounds(t *testing.T) {
	l := StoreListing{
		AppName:    "Receipt Scanner",
		Derivation: DerivationContext{DetectedCapabilities: []string{"camera", "photos"}},
	}
	p := buildCopyPrompt(l)
	if !strings.Contains(p, "Receipt Scanner") {
		t.Error("prompt should include the app name")
	}
	if !strings.Contains(p, "camera") || !strings.Contains(p, "photos") {
		t.Error("prompt must ground on detected capabilities")
	}
	if !strings.Contains(p, "Never invent features") {
		t.Error("prompt must forbid inventing features")
	}
}

func TestParseCopyDraftToleratesFences(t *testing.T) {
	resp := "Sure! Here you go:\n```json\n{\"subtitle\":\"Snap & track\",\"description\":\"d\",\"keywords\":[\"receipt\",\"expense\"],\"whatsNew\":\"v1\"}\n```\nHope that helps!"
	d, err := parseCopyDraft(resp)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.Subtitle != "Snap & track" || d.Description != "d" || d.WhatsNew != "v1" {
		t.Errorf("fields wrong: %+v", d)
	}
	if len(d.Keywords) != 2 {
		t.Errorf("keywords = %v", d.Keywords)
	}
}

func TestParseCopyDraftNoJSON(t *testing.T) {
	if _, err := parseCopyDraft("no json here"); err == nil {
		t.Error("expected error when no JSON object present")
	}
}

func TestClampKeywords(t *testing.T) {
	// Dedup (case-insensitive) + drop overflow past the char budget.
	in := []string{"receipt", "Receipt", "expense", "budget", "finance", "money", "tracker", "scanner", "ocr", "tax", "spending", "verylongkeywordthatpushesoverthelimit"}
	out := clampKeywords(in, 40)
	joined := strings.Join(out, ",")
	if len(joined) > 40 {
		t.Errorf("joined keywords %q exceed 40 chars (%d)", joined, len(joined))
	}
	// "Receipt" duplicate of "receipt" must be dropped.
	seen := 0
	for _, k := range out {
		if strings.EqualFold(k, "receipt") {
			seen++
		}
	}
	if seen > 1 {
		t.Error("case-insensitive duplicate keyword not deduped")
	}
}

func TestApplyCopyDraftPreservesWhenEmpty(t *testing.T) {
	l := StoreListing{Description: "original"}
	applyCopyDraft(&l, CopyDraft{Description: ""}) // empty must not clobber
	if l.Description != "original" {
		t.Error("empty draft field should not overwrite existing")
	}
	applyCopyDraft(&l, CopyDraft{Description: "new"})
	if l.Description != "new" {
		t.Error("non-empty draft field should apply")
	}
}
