package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	YaverNativeAppManifestSchemaVersion = 1
	YaverNativeOAuthProvider            = "yaver-oauth"
)

var YaverNativeAppManifestNames = []string{
	"yaver.app.yaml",
	"yaver.game.yaml",
	"yaver.app.yml",
	"yaver.game.yml",
	"yaver.app.json",
	"yaver.game.json",
}

type YaverNativeAppManifest struct {
	SchemaVersion int                        `yaml:"schemaVersion" json:"schemaVersion"`
	Kind          string                     `yaml:"kind,omitempty" json:"kind,omitempty"`
	ID            string                     `yaml:"id" json:"id"`
	Slug          string                     `yaml:"slug" json:"slug"`
	Title         string                     `yaml:"title" json:"title"`
	Owner         string                     `yaml:"owner,omitempty" json:"owner,omitempty"`
	Runtime       YaverNativeManifestRuntime `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Auth          YaverNativeManifestAuth    `yaml:"auth,omitempty" json:"auth,omitempty"`
	Billing       map[string]interface{}     `yaml:"billing,omitempty" json:"billing,omitempty"`
	Surfaces      []string                   `yaml:"surfaces,omitempty" json:"surfaces,omitempty"`
	Native        YaverNativeManifestNative  `yaml:"native,omitempty" json:"native,omitempty"`
	Source        map[string]interface{}     `yaml:"source,omitempty" json:"source,omitempty"`
	PublishPolicy map[string]interface{}     `yaml:"publishPolicy,omitempty" json:"publishPolicy,omitempty"`
	Meta          map[string]interface{}     `yaml:"meta,omitempty" json:"meta,omitempty"`

	path string `yaml:"-" json:"-"`
}

type YaverNativeManifestRuntime struct {
	Kind                string `yaml:"kind,omitempty" json:"kind,omitempty"`
	PlatformPositioning string `yaml:"platformPositioning,omitempty" json:"platformPositioning,omitempty"`
	EventLogRequired    bool   `yaml:"eventLogRequired,omitempty" json:"eventLogRequired,omitempty"`
}

type YaverNativeManifestAuth struct {
	Provider                          string   `yaml:"provider,omitempty" json:"provider,omitempty"`
	RequiredInYaverBuild              bool     `yaml:"requiredInYaverBuild,omitempty" json:"requiredInYaverBuild,omitempty"`
	StandaloneAuthAllowedOutsideYaver bool     `yaml:"standaloneAuthAllowedOutsideYaver,omitempty" json:"standaloneAuthAllowedOutsideYaver,omitempty"`
	RequiredScopes                    []string `yaml:"requiredScopes,omitempty" json:"requiredScopes,omitempty"`
}

type YaverNativeManifestNative struct {
	BundleMode string                             `yaml:"bundleMode,omitempty" json:"bundleMode,omitempty"`
	Apple      YaverNativeManifestApple           `yaml:"apple,omitempty" json:"apple,omitempty"`
	Android    YaverNativeManifestAndroid         `yaml:"android,omitempty" json:"android,omitempty"`
	Host       YaverNativeManifestHostRequirement `yaml:"host,omitempty" json:"host,omitempty"`
}

type YaverNativeManifestApple struct {
	InfoPlist YaverNativeManifestInfoPlist `yaml:"infoPlist,omitempty" json:"infoPlist,omitempty"`
}

type YaverNativeManifestInfoPlist struct {
	RequiredKeys              []string          `yaml:"requiredKeys,omitempty" json:"requiredKeys,omitempty"`
	UsageDescriptions         map[string]string `yaml:"usageDescriptions,omitempty" json:"usageDescriptions,omitempty"`
	URLSchemes                []string          `yaml:"urlSchemes,omitempty" json:"urlSchemes,omitempty"`
	BonjourServices           []string          `yaml:"bonjourServices,omitempty" json:"bonjourServices,omitempty"`
	ApplicationQueriesSchemes []string          `yaml:"applicationQueriesSchemes,omitempty" json:"applicationQueriesSchemes,omitempty"`
	BackgroundModes           []string          `yaml:"backgroundModes,omitempty" json:"backgroundModes,omitempty"`
}

type YaverNativeManifestAndroid struct {
	PackageQueries []string `yaml:"packageQueries,omitempty" json:"packageQueries,omitempty"`
	Permissions    []string `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Features       []string `yaml:"features,omitempty" json:"features,omitempty"`
}

