package testkit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeStudioRunner implements studio.Runner with no Docker, so the redroid-backed
// testkit adapter can be exercised end-to-end (boot → dump → selector → tap).
type fakeStudioRunner struct {
	cmds []string
	resp func(cmd string) string
}

func (f *fakeStudioRunner) Label() string { return "fake-studio" }

func (f *fakeStudioRunner) Exec(ctx context.Context, cmd string) ([]byte, error) {
	f.cmds = append(f.cmds, cmd)
	if f.resp != nil {
		return []byte(f.resp(cmd)), nil
	}
	return nil, nil
}

func (f *fakeStudioRunner) PutFile(ctx context.Context, local, remote string) error { return nil }

func (f *fakeStudioRunner) GetFile(ctx context.Context, remote, local string) error {
	return os.WriteFile(local, []byte("\x89PNG-canned"), 0o644)
}

func (f *fakeStudioRunner) saw(substr string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

const redroidUIXML = `<?xml version='1.0' encoding='UTF-8'?>` +
	`<hierarchy rotation="0">` +
	`<node text="" resource-id="" class="android.widget.FrameLayout" bounds="[0,0][1080,2340]">` +
	`<node text="Continue with Email" resource-id="io.yaver.mobile:id/email_btn" content-desc="emailBtn" class="android.widget.Button" bounds="[100,200][300,260]"/>` +
	`</node></hierarchy>`

func newWarmRedroidDriver(t *testing.T) (*redroidAndroidDriver, *fakeStudioRunner) {
	t.Helper()
	spec := &Spec{
		Target:  TargetAndroidRedroid,
		URL:     "io.yaver.mobile",
		Redroid: &RedroidSpec{HostWorkDir: "/tmp/qa-data", Container: "yaver-qa"},
	}
	drv, err := newRedroidAndroidDriver(spec)
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	fake := &fakeStudioRunner{resp: func(cmd string) string {
		switch {
		case strings.Contains(cmd, "getprop sys.boot_completed"):
			return "1" // already warm → EnsureReady skips Provision
		case strings.Contains(cmd, "uiautomator dump"):
			return redroidUIXML
		}
		return ""
	}}
	drv.surface.R = fake
	return drv, fake
}

func TestRedroidDriverDefaults(t *testing.T) {
	spec := &Spec{Target: TargetAndroidRedroid, URL: "io.yaver.mobile", Redroid: &RedroidSpec{HostWorkDir: "/tmp/x"}}
	drv, err := newRedroidAndroidDriver(spec)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if drv.surface.Name != "yaver-qa" {
		t.Errorf("container default = %q", drv.surface.Name)
	}
	if drv.app.Package != "io.yaver.mobile" {
		t.Errorf("package from url = %q", drv.app.Package)
	}
}

func TestRedroidWarmBootSkipsProvision(t *testing.T) {
	drv, fake := newWarmRedroidDriver(t)
	id, err := drv.Boot(context.Background())
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if id != "yaver-qa" {
		t.Errorf("deviceID = %q", id)
	}
	if fake.saw("docker run -itd --privileged") {
		t.Errorf("warm boot must NOT cold-provision; cmds=%v", fake.cmds)
	}
}

func TestRedroidTapBySelectorResolvesAndTaps(t *testing.T) {
	drv, fake := newWarmRedroidDriver(t)
	ctx := context.Background()
	if _, err := drv.Boot(ctx); err != nil {
		t.Fatalf("boot: %v", err)
	}
	// by visible text → center of [100,200][300,260] = (200,230)
	if err := drv.TapBySelector(ctx, "", "text=Continue with Email"); err != nil {
		t.Fatalf("tap by text: %v", err)
	}
	if !fake.saw("input tap 200 230") {
		t.Errorf("did not tap node center; cmds=%v", fake.cmds)
	}
	// by resource-id suffix
	if err := drv.TapBySelector(ctx, "", "id=email_btn"); err != nil {
		t.Fatalf("tap by id: %v", err)
	}
}

func TestRedroidAssertVisible(t *testing.T) {
	drv, _ := newWarmRedroidDriver(t)
	ctx := context.Background()
	_, _ = drv.Boot(ctx)
	if err := drv.AssertVisibleBySelector(ctx, "", "text=Continue with Email"); err != nil {
		t.Errorf("present selector should pass: %v", err)
	}
	if err := drv.AssertVisibleBySelector(ctx, "", "text=Nonexistent"); err == nil {
		t.Error("absent selector should fail")
	}
}

func TestRedroidFillAndScreenshot(t *testing.T) {
	drv, fake := newWarmRedroidDriver(t)
	ctx := context.Background()
	_, _ = drv.Boot(ctx)

	if err := drv.FillBySelector(ctx, "", "id=email_btn", "hi@example.com"); err != nil {
		t.Fatalf("fill: %v", err)
	}
	if !fake.saw("input tap 200 230") || !fake.saw("input text") {
		t.Errorf("fill should tap-then-type; cmds=%v", fake.cmds)
	}

	out := filepath.Join(t.TempDir(), "shot.png")
	if err := drv.Screenshot(ctx, "", out); err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if b, _ := os.ReadFile(out); len(b) == 0 {
		t.Error("screenshot file empty")
	}
	if !fake.saw("screencap -p") {
		t.Errorf("no screencap; cmds=%v", fake.cmds)
	}
}

func TestRedroidSpecValidates(t *testing.T) {
	spec := &Spec{
		Name:    "redroid smoke",
		Target:  TargetAndroidRedroid,
		URL:     "io.yaver.mobile",
		Steps:   []Step{{Goto: "/"}},
		Redroid: &RedroidSpec{HostWorkDir: "/tmp/x"},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("android-redroid spec should validate: %v", err)
	}
}
