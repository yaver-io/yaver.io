package main

// `yaver emu <subcmd>` — headless mobile-app emulator.
//
// Replays the flows the Yaver mobile Hot Reload tab drives over HTTP
// so a developer can dogfood clone + dev-server start + live log
// streaming + failure handling before committing / pushing / shipping
// a build. Uses the same MobileClient library the real mobile app
// would (in theory) share if it were also Go — for TS we keep
// quic.ts and mobile_client.go in 1:1 method correspondence.
//
// Subcommands:
//   yaver emu status                     — print agent /health + /info
//   yaver emu projects                   — print /projects/mobile
//   yaver emu clone <git-url> [dir]      — POST /repos/clone, then tail
//                                          /projects/mobile until the new
//                                          project appears (proves Task 1)
//   yaver emu start <workDir> [--framework expo|react-native|flutter|vite|nextjs]
//                                        — POST /dev/start, subscribe to
//                                          /dev/events, print every log
//                                          line the mobile card would
//                                          render. Exits on ready/error.
//   yaver emu stop                       — POST /dev/stop
//   yaver emu e2e <git-url>              — full cycle: clone, start,
//                                          wait ready, stop. Exit 0 on
//                                          success, 1 on any failure.
//
// All subcommands default to http://127.0.0.1:18080 and pick up the
// auth token from ~/.yaver/config.json — override with --agent and
// --token. That way the exact same binary works against the local
// agent, a remote agent via relay URL, or an SSH tunnel.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func runEmu(args []string) {
	if len(args) == 0 {
		emuUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "status":
		runEmuStatus(rest)
	case "projects":
		runEmuProjects(rest)
	case "clone":
		runEmuClone(rest)
	case "start":
		runEmuStart(rest)
	case "stop":
		runEmuStop(rest)
	case "e2e":
		runEmuE2E(rest)
	case "-h", "--help", "help":
		emuUsage()
	default:
		fmt.Fprintf(os.Stderr, "yaver emu: unknown subcommand %q\n\n", sub)
		emuUsage()
		os.Exit(2)
	}
}

func emuUsage() {
	fmt.Println("yaver emu — headless mobile-app emulator for dogfooding")
	fmt.Println()
	fmt.Println("Subcommands:")
	fmt.Println("  status                        agent /health + /info")
	fmt.Println("  projects                      GET /projects/mobile (Hot Reload list)")
	fmt.Println("  clone <git-url> [dir]         clone + verify it appears in the list")
	fmt.Println("  start <workDir> [--framework] start Metro / Expo / Flutter and stream logs")
	fmt.Println("  stop                          stop the active dev server")
	fmt.Println("  e2e <git-url>                 clone → start → ready → stop")
	fmt.Println()
	fmt.Println("Flags (all subcommands):")
	fmt.Println("  --agent URL    default http://127.0.0.1:18080")
	fmt.Println("  --token TOKEN  default read from ~/.yaver/config.json")
}

// buildEmuClient resolves --agent / --token defaults and returns a
// MobileClient ready to use.
func buildEmuClient(fs *flag.FlagSet, args []string) (*MobileClient, []string) {
	agent := fs.String("agent", "http://127.0.0.1:18080", "agent base URL")
	token := fs.String("token", "", "auth token (default: from config)")
	fs.Parse(args)
	if *token == "" {
		cfg, _ := LoadConfig()
		if cfg != nil {
			*token = cfg.AuthToken
		}
	}
	return NewMobileClient(*agent, *token, nil), fs.Args()
}

// ── Subcommands ─────────────────────────────────────────────────

func runEmuStatus(args []string) {
	fs := flag.NewFlagSet("emu status", flag.ExitOnError)
	c, _ := buildEmuClient(fs, args)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	health, err := c.Health(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health: %v\n", err)
		os.Exit(1)
	}
	info, _ := c.Info(ctx)
	fmt.Println("health:", shortJSON(health))
	if info != nil {
		fmt.Println("info:  ", shortJSON(info))
	}
}

func runEmuProjects(args []string) {
	fs := flag.NewFlagSet("emu projects", flag.ExitOnError)
	c, _ := buildEmuClient(fs, args)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := c.ListMobileProjects(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "projects: %v\n", err)
		os.Exit(1)
	}
	if res.Scanning {
		fmt.Println("(scanning in background)")
	}
	if len(res.Projects) == 0 {
		fmt.Println("no mobile projects")
		return
	}
	for _, p := range res.Projects {
		fmt.Printf("  %-6s  %s  %s\n", p.Framework, p.Name, p.Path)
	}
}

