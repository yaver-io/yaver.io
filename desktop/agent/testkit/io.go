package testkit

import "os"

// writeFileImpl is split out so driver_androidemu.go can call into a
// shared helper without each driver re-importing `os`.
func writeFileImpl(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
