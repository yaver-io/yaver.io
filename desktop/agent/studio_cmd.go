package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yaver-io/agent/studio"
)

// runStudio dispatches `yaver studio <subcommand>`. The store-asset studio
// (docs/yaver-store-asset-studio.md). P0 ships the permission-justification
// path (offline static analysis + reviewer prose); capture/record/composite/
// publish stages land in later phases and print a clear pointer until then.
func runStudio(args []string) {
	if len(args) == 0 {
		studioUsage()
		os.Exit(2)
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "permission", "permission-video":
		runStudioPermission(rest)
	case "base":
		runStudioBase(rest)
	case "screenshots", "preview-video", "icons", "feature-graphic", "plan", "status":
		fmt.Fprintf(os.Stderr, "studio %s: not yet implemented — see docs/yaver-store-asset-studio.md (phase plan).\n", sub)
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown studio subcommand %q\n", sub)
		studioUsage()
		os.Exit(2)
	}
}

func studioUsage() {
	fmt.Fprintln(os.Stderr, `yaver studio — store-asset generator

  permission-video --permission <PERM>   Analyze an app's permission usage and
                                          generate the Play Console justification
                                          prose + demo-video shot-list.
  base build|up|ls|gc                     Yaver Base Image: build a warm golden
                                          redroid snapshot once, restore it in
                                          seconds per app-test run.
  screenshots | preview-video | icons | feature-graphic | plan | status
                                          (later phases — see docs)

Run "yaver studio permission-video -h" or "yaver studio base -h" for flags.`)
}

