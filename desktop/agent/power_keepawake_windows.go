//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
)

const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// Windows execution state is thread-scoped. Pin one goroutine to one OS thread
// for the entire agent lifetime; clearing it from a different goroutine would
// leave the original assertion behind or make the protection intermittent.
func startWindowsHeadlessKeepAwake() (func(), error) {
	setThreadExecutionState := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	ready := make(chan error, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		result, _, callErr := setThreadExecutionState.Call(esContinuous | esSystemRequired)
		if result == 0 {
			ready <- fmt.Errorf("SetThreadExecutionState failed: %v", callErr)
			return
		}
		ready <- nil
		<-stop
		setThreadExecutionState.Call(esContinuous)
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { close(stop); <-done }) }, nil
}
