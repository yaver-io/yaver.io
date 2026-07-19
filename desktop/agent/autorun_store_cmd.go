package main

// autorun_store_cmd.go — the `yaver autorun deploy-lease` / `deploy-status` CLI
// that the deploy scripts call (AUTORUN_STORE.md §10.3). This is the wrapper
// hook that actually fixes the concurrent-deploy race: a deploy acquires the
// per-target lease before its first destructive step and exits 3 if a sibling
// holds it. It also gives the mobile/web UI a real feed of "what shipped, when,
// with which build number, and is anything deploying right now".
//
// Exit codes (contract with the deploy scripts):
//   0  acquired / ok
//   3  held by another live autorun (print who, so the operator can wait/abort)
//   4  quota exhausted for the day (don't upload a broken build a 19th time)
//   1  usage / internal error

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func autorunHolder() string {
	// Human-readable, never a secret: tmux session if we have one, else host+pid.
	if s := os.Getenv("TMUX_PANE"); s != "" {
		if sess := os.Getenv("YAVER_TMUX_SESSION"); sess != "" {
			return sess
		}
	}
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/pid%d", host, os.Getpid())
}

// runAutorunDeployLease: yaver autorun deploy-lease <acquire|heartbeat|release|abort|status> --target ...
func runAutorunDeployLease(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yaver autorun deploy-lease <acquire|heartbeat|release|abort|status> --target <t> [--autorun <id>] [--workdir <p>] [--branch <b>] [--build <n>] [--stage <s>] [--outcome <o>] [--ttl <dur>]")
		os.Exit(1)
	}
	verb := args[0]
	fs := flag.NewFlagSet("deploy-lease", flag.ContinueOnError)
	target := fs.String("target", "", "deploy target: testflight | playstore | convex | cloudflare-web | ...")
	autorun := fs.String("autorun", "", "autorun id (defaults to the holder descriptor for a bare deploy)")
	workdir := fs.String("workdir", "", "checkout the deploy runs from")
	branch := fs.String("branch", "", "git branch being shipped")
	build := fs.String("build", "", "CFBundleVersion / versionCode / commit sha")
	stage := fs.String("stage", "uploading", "archiving|exporting|uploading|submitting")
	outcome := fs.String("outcome", "success", "success|failure|quota_exceeded|aborted (for release)")
	ttl := fs.Duration("ttl", 60*time.Minute, "lease TTL")
	asJSON := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(1)
	}
	if *target == "" {
		fmt.Fprintln(os.Stderr, "deploy-lease: --target is required")
		os.Exit(1)
	}
	id := *autorun
	if id == "" {
		id = autorunHolder()
	}
	st, err := openAutorunStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deploy-lease:", err)
		os.Exit(1)
	}
	defer st.Close()
	_, _ = st.SweepOld(autorunStoreRetention) // opportunistic cleanup

	switch verb {
	case "acquire":
		if wd := *workdir; wd == "" {
			cwd, _ := os.Getwd()
			workdir = &cwd
		}
		err := st.AcquireDeployLease(*target, id, autorunHolder(), *workdir, *branch, *build, *ttl)
		switch e := err.(type) {
		case nil:
			fmt.Printf("acquired %s lease (build %s) as %s\n", *target, *build, id)
		case *LeaseHeld:
			fmt.Fprintln(os.Stderr, e.Error())
			os.Exit(3)
		case *QuotaExceeded:
			fmt.Fprintln(os.Stderr, e.Error())
			os.Exit(4)
		default:
			fmt.Fprintln(os.Stderr, "deploy-lease acquire:", err)
			os.Exit(1)
		}
	case "heartbeat":
		if err := st.HeartbeatDeployLease(*target, id, *stage, *ttl); err != nil {
			fmt.Fprintln(os.Stderr, "deploy-lease heartbeat:", err)
			os.Exit(1)
		}
		fmt.Printf("heartbeat %s stage=%s\n", *target, *stage)
	case "release":
		if err := st.ReleaseDeployLease(*target, id, *outcome); err != nil {
			fmt.Fprintln(os.Stderr, "deploy-lease release:", err)
			os.Exit(1)
		}
		fmt.Printf("released %s outcome=%s\n", *target, *outcome)
	case "abort":
		if err := st.AbortDeployLease(*target); err != nil {
			fmt.Fprintln(os.Stderr, "deploy-lease abort:", err)
			os.Exit(1)
		}
		fmt.Printf("aborted %s lease\n", *target)
	case "status":
		cur, err := st.CurrentDeployLease(*target)
		if err != nil {
			fmt.Fprintln(os.Stderr, "deploy-lease status:", err)
			os.Exit(1)
		}
		used, cap, _ := st.DeployQuotaUsed(*target)
		if *asJSON {
			out := map[string]any{"target": *target, "uploads_today": used, "quota": cap}
			if cur != nil {
				out["holder"] = cur.Holder
				out["workdir"] = cur.Workdir
				out["build"] = cur.Build
				out["stage"] = cur.Stage
				out["started_at"] = cur.StartedAt
			}
			b, _ := json.Marshal(out)
			fmt.Println(string(b))
		} else if cur != nil {
			fmt.Printf("%s: DEPLOYING by %s (build %s, %s) since %s — %d/%d uploads today\n",
				*target, cur.Holder, cur.Build, cur.Stage, time.Unix(cur.StartedAt, 0).Format(time.Kitchen), used, cap)
		} else {
			fmt.Printf("%s: free — %d/%d uploads today\n", *target, used, cap)
		}
	default:
		fmt.Fprintln(os.Stderr, "deploy-lease: unknown verb", verb)
		os.Exit(1)
	}
}

// runAutorunDeployStatus: yaver autorun deploy-status [--json] — a lean cross-
// target board (currently deploying? last build? uploads today?) for the UI.
func runAutorunDeployStatus(args []string) {
	fs := flag.NewFlagSet("deploy-status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "JSON output")
	_ = fs.Parse(args)
	st, err := openAutorunStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "deploy-status:", err)
		os.Exit(1)
	}
	defer st.Close()
	targets := []string{"testflight", "playstore", "convex", "cloudflare-web"}
	type row struct {
		Target    string `json:"target"`
		Deploying bool   `json:"deploying"`
		Holder    string `json:"holder,omitempty"`
		Build     string `json:"build,omitempty"`
		Stage     string `json:"stage,omitempty"`
		Uploads   int    `json:"uploads_today"`
		Quota     int    `json:"quota"`
	}
	var rows []row
	for _, t := range targets {
		cur, _ := st.CurrentDeployLease(t)
		used, cap, _ := st.DeployQuotaUsed(t)
		r := row{Target: t, Uploads: used, Quota: cap}
		if cur != nil {
			r.Deploying, r.Holder, r.Build, r.Stage = true, cur.Holder, cur.Build, cur.Stage
		}
		rows = append(rows, r)
	}
	if *asJSON {
		b, _ := json.Marshal(rows)
		fmt.Println(string(b))
		return
	}
	for _, r := range rows {
		if r.Deploying {
			fmt.Printf("  %-14s DEPLOYING %s (build %s, %s)  [%d/%d today]\n", r.Target, r.Holder, r.Build, r.Stage, r.Uploads, r.Quota)
		} else {
			fmt.Printf("  %-14s idle  [%d/%d today]\n", r.Target, r.Uploads, r.Quota)
		}
	}
}
