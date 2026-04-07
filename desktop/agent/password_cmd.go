package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// runForgotPassword handles `yaver forgot-password <email>`.
func runForgotPassword(args []string) {
	fs := flag.NewFlagSet("forgot-password", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: yaver forgot-password <email>")
		fmt.Fprintln(os.Stderr, "  Send a password reset link to the given email address.")
	}
	fs.Parse(args)

	email := fs.Arg(0)
	if email == "" {
		fs.Usage()
		os.Exit(1)
	}

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading config:", err)
		os.Exit(1)
	}

	baseURL := cfg.ConvexSiteURL
	if baseURL == "" {
		baseURL = defaultConvexSiteURL
	}

	if err := RequestPasswordReset(baseURL, email); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	fmt.Println("If an account exists for that email, a reset link has been sent.")
	fmt.Println("Check your inbox and follow the link to set a new password.")
}

// runChangePassword handles `yaver change-password`.
func runChangePassword(args []string) {
	fs := flag.NewFlagSet("change-password", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: yaver change-password")
		fmt.Fprintln(os.Stderr, "  Change your password (email accounts only). Requires current password.")
	}
	fs.Parse(args)

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading config:", err)
		os.Exit(1)
	}

	if cfg.AuthToken == "" {
		fmt.Fprintln(os.Stderr, "Not signed in. Run 'yaver auth' first.")
		os.Exit(1)
	}

	baseURL := cfg.ConvexSiteURL
	if baseURL == "" {
		baseURL = defaultConvexSiteURL
	}

	// Read current password
	fmt.Print("Current password: ")
	currentBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		// Fallback for non-terminal (piped input)
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			currentBytes = []byte(scanner.Text())
		} else {
			fmt.Fprintln(os.Stderr, "Error reading password")
			os.Exit(1)
		}
	}
	currentPassword := strings.TrimSpace(string(currentBytes))

	// Read new password
	fmt.Print("New password (min 8 chars): ")
	newBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading password")
		os.Exit(1)
	}
	newPassword := strings.TrimSpace(string(newBytes))

	if len(newPassword) < 8 {
		fmt.Fprintln(os.Stderr, "Password must be at least 8 characters.")
		os.Exit(1)
	}

	// Confirm new password
	fmt.Print("Confirm new password: ")
	confirmBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error reading password")
		os.Exit(1)
	}
	confirmPassword := strings.TrimSpace(string(confirmBytes))

	if newPassword != confirmPassword {
		fmt.Fprintln(os.Stderr, "Passwords do not match.")
		os.Exit(1)
	}

	if err := ChangePassword(baseURL, cfg.AuthToken, currentPassword, newPassword); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	fmt.Println("Password changed successfully.")
}
