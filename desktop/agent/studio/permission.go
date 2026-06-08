// Package studio is the store-asset generator (screenshots, preview videos,
// permission-justification videos + prose) for third-party app developers.
// See docs/yaver-store-asset-studio.md for the full production spec.
//
// This file is the P0 core: static analysis of an app's permission usage,
// independent of any device/farm. Given an AndroidManifest.xml and a
// FOREGROUND_SERVICE_* permission, it extracts the facts a Play Console
// reviewer (and the prose generator in prose.go) need: which <service>
// declares it, its foregroundServiceType, the specialUse subtype, and a
// best-effort pointer to the UI that triggers it.
//
// Pure + dependency-free so it unit-tests with no simulator, no daemon, no
// keychain (the desktop/agent test convention). The capture/record/composite/
// publish stages live in sibling files added in later phases.
package studio

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fgsTypeForPermission maps each Android FOREGROUND_SERVICE_* permission to the
// android:foregroundServiceType token a <service> must declare to use it.
// Source: Android 14 (API 34) foreground-service-type requirements.
var fgsTypeForPermission = map[string]string{
	"android.permission.FOREGROUND_SERVICE_SPECIAL_USE":      "specialUse",
	"android.permission.FOREGROUND_SERVICE_DATA_SYNC":        "dataSync",
	"android.permission.FOREGROUND_SERVICE_LOCATION":         "location",
	"android.permission.FOREGROUND_SERVICE_CAMERA":           "camera",
	"android.permission.FOREGROUND_SERVICE_MICROPHONE":       "microphone",
	"android.permission.FOREGROUND_SERVICE_MEDIA_PLAYBACK":   "mediaPlayback",
	"android.permission.FOREGROUND_SERVICE_CONNECTED_DEVICE": "connectedDevice",
	"android.permission.FOREGROUND_SERVICE_HEALTH":           "health",
	"android.permission.FOREGROUND_SERVICE_REMOTE_MESSAGING": "remoteMessaging",
	"android.permission.FOREGROUND_SERVICE_PHONE_CALL":       "phoneCall",
	"android.permission.FOREGROUND_SERVICE_MEDIA_PROJECTION": "mediaProjection",
	"android.permission.FOREGROUND_SERVICE_SYSTEM_EXEMPTED":  "systemExempted",
	"android.permission.FOREGROUND_SERVICE_FILE_MANAGEMENT":  "fileManagement",
}

const androidPermissionPrefix = "android.permission."

// ServiceDecl is one <service> element from the manifest.
type ServiceDecl struct {
	Name              string // android:name (may be relative, e.g. ".sandbox.SandboxService")
	FGSTypes          []string
	Exported          bool
	SpecialUseSubtype string // PROPERTY_SPECIAL_USE_FGS_SUBTYPE value, if present
}

// PermissionFacts is everything the prose + video-flow generators need about
// one permission, derived purely from the manifest (+ optional repo scan).
type PermissionFacts struct {
	Platform   string // "android"
	Permission string // fully-qualified, e.g. android.permission.FOREGROUND_SERVICE_SPECIAL_USE

	// FGSType is the foregroundServiceType this permission requires
	// ("specialUse", "dataSync", …). Empty if Permission is not an FGS perm.
	FGSType string

	// Service is the <service> that declares the matching FGSType, if found.
	// nil means the permission is declared but no matching service was located
	// (a real Play-rejection risk worth surfacing).
	Service *ServiceDecl

	// SpecialUseSubtype is hoisted from Service for convenience.
	SpecialUseSubtype string

	// Declared reports whether the permission appears in <uses-permission>.
	Declared bool

	// AllFGSPermissions lists every FOREGROUND_SERVICE_* permission declared,
	// so callers can warn about ones with no backing service.
	AllFGSPermissions []string

	// TriggerHint is a best-effort source location (file path) where the
	// service is started from the UI, filled by FindTrigger. Empty if unknown.
	TriggerHint string
}

// NormalizePermission accepts either the short name (FOREGROUND_SERVICE_SPECIAL_USE)
// or the fully-qualified android.permission.* form and returns the FQ form.
func NormalizePermission(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.Contains(p, ".") && strings.HasPrefix(p, "android.permission.") {
		return p
	}
	// Bare token or some other dotted form: prefix only if it's a bare CONST.
	if !strings.Contains(p, ".") {
		return androidPermissionPrefix + p
	}
	return p
}

