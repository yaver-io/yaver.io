package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalyzeConversationImportFromPastedContent(t *testing.T) {
	oldGen := runConversationImportGenerator
	t.Cleanup(func() { runConversationImportGenerator = oldGen })
	runConversationImportGenerator = func(spec AIGeneratorSpec) (string, error) {
		if !strings.Contains(spec.Prompt, "Imported material:") {
			t.Fatalf("prompt missing imported material")
		}
		return `{
			"suggestedName":"Thread Import",
			"productGoal":"Let a user paste an AI conversation and start building from it.",
			"userProblem":"They do not know how to translate a messy thread into a technical plan.",
			"summary":"Analyze the thread, infer the implementation path, and expose the result in Yaver.",
			"researchTopics":["verify share page shapes","check MCP flow requirements"],
			"surfaces":["mobile app","web dashboard","mcp console"],
			"technicalPlan":["accept shared URL or pasted transcript","normalize content","generate structured technical brief"],
			"dataFlow":["thread input -> import analysis endpoint -> generated prompt -> create flow"],
			"mvpScope":["analyze thread","prefill project brief","start build flow"],
			"risks":["share pages may change"],
			"assumptions":["public share URLs are reachable"],
			"nextPrompt":"Build the import analysis backend and wire it into project creation."
		}`, nil
	}

	out, err := AnalyzeConversationImport(ConversationImportRequest{
		Content: "User wants to paste a Claude thread and turn it into an app plan.",
		WorkDir: ".",
	})
	if err != nil {
		t.Fatalf("AnalyzeConversationImport: %v", err)
	}
	if out.SuggestedName != "Thread Import" {
		t.Fatalf("suggestedName = %q", out.SuggestedName)
	}
	if !strings.Contains(out.GeneratedPrompt, "Technical plan:") {
		t.Fatalf("generated prompt missing technical plan: %s", out.GeneratedPrompt)
	}
	if out.SourceLabel != "Pasted conversation" {
		t.Fatalf("sourceLabel = %q", out.SourceLabel)
	}
}

func TestAnalyzeConversationImportFetchesURL(t *testing.T) {
	oldClient := conversationImportHTTPClient
	oldGen := runConversationImportGenerator
	t.Cleanup(func() {
		conversationImportHTTPClient = oldClient
		runConversationImportGenerator = oldGen
	})
	share := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Imported Share</title></head><body><h1>Build a mobile-first assistant</h1><p>Need mobile and console surfaces.</p></body></html>`))
	}))
	defer share.Close()
	conversationImportHTTPClient = share.Client()
	runConversationImportGenerator = func(spec AIGeneratorSpec) (string, error) {
		if !strings.Contains(spec.Prompt, "Imported Share") {
			t.Fatalf("prompt missing fetched title: %s", spec.Prompt)
		}
		return `{
			"suggestedName":"Imported Share",
			"productGoal":"Build from a fetched share URL.",
			"technicalPlan":["fetch URL","extract text","plan implementation"]
		}`, nil
	}

	out, err := AnalyzeConversationImport(ConversationImportRequest{
		URL:     share.URL,
		WorkDir: ".",
	})
	if err != nil {
		t.Fatalf("AnalyzeConversationImport: %v", err)
	}
	if out.FetchedURL == "" {
		t.Fatalf("expected fetchedURL")
	}
	if !strings.Contains(out.NormalizedText, "Build a mobile-first assistant") {
		t.Fatalf("normalizedText missing fetched body: %s", out.NormalizedText)
	}
}
