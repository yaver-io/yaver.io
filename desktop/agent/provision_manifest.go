package main

// provision_manifest.go — `yaver.provision.yaml`, the builder's declarative
// description of a provisioned-hardware product. This is the piece that
// makes zero-touch "super easy for Talos-alike third parties": a builder
// drops one file in their repo / image, and from it Yaver derives the
// product registration (what the buyer sees when scanning the QR) AND the
// post-claim workload to bring up automatically once the box is owned.
//
// It deliberately reuses the existing companion-compute engine for the
// actual long-running services (yaver.companion.yaml) rather than inventing
// a second service runtime — `setup` here is the one-time bring-up that runs
// after the box self-credentials (typically `yaver companion up`, package
// installs, or a unit enable). See provision_postclaim.go for execution and
// provisioning.ts / provision.go for the claim+attest half.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProvisionManifestName is the canonical filename, mirroring
// CompanionManifestName / yaver.workspace.yaml conventions.
const ProvisionManifestName = "yaver.provision.yaml"

// ProvisionManifest is the on-disk schema.
type ProvisionManifest struct {
	Version int    `yaml:"version"`
	// Product is the SKU slug registered in Convex (deviceProducts.productId).
	Product string `yaml:"product"`
	// Model + Vendor are display strings shown in the claim UI.
	Model  string `yaml:"model"`
	Vendor string `yaml:"vendor"`
	// Platform default for minted devices (linux for a Pi image, etc.).
	Platform string `yaml:"platform"`
	// Services is a display-only summary of what the box runs, surfaced in
	// the claim UI (deviceProducts.defaultServices). The real execution is
	// the companion manifest + the setup steps below.
	Services []string `yaml:"services"`
	// Setup is the one-time bring-up run AFTER the box is claimed and
	// authenticated (see provision_postclaim.go). Each step is a shell
	// command executed in order; a non-zero exit stops the sequence so a
	// half-provisioned box doesn't silently look healthy.
	Setup []ProvisionSetupStep `yaml:"setup"`

	dir string `yaml:"-"`
}

// ProvisionSetupStep is one post-claim command.
type ProvisionSetupStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
	// AllowFailure lets a best-effort step (e.g. an optional package) not
	// abort the rest of the sequence.
	AllowFailure bool `yaml:"allow_failure"`
}

// provisionManifestSearchDirs are where a flashed image may carry the
// manifest, in priority order. The repo/workdir copy wins for `yaver
// provision mint` run by a builder; the /etc copy is the conventional
// baked-image location read on the device post-claim.
func provisionManifestSearchDirs(workDir string) []string {
	dirs := []string{}
	if explicit := strings.TrimSpace(os.Getenv("YAVER_PROVISION_MANIFEST_DIR")); explicit != "" {
		dirs = append(dirs, explicit)
	}
	if strings.TrimSpace(workDir) != "" {
		dirs = append(dirs, workDir)
	}
	if cfgDir, err := ConfigDir(); err == nil {
		dirs = append(dirs, cfgDir)
	}
	dirs = append(dirs, "/etc/yaver", "/boot/firmware", "/boot")
	return dirs
}

// LoadProvisionManifest reads yaver.provision.yaml from a specific dir.
// Returns (nil, nil) when the file is absent.
func LoadProvisionManifest(dir string) (*ProvisionManifest, error) {
	path := filepath.Join(dir, ProvisionManifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m ProvisionManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if strings.TrimSpace(m.Product) == "" {
		return nil, fmt.Errorf("%s: `product` is required", path)
	}
	m.dir = dir
	return &m, nil
}

// FindProvisionManifest walks the search dirs and returns the first
// manifest found (or nil, nil if none).
func FindProvisionManifest(workDir string) (*ProvisionManifest, error) {
	for _, dir := range provisionManifestSearchDirs(workDir) {
		m, err := LoadProvisionManifest(dir)
		if err != nil {
			return nil, err
		}
		if m != nil {
			return m, nil
		}
	}
	return nil, nil
}
