package studio

import (
	"context"
	"os"
	"strings"
	"testing"
)

// fakeRunner records commands and returns canned outputs, so the redroid driver
// can be exercised with no Docker / device (the desktop/agent no-real-deps test
// convention — a real in-proc implementation, not a mock framework).
type fakeRunner struct {
	cmds    []string
	puts    []string
	gets    []string
	respond func(cmd string) string
	getData []byte
}

func (f *fakeRunner) Label() string { return "fake" }

func (f *fakeRunner) Exec(ctx context.Context, cmd string) ([]byte, error) {
	f.cmds = append(f.cmds, cmd)
	if f.respond != nil {
		return []byte(f.respond(cmd)), nil
	}
	return nil, nil
}

func (f *fakeRunner) PutFile(ctx context.Context, local, remote string) error {
	f.puts = append(f.puts, local+" -> "+remote)
	return nil
}

func (f *fakeRunner) GetFile(ctx context.Context, remote, local string) error {
	f.gets = append(f.gets, remote+" -> "+local)
	data := f.getData
	if data == nil {
		data = []byte("CANNED")
	}
	return os.WriteFile(local, data, 0o644)
}

func (f *fakeRunner) saw(substr string) bool {
	for _, c := range f.cmds {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func newSurface(r Runner) *RedroidSurface {
	return &RedroidSurface{R: r, HostWorkDir: "/tmp/studio-host"}
}

func TestProvisionSequence(t *testing.T) {
	f := &fakeRunner{respond: func(cmd string) string {
		switch {
		case strings.Contains(cmd, "lsmod"):
			return "1" // binder already loaded → skip helper
		case strings.Contains(cmd, "getprop sys.boot_completed"):
			return "1"
		}
		return ""
	}}
	s := newSurface(f)
	if err := s.Provision(context.Background()); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !f.saw("docker run -itd --privileged --name yaver-studio-redroid") {
		t.Error("did not issue redroid run command")
	}
	if !f.saw("-v '/tmp/studio-host':/data") {
		t.Errorf("bind mount missing; cmds=%v", f.cmds)
	}
	if !f.saw("getprop sys.boot_completed") {
		t.Error("did not poll boot")
	}
}

func TestProvisionLoadsBinderWhenAbsent(t *testing.T) {
	f := &fakeRunner{respond: func(cmd string) string {
		if strings.Contains(cmd, "lsmod") {
			return "0" // not loaded → must load via helper
		}
		if strings.Contains(cmd, "getprop sys.boot_completed") {
			return "1"
		}
		return ""
	}}
	s := newSurface(f)
	if err := s.Provision(context.Background()); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if !f.saw("modprobe binder_linux") || !f.saw("--privileged -v /lib/modules:/lib/modules") {
		t.Errorf("did not load binder via privileged helper; cmds=%v", f.cmds)
	}
}

func TestInstall(t *testing.T) {
	f := &fakeRunner{respond: func(cmd string) string {
		if strings.Contains(cmd, "pm install") {
			return "Success"
		}
		return ""
	}}
	s := newSurface(f)
	if err := s.Install(context.Background(), "/local/app.apk"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(f.puts) != 1 {
		t.Errorf("expected 1 PutFile, got %v", f.puts)
	}
	if !f.saw("docker cp") || !f.saw("pm install -r -g") {
		t.Errorf("install commands missing; cmds=%v", f.cmds)
	}
}

func TestDriverVerbs(t *testing.T) {
	f := &fakeRunner{}
	d := newSurface(f).Driver()
	ctx := context.Background()
	app := App{Package: "io.yaver.mobile", Activity: ".MainActivity"}
	_ = d.Launch(ctx, app)
	_ = d.StartForegroundService(ctx, "io.yaver.mobile/.sandbox.SandboxService", "io.yaver.mobile.sandbox.START")
	_ = d.ExpandNotifications(ctx)
	_ = d.Home(ctx)
	_ = d.StopService(ctx, "io.yaver.mobile/.sandbox.SandboxService")
	for _, want := range []string{
		"am start -n io.yaver.mobile/.MainActivity",
		"am start-foreground-service -n io.yaver.mobile/.sandbox.SandboxService -a io.yaver.mobile.sandbox.START",
		"cmd statusbar expand-notifications",
		"input keyevent KEYCODE_HOME",
		"am stopservice -n io.yaver.mobile/.sandbox.SandboxService",
	} {
		if !f.saw(want) {
			t.Errorf("missing command %q\ncmds=%v", want, f.cmds)
		}
	}
}

func TestTapText(t *testing.T) {
	xml := `<hierarchy><node text="Sign in" bounds="[0,0][10,10]"/>` +
		`<node text="Continue with Email" bounds="[100,200][300,260]"/></hierarchy>`
	f := &fakeRunner{respond: func(cmd string) string {
		if strings.Contains(cmd, "uiautomator dump") {
			return xml
		}
		return ""
	}}
	d := newSurface(f).Driver()
	if err := d.TapText(context.Background(), "Continue with Email"); err != nil {
		t.Fatalf("taptext: %v", err)
	}
	if !f.saw("input tap 200 230") { // center of [100,200][300,260]
		t.Errorf("expected tap at center; cmds=%v", f.cmds)
	}
}

func TestScreenshotAndRecord(t *testing.T) {
	f := &fakeRunner{getData: []byte("\x89PNGDATA")}
	d := newSurface(f).Driver()
	ctx := context.Background()
	png, err := d.Screenshot(ctx)
	if err != nil || string(png) != "\x89PNGDATA" {
		t.Fatalf("screenshot: %v %q", err, png)
	}
	if !f.saw("screencap -p") {
		t.Error("no screencap")
	}
	if err := d.RecordStart(ctx, 30); err != nil {
		t.Fatalf("record start: %v", err)
	}
	if !f.saw("docker exec -d yaver-studio-redroid screenrecord") {
		t.Errorf("record not detached; cmds=%v", f.cmds)
	}
	mp4, err := d.RecordStop(ctx)
	if err != nil || string(mp4) != "\x89PNGDATA" {
		t.Fatalf("record stop: %v", err)
	}
	if !f.saw("pkill -INT screenrecord") {
		t.Error("record stop did not SIGINT")
	}
}

func TestPermissionProofSteps(t *testing.T) {
	facts, _ := analyzeAndroidManifestReader(strings.NewReader(specialUseManifest), "FOREGROUND_SERVICE_SPECIAL_USE")
	spec := PermissionVideoSpec{
		App:         App{Package: "io.example", Activity: ".MainActivity"},
		Facts:       facts,
		StartAction: "io.example.sandbox.START",
	}
	steps := PermissionProofSteps(spec)
	if len(steps) != 6 { // open + start + notif + bg + still + stop
		t.Fatalf("expected 6 steps, got %d", len(steps))
	}
	if steps[0].Caption != "1. Open the app" {
		t.Errorf("first step caption = %q", steps[0].Caption)
	}
}

func TestRunFlowRecordingCues(t *testing.T) {
	f := &fakeRunner{getData: []byte("MP4")}
	surface := newSurface(f)
	steps := []Step{
		{Caption: "one", Run: func(ctx context.Context, d Driver) error { return nil }, HoldSec: 0},
		{Caption: "two", Run: func(ctx context.Context, d Driver) error { return nil }, HoldSec: 0},
	}
	mp4, cues, err := RunFlowRecording(context.Background(), surface, steps, 20)
	if err != nil {
		t.Fatalf("run flow: %v", err)
	}
	if string(mp4) != "MP4" {
		t.Errorf("mp4 bytes = %q", mp4)
	}
	if len(cues) != 2 || cues[0].Text != "one" || cues[1].Text != "two" {
		t.Errorf("cues = %+v", cues)
	}
}
