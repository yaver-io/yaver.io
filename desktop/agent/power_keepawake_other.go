//go:build !windows

package main

func startWindowsHeadlessKeepAwake() (func(), error) { return nil, nil }
