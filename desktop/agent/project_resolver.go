package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ResolvedProjectRef struct {
	Name           string `json:"name,omitempty"`
	Path           string `json:"path"`
	Stack          string `json:"stack,omitempty"`
	WorkspaceRoot  string `json:"workspaceRoot,omitempty"`
	RequestedName  string `json:"requestedName,omitempty"`
	RequestedPath  string `json:"requestedPath,omitempty"`
	ResolutionHint string `json:"resolutionHint,omitempty"`
}

func resolveProjectRef(projectName, projectPath string) (*ResolvedProjectRef, error) {
	projectName = strings.TrimSpace(projectName)
	projectPath = strings.TrimSpace(projectPath)

	ref := &ResolvedProjectRef{
		RequestedName: projectName,
		RequestedPath: projectPath,
	}

	if projectName != "" {
		if stack, path, root := resolveAppFromWorkspaceFull(projectName); strings.TrimSpace(path) != "" {
			ref.Name = projectName
			ref.Path = path
			ref.Stack = strings.TrimSpace(stack)
			ref.WorkspaceRoot = strings.TrimSpace(root)
			ref.ResolutionHint = "workspace-manifest"
			return finalizeResolvedProjectRef(ref)
		}
	}

	if projectPath != "" {
		ref.Path = projectPath
		ref.ResolutionHint = "explicit-path"
		return finalizeResolvedProjectRef(ref)
	}

	if projectName != "" {
		if mp := findMobileProjectByName(projectName); mp != nil && strings.TrimSpace(mp.Path) != "" {
			ref.Name = firstNonEmpty(strings.TrimSpace(mp.Name), projectName)
			ref.Path = strings.TrimSpace(mp.Path)
			ref.Stack = frameworkToProjectStack(strings.TrimSpace(mp.Framework))
			ref.ResolutionHint = "mobile-project-scan"
			return finalizeResolvedProjectRef(ref)
		}
		if path, err := findProject(projectName); err == nil && strings.TrimSpace(path) != "" {
			ref.Name = projectName
			ref.Path = path
			ref.ResolutionHint = "discovery-cache"
			return finalizeResolvedProjectRef(ref)
		}
	}

	return nil, fmt.Errorf("could not resolve project reference")
}

func finalizeResolvedProjectRef(ref *ResolvedProjectRef) (*ResolvedProjectRef, error) {
	if ref == nil || strings.TrimSpace(ref.Path) == "" {
		return nil, fmt.Errorf("project path is required")
	}
	if !filepath.IsAbs(ref.Path) {
		if abs, err := filepath.Abs(ref.Path); err == nil {
			ref.Path = abs
		}
	}
	info, err := os.Stat(ref.Path)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("project path %q does not exist", ref.Path)
	}
	projectInfo := DetectProjectInfo(ref.Path)
	if strings.TrimSpace(ref.Name) == "" {
		ref.Name = strings.TrimSpace(projectInfo.Name)
	}
	if strings.TrimSpace(ref.Stack) == "" {
		ref.Stack = detectProjectStack(ref.Path, projectInfo)
	}
	if strings.TrimSpace(ref.WorkspaceRoot) == "" {
		if root, _ := loadNearestWorkspaceManifest(ref.Path); root != "" {
			ref.WorkspaceRoot = root
		}
	}
	return ref, nil
}

func detectProjectStack(path string, info ProjectInfo) string {
	if stack := frameworkToProjectStack(strings.TrimSpace(info.Framework)); stack != "" {
		return stack
	}
	switch {
	case fileExists(filepath.Join(path, "go.mod")):
		return "go"
	case fileExists(filepath.Join(path, "Cargo.toml")):
		return "rust"
	case fileExists(filepath.Join(path, "pubspec.yaml")):
		return "flutter"
	case fileExists(filepath.Join(path, "package.json")):
		return "node"
	default:
		return ""
	}
}

func frameworkToProjectStack(framework string) string {
	switch strings.ToLower(strings.TrimSpace(framework)) {
	case "expo":
		return "react-native-expo"
	case "react-native":
		return "react-native"
	case "flutter":
		return "flutter"
	case "next":
		return "nextjs"
	case "vite":
		return "vite"
	case "go":
		return "go"
	case "python":
		return "python"
	case "rust":
		return "rust"
	default:
		return ""
	}
}
