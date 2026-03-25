package main

import (
	"os"
	"path/filepath"
	"strings"
)

// ProjectAction represents a deployable/runnable target within a project.
type ProjectAction struct {
	Label     string `json:"label"`               // "Hot Reload", "Deploy Web", "Build CLI"
	Target    string `json:"target"`               // subdirectory: "mobile/", "web/", "."
	Type      string `json:"type"`                 // "dev-server", "deploy", "build", "test"
	Framework string `json:"framework,omitempty"`  // "expo", "nextjs", "vite", "go", "flutter"
	Platform  string `json:"platform,omitempty"`   // "vercel", "convex", "supabase", "docker", "testflight", "playstore"
	Command   string `json:"command,omitempty"`    // direct command if known
	Icon      string `json:"icon,omitempty"`       // emoji for mobile UI
}

// DetectProjectActions scans a project directory and returns all available actions.
// Checks root dir + immediate subdirectories for deployable targets.
func DetectProjectActions(projectPath string) []ProjectAction {
	var actions []ProjectAction

	// Scan root + subdirs
	dirs := []string{projectPath}
	entries, err := os.ReadDir(projectPath)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "_") {
				if e.Name() == "node_modules" || e.Name() == "vendor" || e.Name() == "dist" || e.Name() == "build" {
					continue
				}
				dirs = append(dirs, filepath.Join(projectPath, e.Name()))
			}
		}
	}

	for _, dir := range dirs {
		rel := "."
		if dir != projectPath {
			rel = filepath.Base(dir) + "/"
		}

		actions = append(actions, detectActionsInDir(dir, rel)...)
	}

	return actions
}

func detectActionsInDir(dir, rel string) []ProjectAction {
	var actions []ProjectAction
	name := filepath.Base(dir)

	// Mobile (Expo / React Native)
	if hasFile(dir, "app.json") || hasFile(dir, "app.config.js") || hasFile(dir, "app.config.ts") {
		if pkgHas(dir, "expo") {
			actions = append(actions, ProjectAction{
				Label: "Hot Reload", Target: rel, Type: "dev-server",
				Framework: "expo", Icon: "\U0001F4F1",
				Command: "/dev/start",
			})
			actions = append(actions, ProjectAction{
				Label: "Build iOS", Target: rel, Type: "build",
				Framework: "expo", Platform: "testflight", Icon: "\U0001F34E",
			})
			actions = append(actions, ProjectAction{
				Label: "Build Android", Target: rel, Type: "build",
				Framework: "expo", Platform: "playstore", Icon: "\U0001F916",
			})
		}
	}

	// Flutter
	if hasFile(dir, "pubspec.yaml") {
		actions = append(actions, ProjectAction{
			Label: "Hot Reload", Target: rel, Type: "dev-server",
			Framework: "flutter", Icon: "\U0001F4F1",
			Command: "/dev/start",
		})
		actions = append(actions, ProjectAction{
			Label: "Build iOS", Target: rel, Type: "build",
			Framework: "flutter", Platform: "testflight", Icon: "\U0001F34E",
		})
		actions = append(actions, ProjectAction{
			Label: "Build Android", Target: rel, Type: "build",
			Framework: "flutter", Platform: "playstore", Icon: "\U0001F916",
		})
	}

	// Next.js
	if hasFile(dir, "next.config.ts") || hasFile(dir, "next.config.js") || hasFile(dir, "next.config.mjs") {
		actions = append(actions, ProjectAction{
			Label: "Dev Server", Target: rel, Type: "dev-server",
			Framework: "nextjs", Icon: "\u25B2",
			Command: "/dev/start",
		})
		actions = append(actions, ProjectAction{
			Label: "Deploy", Target: rel, Type: "deploy",
			Framework: "nextjs", Platform: "vercel", Icon: "\U0001F680",
		})
	}

	// Vite
	if hasFile(dir, "vite.config.ts") || hasFile(dir, "vite.config.js") {
		actions = append(actions, ProjectAction{
			Label: "Dev Server", Target: rel, Type: "dev-server",
			Framework: "vite", Icon: "\u26A1",
			Command: "/dev/start",
		})
		actions = append(actions, ProjectAction{
			Label: "Deploy", Target: rel, Type: "deploy",
			Framework: "vite", Platform: "vercel", Icon: "\U0001F680",
		})
	}

	// Convex
	if hasFile(dir, "convex") {
		actions = append(actions, ProjectAction{
			Label: "Deploy Backend", Target: rel, Type: "deploy",
			Platform: "convex", Icon: "\U0001F9E0",
			Command: "npx convex deploy --yes",
		})
	}

	// Supabase
	if hasFile(dir, "supabase") || hasFile(dir, "supabase.json") {
		actions = append(actions, ProjectAction{
			Label: "Deploy DB", Target: rel, Type: "deploy",
			Platform: "supabase", Icon: "\U0001F5C4",
			Command: "supabase db push",
		})
	}

	// Firebase
	if hasFile(dir, "firebase.json") {
		actions = append(actions, ProjectAction{
			Label: "Deploy", Target: rel, Type: "deploy",
			Platform: "firebase", Icon: "\U0001F525",
			Command: "firebase deploy",
		})
	}

	// Docker
	if hasFile(dir, "Dockerfile") || hasFile(dir, "docker-compose.yml") || hasFile(dir, "docker-compose.yaml") {
		actions = append(actions, ProjectAction{
			Label: "Run Container", Target: rel, Type: "deploy",
			Platform: "docker", Icon: "\U0001F433",
		})
	}

	// Go binary
	if hasFile(dir, "go.mod") && (name != "." && !pkgHas(dir, "expo")) {
		actions = append(actions, ProjectAction{
			Label: "Build", Target: rel, Type: "build",
			Framework: "go", Icon: "\U0001F528",
			Command: "go build ./...",
		})
	}

	// Rust
	if hasFile(dir, "Cargo.toml") {
		actions = append(actions, ProjectAction{
			Label: "Build", Target: rel, Type: "build",
			Framework: "rust", Icon: "\U0001F980",
			Command: "cargo build --release",
		})
	}

	// Python
	if hasFile(dir, "pyproject.toml") || hasFile(dir, "setup.py") {
		actions = append(actions, ProjectAction{
			Label: "Run", Target: rel, Type: "build",
			Framework: "python", Icon: "\U0001F40D",
		})
	}

	return actions
}

func hasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func pkgHas(dir, dep string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"`+dep+`"`)
}
