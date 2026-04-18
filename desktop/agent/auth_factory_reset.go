package main

import (
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"syscall"
)

func runAuthFactoryReset(args []string) {
	fs := flag.NewFlagSet("auth factory-reset", flag.ExitOnError)
	convexURL := fs.String("convex-url", defaultConvexSiteURL, "Convex site URL")
	headless := fs.Bool("headless", false, "Use device code flow after reset")
	skipNPM := fs.Bool("skip-npm", false, "Skip npm refresh and reuse the current yaver binary")
	fs.Parse(args)

	fmt.Println("Factory-resetting Yaver auth state...")

	cfg, err := LoadConfig()
	if err == nil && cfg.AuthToken != "" {
		runSignout()
	} else if pid, running := isAgentRunning(); running {
		if proc, findErr := os.FindProcess(pid); findErr == nil {
			terminateProcess(proc)
			fmt.Println("Stopped running Yaver agent.")
		}
	}

	for _, resolver := range []func() (string, error){ConfigPath, pairedTokensPath} {
		path, pathErr := resolver()
		if pathErr != nil || path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove %s: %v\n", path, err)
		}
	}

	nextBinary := ""
	if !*skipNPM {
		if path, refreshErr := refreshNpmYaverCLI(); refreshErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: npm refresh failed: %v\n", refreshErr)
			fmt.Fprintln(os.Stderr, "Continuing with the current yaver binary.")
		} else {
			nextBinary = path
		}
	}
	if nextBinary == "" {
		if exe, exeErr := os.Executable(); exeErr == nil {
			nextBinary = exe
		}
	}
	if nextBinary == "" {
		fmt.Fprintln(os.Stderr, "Error: could not locate yaver binary for restart")
		os.Exit(1)
	}

	nextArgs := []string{"auth", "--convex-url", *convexURL}
	if *headless {
		nextArgs = append(nextArgs, "--headless")
	}

	fmt.Println()
	fmt.Printf("Restarting sign-in with %s...\n", filepath.Base(nextBinary))
	if err := restartAuthBinary(nextBinary, nextArgs); err != nil {
		fmt.Fprintf(os.Stderr, "Error: restart sign-in: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func refreshNpmYaverCLI() (string, error) {
	augmentAgentPATH()
	npmPath, err := osexec.LookPath("npm")
	if err != nil {
		return "", fmt.Errorf("npm not found in PATH")
	}
	fmt.Println("Refreshing npm-installed Yaver CLI...")
	cmd := osexec.Command(npmPath, "install", "-g", "yaver-cli@latest")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		return "", err
	}
	augmentAgentPATH()
	yaverPath, err := osexec.LookPath("yaver")
	if err != nil {
		return "", fmt.Errorf("npm refresh succeeded but `yaver` is not on PATH")
	}
	return yaverPath, nil
}

func restartAuthBinary(binaryPath string, args []string) error {
	cmd := osexec.Command(binaryPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	return cmd.Run()
}

func spawnAuthFactoryReset(headless bool) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"auth", "factory-reset", "--skip-npm"}
	if headless {
		args = append(args, "--headless")
	}
	cmd := osexec.Command(execPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
