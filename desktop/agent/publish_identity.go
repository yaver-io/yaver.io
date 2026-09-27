package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var ipaBundleIDPattern = regexp.MustCompile(`<key>CFBundleIdentifier</key>\s*<string>([^<]+)</string>`)

// verifyIPAIdentity checks the artifact itself before altool gets a chance to
// mutate App Store Connect. XML plists are parsed directly; binary plists use
// macOS plutil, available on every supported TestFlight upload host.
func verifyIPAIdentity(ipaPath, expected string) error {
	zr, err := zip.OpenReader(ipaPath)
	if err != nil {
		return fmt.Errorf("open IPA for identity verification: %w", err)
	}
	defer zr.Close()
	var plist *zip.File
	for _, file := range zr.File {
		name := filepath.ToSlash(file.Name)
		if strings.HasPrefix(name, "Payload/") && strings.HasSuffix(name, ".app/Info.plist") {
			plist = file
			break
		}
	}
	if plist == nil {
		return fmt.Errorf("IPA identity verification: Payload/*.app/Info.plist missing")
	}
	rc, err := plist.Open()
	if err != nil {
		return fmt.Errorf("open IPA Info.plist: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(rc, 4<<20))
	rc.Close()
	if err != nil {
		return fmt.Errorf("read IPA Info.plist: %w", err)
	}
	actual := ""
	if match := ipaBundleIDPattern.FindSubmatch(data); len(match) == 2 {
		actual = strings.TrimSpace(string(match[1]))
	} else {
		tmp, err := os.CreateTemp("", "yaver-ipa-info-*.plist")
		if err != nil {
			return err
		}
		name := tmp.Name()
		defer os.Remove(name)
		if err := tmp.Chmod(0o600); err != nil {
			tmp.Close()
			return err
		}
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		out, err := osexec.Command("plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", name).CombinedOutput()
		if err != nil {
			return fmt.Errorf("read IPA bundle identifier: %s", strings.TrimSpace(string(out)))
		}
		actual = strings.TrimSpace(string(out))
	}
	if actual == "" {
		return fmt.Errorf("IPA identity verification: CFBundleIdentifier missing")
	}
	if actual != expected {
		return fmt.Errorf("IPA identity mismatch: manifest=%q artifact=%q; refusing App Store upload", expected, actual)
	}
	return nil
}
