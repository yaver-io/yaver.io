package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

//go:embed hermesc/mac-arm64/hermesc
var hermescARM64 []byte

//go:embed hermesc/mac-x64/hermesc
var hermescX64 []byte

//go:embed hermesc/linux-x64/hermesc
var hermescLinuxX64 []byte

var (
	hermescPath     string
	hermescPathErr  error
	hermescPathOnce sync.Once
)

// GetEmbeddedHermesc extracts the embedded hermesc binary to a temp file
// and returns its path. Extraction happens only once per process lifetime.
func GetEmbeddedHermesc() (string, error) {
	hermescPathOnce.Do(func() {
		var binary []byte
		switch runtime.GOOS + "/" + runtime.GOARCH {
		case "darwin/arm64":
			binary = hermescARM64
		case "darwin/amd64":
			binary = hermescX64
		case "linux/amd64":
			binary = hermescLinuxX64
		default:
			hermescPathErr = fmt.Errorf("unsupported platform for embedded hermesc: %s/%s", runtime.GOOS, runtime.GOARCH)
			return
		}

		tmp, tmpErr := os.CreateTemp("", "yaver-hermesc-*")
		if tmpErr != nil {
			hermescPathErr = tmpErr
			return
		}

		if _, writeErr := tmp.Write(binary); writeErr != nil {
			hermescPathErr = writeErr
			return
		}

		if chmodErr := tmp.Chmod(0755); chmodErr != nil {
			hermescPathErr = chmodErr
			return
		}

		tmp.Close()
		hermescPath = tmp.Name()
	})
	return hermescPath, hermescPathErr
}

func embeddedHermescSummary() (string, error) {
	hermescBin, err := GetEmbeddedHermesc()
	if err != nil {
		return "", fmt.Errorf("not available: %w", err)
	}
	out, err := exec.Command(hermescBin, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("present but --version failed: %v", err)
	}
	lines := strings.Split(string(out), "\n")
	bcLine := ""
	rnLine := ""
	for _, l := range lines {
		if strings.Contains(l, "HBC bytecode version") {
			bcLine = strings.TrimSpace(l)
		}
		if strings.Contains(l, "Hermes release version") {
			rnLine = strings.TrimSpace(l)
		}
	}
	if bcLine == "" {
		return hermescBin, nil
	}
	if rnLine == "" {
		return bcLine, nil
	}
	return fmt.Sprintf("%s (%s)", bcLine, rnLine), nil
}
