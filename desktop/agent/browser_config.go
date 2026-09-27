package main

// browser_config.go loads non-secret project browser targets. Browser profile
// contents stay in owner-only Yaver storage; the committed file only names a
// target, engine, driver, viewport, and local profile alias.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type BrowserTargetConfig struct {
	Engine  string `yaml:"engine" json:"engine"`
	Driver  string `yaml:"driver" json:"driver"`
	Profile string `yaml:"profile,omitempty" json:"profile,omitempty"`
	Headful bool   `yaml:"headful,omitempty" json:"headful,omitempty"`
	Width   int    `yaml:"width,omitempty" json:"width,omitempty"`
	Height  int    `yaml:"height,omitempty" json:"height,omitempty"`
}

type BrowserProjectConfig struct {
	Version int                            `yaml:"version" json:"version"`
	Default string                         `yaml:"default" json:"default"`
	Targets map[string]BrowserTargetConfig `yaml:"targets" json:"targets"`
	Matrix  map[string][]string            `yaml:"matrix,omitempty" json:"matrix,omitempty"`
	Path    string                         `yaml:"-" json:"path,omitempty"`
}

func loadBrowserProjectConfig(workDir string) (*BrowserProjectConfig, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(abs, ".yaver", "browser.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > 256*1024 {
		return nil, fmt.Errorf("browser config exceeds 256KB")
	}
	var cfg BrowserProjectConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.Path = path
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if cfg.Version != 1 {
		return nil, fmt.Errorf("unsupported browser config version %d", cfg.Version)
	}
	for name, target := range cfg.Targets {
		if err := validateBrowserTarget(name, target); err != nil {
			return nil, err
		}
	}
	if cfg.Default != "" {
		if _, ok := cfg.Targets[cfg.Default]; !ok {
			return nil, fmt.Errorf("default browser target %q is not defined", cfg.Default)
		}
	}
	return &cfg, nil
}

func validateBrowserTarget(name string, target BrowserTargetConfig) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("browser target name is empty")
	}
	engine := strings.ToLower(strings.TrimSpace(target.Engine))
	driver := strings.ToLower(strings.TrimSpace(target.Driver))
	if engine != "chrome" && engine != "firefox" && engine != "safari" {
		return fmt.Errorf("browser target %q: engine must be chrome, firefox, or safari", name)
	}
	if driver == "" {
		if engine == "chrome" {
			driver = "cdp"
		} else {
			driver = "webdriver"
		}
	}
	if driver != "cdp" && driver != "webdriver" {
		return fmt.Errorf("browser target %q: driver must be cdp or webdriver", name)
	}
	if engine != "chrome" && driver != "webdriver" {
		return fmt.Errorf("browser target %q: %s requires webdriver", name, engine)
	}
	if target.Width != 0 && (target.Width < 200 || target.Width > 3840) {
		return fmt.Errorf("browser target %q: width must be 200..3840", name)
	}
	if target.Height != 0 && (target.Height < 200 || target.Height > 2160) {
		return fmt.Errorf("browser target %q: height must be 200..2160", name)
	}
	return nil
}

func resolveBrowserTarget(workDir, requested string) (string, BrowserTargetConfig, error) {
	cfg, err := loadBrowserProjectConfig(workDir)
	if err != nil {
		return "", BrowserTargetConfig{}, err
	}
	if cfg == nil {
		if strings.TrimSpace(requested) != "" {
			return "", BrowserTargetConfig{}, fmt.Errorf("browser target %q requested but .yaver/browser.yaml is missing", requested)
		}
		return "default", BrowserTargetConfig{Engine: "chrome", Driver: "cdp"}, nil
	}
	name := strings.TrimSpace(requested)
	if name == "" {
		name = strings.TrimSpace(cfg.Default)
	}
	if name == "" && len(cfg.Targets) == 1 {
		for only := range cfg.Targets {
			name = only
		}
	}
	if name == "" {
		return "", BrowserTargetConfig{}, fmt.Errorf("browser config has no default target")
	}
	target, ok := cfg.Targets[name]
	if !ok {
		return "", BrowserTargetConfig{}, fmt.Errorf("browser target %q is not configured", name)
	}
	target.Engine = strings.ToLower(strings.TrimSpace(target.Engine))
	target.Driver = strings.ToLower(strings.TrimSpace(target.Driver))
	if target.Driver == "" {
		if target.Engine == "chrome" {
			target.Driver = "cdp"
		} else {
			target.Driver = "webdriver"
		}
	}
	return name, target, nil
}
