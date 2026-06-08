package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yaver-io/agent/studio"
)

// runStudioBase dispatches `yaver studio base <build|up|ls|gc>` — the Yaver Base
// Image lifecycle (docs/yaver-ai-app-test-agent.md §14). CLI runs synchronously
// in the foreground (the ops verbs in ops_qa.go run it async for the UI).
func runStudioBase(args []string) {
	if len(args) == 0 {
		studioBaseUsage()
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "build":
		runStudioBaseBuild(rest)
	case "up", "restore":
		runStudioBaseUp(rest)
	case "ls", "list":
		runStudioBaseList(rest)
	case "gc", "prune":
		runStudioBaseGC(rest)
	case "-h", "--help", "help":
		studioBaseUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown base subcommand %q\n", sub)
		studioBaseUsage()
		os.Exit(2)
	}
}

func studioBaseUsage() {
	fmt.Fprintln(os.Stderr, `yaver studio base — Yaver Base Image (warm golden redroid snapshot)

  build [--version V] [--yaver-apk APK] [--image IMG]   Cold-boot redroid, bake
                                                         the APK, snapshot /data.
  up    [--version V|latest]                             Restore + warm-boot,
                                                         leave the container up.
  ls                                                     List snapshots (this arch).
  gc    [--keep N]                                       Prune all but N newest.

Common flags:
  --ssh-host user@host   Run on an on-prem host (default: local farm box)
  --ssh-opts "..."       Extra ssh/scp options
  --host-workdir DIR     /data bind-mount on the surface host (ssh: required)
  --snapshot-dir DIR     Where snapshots live (ssh: required)
  --container NAME       redroid container name (default yaver-base)`)
}

// baseFlags registers the shared flag set + returns a builder for the request.
func baseFlags(name string) (*flag.FlagSet, func() studioBaseRequest) {
	fs := flag.NewFlagSet("studio base "+name, flag.ExitOnError)
	version := fs.String("version", "", "snapshot label (build) / which version (up; empty = latest)")
	image := fs.String("image", "redroid/redroid:13.0.0-latest", "redroid image")
	yaverAPK := fs.String("yaver-apk", "", "APK to bake into the base (build)")
	sshHost := fs.String("ssh-host", "", "on-prem host (empty = local farm box)")
	sshOpts := fs.String("ssh-opts", "-o ConnectTimeout=10", "extra ssh/scp options")
	hostWorkDir := fs.String("host-workdir", "", "/data bind-mount on the surface host")
	snapshotDir := fs.String("snapshot-dir", "", "snapshot store dir on the surface host")
	container := fs.String("container", "yaver-base", "redroid container name")
	keep := fs.Int("keep", 2, "snapshots to retain (gc)")
	return fs, func() studioBaseRequest {
		return studioBaseRequest{
			Version: *version, Image: *image, YaverAPK: *yaverAPK,
			SSHHost: *sshHost, SSHOpts: *sshOpts, HostWorkDir: *hostWorkDir,
			SnapshotDir: *snapshotDir, Container: *container, Keep: *keep,
		}
	}
}

func cliBaseSpec(req studioBaseRequest) *studio.BaseSpec {
	spec, err := baseSpecFromReq(req, func(l string) { fmt.Fprintf(os.Stderr, "  %s\n", l) })
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	if absAPK := strings.TrimSpace(req.YaverAPK); absAPK != "" {
		if abs, err := filepath.Abs(absAPK); err == nil {
			spec.YaverAPK = abs
		}
	}
	return spec
}

func runStudioBaseBuild(args []string) {
	fs, build := baseFlags("build")
	fs.Parse(args)
	spec := cliBaseSpec(build())
	fmt.Fprintf(os.Stderr, "→ building Yaver Base Image on %s\n", spec.R.Label())
	man, err := spec.Build(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("built base %s (%s, %d bytes, baked=%v, sha %s)\n", man.Version, man.Arch, man.Bytes, man.YaverBaked, shortSHAcli(man.SHA256))
}

func runStudioBaseUp(args []string) {
	fs, build := baseFlags("up")
	fs.Parse(args)
	spec := cliBaseSpec(build())
	fmt.Fprintf(os.Stderr, "→ restoring Yaver Base Image on %s\n", spec.R.Label())
	_, man, err := spec.Up(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "up failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("base %s warm — container %q ready to drive\n", man.Version, spec.Container)
}

func runStudioBaseList(args []string) {
	fs, build := baseFlags("ls")
	fs.Parse(args)
	spec := cliBaseSpec(build())
	mans, err := spec.List(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "list failed: %v\n", err)
		os.Exit(1)
	}
	if len(mans) == 0 {
		fmt.Println("no base snapshots (run `yaver studio base build`)")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "VERSION\tARCH\tBYTES\tBAKED\tCREATED\tSHA")
	for _, m := range mans {
		created := ""
		if m.CreatedAtMs > 0 {
			created = time.UnixMilli(m.CreatedAtMs).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%v\t%s\t%s\n", m.Version, m.Arch, m.Bytes, m.YaverBaked, created, shortSHAcli(m.SHA256))
	}
	w.Flush()
}

func runStudioBaseGC(args []string) {
	fs, build := baseFlags("gc")
	fs.Parse(args)
	req := build()
	spec := cliBaseSpec(req)
	removed, err := spec.GC(context.Background(), req.Keep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gc failed: %v\n", err)
		os.Exit(1)
	}
	if len(removed) == 0 {
		fmt.Printf("nothing to prune (keeping newest %d)\n", req.Keep)
		return
	}
	fmt.Printf("pruned %d snapshot(s): %s\n", len(removed), strings.Join(removed, ", "))
}

func shortSHAcli(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