func runEmuClone(args []string) {
	fs := flag.NewFlagSet("emu clone", flag.ExitOnError)
	ghToken := fs.String("github-token", "", "GitHub PAT for private repos (defaults to $GITHUB_TOKEN / $GH_TOKEN)")
	ghUser := fs.String("github-user", "x-access-token", "GitHub username to pair with the PAT")
	c, rest := buildEmuClient(fs, args)
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver emu clone <git-url> [dir] [--github-token TOKEN]")
		os.Exit(2)
	}
	url := rest[0]
	dir := ""
	if len(rest) > 1 {
		dir = rest[1]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := maybeStoreGitToken(ctx, c, url, *ghToken, *ghUser); err != nil {
		fmt.Fprintf(os.Stderr, "credential setup: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[clone] POST /repos/clone %s\n", url)
	res, err := c.CloneRepo(ctx, url, dir, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "clone: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[clone] cloned to %s\n", res.Path)
	want := filepath.Base(strings.TrimSuffix(res.Path, "/"))
	// Poll /projects/mobile for ~15s; the agent's cache invalidation
	// (Task 1) should surface the new project within 2.5s.
	start := time.Now()
	for time.Since(start) < 15*time.Second {
		time.Sleep(1 * time.Second)
		list, err := c.ListMobileProjects(ctx)
		if err != nil {
			continue
		}
		for _, p := range list.Projects {
			if p.Path == res.Path || strings.EqualFold(p.Name, want) {
				fmt.Printf("[clone] ✔ project visible in /projects/mobile after %s: %s (%s)\n",
					time.Since(start).Round(100*time.Millisecond), p.Name, p.Framework)
				return
			}
		}
	}
	fmt.Fprintf(os.Stderr, "[clone] ✗ project did NOT appear in /projects/mobile within 15s\n")
	os.Exit(1)
}

func runEmuStart(args []string) {
	fs := flag.NewFlagSet("emu start", flag.ExitOnError)
	framework := fs.String("framework", "", "framework (expo, react-native, flutter, vite, nextjs)")
	timeout := fs.Duration("timeout", 5*time.Minute, "max time to wait for ready")
	c, rest := buildEmuClient(fs, args)
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver emu start <workDir> [--framework expo]")
		os.Exit(2)
	}
	workDir := rest[0]
	streamDevRunUntilReady(c, workDir, *framework, *timeout, true)
}

