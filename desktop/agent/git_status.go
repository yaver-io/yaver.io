package main

import (
	"fmt"
	"io"
	osexec "os/exec"
	"strings"
)

// gitUserConfig is what `git config --global` exposes about the human
// behind a box: identity for commits, surfaced in `yaver status` so the
// user can spot a missing configuration before their first commit fails.
type gitUserConfig struct {
	UserName  string `json:"userName,omitempty"`
	UserEmail string `json:"userEmail,omitempty"`
}

// gitStatusSummary is the bundled "ready to clone, pull, push, commit?"
// view: identity + GH/GitLab credential readiness. Shared between local
// `yaver status` and remote primary/secondary status renders.
type gitStatusSummary struct {
	Config    gitUserConfig                     `json:"config"`
	Providers []machineOnboardingProviderStatus `json:"providers,omitempty"`
}

func collectGitUserConfig() gitUserConfig {
	var c gitUserConfig
	if out, err := osexec.Command("git", "config", "--global", "--get", "user.name").Output(); err == nil {
		c.UserName = strings.TrimSpace(string(out))
	}
	if out, err := osexec.Command("git", "config", "--global", "--get", "user.email").Output(); err == nil {
		c.UserEmail = strings.TrimSpace(string(out))
	}
	return c
}

func collectGitStatusSummary() gitStatusSummary {
	full := collectMachineOnboardingStatus()
	providers := make([]machineOnboardingProviderStatus, 0, 2)
	for _, p := range full.Providers {
		if p.ID == "github" || p.ID == "gitlab" {
			providers = append(providers, p)
		}
	}
	return gitStatusSummary{
		Config:    collectGitUserConfig(),
		Providers: providers,
	}
}

// renderGitStatusBlock prints the Git section used by both local
// `yaver status` and remote primary/secondary status.
//
// indent: leading whitespace for child lines (local uses "", remote uses
// "  " to nest under the report header).
func renderGitStatusBlock(w io.Writer, summary gitStatusSummary, indent string) {
	fmt.Fprintln(w, indent+"Git:")
	child := indent + "  "

	if summary.Config.UserName != "" {
		fmt.Fprintf(w, "%suser.name:    %s\n", child, summary.Config.UserName)
	} else {
		fmt.Fprintf(w, "%suser.name:    \033[31m●\033[0m not set (run: git config --global user.name \"Your Name\")\n", child)
	}
	if summary.Config.UserEmail != "" {
		fmt.Fprintf(w, "%suser.email:   %s\n", child, summary.Config.UserEmail)
	} else {
		fmt.Fprintf(w, "%suser.email:   \033[31m●\033[0m not set (run: git config --global user.email you@example.com)\n", child)
	}

	for _, p := range summary.Providers {
		marker, label := gitProviderMarker(p)
		fmt.Fprintf(w, "%s%-12s  %s %s\n", child, p.ID+":", marker, label)
	}
}

func gitProviderMarker(p machineOnboardingProviderStatus) (marker, label string) {
	switch {
	case p.Ready:
		marker = "\033[32m●\033[0m"
	case p.Configured:
		marker = "\033[33m●\033[0m"
	default:
		marker = "\033[31m●\033[0m"
	}
	if !p.Configured {
		if strings.TrimSpace(p.Detail) != "" {
			label = p.Detail
		} else {
			label = "not configured"
		}
		return
	}
	parts := []string{}
	if p.Username != "" {
		host := p.Host
		if host == "" {
			host = p.Name
		}
		parts = append(parts, p.Username+"@"+host)
	} else if p.Host != "" {
		parts = append(parts, p.Host)
	}
	if p.CloneSource != "" {
		parts = append(parts, "clone="+p.CloneSource)
	}
	if p.CISource != "" {
		parts = append(parts, "ci="+p.CISource)
	}
	if len(parts) == 0 {
		label = "configured"
		return
	}
	label = strings.Join(parts, " · ")
	return
}
