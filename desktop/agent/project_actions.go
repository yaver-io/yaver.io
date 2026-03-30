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
	Supported bool   `json:"supported"`            // false = shown but disabled ("not supported yet")
	Reason    string `json:"reason,omitempty"`     // why it's not supported
}

// DetectProjectActions scans a project directory and returns all available actions.
// Checks root dir + immediate subdirectories for deployable targets.
// Deduplicates actions with the same label+platform (monorepo subdirs can overlap).
func DetectProjectActions(projectPath string) []ProjectAction {
	var actions []ProjectAction

	// Scan root + subdirs
	dirs := []string{projectPath}
	entries, err := os.ReadDir(projectPath)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasPrefix(e.Name(), "_") {
				skip := map[string]bool{
					"node_modules": true, "vendor": true, "dist": true, "build": true,
					"ios": true, "android": true, ".expo": true, "pods": true,
				}
				if skip[e.Name()] {
					continue
				}
				dirs = append(dirs, filepath.Join(projectPath, e.Name()))
			}
		}
	}

	seen := map[string]bool{}
	for _, dir := range dirs {
		rel := "."
		if dir != projectPath {
			rel = filepath.Base(dir) + "/"
		}

		for _, a := range detectActionsInDir(dir, rel) {
			// Deduplicate by label+platform — keep the first (root wins over subdir)
			key := a.Label + "|" + a.Platform + "|" + a.Framework
			if seen[key] {
				continue
			}
			seen[key] = true
			actions = append(actions, a)
		}
	}

	return actions
}