func runEmuStop(args []string) {
	fs := flag.NewFlagSet("emu stop", flag.ExitOnError)
	c, _ := buildEmuClient(fs, args)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.StopDevServer(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "stop: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("[stop] dev server stopped")
}

func runEmuE2E(args []string) {
	fs := flag.NewFlagSet("emu e2e", flag.ExitOnError)
	framework := fs.String("framework", "", "framework override")
	timeout := fs.Duration("timeout", 8*time.Minute, "max wait")
	ghToken := fs.String("github-token", "", "GitHub PAT for private repos (defaults to $GITHUB_TOKEN / $GH_TOKEN)")
	ghUser := fs.String("github-user", "x-access-token", "GitHub username to pair with the PAT")
	c, rest := buildEmuClient(fs, args)
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver emu e2e <git-url> [--github-token TOKEN]")
		os.Exit(2)
	}
	url := rest[0]
	fmt.Println("=== E2E: clone + start + ready + stop ===")
	// ── Clone ─────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := maybeStoreGitToken(ctx, c, url, *ghToken, *ghUser); err != nil {
		fmt.Fprintf(os.Stderr, "credential setup: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[1/4] cloning %s\n", url)
	cloneRes, err := c.CloneRepo(ctx, url, "", "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e clone failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("      cloned to %s\n", cloneRes.Path)
	// ── Wait for project to appear ────────────────────────────
	fmt.Println("[2/4] waiting for /projects/mobile to pick up the clone")
	found := false
	for i := 0; i < 15; i++ {
		time.Sleep(1 * time.Second)
		list, _ := c.ListMobileProjects(ctx)
		if list == nil {
			continue
		}
		for _, p := range list.Projects {
			if p.Path == cloneRes.Path {
				fmt.Printf("      ✔ %s (%s) appeared after %d s\n", p.Name, p.Framework, i+1)
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		fmt.Fprintln(os.Stderr, "      ✗ project never appeared in mobile list")
		os.Exit(1)
	}
	// ── Start dev server + stream until ready ─────────────────
	fmt.Println("[3/4] starting dev server and streaming /dev/events")
	if !streamDevRunUntilReady(c, cloneRes.Path, *framework, *timeout, false) {
		os.Exit(1)
	}
	// ── Stop ──────────────────────────────────────────────────
	fmt.Println("[4/4] stopping dev server")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := c.StopDevServer(stopCtx); err != nil {
		fmt.Fprintf(os.Stderr, "stop failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("=== E2E passed ===")
}

// streamDevRunUntilReady issues POST /dev/start, opens /dev/events,
// prints every log/lifecycle event like the mobile card would, and
// returns true when the server transitions to ready. Returns false
// on error, timeout, or explicit "error" SSE event. If stopOnSignal
// is true, Ctrl-C will trigger POST /dev/stop before exit.
func streamDevRunUntilReady(c *MobileClient, workDir, framework string, timeout time.Duration, stopOnSignal bool) bool {
	startCtx, startCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startCancel()
	if err := c.StartDevServer(startCtx, DevStartRequest{Framework: framework, WorkDir: workDir}); err != nil {
		fmt.Fprintf(os.Stderr, "/dev/start failed: %v\n", err)
		return false
	}
	fmt.Println("[start] request accepted; subscribing to /dev/events")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	readyCh := make(chan bool, 1)
	errorCh := make(chan string, 1)
	if stopOnSignal {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\n[emu] Ctrl-C → stopping dev server")
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			c.StopDevServer(stopCtx)
			cancel()
		}()
	}
	// SSE stream in its own goroutine.
	go func() {
		err := c.SubscribeDevEvents(ctx, func(ev DevServerEvent) {
			switch ev.Type {
			case "log":
				if ev.LogLine != "" {
					fmt.Printf("  %s\n", ev.LogLine)
				}
			case "ready":
				fmt.Printf("[ready] %s · %s\n", ev.Framework, ev.Message)
				select {
				case readyCh <- true:
				default:
				}
			case "error":
				fmt.Fprintf(os.Stderr, "[error] %s\n", ev.Message)
				select {
				case errorCh <- ev.Message:
				default:
				}
			case "stopped":
				fmt.Println("[stopped]")
			case "starting":
				fmt.Printf("[starting] %s\n", ev.Message)
			}
		})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "[events] stream error: %v\n", err)
		}
	}()
	// Also poll /dev/status every 2s as a backup for the error
	// surface — the mobile card does the same thing (Tasks 2+3).
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "[emu] timed out waiting for ready (%s)\n", timeout)
			return false
		case <-readyCh:
			return true
		case msg := <-errorCh:
			fmt.Fprintf(os.Stderr, "[emu] dev server reported failure: %s\n", msg)
			return false
		case <-poll.C:
			pollCtx, pollCancel := context.WithTimeout(context.Background(), 3*time.Second)
			status, err := c.GetDevStatus(pollCtx)
			pollCancel()
			if err != nil {
				continue
			}
			if status != nil && status.Error != "" {
				fmt.Fprintf(os.Stderr, "[emu] /dev/status reports error: %s\n", status.Error)
				return false
			}
			if status != nil && status.Running {
				// SSE may have missed the "ready" frame but the
				// server is clearly up — treat as success.
				fmt.Printf("[ready] (via /dev/status) %s · port %d\n", status.Framework, status.Port)
				return true
			}
		}
	}
}

// maybeStoreGitToken uploads a PAT to the agent's git-credentials
// store so a subsequent clone of a private repo on that host will
// authenticate. Falls back to $GITHUB_TOKEN / $GH_TOKEN when no flag
// is given. No-op when the URL is SSH or no token is available —
// the clone may still succeed if creds are already stored on the
// box, and we don't want to wipe prior creds by writing an empty one.
func maybeStoreGitToken(ctx context.Context, c *MobileClient, rawURL, token, user string) error {
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return nil
	}
	host := emuHostFromURL(rawURL)
	if host == "" {
		return nil
	}
	fmt.Printf("[auth] storing PAT for %s (user=%s, len=%d)\n", host, user, len(token))
	return c.SetGitCredential(ctx, host, user, token)
}

// emuHostFromURL extracts a git host from a clone URL. Mirrors the
// agent's own hostFromURL logic. Returns "" for malformed input or
// pure ssh://user@host:path short form.
func emuHostFromURL(rawURL string) string {
	// SSH short form: git@github.com:user/repo
	if strings.Contains(rawURL, "@") && strings.Contains(rawURL, ":") && !strings.Contains(rawURL, "://") {
		at := strings.Index(rawURL, "@")
		colon := strings.Index(rawURL[at:], ":")
		if colon > 0 {
			return rawURL[at+1 : at+colon]
		}
	}
	if !strings.Contains(rawURL, "://") {
		return ""
	}
	after := rawURL[strings.Index(rawURL, "://")+3:]
	if i := strings.IndexAny(after, "/:"); i >= 0 {
		host := after[:i]
		if i := strings.Index(host, "@"); i >= 0 {
			host = host[i+1:]
		}
		return host
	}
	return after
}

func shortJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
