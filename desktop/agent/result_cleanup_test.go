package main

import (
	"strings"
	"testing"
)

func TestStripPromptEcho_PassThrough(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "Hello world", "Hello world"},
		{
			"answer-only",
			"Here is the ls output for /root:\nWorkspace\nbootstrap.sh",
			"Here is the ls output for /root:\nWorkspace\nbootstrap.sh",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripPromptEcho(tc.in); got != tc.want {
				t.Fatalf("stripPromptEcho mismatch:\n  in:   %q\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripPromptEcho_DevServerContextSliced(t *testing.T) {
	in := linesOf(
		"[Yaver Agent Context]",
		"Working directory: /root/talos",
		"",
		"IMPORTANT — Dev Server Proxy Rules:",
		"… (lots of rules) …",
		"10. If /dev/start fails or times out, check if another process is using the port:",
		"    lsof -i:8081",
		"    Kill any stale expo/metro processes before retrying.",
		"",
		"Here is the ls output for /root:",
		"Workspace",
		"bootstrap.sh",
	)
	got := stripPromptEcho(in)
	if !strings.HasPrefix(got, "Here is the ls output") {
		t.Fatalf("expected answer to start the cleaned content, got: %q", got)
	}
	if strings.Contains(got, "Working directory") || strings.Contains(got, "Dev Server Proxy") {
		t.Fatalf("system-context bled through: %q", got)
	}
}

func TestStripPromptEcho_WrapperCapabilitiesSliced(t *testing.T) {
	in := linesOf(
		"[Yaver wrapper capabilities]",
		"You are running inside Yaver, not a generic terminal.",
		"Working directory for these flows: /root",
		"",
		"Web / WebView preview rules:",
		"- For browser-style preview, use web_preview_start or POST /dev/web-preview/start.",
		"",
		"Remote visual feedback:",
		"- If the user wants visual confirmation of what is rendering, use vibe_preview_start, vibe_preview_status, vibe_preview_snapshot, or related Yaver preview tools instead of asking them to guess.",
		"",
		"Sure — here's what I found:",
		"foo",
		"bar",
	)
	got := stripPromptEcho(in)
	if !strings.HasPrefix(got, "Sure") {
		t.Fatalf("expected answer to start the cleaned content, got: %q", got)
	}
	if strings.Contains(got, "wrapper capabilities") || strings.Contains(got, "vibe_preview") {
		t.Fatalf("system-context bled through: %q", got)
	}
}

// Two stacked context blocks (DevServer THEN WrapperCapabilities) — make sure
// we slice after the LAST end-marker, not the first, so all noise gets
// stripped even when the agent injected multiple blocks.
func TestStripPromptEcho_BothContextBlocksSliced(t *testing.T) {
	in := linesOf(
		"[Yaver Agent Context]",
		"…",
		"    Kill any stale expo/metro processes before retrying.",
		"",
		"[Yaver wrapper capabilities]",
		"…",
		"- If the user wants visual confirmation of what is rendering, use vibe_preview_start, vibe_preview_status, vibe_preview_snapshot, or related Yaver preview tools instead of asking them to guess.",
		"",
		"Done.",
	)
	got := stripPromptEcho(in)
	if got != "Done." {
		t.Fatalf("expected only the answer to remain, got: %q", got)
	}
}

func TestStripPromptEcho_CodexBannerStripped(t *testing.T) {
	in := linesOf(
		"Reading additional input from stdin...",
		"OpenAI Codex v0.123.0 (research preview)",
		"workdir: /root",
		"model: gpt-5.4",
		"provider: openai",
		"approval: never",
		"sandbox: workspace-write [workdir, /tmp, $TMPDIR, /root/.codex/memories]",
		"reasoning effort: none",
		"reasoning summaries: none",
		"",
		"Here is the ls output for /root:",
		"Workspace",
	)
	got := stripPromptEcho(in)
	if !strings.HasPrefix(got, "Here is the ls output") {
		t.Fatalf("expected answer to lead, got: %q", got)
	}
	if strings.Contains(got, "OpenAI Codex") || strings.Contains(got, "workdir:") || strings.Contains(got, "Reading additional input") {
		t.Fatalf("codex preamble bled through: %q", got)
	}
}

func TestStripPromptEcho_TokensUsedFooterStripped(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"single-line",
			"Done.\ntokens used 1,234",
			"Done.",
		},
		{
			"two-line",
			"Done.\n\ntokens used\n9,158",
			"Done.",
		},
		{
			"case-insensitive-ANSI-bytes",
			"Done.\n[2mtokens used[0m\n9,158",
			"Done.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripPromptEcho(tc.in); got != tc.want {
				t.Fatalf("stripPromptEcho:\n  in:   %q\n  got:  %q\n  want: %q", tc.in, got, tc.want)
			}
		})
	}
}

