package main

import (
	"fmt"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
)

// uploadToTestFlight uploads an IPA to TestFlight via xcrun altool or xcrun notarytool.
func uploadToTestFlight(ipaPath string) error {
	return uploadToTestFlightForProject(ipaPath, "", nil)
}

func uploadToTestFlightForProject(ipaPath, project string, releaseEnv map[string]string) error {
	if _, err := os.Stat(ipaPath); err != nil {
		return fmt.Errorf("IPA not found: %w", err)
	}

	// Get credentials from vault
	apiKeyID := releaseEnv["APP_STORE_KEY_ID"]
	apiIssuer := releaseEnv["APP_STORE_KEY_ISSUER"]
	apiKeyPath := releaseEnv["APP_STORE_KEY_PATH"]
	if apiKeyID == "" {
		apiKeyID = releaseEnv["APP_STORE_API_KEY_ID"]
		apiIssuer = releaseEnv["APP_STORE_API_ISSUER"]
		apiKeyPath = releaseEnv["APP_STORE_API_KEY_PATH"]
	}
	if apiKeyID == "" && project == "" {
		apiKeyID = getVaultToken("appstore-api-key-id")
		apiIssuer = getVaultToken("appstore-api-issuer")
		apiKeyPath = getVaultToken("appstore-api-key-path")
	}

	if project == "" && (apiKeyID == "" || apiIssuer == "") {
		// Try environment variables as fallback
		apiKeyID = os.Getenv("APP_STORE_API_KEY_ID")
		apiIssuer = os.Getenv("APP_STORE_API_ISSUER")
		apiKeyPath = os.Getenv("APP_STORE_API_KEY_PATH")
	}
	if project != "" && (apiKeyPath == "" || apiKeyID == "" || apiIssuer == "") {
		creds, err := resolveAppleASCCreds(project)
		if err != nil {
			return err
		}
		apiKeyID, apiIssuer = creds.KeyID, creds.IssuerID
		tmp, err := os.MkdirTemp("", "yaver-asc-key-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		apiKeyPath = filepath.Join(tmp, "AuthKey_"+apiKeyID+".p8")
		if err := os.WriteFile(apiKeyPath, []byte(creds.KeyPEM), 0o600); err != nil {
			return err
		}
	}

	if apiKeyID == "" || apiIssuer == "" {
		return fmt.Errorf("App Store Connect credentials required.\n" +
			"Add to vault:\n" +
			"  yaver vault add appstore-api-key-id --category api-key --value <KEY_ID>\n" +
			"  yaver vault add appstore-api-issuer --category api-key --value <ISSUER_ID>\n" +
			"  yaver vault add appstore-api-key-path --category signing-key --value /path/to/AuthKey.p8")
	}

	log.Printf("[testflight] Uploading %s to TestFlight...", ipaPath)

	// Try xcrun altool first (older method, still works)
	args := []string{
		"altool", "--upload-app",
		"--type", "ios",
		"--file", ipaPath,
		"--apiKey", apiKeyID,
		"--apiIssuer", apiIssuer,
	}
	commandEnv := os.Environ()
	if apiKeyPath != "" {
		// Set the API key search path
		keyDir := apiKeyPath
		if strings.HasSuffix(keyDir, ".p8") {
			keyDir = keyDir[:strings.LastIndex(keyDir, "/")]
		}
		commandEnv = append(commandEnv, "API_PRIVATE_KEYS_DIR="+keyDir)
	}

	cmd := osexec.Command("xcrun", args...)
	cmd.Env = commandEnv
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := string(out)
		if strings.Contains(output, "No errors uploading") || strings.Contains(output, "success") {
			// altool prints non-zero exit code but says success sometimes
			return nil
		}
		return fmt.Errorf("xcrun altool failed: %s\n%s", err, output)
	}

	log.Printf("[testflight] Upload complete: %s", strings.TrimSpace(string(out)))
	return nil
}

// uploadToPlayStore uploads an AAB to Google Play Store internal testing track.
func uploadToPlayStore(aabPath string) error {
	return uploadToPlayStoreWithEnv(aabPath, ".", "", nil)
}

func uploadToPlayStoreWithEnv(aabPath, workDir, project string, releaseEnv map[string]string) error {
	if _, err := os.Stat(aabPath); err != nil {
		return fmt.Errorf("AAB not found: %w", err)
	}

	// Check for upload script or bundletool
	keyFile := releaseEnv["PLAY_STORE_KEY_FILE"]
	if keyFile == "" && ownReleaseProject(project) {
		keyFile = getVaultToken("play-store-key-file")
	}
	if keyFile == "" && ownReleaseProject(project) {
		keyFile = os.Getenv("PLAY_STORE_KEY_FILE")
	}

	if keyFile == "" {
		return fmt.Errorf("Google Play service account key required.\n" +
			"Add to vault:\n" +
			"  yaver vault add play-store-key-file --category signing-key --value /path/to/service-account.json\n" +
			"Or set PLAY_STORE_KEY_FILE environment variable.")
	}

	// Prefer the project's dependency-bootstrap wrapper, then its raw uploader.
	// A generated /tmp script must never resolve this relative to itself.
	runner := filepath.Join(workDir, "scripts", "run-playstore-upload.sh")
	uploadScript := filepath.Join(workDir, "scripts", "upload-playstore.py")
	var cmd *osexec.Cmd
	if info, err := os.Stat(runner); err == nil && !info.IsDir() {
		cmd = osexec.Command("bash", runner)
	} else if _, err := os.Stat(uploadScript); err == nil {
		cmd = osexec.Command("python3", uploadScript)
	}
	if cmd != nil {
		cmd.Env = append(os.Environ(), "PLAY_STORE_KEY_FILE="+keyFile, "AAB_PATH="+aabPath)
		for key, value := range releaseEnv {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("upload failed: %s\n%s", err, string(out))
		}
		log.Printf("[playstore] Upload complete: %s", strings.TrimSpace(string(out)))
		return nil
	}

	// Fallback: try google-play CLI
	out, err := osexec.Command("which", "google-play").Output()
	if err == nil && len(out) > 0 {
		cmd := osexec.Command("google-play", "upload",
			"--key", keyFile,
			"--aab", aabPath,
			"--track", "internal")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("google-play upload: %s\n%s", err, string(output))
		}
		return nil
	}

	return fmt.Errorf("no upload tool found. Install google-play CLI or add scripts/upload-playstore.py")
}

// generateIOSManifest creates a manifest.plist for iOS OTA install.
func generateIOSManifest(ipaURL, bundleID, version, title string) []byte {
	manifest := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>items</key>
  <array>
    <dict>
      <key>assets</key>
      <array>
        <dict>
          <key>kind</key>
          <string>software-package</string>
          <key>url</key>
          <string>%s</string>
        </dict>
      </array>
      <key>metadata</key>
      <dict>
        <key>bundle-identifier</key>
        <string>%s</string>
        <key>bundle-version</key>
        <string>%s</string>
        <key>kind</key>
        <string>software</string>
        <key>title</key>
        <string>%s</string>
      </dict>
    </dict>
  </array>
</dict>
</plist>`, ipaURL, bundleID, version, title)
	return []byte(manifest)
}
