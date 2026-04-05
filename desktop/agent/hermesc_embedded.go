package main

import (
	_ "embed"
	"fmt"
	"os"
	"runtime"
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
	hermescPathOnce sync.Once
)

// GetEmbeddedHermesc extracts the embedded hermesc binary to a temp file
// and returns its path. Extraction happens only once per process lifetime.
func GetEmbeddedHermesc() (string, error) {
	var err error
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
			err = fmt.Errorf("unsupported platform for embedded hermesc: %s/%s", runtime.GOOS, runtime.GOARCH)
			return
		}

		tmp, tmpErr := os.CreateTemp("", "yaver-hermesc-*")
		if tmpErr != nil {
			err = tmpErr
			return
		}

		if _, writeErr := tmp.Write(binary); writeErr != nil {
			err = writeErr
			return
		}

		if chmodErr := tmp.Chmod(0755); chmodErr != nil {
			err = chmodErr
			return
		}

		tmp.Close()
		hermescPath = tmp.Name()
	})
	return hermescPath, err
}
