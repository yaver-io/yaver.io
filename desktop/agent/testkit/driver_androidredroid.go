package testkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yaver-io/agent/studio"
)

// driver_androidredroid.go — backs the `android-redroid` target with the Studio
// redroid surface (Android-in-Docker, no adb/AVD, no KVM). It REUSES testkit's
// selector engine (ParseAndroidSelector + FindAndroidNode) against the surface's
// UIAutomator dump, and the surface's docker-exec verbs for tap/type/screenshot.
// So android-emu and android-redroid share one step vocabulary, one selector
// policy — two backends. This is Decision A in docs/yaver-ai-app-test-agent.md
// §15: redroid is a first-class testkit Target, not a separate stack.
//
// When redroid.base is set, Boot restores a warm Yaver Base Image (studio P0)
// and leaves it running — the fast path that skips cold boot + Yaver install.

type redroidAndroidDriver struct {
	surface     *studio.RedroidSurface
	drv         studio.Driver
	app         studio.App
	apkPath     string
	base        string // optional Yaver Base Image version to restore
	snapshotDir string
	keepBase    bool // restored from a warm base → don't tear down on Shutdown
}

var _ androidDriver = (*redroidAndroidDriver)(nil)

func newRedroidAndroidDriver(spec *Spec) (*redroidAndroidDriver, error) {
	cfg := spec.Redroid
	if cfg == nil {
		cfg = &RedroidSpec{}
	}

	var runner studio.Runner = studio.LocalRunner{}
	if h := strings.TrimSpace(cfg.SSHHost); h != "" {
		runner = studio.SSHRunner{Host: h, Opts: strings.Fields(cfg.SSHOpts)}
	}
	_, local := runner.(studio.LocalRunner)

	hostWork := strings.TrimSpace(cfg.HostWorkDir)
	snapDir := strings.TrimSpace(cfg.SnapshotDir)
	if local {
		if home, err := os.UserHomeDir(); err == nil {
			if hostWork == "" {
				hostWork = filepath.Join(home, ".yaver", "qa-data")
			}
			if snapDir == "" {
				snapDir = filepath.Join(home, ".yaver", "base")
			}
		}
	}
	if hostWork == "" {
		return nil, fmt.Errorf("android-redroid: redroid.host_workdir is required for an ssh_host runner")
	}

	container := strings.TrimSpace(cfg.Container)
	if container == "" {
		container = "yaver-qa"
	}
	pkg := strings.TrimSpace(cfg.Package)
	if pkg == "" {
		pkg = strings.TrimSpace(spec.URL) // mirror android-emu: the url slot carries the package
	}

	return &redroidAndroidDriver{
		surface: &studio.RedroidSurface{
			R: runner, Name: container, Image: cfg.Image, HostWorkDir: hostWork,
		},
		app:         studio.App{Package: pkg, Activity: strings.TrimSpace(cfg.Activity)},
		apkPath:     spec.App,
		base:        strings.TrimSpace(cfg.Base),
		snapshotDir: snapDir,
	}, nil
}

// Available is a no-op: redroid runs through Docker on the surface host, and the
// surface's Provision validates binder/kernel. Nothing host-local (adb/emulator)
// is required, which is the whole point versus android-emu.
func (r *redroidAndroidDriver) Available() error { return nil }

func (r *redroidAndroidDriver) Boot(ctx context.Context) (string, error) {
	if r.base != "" {
		bs := &studio.BaseSpec{
			R: r.surface.R, Image: r.surface.Image, HostWorkDir: r.surface.HostWorkDir,
			SnapshotDir: r.snapshotDir, Version: r.base, Container: r.surface.Name,
		}
		surf, _, err := bs.Up(ctx)
		if err != nil {
			return "", fmt.Errorf("restore base %q: %w", r.base, err)
		}
		r.surface = surf
		r.keepBase = true
	} else if err := r.surface.EnsureReady(ctx); err != nil {
		return "", err
	}
	r.drv = r.surface.Driver()
	return r.surface.Name, nil
}

func (r *redroidAndroidDriver) Install(ctx context.Context, _ string) error {
	if strings.TrimSpace(r.apkPath) == "" {
		return fmt.Errorf("install: no apk (set spec.app)")
	}
	return r.surface.Install(ctx, r.apkPath)
}

// Shutdown tears the surface down — UNLESS it was restored from a warm base, in
// which case it's left running for the next run to reuse.
func (r *redroidAndroidDriver) Shutdown(ctx context.Context, _ string) error {
	if r.keepBase {
		return nil
	}
	return r.surface.Teardown(ctx)
}

func (r *redroidAndroidDriver) SetPackage(pkg string) { r.app.Package = pkg }

func (r *redroidAndroidDriver) Launch(ctx context.Context, _ string) error {
	if strings.TrimSpace(r.app.Package) == "" {
		return fmt.Errorf("launch: no package (set redroid.package or url)")
	}
	return r.driver().Launch(ctx, r.app)
}

func (r *redroidAndroidDriver) DumpAndroidUI(ctx context.Context, _ string) ([]byte, error) {
	dumper, ok := r.driver().(studio.Dumper)
	if !ok {
		return nil, fmt.Errorf("redroid surface does not support UI dumps")
	}
	xml, err := dumper.ViewTree(ctx)
	if err != nil {
		return nil, err
	}
	return []byte(xml), nil
}

func (r *redroidAndroidDriver) TapBySelector(ctx context.Context, deviceID, selector string) error {
	xmlBytes, err := r.DumpAndroidUI(ctx, deviceID)
	if err != nil {
		return err
	}
	x, y, err := FindAndroidNode(xmlBytes, ParseAndroidSelector(selector))
	if err != nil {
		return err
	}
	return r.driver().Tap(ctx, x, y)
}

func (r *redroidAndroidDriver) FillBySelector(ctx context.Context, deviceID, selector, text string) error {
	if err := r.TapBySelector(ctx, deviceID, selector); err != nil {
		return err
	}
	return r.driver().Type(ctx, text)
}

func (r *redroidAndroidDriver) AssertVisibleBySelector(ctx context.Context, deviceID, selector string) error {
	xmlBytes, err := r.DumpAndroidUI(ctx, deviceID)
	if err != nil {
		return err
	}
	if _, err := FindAndroidNodeDetails(xmlBytes, ParseAndroidSelector(selector)); err != nil {
		return err
	}
	return nil
}

func (r *redroidAndroidDriver) Screenshot(ctx context.Context, _ string, outPath string) error {
	png, err := r.driver().Screenshot(ctx)
	if err != nil {
		return err
	}
	return writeFile(outPath, png)
}

func (r *redroidAndroidDriver) driver() studio.Driver {
	if r.drv == nil {
		r.drv = r.surface.Driver()
	}
	return r.drv
}
