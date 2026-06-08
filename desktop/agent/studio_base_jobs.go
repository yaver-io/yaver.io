package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yaver-io/agent/studio"
)

// studio_base_jobs.go — async wrappers around the Yaver Base Image
// (studio/base.go). Build (cold boot + bake + snapshot) and Up (restore + warm
// boot) are minutes-long, so they run as studioJobs the mobile/web UI and an
// agentic LLM poll via studio_job_status. List/GC are fast and exposed
// synchronously by the ops verbs (ops_qa.go).

// studioBaseRequest is the start payload for base build/up (and list/gc, which
// reuse the runner + dir fields).
type studioBaseRequest struct {
	Version     string `json:"version"`     // build: label; up: which (empty → latest)
	Image       string `json:"image"`       // redroid image
	YaverAPK    string `json:"yaverApk"`    // build: APK to bake into the base
	SSHHost     string `json:"sshHost"`     // empty = local runner (managed-cloud farm box)
	SSHOpts     string `json:"sshOpts"`     //
	HostWorkDir string `json:"hostWorkDir"` // /data bind-mount on the surface host
	SnapshotDir string `json:"snapshotDir"` // where snapshots live on that host
	Container   string `json:"container"`   // container name (default yaver-base)
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	DPI         int    `json:"dpi"`
	Keep        int    `json:"keep"` // gc: snapshots to retain
}

// baseSpecFromReq resolves the runner (ssh vs local) and default dirs. For the
// local runner (this box is the farm), dirs default under ~/.yaver; for an ssh
// runner the caller must pass absolute remote paths.
func baseSpecFromReq(req studioBaseRequest, logf func(string)) (*studio.BaseSpec, error) {
	var runner studio.Runner = studio.LocalRunner{}
	if h := strings.TrimSpace(req.SSHHost); h != "" {
		runner = studio.SSHRunner{Host: h, Opts: strings.Fields(req.SSHOpts)}
	}
	hostWork := strings.TrimSpace(req.HostWorkDir)
	snapDir := strings.TrimSpace(req.SnapshotDir)
	if _, local := runner.(studio.LocalRunner); local {
		if home, err := os.UserHomeDir(); err == nil {
			if hostWork == "" {
				hostWork = filepath.Join(home, ".yaver", "base-data")
			}
			if snapDir == "" {
				snapDir = filepath.Join(home, ".yaver", "base")
			}
		}
	}
	if hostWork == "" || snapDir == "" {
		return nil, fmt.Errorf("hostWorkDir and snapshotDir are required for an ssh runner")
	}
	return &studio.BaseSpec{
		R: runner, Image: req.Image, HostWorkDir: hostWork, SnapshotDir: snapDir,
		Version: req.Version, Container: req.Container, YaverAPK: req.YaverAPK,
		Width: req.Width, Height: req.Height, DPI: req.DPI, Log: logf,
	}, nil
}

func (m *studioJobManager) startBaseBuild(req studioBaseRequest) (*studioJob, error) {
	job := m.newJob("base-build", "")
	spec, err := baseSpecFromReq(req, func(l string) { job.log("", l) })
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail(job, fmt.Sprintf("panic: %v", r))
			}
		}()
		job.mu.Lock()
		job.State = studioRunning
		job.mu.Unlock()
		job.log("building", "building base on "+spec.R.Label())
		man, err := spec.Build(context.Background())
		if err != nil {
			m.fail(job, "build: "+err.Error())
			return
		}
		job.mu.Lock()
		job.Dir = spec.SnapshotDir
		job.ShotNames = []string{man.Version}
		job.State = studioCompleted
		job.FinishedAt = time.Now()
		job.mu.Unlock()
		job.log("done", fmt.Sprintf("base %s built (%s, %d bytes, baked=%v)", man.Version, man.Arch, man.Bytes, man.YaverBaked))
	}()
	return job, nil
}

func (m *studioJobManager) startBaseUp(req studioBaseRequest) (*studioJob, error) {
	job := m.newJob("base-up", "")
	spec, err := baseSpecFromReq(req, func(l string) { job.log("", l) })
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail(job, fmt.Sprintf("panic: %v", r))
			}
		}()
		job.mu.Lock()
		job.State = studioRunning
		job.mu.Unlock()
		job.log("restoring", "restoring base on "+spec.R.Label())
		_, man, err := spec.Up(context.Background())
		if err != nil {
			m.fail(job, "up: "+err.Error())
			return
		}
		// The warm container is intentionally LEFT RUNNING for the test run.
		job.mu.Lock()
		job.Dir = spec.HostWorkDir
		job.ShotNames = []string{man.Version}
		job.State = studioCompleted
		job.FinishedAt = time.Now()
		job.mu.Unlock()
		job.log("warm", fmt.Sprintf("base %s warm — container %q ready to drive", man.Version, spec.Container))
	}()
	return job, nil
}
