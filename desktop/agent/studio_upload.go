package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// studio_upload.go — push captured Studio screenshots to the stores. iOS reuses
// the proven embedded ASC backend (shots_asc.go: ascUploadScreenshots / metadata
// / submit) so the iOS/macOS path is first-class. Android uses a Play upload
// script when present (service-account creds required), else returns a clear
// error rather than pretending.

// studioUploadScreenshots uploads the PNGs in dir to the store for platform.
//   - ios:     App Store Connect (embedded python, APP_STORE_KEY_* auth)
//   - android: Google Play (service-account script; best-effort)
//
// When submit is true (iOS), it also sets metadata + attempts submit-for-review.
func studioUploadScreenshots(platform, dir, bundleID, locale, version string, submit bool) (map[string]any, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("dir required")
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("dir not found: %s", dir)
	}
	if locale == "" {
		locale = "en-US"
	}

	switch strings.ToLower(platform) {
	case "ios", "appstore", "apple":
		if strings.TrimSpace(bundleID) == "" {
			return nil, fmt.Errorf("bundleId required for iOS (App Store Connect)")
		}
		if err := ascUploadScreenshots(bundleID, dir, locale); err != nil {
			return nil, fmt.Errorf("asc upload: %w", err)
		}
		res := map[string]any{"platform": "ios", "uploaded": true, "bundleId": bundleID, "locale": locale}
		if submit {
			if err := ascSetMetadata(bundleID, ""); err != nil {
				res["metadataError"] = err.Error()
			}
			submitted, err := ascSubmitForReview(bundleID, version)
			res["submitted"] = submitted
			if err != nil {
				res["submitError"] = err.Error()
			}
		}
		return res, nil

	case "android", "play", "playstore":
		script := findPlayUploadScript()
		if script == "" {
			return nil, fmt.Errorf("Play upload script not found — set STUDIO_PLAY_UPLOAD to an upload script and provide PLAY_STORE_KEY_FILE (service account). Screenshots are at %s for manual upload", dir)
		}
		pkg := strings.TrimSpace(bundleID) // for android, bundleID carries the package name
		args := []string{script, "--dir", dir}
		if pkg != "" {
			args = append(args, "--package", pkg)
		}
		cmd := exec.Command("python3", args...)
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("play upload: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return map[string]any{"platform": "android", "uploaded": true, "package": pkg, "output": strings.TrimSpace(string(out))}, nil

	default:
		return nil, fmt.Errorf("unknown platform %q (use ios or android)", platform)
	}
}

// findPlayUploadScript locates a Play screenshot upload script: the env override
// first, then the repo's known location.
func findPlayUploadScript() string {
	if p := strings.TrimSpace(os.Getenv("STUDIO_PLAY_UPLOAD")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, c := range []string{
		"scripts/upload-playstore-screenshots.py",
		filepath.Join(os.Getenv("HOME"), "Workspace", "yaver.io", "scripts", "upload-playstore-screenshots.py"),
	} {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
