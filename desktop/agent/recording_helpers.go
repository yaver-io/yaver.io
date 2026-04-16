package main

import "os"

// Small fs helpers shared by the recording drivers. Kept in their own
// file so the platform-specific drivers don't redeclare them.

func removeFileIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