// FGSTypeForPermission returns the foregroundServiceType token for an FGS
// permission, or "" if the permission is not a foreground-service permission.
func FGSTypeForPermission(permission string) string {
	return fgsTypeForPermission[NormalizePermission(permission)]
}

// localAttr returns the value of the attribute with the given local name,
// ignoring XML namespace (Android attrs are namespaced as android:name etc.).
func localAttr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

// AnalyzeAndroidManifest parses an AndroidManifest.xml and extracts the facts
// for one permission. It never returns a nil PermissionFacts on success; the
// caller inspects .Declared and .Service to decide whether the manifest is
// actually wired correctly.
func AnalyzeAndroidManifest(manifestPath, permission string) (*PermissionFacts, error) {
	f, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open manifest: %w", err)
	}
	defer f.Close()
	return analyzeAndroidManifestReader(f, permission)
}

func analyzeAndroidManifestReader(r io.Reader, permission string) (*PermissionFacts, error) {
	perm := NormalizePermission(permission)
	facts := &PermissionFacts{
		Platform:   "android",
		Permission: perm,
		FGSType:    fgsTypeForPermission[perm],
	}

	dec := xml.NewDecoder(r)
	var services []ServiceDecl
	var cur *ServiceDecl
	fgsSet := map[string]bool{}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		switch se := tok.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "uses-permission", "uses-permission-sdk-23":
				name := localAttr(se, "name")
				if name == "" {
					break
				}
				if name == perm {
					facts.Declared = true
				}
				if strings.HasPrefix(name, "android.permission.FOREGROUND_SERVICE") {
					fgsSet[name] = true
				}
			case "service":
				svc := ServiceDecl{
					Name:     localAttr(se, "name"),
					Exported: strings.EqualFold(localAttr(se, "exported"), "true"),
				}
				if t := localAttr(se, "foregroundServiceType"); t != "" {
					for _, p := range strings.Split(t, "|") {
						if p = strings.TrimSpace(p); p != "" {
							svc.FGSTypes = append(svc.FGSTypes, p)
						}
					}
				}
				services = append(services, svc)
				cur = &services[len(services)-1]
			case "property":
				if cur != nil && localAttr(se, "name") == "android.app.PROPERTY_SPECIAL_USE_FGS_SUBTYPE" {
					cur.SpecialUseSubtype = localAttr(se, "value")
				}
			}
		case xml.EndElement:
			if se.Name.Local == "service" {
				cur = nil
			}
		}
	}

	for k := range fgsSet {
		facts.AllFGSPermissions = append(facts.AllFGSPermissions, k)
	}
	sort.Strings(facts.AllFGSPermissions)

	// Bind the service that declares the required foregroundServiceType.
	if facts.FGSType != "" {
		for i := range services {
			for _, t := range services[i].FGSTypes {
				if t == facts.FGSType {
					facts.Service = &services[i]
					facts.SpecialUseSubtype = services[i].SpecialUseSubtype
					break
				}
			}
			if facts.Service != nil {
				break
			}
		}
	}

	return facts, nil
}

// FindTrigger does a best-effort scan of the repo for where the service is
// started from app code, so the generated demo flow and the prose can name the
// entry point. It looks for the service's simple class name and common
// start-foreground call sites. Returns the first matching file path relative to
// root, or "" if nothing is found. Never errors — trigger discovery is advisory.
func FindTrigger(root string, facts *PermissionFacts) string {
	if facts == nil || facts.Service == nil || strings.TrimSpace(root) == "" {
		return ""
	}
	simple := facts.Service.Name
	if i := strings.LastIndex(simple, "."); i >= 0 {
		simple = simple[i+1:]
	}
	if simple == "" {
		return ""
	}
	var hit string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || hit != "" {
			if hit != "" {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == "node_modules" || base == ".git" || base == "build" || base == ".gradle" || base == "Pods" {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".kt", ".java", ".ts", ".tsx", ".js", ".jsx":
		default:
			return nil
		}
		// Skip the service definition file itself; we want the *caller*.
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		s := string(b)
		// The trigger is a site that references the service AND starts it,
		// without being the service's own class declaration.
		if strings.Contains(s, simple) &&
			(strings.Contains(s, "startForegroundService") ||
				strings.Contains(s, "startService") ||
				strings.Contains(s, "ACTION_START") ||
				strings.Contains(s, "start(")) &&
			!strings.Contains(s, "class "+simple) {
			if rel, e := filepath.Rel(root, path); e == nil {
				hit = rel
			} else {
				hit = path
			}
		}
		return nil
	})
	return hit
}