// Full end-to-end fixture lifted from the screenshots. Asserts the user
// never sees our own prompt + Codex's preamble — only the answer + the
// `ls` listing. This is the "happy path" the mobile bubble renders.
func TestStripPromptEcho_FullCodexFixture(t *testing.T) {
	raw := linesOf(
		"Reading additional input from stdin...",
		"[35m[3mcodex[0m[0m",
		"OpenAI Codex v0.123.0 (research preview)",
		"[1mworkdir:[0m /root",
		"[1mmodel:[0m gpt-5.4",
		"[1mprovider:[0m openai",
		"[1mapproval:[0m never",
		"[1msandbox:[0m workspace-write [workdir, /tmp, $TMPDIR, /root/.codex/memories]",
		"[1mreasoning effort:[0m none",
		"[1mreasoning summaries:[0m none",
		"",
		"[Yaver wrapper capabilities]",
		"You are running inside Yaver, not a generic terminal.",
		"Working directory for these flows: /root",
		"",
		"Mobile / Hermes rules:",
		"- For React Native / Expo app serving, use Yaver's dev flow.",
		"- Never tell the user to open Expo Go.",
		"",
		"Web / WebView preview rules:",
		"- When the preview starts, surface the returned iframeUrl or webUrl explicitly to the user.",
		"",
		"Remote visual feedback:",
		"- If the user wants visual confirmation of what is rendering, use vibe_preview_start, vibe_preview_status, vibe_preview_snapshot, or related Yaver preview tools instead of asking them to guess.",
		"",
		"Here is the ls output for /root:",
		"```",
		"Workspace",
		"bootstrap.sh",
		"carrotbet",
		"go",
		"snap",
		"```",
		"",
		"[2mtokens used[0m",
		"9,158",
	)
	got := stripPromptEcho(raw)

	mustContain(t, got, "Here is the ls output for /root:")
	mustContain(t, got, "Workspace")
	mustContain(t, got, "carrotbet")
	mustNotContain(t, got, "OpenAI Codex")
	mustNotContain(t, got, "Reading additional input")
	mustNotContain(t, got, "wrapper capabilities")
	mustNotContain(t, got, "vibe_preview")
	mustNotContain(t, got, "tokens used")
	mustNotContain(t, got, "9,158")
	// And no leftover ANSI runs.
	if strings.Contains(got, "[") {
		t.Fatalf("ANSI escape leaked through: %q", got)
	}
}

