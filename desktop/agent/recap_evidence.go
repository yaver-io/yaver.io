package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	recapCompleteUnknown    = "unknown"
	recapCompleteIncomplete = "incomplete"
	recapCompleteComplete   = "complete"
)

type recapCompletionEvidence struct {
	Landed              bool
	Complete            string
	PriorityCount       int
	EvidencedPriorities int
}

var recapPriorityRE = regexp.MustCompile(`(?m)^##\s+(P\d+)\b`)

// recapLanded reports whether a run produced landed tree state rather than a
// bare completion claim from the loop itself.
func recapLanded(summary autorunRunSummary) bool {
	return summary.Commits > 0 && summary.FinalCommit != ""
}

func deriveRecapCompletion(taskPath, progressPath string, landed bool) recapCompletionEvidence {
	ev := recapCompletionEvidence{
		Landed:   landed,
		Complete: recapCompleteUnknown,
	}

	taskBytes, err := os.ReadFile(taskPath)
	if err != nil {
		return ev
	}
	priorities := recapTaskPriorities(string(taskBytes))
	ev.PriorityCount = len(priorities)
	if len(priorities) == 0 {
		return ev
	}

	progressBytes, err := os.ReadFile(progressPath)
	if err != nil || len(progressBytes) == 0 {
		return ev
	}

	ev.EvidencedPriorities = recapEvidencedPriorityCount(string(progressBytes), priorities)
	if ev.EvidencedPriorities >= ev.PriorityCount && landed {
		ev.Complete = recapCompleteComplete
		return ev
	}
	ev.Complete = recapCompleteIncomplete
	return ev
}

func recapTaskPriorities(task string) []string {
	matches := recapPriorityRE.FindAllStringSubmatch(task, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		id := strings.ToUpper(strings.TrimSpace(m[1]))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func recapEvidencedPriorityCount(progress string, priorities []string) int {
	if len(priorities) == 0 {
		return 0
	}
	want := map[string]bool{}
	for _, p := range priorities {
		want[strings.ToUpper(strings.TrimSpace(p))] = true
	}
	found := map[string]bool{}
	for _, block := range strings.Split(progress, "\n## ") {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" {
			continue
		}
		if !recapProgressBlockCarriesEvidence(trimmed) {
			continue
		}
		for _, m := range recapPriorityRE.FindAllStringSubmatch(trimmed, -1) {
			id := strings.ToUpper(strings.TrimSpace(m[1]))
			if want[id] {
				found[id] = true
			}
		}
		// DOER REPORT bodies usually mention priorities inline rather than as headings.
		for _, token := range strings.FieldsFunc(trimmed, func(r rune) bool {
			return !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		}) {
			id := strings.ToUpper(strings.TrimSpace(token))
			if len(id) >= 2 && id[0] == 'P' && want[id] {
				found[id] = true
			}
		}
	}
	return len(found)
}

func recapProgressBlockCarriesEvidence(block string) bool {
	low := strings.ToLower(block)
	if strings.Contains(low, "master instruction") {
		return false
	}
	return strings.Contains(low, "doer report") ||
		strings.Contains(low, "gate passed") ||
		strings.Contains(low, "verified in the git log") ||
		strings.Contains(low, "implemented") ||
		strings.Contains(low, "completed")
}