func detectActionsInDir(dir, rel string) []ProjectAction {
	var actions []ProjectAction
	_ = filepath.Base(dir)

	// Mobile (Expo / React Native)
	if hasFile(dir, "app.json") || hasFile(dir, "app.config.js") || hasFile(dir, "app.config.ts") {
		if pkgHas(dir, "expo") {
			actions = append(actions, ProjectAction{
				Label: "Hot Reload", Target: rel, Type: "dev-server",
				Framework: "expo", Icon: "\U0001F4F1",
				Command: "/dev/start", Supported: true,
			})
			actions = append(actions, ProjectAction{
				Label: "Build iOS", Target: rel, Type: "build",
				Framework: "expo", Platform: "testflight", Icon: "\U0001F34E", Supported: true,
			})
			actions = append(actions, ProjectAction{
				Label: "Build Android", Target: rel, Type: "build",
				Framework: "expo", Platform: "playstore", Icon: "\U0001F916", Supported: true,
			})
		}
	}

	// Bare React Native (without Expo)
	if pkgHas(dir, "react-native") && !pkgHas(dir, "expo") {
		actions = append(actions, ProjectAction{
			Label: "Hot Reload", Target: rel, Type: "dev-server",
			Framework: "react-native", Icon: "\U0001F4F1",
			Command: "/dev/start", Supported: true,
		})
		actions = append(actions, ProjectAction{
			Label: "Build iOS", Target: rel, Type: "build",
			Framework: "react-native", Platform: "testflight", Icon: "\U0001F34E", Supported: true,
		})
		actions = append(actions, ProjectAction{
			Label: "Build Android", Target: rel, Type: "build",
			Framework: "react-native", Platform: "playstore", Icon: "\U0001F916", Supported: true,
		})
	}

	// Flutter — hot reload shown but disabled, builds supported
	if hasFile(dir, "pubspec.yaml") {
		actions = append(actions, ProjectAction{
			Label: "Hot Reload", Target: rel, Type: "dev-server",
			Framework: "flutter", Icon: "\U0001F4F1",
			Supported: false, Reason: "Flutter hot reload coming soon",
		})
		actions = append(actions, ProjectAction{
			Label: "Build iOS", Target: rel, Type: "build",
			Framework: "flutter", Platform: "testflight", Icon: "\U0001F34E", Supported: true,
		})
		actions = append(actions, ProjectAction{
			Label: "Build Android", Target: rel, Type: "build",
			Framework: "flutter", Platform: "playstore", Icon: "\U0001F916", Supported: true,
		})
	}

	// Swift (native iOS) — shown but hot reload not supported
	if hasFile(dir, "Package.swift") || (hasFile(dir, "*.xcodeproj") || hasDir(dir, ".xcodeproj")) {
		actions = append(actions, ProjectAction{
			Label: "Hot Reload", Target: rel, Type: "dev-server",
			Framework: "swift", Icon: "\U0001F34E",
			Supported: false, Reason: "Native Swift hot reload coming soon",
		})
		actions = append(actions, ProjectAction{
			Label: "Build iOS", Target: rel, Type: "build",
			Framework: "swift", Platform: "testflight", Icon: "\U0001F34E", Supported: true,
		})
	}

	// Kotlin (native Android) — shown but hot reload not supported
	if hasFile(dir, "build.gradle.kts") && !pkgHas(dir, "react-native") && !hasFile(dir, "pubspec.yaml") {
		actions = append(actions, ProjectAction{
			Label: "Hot Reload", Target: rel, Type: "dev-server",
			Framework: "kotlin", Icon: "\U0001F916",
			Supported: false, Reason: "Native Kotlin hot reload coming soon",
		})
		actions = append(actions, ProjectAction{
			Label: "Build Android", Target: rel, Type: "build",
			Framework: "kotlin", Platform: "playstore", Icon: "\U0001F916", Supported: true,
		})
	}

	// Next.js
	if hasFile(dir, "next.config.ts") || hasFile(dir, "next.config.js") || hasFile(dir, "next.config.mjs") {
		actions = append(actions, ProjectAction{
			Label: "Dev Server", Target: rel, Type: "dev-server",
			Framework: "nextjs", Icon: "\u25B2",
			Command: "/dev/start", Supported: true,
		})
		actions = append(actions, ProjectAction{
			Label: "Deploy", Target: rel, Type: "deploy",
			Framework: "nextjs", Platform: "vercel", Icon: "\U0001F680", Supported: true,
		})
	}

	// Vite
	if hasFile(dir, "vite.config.ts") || hasFile(dir, "vite.config.js") {
		actions = append(actions, ProjectAction{
			Label: "Dev Server", Target: rel, Type: "dev-server",
			Framework: "vite", Icon: "\u26A1",
			Command: "/dev/start", Supported: true,
		})
		actions = append(actions, ProjectAction{
			Label: "Deploy", Target: rel, Type: "deploy",
			Framework: "vite", Platform: "vercel", Icon: "\U0001F680", Supported: true,
		})
	}

	// Vercel (static site or any project with vercel.json)
	if hasFile(dir, "vercel.json") {
		// Don't add if we already detected Next.js or Vite deploy above
		hasDeploy := false
		for _, a := range actions {
			if a.Type == "deploy" && a.Platform == "vercel" {
				hasDeploy = true
				break
			}
		}
		if !hasDeploy {
			actions = append(actions, ProjectAction{
				Label: "Deploy Frontend", Target: rel, Type: "deploy",
				Platform: "vercel", Icon: "\U0001F680", Supported: true,
				Command: "vercel --prod",
			})
		}
	}

	// Convex
	if hasFile(dir, "convex") {
		actions = append(actions, ProjectAction{
			Label: "Deploy Backend", Target: rel, Type: "deploy",
			Platform: "convex", Icon: "\U0001F9E0", Supported: true,
			Command: "npx convex deploy --yes",
		})
	}

	// Supabase
	if hasFile(dir, "supabase") || hasFile(dir, "supabase.json") {
		actions = append(actions, ProjectAction{
			Label: "Deploy DB", Target: rel, Type: "deploy",
			Platform: "supabase", Icon: "\U0001F5C4", Supported: true,
			Command: "supabase db push",
		})
	}

	// Firebase
	if hasFile(dir, "firebase.json") {
		actions = append(actions, ProjectAction{
			Label: "Deploy", Target: rel, Type: "deploy",
			Platform: "firebase", Icon: "\U0001F525", Supported: true,
			Command: "firebase deploy",
		})
	}

	// Docker
	if hasFile(dir, "Dockerfile") || hasFile(dir, "docker-compose.yml") || hasFile(dir, "docker-compose.yaml") {
		actions = append(actions, ProjectAction{
			Label: "Run Container", Target: rel, Type: "deploy",
			Platform: "docker", Icon: "\U0001F433", Supported: true,
		})
	}

	// Go binary
	if hasFile(dir, "go.mod") && !pkgHas(dir, "expo") {
		actions = append(actions, ProjectAction{
			Label: "Run", Target: rel, Type: "run",
			Framework: "go", Icon: "\U0001F4BB", Supported: true,
			Command: "go run .",
		})
		actions = append(actions, ProjectAction{
			Label: "Build", Target: rel, Type: "build",
			Framework: "go", Icon: "\U0001F528", Supported: true,
			Command: "go build ./...",
		})
	}

	// Rust
	if hasFile(dir, "Cargo.toml") {
		actions = append(actions, ProjectAction{
			Label: "Run", Target: rel, Type: "run",
			Framework: "rust", Icon: "\U0001F4BB", Supported: true,
			Command: "cargo run",
		})
		actions = append(actions, ProjectAction{
			Label: "Build", Target: rel, Type: "build",
			Framework: "rust", Icon: "\U0001F528", Supported: true,
			Command: "cargo build --release",
		})
	}

	// Python — find entry point
	if hasFile(dir, "pyproject.toml") || hasFile(dir, "setup.py") || hasFile(dir, "requirements.txt") {
		entryPoint := findPythonEntry(dir)
		if entryPoint != "" {
			actions = append(actions, ProjectAction{
				Label: "Run", Target: rel, Type: "run",
				Framework: "python", Icon: "\U0001F4BB", Supported: true,
				Command: "python3 " + entryPoint,
			})
		}
	}

	return actions
}

// findPythonEntry finds the main entry point for a Python project.
func findPythonEntry(dir string) string {
	// Common entry points in priority order
	candidates := []string{
		"main.py", "app.py", "run.py", "server.py", "manage.py",
		"cli.py", "src/main.py", "src/app.py",
	}
	for _, c := range candidates {
		if hasFile(dir, c) {
			return c
		}
	}
	// Check if there's a __main__.py (runnable package)
	if hasFile(dir, "__main__.py") {
		return "__main__.py"
	}
	// Look for any .py with if __name__ == "__main__"
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".py") {
				data, err := os.ReadFile(filepath.Join(dir, e.Name()))
				if err == nil && strings.Contains(string(data), `__name__`) && strings.Contains(string(data), `__main__`) {
					return e.Name()
				}
			}
		}
	}
	return ""
}

func hasFile(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// hasDir checks if dir contains a subdirectory matching the suffix (e.g. ".xcodeproj").
func hasDir(dir, suffix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			return true
		}
	}
	return false
}

func pkgHas(dir, dep string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"`+dep+`"`)
}