// TestStripPromptEcho_CodexEchoes_RealCapture exercises the codex
// 0.123.0 "Run ls" capture from yaver-test-ephemeral. Codex emits the
// listing three times: once as the raw exec output, then twice as
// identical fenced markdown blocks (codex bug). The cleanup must keep
// exactly one fenced copy and reduce the exec stanza to a `$ <cmd>`
// header.
func TestStripPromptEcho_CodexEchoes_RealCapture(t *testing.T) {
	raw := linesOf(
		"[35m[3mcodex[0m[0m",
		"Running `ls` in `/root` now.",
		"[35m[3mexec[0m[0m",
		"[1m/bin/bash -lc ls[0m in /root",
		"[32m succeeded in 0ms:[0m",
		"Workspace",
		"bootstrap.sh",
		"carrotbet",
		"go",
		"snap",
		"yaver-scope2",
		"",
		"[35m[3mcodex[0m[0m",
		"Here is the `ls` output for `/root`:",
		"",
		"```text",
		"Workspace",
		"bootstrap.sh",
		"carrotbet",
		"go",
		"snap",
		"yaver-scope2",
		"```",
		"Here is the `ls` output for `/root`:",
		"",
		"```text",
		"Workspace",
		"bootstrap.sh",
		"carrotbet",
		"go",
		"snap",
		"yaver-scope2",
		"```",
		"[2mtokens used[0m",
		"9,154",
	)
	got := stripPromptEcho(raw)
	if c := strings.Count(got, "yaver-scope2"); c != 1 {
		t.Fatalf("expected `yaver-scope2` exactly once, got %d times.\nfull output:\n%s", c, got)
	}
	if c := strings.Count(got, "Here is the `ls` output"); c != 1 {
		t.Fatalf("expected the lead-in line exactly once, got %d times.\nfull output:\n%s", c, got)
	}
	mustContain(t, got, "**$ /bin/bash -lc ls**")
	mustNotContain(t, got, "tokens used")
	mustNotContain(t, got, "succeeded in")
}

// Production capture (yaver-test-ephemeral, codex-cli 0.123.0, "Run ls"
// completed task). The `tokens used\n9,147` footer is wedged BETWEEN
// the two duplicated answer blocks — earlier the footer-stripper
// only fired at end-of-string (`$`), so the footer survived in the
// middle, the two answer blocks weren't adjacent, and dedupeCodexEchoes
// rule (4) couldn't collapse them. Net effect on mobile: the listing
// rendered twice.
func TestStripPromptEcho_TokensUsedBetweenDuplicates(t *testing.T) {
	raw := linesOf(
		"[35m[3mcodex[0m[0m",
		"Running `ls` in `/root` now.",
		"[35m[3mexec[0m[0m",
		"[1m/bin/bash -lc ls[0m in /root",
		"[32m succeeded in 0ms:[0m",
		"Workspace",
		"bootstrap.sh",
		"yaver-scope2",
		"",
		"[35m[3mcodex[0m[0m",
		"Here is the `ls` output for `/root`:",
		"",
		"```text",
		"Workspace",
		"bootstrap.sh",
		"yaver-scope2",
		"```",
		"[2mtokens used[0m",
		"9,147",
		"Here is the `ls` output for `/root`:",
		"",
		"```text",
		"Workspace",
		"bootstrap.sh",
		"yaver-scope2",
		"```",
	)
	got := stripPromptEcho(raw)
	if c := strings.Count(got, "yaver-scope2"); c != 1 {
		t.Fatalf("expected `yaver-scope2` exactly once, got %d times.\nfull output:\n%s", c, got)
	}
	if c := strings.Count(got, "Here is the `ls` output"); c != 1 {
		t.Fatalf("expected the lead-in line exactly once, got %d times.\nfull output:\n%s", c, got)
	}
	mustNotContain(t, got, "tokens used")
}

