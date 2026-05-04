package main

// swift_cmd.go — `yaver swift {doctor,logic}` CLI surface. Wraps
// the Linux Swift toolchain detect in swift_toolchain.go and runs
// `swift build` / `swift test` for SwiftPM projects so a developer
// can iterate on iOS app *logic* (Foundation/Dispatch/etc.) on a
// Linux box without a Mac. UI iteration still needs a paired
// remote-mac builder (Phase 5).
//
// Surface:
//
//	yaver swift doctor                          probe toolchain
//	yaver swift logic [path] [--build] [--test] build/test SwiftPM
//
// Streams stdout/stderr from swift directly to the user's terminal
// so a TDD red/green loop matches what `swift test` prints when
// run by hand. Exit code mirrors swift's so CI pipelines can chain.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// runSwift dispatches `yaver swift <sub> ...`. Called from main.go.
func runSwift(args []string) {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printSwiftUsage()
		if len(args) == 0 {
			os.Exit(2)
		}
		return
	}
	switch args[0] {
	case "doctor":
		runSwiftDoctor(args[1:])
	case "logic":
		runSwiftLogic(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "yaver swift: unknown subcommand %q\n\n", args[0])
		printSwiftUsage()
		os.Exit(2)
	}
}

func printSwiftUsage() {
	fmt.Println("usage: yaver swift <command>")
	fmt.Println()
	fmt.Println("Build + test Linux-compatible Swift packages without a Mac.")
	fmt.Println("UI / iOS app builds still require a macOS host or a paired")
	fmt.Println("remote-mac builder.")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  doctor                                Probe the Linux Swift toolchain")
	fmt.Println("  logic [path] [--build] [--test]       Build and/or test a SwiftPM project")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  yaver swift doctor")
	fmt.Println("  yaver swift logic                     # cwd, build + test")
	fmt.Println("  yaver swift logic ./packages/MyAppCore")
	fmt.Println("  yaver swift logic --test              # tests only")
	fmt.Println()
	fmt.Println("Tip: split a SwiftUI iOS app into a Package.swift library")
	fmt.Println("(testable on Linux) + an Xcode app target that imports it.")
}

func runSwiftDoctor(args []string) {
	fs := flag.NewFlagSet("swift doctor", flag.ExitOnError)
	wantJSON := fs.Bool("json", false, "Emit a single-line JSON document for the dashboard")
	fs.Parse(args)

	tc, _ := DetectSwiftToolchain(context.Background())
	if *wantJSON {
		_ = json.NewEncoder(os.Stdout).Encode(tc)
		if !tc.Available {
			os.Exit(1)
		}
		return
	}

	if !tc.Available {
		fmt.Println("✗ Swift toolchain not detected")
		if tc.Notes != "" {
			fmt.Println("  " + tc.Notes)
		}
		os.Exit(1)
	}
	fmt.Printf("✓ Swift %s at %s\n", tc.Version, tc.Path)
	fmt.Println()
	fmt.Println("Linux Swift can build + test SwiftPM packages that import")
	fmt.Println("Foundation, Dispatch, Combine, swift-nio, Vapor, Hummingbird,")
	fmt.Println("swift-collections, swift-async-algorithms, etc.")
	fmt.Println()
	fmt.Println("UIKit / SwiftUI / AVFoundation / iOS app builds need either:")
	fmt.Println("  • a macOS host with Xcode, or")
	fmt.Println("  • a paired remote-mac builder (`yaver builder use <alias>`,")
	fmt.Println("    Phase 5 — coming soon).")
}

func runSwiftLogic(args []string) {
	fs := flag.NewFlagSet("swift logic", flag.ExitOnError)
	doBuild := fs.Bool("build", false, "Run `swift build`")
	doTest := fs.Bool("test", false, "Run `swift test`")
	verbose := fs.Bool("v", false, "Pass --verbose to swift")
	fs.Parse(args)

	// Default behavior when neither flag is set: build, then test —
	// matches the TDD loop most users want from a single command.
	if !*doBuild && !*doTest {
		*doBuild = true
		*doTest = true
	}

	workDir := "."
	if fs.NArg() > 0 {
		workDir = fs.Arg(0)
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve %q: %v\n", workDir, err)
		os.Exit(1)
	}
	if !hasSwiftPackageManifest(abs) {
		fmt.Fprintf(os.Stderr, "no Package.swift found in %s\n", abs)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "yaver swift logic only operates on SwiftPM projects with a top-level Package.swift.")
		fmt.Fprintln(os.Stderr, "Tip: factor out a `MyAppCore/` Package.swift from your Xcode project so its")
		fmt.Fprintln(os.Stderr, "logic can red/green on Linux without a Mac.")
		os.Exit(2)
	}

	ctx := context.Background()
	tc, err := DetectSwiftToolchain(ctx)
	if err != nil || !tc.Available {
		fmt.Fprintln(os.Stderr, "swift toolchain not available")
		if tc != nil && tc.Notes != "" {
			fmt.Fprintln(os.Stderr, "  "+tc.Notes)
		}
		os.Exit(1)
	}
	fmt.Printf("→ swift %s in %s\n", tc.Version, abs)

	if *doBuild {
		if err := runSwiftSubcommand(ctx, tc, abs, "build", *verbose); err != nil {
			// swift's own exit code is mirrored by ExitError below;
			// when err is something else (toolchain crash), report
			// what we know and exit 1.
			if exitCode, ok := exitCodeFromError(err); ok {
				os.Exit(exitCode)
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *doTest {
		if err := runSwiftSubcommand(ctx, tc, abs, "test", *verbose); err != nil {
			if exitCode, ok := exitCodeFromError(err); ok {
				os.Exit(exitCode)
			}
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

// hasSwiftPackageManifest reports whether dir is the root of a
// SwiftPM project. Refuses to walk upward — if the user pointed at
// a subdirectory of a Package.swift project they meant that
// subdirectory, and reading their parent silently would surprise
// the autodev / scripted callers.
func hasSwiftPackageManifest(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "Package.swift"))
	return err == nil && !st.IsDir()
}

// runSwiftSubcommand execs `swift <sub>` in dir and streams output
// to the calling terminal. Returns the underlying *exec.ExitError
// on a non-zero exit so the caller can mirror swift's status code.
func runSwiftSubcommand(ctx context.Context, tc *SwiftToolchain, dir, sub string, verbose bool) error {
	args := []string{sub}
	if verbose {
		args = append(args, "--verbose")
	}
	fmt.Printf("\n$ swift %s\n", joinArgs(args))
	cmd := exec.CommandContext(ctx, tc.Path, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		// SwiftPM's progress dots / spinners are noisy in CI logs;
		// the parser-friendly output is plenty for the user.
		"NO_COLOR=1",
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("swift %s: %w", sub, err)
	}
	return nil
}

// exitCodeFromError unwraps an ExitError and returns its exit status,
// or (0, false) when err didn't come from a subprocess. Used so
// `yaver swift logic` exits with the same code swift itself would.
func exitCodeFromError(err error) (int, bool) {
	for e := err; e != nil; {
		if ex, ok := e.(*exec.ExitError); ok {
			return ex.ExitCode(), true
		}
		// errors.Unwrap path so wrapped errors with `%w` still match.
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return 0, false
}

// joinArgs is a tiny helper that prints args nicely without pulling
// in strings.Join for a one-line caller.
func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