type YaverNativeManifestHostRequirement struct {
	RequiresYaverOAuth bool     `yaml:"requiresYaverOAuth,omitempty" json:"requiresYaverOAuth,omitempty"`
	RequiredSurfaces   []string `yaml:"requiredSurfaces,omitempty" json:"requiredSurfaces,omitempty"`
}

type YaverNativeManifestAudit struct {
	OK       bool                           `json:"ok"`
	Findings []YaverNativeManifestAuditItem `json:"findings"`
}

type YaverNativeManifestAuditItem struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

func LoadYaverNativeAppManifest(dir string) (*YaverNativeAppManifest, error) {
	for _, name := range YaverNativeAppManifestNames {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var m YaverNativeAppManifest
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		m.path = path
		return &m, nil
	}
	return nil, nil
}

func AuditYaverNativeAppManifest(m *YaverNativeAppManifest) YaverNativeManifestAudit {
	audit := YaverNativeManifestAudit{OK: true}
	add := func(severity, code, message string) {
		audit.Findings = append(audit.Findings, YaverNativeManifestAuditItem{
			Severity: severity,
			Code:     code,
			Message:  message,
		})
		if severity == "error" {
			audit.OK = false
		}
	}
	if m == nil {
		add("error", "manifest_missing", "No yaver.app.yaml, yaver.game.yaml, yaver.app.json, or yaver.game.json was found.")
		return audit
	}
	if m.SchemaVersion == 0 {
		add("error", "schema_version_missing", "schemaVersion is required.")
	} else if m.SchemaVersion != YaverNativeAppManifestSchemaVersion {
		add("error", "schema_version_unsupported", fmt.Sprintf("schemaVersion must be %d.", YaverNativeAppManifestSchemaVersion))
	}
	if strings.TrimSpace(m.ID) == "" {
		add("error", "id_missing", "id is required.")
	}
	if strings.TrimSpace(m.Slug) == "" {
		add("error", "slug_missing", "slug is required.")
	}
	if strings.TrimSpace(m.Title) == "" {
		add("error", "title_missing", "title is required.")
	}
	if strings.TrimSpace(m.Auth.Provider) != YaverNativeOAuthProvider {
		add("error", "yaver_oauth_required", "Yaver-native apps must declare auth.provider: yaver-oauth.")
	}
	if !m.Auth.RequiredInYaverBuild {
		add("error", "yaver_auth_must_be_required", "Yaver-native apps must require Yaver OAuth in Yaver builds.")
	}
	requiredScopes := []string{"openid", "profile", "yaver.apps.run", "yaver.apps.events.write", "yaver.ai.invoke"}
	if strings.EqualFold(m.Kind, "game") || strings.Contains(m.Runtime.Kind, "game") {
		requiredScopes = append(requiredScopes, "yaver.games.play", "yaver.games.save")
	}
	scopeSet := map[string]bool{}
	for _, scope := range m.Auth.RequiredScopes {
		scopeSet[scope] = true
	}
	for _, scope := range requiredScopes {
		if !scopeSet[scope] {
			add("error", "missing_required_scope", fmt.Sprintf("auth.requiredScopes is missing %s.", scope))
		}
	}
	if m.Native.Host.RequiresYaverOAuth == false {
		add("warning", "native_host_oauth_not_declared", "native.host.requiresYaverOAuth should be true for catalog builds.")
	}
	if len(m.Surfaces) == 0 {
		add("warning", "surfaces_missing", "surfaces should list the Yaver runtime surfaces this app supports.")
	}
	if len(m.Native.Apple.InfoPlist.RequiredKeys) == 0 && len(m.Native.Apple.InfoPlist.UsageDescriptions) == 0 {
		add("warning", "apple_info_plist_requirements_missing", "native.apple.infoPlist should declare required host Info.plist keys or usage descriptions.")
	}
	return audit
}