// Production capture lifted from the mobile screenshots (yaver-test-
// ephemeral, opencode 0.x, "run ls" against /root). opencode_stream.go
// has already rewritten the live `$ ls` shell line to `**$ ls**` and
// scrubbed ANSI; what arrives here is the post-stream-filter content.
// Without dedupeOpencodeEchoes the bare rows AFTER the marker survive
// alongside the fenced answer, so the mobile bubble's collapsed view
// picks "bootstrap.sh" as the summary line and the expanded view shows
// the listing twice.
func TestStripPromptEcho_OpencodeEchoes(t *testing.T) {
	raw := linesOf(
		"",
		"**$ ls**",
		"bootstrap.sh",
		"carrotbet",
		"go",
		"snap",
		"Workspace",
		"yaver-1.99.22",
		"yaver-1.99.25",
		"yaver-devfix",
		"yaver-new",
		"yaver-old-1777021488",
		"yaver-old-1777021550",
		"yaver-old-restart-1777022465",
		"yaver-pre-devfix-1777027258",
		"yaver-pre-rundownfix-1777037122",
		"yaver-pre-runnerfix-1777035336",
		"yaver-pre-scope-1777038232",
		"yaver-rundownfix",
		"yaver-runnerfix",
		"yaver-scope",
		"yaver-scope2",
		"",
		"18 items in the current directory:",
		"",
		"```text",
		"bootstrap.sh",
		"carrotbet",
		"go",
		"snap",
		"Workspace",
		"yaver-1.99.22",
		"yaver-1.99.25",
		"yaver-devfix",
		"yaver-new",
		"yaver-old-1777021488",
		"yaver-old-1777021550",
		"yaver-old-restart-1777022465",
		"yaver-pre-devfix-1777027258",
		"yaver-pre-rundownfix-1777037122",
		"yaver-pre-runnerfix-1777035336",
		"yaver-pre-scope-1777038232",
		"yaver-rundownfix",
		"yaver-runnerfix",
		"yaver-scope",
		"yaver-scope2",
		"```",
	)
	got := stripPromptEcho(raw)
	// Each filename should appear exactly once now — inside the fence.
	if c := strings.Count(got, "yaver-scope2"); c != 1 {
		t.Fatalf("expected `yaver-scope2` exactly once, got %d.\nfull output:\n%s", c, got)
	}
	if c := strings.Count(got, "yaver-pre-devfix-1777027258"); c != 1 {
		t.Fatalf("expected `yaver-pre-devfix-1777027258` exactly once, got %d.\nfull output:\n%s", c, got)
	}
	// Marker, prose answer, and fence kept.
	mustContain(t, got, "**$ ls**")
	mustContain(t, got, "18 items in the current directory:")
	mustContain(t, got, "```text")
	// First non-`$ ` line of the cleaned output must be the prose answer,
	// NOT a stray filename — that's what the mobile collapsed view picks
	// as its summary.
	firstNonShellLine := ""
	for _, line := range strings.Split(got, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "**$ ") {
			continue
		}
		firstNonShellLine = t
		break
	}
	if firstNonShellLine != "18 items in the current directory:" {
		t.Fatalf("expected first prose line to be the answer, got %q.\nfull output:\n%s",
			firstNonShellLine, got)
	}
}

// Negative case: when the rows after `**$ <cmd>**` do NOT match a
// fenced block (different command, no fence), they must be preserved.
// Otherwise we'd silently drop the only copy of the tool output for
// commands like `cat foo` or `grep -r bar`.
func TestStripPromptEcho_OpencodeKeepsUniqueRows(t *testing.T) {
	raw := linesOf(
		"**$ cat /etc/hostname**",
		"yaver-test-ephemeral",
		"",
		"The hostname is `yaver-test-ephemeral`.",
	)
	got := stripPromptEcho(raw)
	mustContain(t, got, "yaver-test-ephemeral")
	mustContain(t, got, "**$ cat /etc/hostname**")
}

// Negative case: rows partially overlap a fence (3 of 5 match) but
// below the 70% threshold — keep them. Avoids deleting genuinely
// distinct stdout that happens to share a few lines with a later
// example block.
func TestStripPromptEcho_OpencodePartialOverlapKeeps(t *testing.T) {
	raw := linesOf(
		"**$ ls**",
		"a",
		"b",
		"c",
		"d",
		"e",
		"",
		"Some of those overlap with the example below:",
		"",
		"```text",
		"a",
		"b",
		"c",
		"```",
	)
	got := stripPromptEcho(raw)
	// Below threshold — original stdout rows must survive.
	if c := strings.Count(got, "\nd\n"); c != 1 {
		t.Fatalf("expected unique row `d` to survive once, got %d.\nfull output:\n%s", c, got)
	}
	if c := strings.Count(got, "\ne\n"); c != 1 {
		t.Fatalf("expected unique row `e` to survive once, got %d.\nfull output:\n%s", c, got)
	}
}

func linesOf(lines ...string) string {
	return strings.Join(lines, "\n")
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected output to contain %q\nfull output: %q", needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected output NOT to contain %q\nfull output: %q", needle, haystack)
	}
}
