package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type redroidResourceProbe struct {
	OS                  string
	Arch                string
	DockerPresent       bool
	DockerReachable     bool
	DockerDetail        string
	RedroidImagePresent bool
	DefaultWorkDir      string
}

type redroidResourceStatus struct {
	Kind                string   `json:"kind"`
	Dedicated           bool     `json:"dedicated"`
	Ready               bool     `json:"ready"`
	CanHostHere         bool     `json:"canHostHere"`
	OS                  string   `json:"os"`
	Arch                string   `json:"arch"`
	DockerPresent       bool     `json:"dockerPresent"`
	DockerReachable     bool     `json:"dockerReachable"`
	DockerDetail        string   `json:"dockerDetail,omitempty"`
	RedroidImage        string   `json:"redroidImage"`
	RedroidImagePresent bool     `json:"redroidImagePresent"`
	DefaultWorkDir      string   `json:"defaultWorkDir"`
	State               string   `json:"state"`
	Summary             string   `json:"summary"`
	NextActions         []string `json:"nextActions"`
	Notes               []string `json:"notes"`
}

func init() {
	registerOpsVerb(opsVerbSpec{
		Name:        "redroid_resource_status",
		Description: "Check whether this machine can host a private dedicated Android clone resource for the signed-in user. Read-only; does not start Docker or install apps.",
		Schema: map[string]interface{}{
			"type":                 "object",
			"properties":           map[string]interface{}{},
			"additionalProperties": false,
		},
		Handler:    opsRedroidResourceStatusHandler,
		AllowGuest: false,
	})
}

func opsRedroidResourceStatusHandler(c OpsContext, _ json.RawMessage) OpsResult {
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return OpsResult{OK: true, Initial: buildRedroidResourceStatus(probeRedroidResource(ctx))}
}

func probeRedroidResource(ctx context.Context) redroidResourceProbe {
	p := redroidResourceProbe{
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		DefaultWorkDir: defaultRedroidWorkDir(),
	}
	if _, err := exec.LookPath("docker"); err == nil {
		p.DockerPresent = true
	} else {
		p.DockerDetail = "docker command not found"
		return p
	}

	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(dctx, "docker", "info", "--format", "{{.ServerVersion}} {{.OSType}} {{.Architecture}}").CombinedOutput()
	p.DockerDetail = strings.TrimSpace(string(out))
	if err != nil {
		if p.DockerDetail == "" {
			p.DockerDetail = err.Error()
		}
		return p
	}
	p.DockerReachable = true

	ictx, cancelImage := context.WithTimeout(ctx, 5*time.Second)
	defer cancelImage()
	img, _ := exec.CommandContext(ictx, "docker", "images", "-q", defaultRedroidImage).CombinedOutput()
	p.RedroidImagePresent = strings.TrimSpace(string(img)) != ""
	return p
}

func buildRedroidResourceStatus(p redroidResourceProbe) redroidResourceStatus {
	st := redroidResourceStatus{
		Kind:                "android-redroid",
		Dedicated:           true,
		OS:                  p.OS,
		Arch:                p.Arch,
		DockerPresent:       p.DockerPresent,
		DockerReachable:     p.DockerReachable,
		DockerDetail:        p.DockerDetail,
		RedroidImage:        defaultRedroidImage,
		RedroidImagePresent: p.RedroidImagePresent,
		DefaultWorkDir:      p.DefaultWorkDir,
		Notes: []string{
			"One redroid resource is treated as one user's private Android clone; it is not a shared multi-tenant phone.",
			"Redroid is for automation and app QA. Real-phone home-hosting still needs physical-device proof.",
		},
	}

	switch {
	case p.OS != "linux":
		st.State = "unsupported_host"
		st.Summary = "Redroid needs a Linux host with Android binder support. Use a managed Yaver Linux resource or a Linux box for this Android clone."
		st.NextActions = []string{
			"Use managed cloud or a Linux host for the dedicated Android clone.",
			"Use a physical Android phone for home-hosting and real-device checks.",
		}
	case !p.DockerPresent:
		st.State = "docker_missing"
		st.Summary = "Docker is not installed on this Linux host."
		st.NextActions = []string{
			"Install Docker on the Linux host.",
			"Run testkit_deps_check after Docker is installed.",
		}
	case !p.DockerReachable:
		st.State = "docker_unreachable"
		st.Summary = "Docker is installed but the daemon is not reachable."
		st.NextActions = []string{
			"Start Docker and make sure this user can reach the Docker daemon.",
			"Run docker info, then redroid_resource_status again.",
		}
	case !p.RedroidImagePresent:
		st.State = "image_missing"
		st.Summary = "Docker is reachable, but the redroid image is not present yet."
		st.NextActions = []string{
			"Run testkit_deps_install with redroid included, or pull " + defaultRedroidImage + ".",
			"After the image is present, run qa_base_build or android_app_status to boot the private Android resource.",
		}
	default:
		st.State = "ready"
		st.Ready = true
		st.CanHostHere = true
		st.Summary = "This Linux host is ready to host a private dedicated redroid Android resource."
		st.NextActions = []string{
			"Run qa_base_build to create a warm private Android base.",
			"Run android_app_install or qa_run against this resource.",
		}
	}

	return st
}
