package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type PublishProof struct {
	Kind       string `json:"kind"`
	OK         bool   `json:"ok"`
	RemoteID   string `json:"remoteId,omitempty"`
	Detail     string `json:"detail,omitempty"`
	VerifiedAt string `json:"verifiedAt"`
}

func supportedPublishPostcondition(kind string) bool {
	switch kind {
	case "artifact-sha256", "npm-version-visible", "pypi-version-visible", "pubdev-version-visible", "apple-upload-log-accepted",
		"google-edit-committed", "microsoft-submission-created",
		"cloudflare-deploy-reported", "convex-functions-verified",
		"sfmg-play-track-updated", "medici-health-verified", "command-completed":
		return true
	default:
		return false
	}
}

func (pm *PublishManager) addProof(run *PublishRun, proof PublishProof) {
	proof.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
	pm.mu.Lock()
	run.Proofs = append(run.Proofs, proof)
	pm.persistRunLocked(run)
	pm.mu.Unlock()
}

func outputContains(output, marker string) error {
	if !strings.Contains(strings.ToLower(output), strings.ToLower(marker)) {
		return fmt.Errorf("release postcondition missing command evidence %q", marker)
	}
	return nil
}

func (pm *PublishManager) verifyPostconditions(run *PublishRun, target PublishTarget, output string) error {
	for _, condition := range target.Postconditions {
		proof := PublishProof{Kind: condition.Kind, OK: true}
		var err error
		switch condition.Kind {
		case "artifact-sha256":
			pm.mu.RLock()
			artifacts := append([]PublishArtifact(nil), run.Artifacts...)
			pm.mu.RUnlock()
			if len(artifacts) == 0 {
				err = fmt.Errorf("release postcondition artifact-sha256: no archived artifact")
			} else {
				for _, artifact := range artifacts {
					if artifact.SHA256 == "" {
						err = fmt.Errorf("release postcondition artifact-sha256: %s has no digest", artifact.Name)
						break
					}
				}
				proof.Detail = fmt.Sprintf("%d artifact digest(s) recorded", len(artifacts))
			}
		case "npm-version-visible":
			proof.RemoteID, err = retryRegistryProof(func() (string, error) {
				return verifyNPMVersion(run.WorkDir, target.Identity.PackageName)
			})
			proof.Detail = "npm registry returned the published package version"
		case "pypi-version-visible":
			proof.RemoteID, err = retryRegistryProof(func() (string, error) {
				return verifyPyPIVersion(run.WorkDir, target.Identity.PackageName)
			})
			proof.Detail = "PyPI returned the published package version"
		case "pubdev-version-visible":
			proof.RemoteID, err = retryRegistryProof(func() (string, error) {
				return verifyPubDevVersion(run.WorkDir, target.Identity.PackageName)
			})
			proof.Detail = "pub.dev returned the published package version"
		case "apple-upload-log-accepted":
			err = outputContains(output, "uploaded")
			proof.Detail = "project uploader's checked App Store upload log reported acceptance"
		case "google-edit-committed":
			err = outputContains(output, "released versioncode")
			if err != nil && strings.Contains(strings.ToLower(output), "edit committed") && strings.Contains(strings.ToLower(output), "track") {
				err = nil
			}
			proof.Detail = "project uploader committed and reported the Play versionCode"
		case "microsoft-submission-created":
			proof.RemoteID, err = microsoftSubmissionID(output)
			proof.Detail = "Partner Center returned a submission ID"
		case "cloudflare-deploy-reported":
			err = outputContains(output, "deployed to https://")
			proof.Detail = "Cloudflare deploy command returned its production URL"
		case "convex-functions-verified":
			err = outputContains(output, "production deploy verified")
			proof.Detail = "post-deploy function inventory completed"
		case "sfmg-play-track-updated":
			err = outputContains(output, "play console")
			if err == nil {
				err = outputContains(output, "track updated")
			}
			proof.Detail = "SFMG uploader reported the committed Play track and versionCode"
		case "medici-health-verified":
			err = outputContains(output, "HTTP 200")
			if err == nil {
				err = outputContains(output, "medici-tutor → active")
			}
			proof.Detail = "public HTTPS operation and required tutor service are healthy"
		case "command-completed":
			proof.Detail = "project-owned command completed with exit zero"
		default:
			err = fmt.Errorf("release postcondition %q is not implemented; refusing to call the publication complete", condition.Kind)
		}
		if err != nil {
			proof.OK = false
			proof.Detail = err.Error()
			pm.addProof(run, proof)
			return err
		}
		pm.addProof(run, proof)
	}
	return nil
}

