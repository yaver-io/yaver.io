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
	ctx := yaverDevServerContext("/Users/kivanccakmak/Workspace/yaver.io/demo/AcmeStore")
	if !strings.Contains(ctx, "AcmeStore") {
		t.Log("context:", ctx)
		t.Fatal("expected AcmeStore in context")
	}
}
