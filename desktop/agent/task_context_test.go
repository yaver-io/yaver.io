package main

import (
	"strings"
	"testing"
)

func TestYaverDevServerContext(t *testing.T) {
	ctx := yaverDevServerContext("/tmp/test-project")
	if !strings.Contains(ctx, "Dev Server Proxy Rules") {
		t.Fatal("expected dev server proxy rules in context")
	}
	if !strings.Contains(ctx, "/dev/start") {
		t.Fatal("expected /dev/start in context")
	}
	if !strings.Contains(ctx, "NEVER output exp://") {
		t.Fatal("expected exp:// warning in context")
	}
	if !strings.Contains(ctx, "/dev/reload") {
		t.Fatal("expected /dev/reload in context")
	}
}

func TestYaverDevServerContextIncludesProject(t *testing.T) {
	// Use the actual repo dir to test project detection
	ctx := yaverDevServerContext("/Users/kivanccakmak/Workspace/yaver.io/demo/BentoApp")
	if !strings.Contains(ctx, "BentoApp") {
		t.Log("context:", ctx)
		t.Fatal("expected BentoApp in context")
	}
}

func TestYaverWrapperCapabilityContextForTerminal(t *testing.T) {
	ctx := yaverWrapperCapabilityContext("/tmp/test-project", terminalLocalTaskSource)
	if !strings.Contains(ctx, "open the Yaver app") {
		t.Fatal("expected Hermes guidance to mention opening the Yaver app")
	}
	if !strings.Contains(ctx, "iframeUrl") || !strings.Contains(ctx, "webUrl") {
		t.Fatal("expected web preview URL guidance in wrapper context")
	}
	if !strings.Contains(ctx, "http://localhost:18080") {
		t.Fatal("expected localhost agent guidance in wrapper context")
	}
}

func TestYaverWrapperCapabilityContextForRemoteTerminal(t *testing.T) {
	ctx := yaverWrapperCapabilityContext("/tmp/test-project", terminalRemoteTaskSource)
	if !strings.Contains(ctx, "attached remote machine") {
		t.Fatal("expected remote workspace wording in wrapper context")
	}
}
