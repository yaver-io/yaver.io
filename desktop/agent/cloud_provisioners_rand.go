package main

import "crypto/rand"

// readFromRand is a thin wrapper so cloud_provisioners.go can read random bytes
// without a second crypto/rand import.
func readFromRand(b []byte) (int, error) { return rand.Read(b) }