func runStudioPermission(args []string) {
	fs := flag.NewFlagSet("studio permission-video", flag.ExitOnError)
	permission := fs.String("permission", "", "Permission to justify, e.g. FOREGROUND_SERVICE_SPECIAL_USE")
	manifest := fs.String("manifest", "", "Path to AndroidManifest.xml (default: auto-detect under --path)")
	path := fs.String("path", "", "Project path to scan (default: current directory)")
	app := fs.String("app", "", "App display name (default: project directory name)")
	what := fs.String("what", "", "One clause describing what the service does, e.g. \"an on-device coding agent and a local Linux environment\"")
	out := fs.String("out", "", "Write the justification markdown to this directory (default: stdout only)")
	// Capture flags — drive a real surface to RECORD the demo video.
	capture := fs.Bool("capture", false, "Also record the demo video on a capture surface (redroid)")
	apk := fs.String("apk", "", "APK to install on the surface (built for the surface arch; x86_64 for redroid on an x86 box)")
	pkg := fs.String("package", "", "App package id (default: from --manifest <manifest package>) ")
	activity := fs.String("activity", ".MainActivity", "Launch activity")
	startAction := fs.String("start-action", "", "Foreground-service start intent action (e.g. io.yaver.mobile.sandbox.START)")
	sshHost := fs.String("ssh-host", "", "Run the surface on this host over ssh (on-prem). Empty = local runner (managed cloud farm box).")
	sshOpts := fs.String("ssh-opts", "-o ConnectTimeout=10", "Extra ssh/scp options")
	hostWorkDir := fs.String("host-workdir", "", "Absolute dir on the surface host for the redroid /data bind-mount + file exchange")
	image := fs.String("redroid-image", "redroid/redroid:13.0.0-latest", "redroid image")
	maxSec := fs.Int("max-sec", 0, "Max recording seconds (0 = derive from flow)")
	fs.Parse(args)

	if strings.TrimSpace(*permission) == "" {
		fmt.Fprintln(os.Stderr, "Pass --permission (e.g. --permission FOREGROUND_SERVICE_SPECIAL_USE).")
		os.Exit(2)
	}

	root := strings.TrimSpace(*path)
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	manifestPath := strings.TrimSpace(*manifest)
	if manifestPath == "" {
		manifestPath = findAndroidManifest(root)
	}
	if manifestPath == "" {
		fmt.Fprintln(os.Stderr, "Could not find an AndroidManifest.xml. Pass --manifest <path>.")
		os.Exit(1)
	}

	facts, err := studio.AnalyzeAndroidManifest(manifestPath, *permission)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze manifest: %v\n", err)
		os.Exit(1)
	}
	facts.TriggerHint = studio.FindTrigger(root, facts)

	appName := strings.TrimSpace(*app)
	if appName == "" {
		appName = filepath.Base(root)
	}

	j := studio.GenerateJustification(facts, appName, *what)
	md := j.Markdown(facts.Permission)

	fmt.Fprintf(os.Stderr, "→ manifest: %s\n", manifestPath)
	if facts.Service != nil {
		fmt.Fprintf(os.Stderr, "→ service:  %s (foregroundServiceType=%s)\n", facts.Service.Name, facts.FGSType)
	}
	if facts.SpecialUseSubtype != "" {
		fmt.Fprintf(os.Stderr, "→ subtype:  %s\n", facts.SpecialUseSubtype)
	}
	if facts.TriggerHint != "" {
		fmt.Fprintf(os.Stderr, "→ trigger:  %s\n", facts.TriggerHint)
	}
	fmt.Fprintln(os.Stderr, "")

	fmt.Println(md)

	if dir := strings.TrimSpace(*out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir %s: %v\n", dir, err)
			os.Exit(1)
		}
		outPath := filepath.Join(dir, "justification.md")
		if err := os.WriteFile(outPath, []byte(md), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "✓ wrote %s\n", outPath)
	}

	if !*capture {
		fmt.Fprintln(os.Stderr, "\nNext: add --capture (with --apk + --host-workdir, and --ssh-host for on-prem)\nto RECORD the demo video on a redroid surface. The shot-list above is the\nscene the recorder follows. See docs/yaver-store-asset-studio.md §6.")
		return
	}

	// --- capture: drive a redroid surface to record the demo video ---
	if strings.TrimSpace(*apk) == "" || strings.TrimSpace(*hostWorkDir) == "" {
		fmt.Fprintln(os.Stderr, "--capture needs --apk and --host-workdir")
		os.Exit(2)
	}
	pkgID := strings.TrimSpace(*pkg)
	if pkgID == "" {
		pkgID = readAndroidPackage(manifestPath)
	}
	if pkgID == "" {
		fmt.Fprintln(os.Stderr, "could not determine app package; pass --package")
		os.Exit(2)
	}

	var runner studio.Runner = studio.LocalRunner{}
	if h := strings.TrimSpace(*sshHost); h != "" {
		runner = studio.SSHRunner{Host: h, Opts: strings.Fields(*sshOpts)}
	}
	surface := &studio.RedroidSurface{
		R: runner, Image: *image, HostWorkDir: *hostWorkDir,
		Log: func(m string) { fmt.Fprintf(os.Stderr, "  [redroid] %s\n", m) },
	}
	spec := studio.PermissionVideoSpec{
		App:          studio.App{Package: pkgID, Activity: *activity},
		ArtifactPath: *apk,
		Facts:        facts,
		StartAction:  *startAction,
		MaxSec:       *maxSec,
	}
	fmt.Fprintf(os.Stderr, "\n→ capturing on %s (redroid) …\n", runner.Label())
	mp4, cues, _, err := studio.CapturePermissionVideo(context.Background(), surface, spec, appName, *what)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capture failed: %v\n", err)
		os.Exit(1)
	}
	dir := strings.TrimSpace(*out)
	if dir == "" {
		dir = "."
	}
	os.MkdirAll(dir, 0o755)
	mp4Path := filepath.Join(dir, "permission-demo.mp4")
	if err := os.WriteFile(mp4Path, mp4, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write mp4: %v\n", err)
		os.Exit(1)
	}
	cuesJSON, _ := json.MarshalIndent(cues, "", "  ")
	os.WriteFile(filepath.Join(dir, "captions.json"), cuesJSON, 0o644)
	fmt.Fprintf(os.Stderr, "✓ recorded %s (%d bytes) + captions.json\n", mp4Path, len(mp4))

	// Composite captions in-line when ffmpeg can draw text; else leave the raw
	// clip + captions.json for manual compositing.
	if capped, cerr := studio.CaptionMP4(context.Background(), mp4, cues, "", ""); cerr == nil {
		capPath := filepath.Join(dir, "permission-demo-captioned.mp4")
		if err := os.WriteFile(capPath, capped, 0o644); err == nil {
			fmt.Fprintf(os.Stderr, "✓ captioned %s (%d bytes)\n", capPath, len(capped))
		}
	} else {
		fmt.Fprintf(os.Stderr, "  (skipped captioning: %v — ship %s + captions.json)\n", cerr, filepath.Base(mp4Path))
	}
}

// readAndroidPackage extracts the package= attribute from a manifest (best
// effort; Expo/AGP often inject it at build time so it may be empty).
func readAndroidPackage(manifestPath string) string {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return ""
	}
	s := string(b)
	if i := strings.Index(s, "package=\""); i >= 0 {
		rest := s[i+len("package=\""):]
		if j := strings.IndexByte(rest, '"'); j >= 0 {
			return rest[:j]
		}
	}
	return ""
}

// findAndroidManifest looks for the main AndroidManifest.xml under root,
// preferring the conventional app source path before falling back to a walk.
func findAndroidManifest(root string) string {
	for _, rel := range []string{
		filepath.Join("android", "app", "src", "main", "AndroidManifest.xml"),
		filepath.Join("mobile", "android", "app", "src", "main", "AndroidManifest.xml"),
		filepath.Join("app", "src", "main", "AndroidManifest.xml"),
		"AndroidManifest.xml",
	} {
		p := filepath.Join(root, rel)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	// Fallback: shallow walk, prefer a src/main path, skip build/node_modules.
	var found string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			if found != "" {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "build", ".gradle", "Pods", "intermediates":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "AndroidManifest.xml" && strings.Contains(p, filepath.Join("src", "main")) {
			found = p
		}
		return nil
	})
	return found
}
