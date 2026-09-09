package main

// workspace_layout.go defines the placement contract for repositories Yaver
// manages itself. It does not restrict user-selected paths: an existing repo
// under ~/src, on an attached disk, or anywhere else remains a valid project.

import (
	"fmt"
	"os"
	"path/filepath"
)

type WorkspaceLayoutStatus struct {
	OK                      bool   `json:"ok"`
	Mode                    string `json:"mode"`
	Root                    string `json:"root"`
	Repositories            string `json:"repositories"`
	Worktrees               string `json:"worktrees"`
	ManagedRepositoryCount  int    `json:"managedRepositoryCount"`
	ManagedWorktreeCount    int    `json:"managedWorktreeCount"`
	OutsideManagedCount     int    `json:"outsideManagedCount"`
	ExplicitPathsSupported  bool   `json:"explicitPathsSupported"`
	CleanupRequiresExplicit bool   `json:"cleanupRequiresExplicit"`
}

func CollectWorkspaceLayoutStatus() (WorkspaceLayoutStatus, error) {
	root, err := DefaultWorkspaceDir()
	if err != nil {
		return WorkspaceLayoutStatus{}, err
	}
	repos, err := DefaultWorkspaceReposDir()
	if err != nil {
		return WorkspaceLayoutStatus{}, err
	}
	worktrees, err := DefaultWorkspaceWorktreesDir()
	if err != nil {
		return WorkspaceLayoutStatus{}, err
	}
	status := WorkspaceLayoutStatus{
		OK:                      true,
		Mode:                    "managed-default",
		Root:                    root,
		Repositories:            repos,
		Worktrees:               worktrees,
		ExplicitPathsSupported:  true,
		CleanupRequiresExplicit: true,
	}
	status.ManagedRepositoryCount = countWorkspaceGitTrees(repos, 1)
	status.ManagedWorktreeCount = countWorkspaceGitTrees(worktrees, 3)

	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		return WorkspaceLayoutStatus{}, fmt.Errorf("read workspace root: %w", readErr)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == DefaultWorkspaceReposDirName || entry.Name() == DefaultWorkspaceWorktreesDirName {
			continue
		}
		if hasGitMarker(filepath.Join(root, entry.Name())) {
			// These are supported custom/legacy checkouts, not automatic deletion
			// candidates. Cleanup needs branch, dirtiness and process evidence.
			status.OutsideManagedCount++
		}
	}
	return status, nil
}

func countWorkspaceGitTrees(root string, maxDepth int) int {
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return filepath.SkipDir
		}
		depth := 1
		for _, c := range rel {
			if c == filepath.Separator {
				depth++
			}
		}
		if depth > maxDepth {
			return filepath.SkipDir
		}
		if entry.IsDir() && hasGitMarker(path) {
			count++
			return filepath.SkipDir
		}
		return nil
	})
	return count
}

func hasGitMarker(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}
