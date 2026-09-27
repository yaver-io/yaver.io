package main

import (
	"os"
	"strings"
	"testing"
)

// Windows 11 24H2 makes WMIC a disabled-by-default optional feature. Keep the
// Store/clean-machine path on built-in PowerShell CIM so remote-seat discovery
// and resource telemetry do not turn green only on upgraded developer PCs.
func TestWindowsProcessAndResourceProbesDoNotDependOnWMIC(t *testing.T) {
	source, err := os.ReadFile("process_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if strings.Contains(strings.ToLower(text), `command("wmic"`) {
		t.Fatal("native Windows probes must not depend on the optional/deprecated wmic.exe")
	}
	for _, required := range []string{"Get-CimInstance Win32_Process", "Get-CimInstance Win32_OperatingSystem", "Get-CimInstance Win32_Processor"} {
		if !strings.Contains(text, required) {
			t.Fatalf("native Windows probe is missing %q", required)
		}
	}
}