func retryRegistryProof(check func() (string, error)) (string, error) {
	delay := 2 * time.Second
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		remoteID, err := check()
		if err == nil {
			return remoteID, nil
		}
		lastErr = err
		if attempt == 5 {
			break
		}
		time.Sleep(delay)
		delay *= 2
		if delay > 15*time.Second {
			delay = 15 * time.Second
		}
	}
	return "", lastErr
}

func publishRegistryGET(rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry returned HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func manifestNameVersion(path string) (string, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	namePattern := regexp.MustCompile(`(?m)^name\s*[:=]\s*["']?([^"'\s]+)`)
	versionPattern := regexp.MustCompile(`(?m)^version\s*[:=]\s*["']?([^"'\s]+)`)
	nameMatch, versionMatch := namePattern.FindSubmatch(b), versionPattern.FindSubmatch(b)
	if len(nameMatch) != 2 || len(versionMatch) != 2 {
		return "", "", fmt.Errorf("manifest name/version required in %s", filepath.Base(path))
	}
	return string(nameMatch[1]), string(versionMatch[1]), nil
}

func verifyPyPIVersion(workDir, expectedName string) (string, error) {
	name, version, err := manifestNameVersion(filepath.Join(workDir, "pyproject.toml"))
	if err != nil {
		return "", err
	}
	if name != expectedName {
		return "", fmt.Errorf("package identity changed: manifest=%q pyproject.toml=%q", expectedName, name)
	}
	if _, err := publishRegistryGET("https://pypi.org/pypi/" + url.PathEscape(name) + "/" + url.PathEscape(version) + "/json"); err != nil {
		return "", fmt.Errorf("PyPI did not confirm %s@%s: %w", name, version, err)
	}
	return name + "@" + version, nil
}

func verifyPubDevVersion(workDir, expectedName string) (string, error) {
	name, version, err := manifestNameVersion(filepath.Join(workDir, "pubspec.yaml"))
	if err != nil {
		return "", err
	}
	if name != expectedName {
		return "", fmt.Errorf("package identity changed: manifest=%q pubspec.yaml=%q", expectedName, name)
	}
	body, err := publishRegistryGET("https://pub.dev/api/packages/" + url.PathEscape(name))
	if err != nil {
		return "", fmt.Errorf("pub.dev did not confirm %s@%s: %w", name, version, err)
	}
	var payload struct {
		Versions []struct {
			Version string `json:"version"`
		} `json:"versions"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return "", fmt.Errorf("pub.dev returned an invalid package response")
	}
	for _, candidate := range payload.Versions {
		if candidate.Version == version {
			return name + "@" + version, nil
		}
	}
	return "", fmt.Errorf("pub.dev did not list %s@%s", name, version)
}

func verifyNPMVersion(workDir, expectedName string) (string, error) {
	b, err := os.ReadFile(filepath.Join(workDir, "package.json"))
	if err != nil {
		return "", err
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return "", err
	}
	if expectedName != "" && pkg.Name != expectedName {
		return "", fmt.Errorf("package identity changed: manifest=%q package.json=%q", expectedName, pkg.Name)
	}
	if pkg.Name == "" || pkg.Version == "" {
		return "", fmt.Errorf("package.json name/version required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := osexec.CommandContext(ctx, "npm", "view", pkg.Name+"@"+pkg.Version, "version", "--json")
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("npm registry did not confirm %s@%s: %s", pkg.Name, pkg.Version, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), pkg.Version) {
		return "", fmt.Errorf("npm registry returned %q, expected version %s", strings.TrimSpace(string(out)), pkg.Version)
	}
	return pkg.Name + "@" + pkg.Version, nil
}

func microsoftSubmissionID(output string) (string, error) {
	start := strings.LastIndex(output, "{")
	if start < 0 {
		return "", fmt.Errorf("Partner Center command returned no JSON receipt")
	}
	var receipt struct {
		SubmissionID string `json:"submissionId"`
	}
	if err := json.Unmarshal([]byte(output[start:]), &receipt); err != nil || strings.TrimSpace(receipt.SubmissionID) == "" {
		return "", fmt.Errorf("Partner Center command returned no submissionId")
	}
	return receipt.SubmissionID, nil
}
