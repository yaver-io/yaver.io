package main

import (
	"strings"
	"testing"
)

func TestTemplateManagerSurveyApp(t *testing.T) {
	tm := NewTemplateManager(t.TempDir())

	templates, err := tm.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	found := false
	for _, template := range templates {
		if template.Name == "survey-app" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("survey-app template missing from list")
	}

	files, err := tm.generateTemplate("survey-app", "survey-app")
	if err != nil {
		t.Fatalf("generateTemplate() error = %v", err)
	}

	var haveHTTP, haveEnv, haveReadme bool
	for _, file := range files {
		switch file.Path {
		case "backend/convex/http.ts":
			haveHTTP = strings.Contains(file.Content, "/survey/submit-public")
		case ".env.local.example":
			haveEnv = strings.Contains(file.Content, "NEXT_PUBLIC_CONVEX_URL=http://127.0.0.1:3211")
		case "README.md":
			haveReadme = strings.Contains(file.Content, "yaver code --mesh --allowed-runners ollama,opencode,codex")
		}
	}

	if !haveHTTP {
		t.Fatal("survey-app template missing backend/convex/http.ts route wiring")
	}
	if !haveEnv {
		t.Fatal("survey-app template missing local Convex URL example")
	}
	if !haveReadme {
		t.Fatal("survey-app template missing mesh runner guidance")
	}
}
