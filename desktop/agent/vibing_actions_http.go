package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type vibingProjectActionRequest struct {
	ProjectName string `json:"projectName"`
	ProjectPath string `json:"projectPath"`
	BundleID    string `json:"bundleId"`
	Message     string `json:"message"`
	Target      string `json:"target"`
}

func (s *HTTPServer) resolveVibingProjectAction(w http.ResponseWriter, r *http.Request, req vibingProjectActionRequest) (string, string, bool) {
	projectPath, projectName := s.resolveVibingProjectForRequest(req.ProjectPath, req.ProjectName, req.BundleID)
	if projectPath == "" {
		jsonError(w, http.StatusBadRequest, "This app is not linked to a detected project on the selected machine.")
		return "", "", false
	}
	if guestUID := strings.TrimSpace(r.Header.Get("X-Yaver-GuestUserID")); guestUID != "" {
		if s.guestConfigMgr != nil && !s.guestConfigMgr.GuestCanAccessProject(guestUID, projectName) {
			jsonError(w, http.StatusForbidden, "Your guest access is not allowed for this project.")
			return "", "", false
		}
	}
	return projectPath, projectName, true
}

func (s *HTTPServer) handleVibingCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req vibingProjectActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	projectPath, projectName, ok := s.resolveVibingProjectAction(w, r, req)
	if !ok {
		return
	}

	if _, err := runGit(projectPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		jsonError(w, http.StatusBadRequest, "resolved project is not a git repository")
		return
	}

	statusOut, err := runGit(projectPath, "status", "--porcelain")
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "git status failed: "+err.Error())
		return
	}
	if strings.TrimSpace(statusOut) == "" {
		jsonError(w, http.StatusBadRequest, "no local changes to commit")
		return
	}

	message := strings.TrimSpace(req.Message)
	if message == "" {
		label := strings.TrimSpace(projectName)
		if label == "" {
			label = "project"
		}
		message = fmt.Sprintf("chore: sync %s from Yaver vibing", label)
	}

	if out, err := runGit(projectPath, "add", "-A"); err != nil {
		jsonError(w, http.StatusInternalServerError, "git add failed: "+strings.TrimSpace(out))
		return
	}
	if out, err := runGit(projectPath, "commit", "-m", message); err != nil {
		jsonError(w, http.StatusBadRequest, "git commit failed: "+strings.TrimSpace(out))
		return
	}
	if out, err := runGit(projectPath, "pull", "--rebase", "--autostash"); err != nil {
		jsonError(w, http.StatusBadRequest, "git pull --rebase failed: "+strings.TrimSpace(out))
		return
	}

	branch, _ := runGit(projectPath, "rev-parse", "--abbrev-ref", "HEAD")
	pushOut, pushErr := runGit(projectPath, "push")
	if pushErr != nil && strings.TrimSpace(branch) != "" {
		pushOut, pushErr = runGit(projectPath, "push", "--set-upstream", "origin", strings.TrimSpace(branch))
	}
	if pushErr != nil {
		jsonError(w, http.StatusBadRequest, "git push failed: "+strings.TrimSpace(pushOut))
		return
	}

	commit, _ := runGit(projectPath, "rev-parse", "--short", "HEAD")
	jsonReply(w, http.StatusOK, map[string]any{
		"ok":          true,
		"projectName": projectName,
		"projectPath": projectPath,
		"branch":      strings.TrimSpace(branch),
		"commit":      strings.TrimSpace(commit),
		"message":     message,
	})
}

func (s *HTTPServer) handleVibingDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var req vibingProjectActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	projectPath, projectName, ok := s.resolveVibingProjectAction(w, r, req)
	if !ok {
		return
	}

	targets := detectDeployTargets(projectPath)
	if len(targets) == 0 {
		jsonError(w, http.StatusBadRequest, "no deploy targets detected for this project")
		return
	}
	var target *DeployTarget
	targetID := strings.TrimSpace(req.Target)
	if targetID != "" {
		for i := range targets {
			if strings.EqualFold(targets[i].ID, targetID) {
				target = &targets[i]
				break
			}
		}
	} else {
		preferred := []string{"cloudflare", "vercel", "netlify", "convex"}
		for _, id := range preferred {
			for i := range targets {
				if targets[i].ID == id {
					target = &targets[i]
					break
				}
			}
			if target != nil {
				break
			}
		}
		if target == nil {
			target = &targets[0]
		}
	}
	if target == nil {
		jsonError(w, http.StatusBadRequest, "unknown deploy target")
		return
	}

	title := fmt.Sprintf("Deploy %s to %s", firstNonEmpty(projectName, "project"), target.Name)
	task, err := s.taskMgr.CreateTask(title, "", "", "mobile", projectPath, target.Command, nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("deploy failed: %v", err))
		return
	}
	jsonReply(w, http.StatusAccepted, map[string]any{
		"ok":          true,
		"taskId":      task.ID,
		"target":      target.ID,
		"projectName": projectName,
		"projectPath": projectPath,
		"message":     fmt.Sprintf("Deploy started for %s via %s.", firstNonEmpty(projectName, "project"), target.Name),
	})
}
