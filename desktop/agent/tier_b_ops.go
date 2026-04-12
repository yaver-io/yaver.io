package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ---- Staging creation ----

// CreateStagingResult reports on a prod→staging materialization.
type CreateStagingResult struct {
	SourceDir  string   `json:"sourceDir"`
	TargetDir  string   `json:"targetDir"`
	Domain     string   `json:"domain,omitempty"`
	Upstream   string   `json:"upstream,omitempty"`
	Steps      []string `json:"steps"`
	Error      string   `json:"error,omitempty"`
}

// CreateStaging copies a source project directory to a staging sibling,
// bumps service ports to avoid conflicts, clones backend data, and optionally
// attaches a subdomain through Caddy.
func CreateStaging(sourceDir, name, domain string) (*CreateStagingResult, error) {
	if name == "" {
		name = "staging"
	}
	res := &CreateStagingResult{SourceDir: sourceDir}
	parent := filepath.Dir(sourceDir)
	target := filepath.Join(parent, filepath.Base(sourceDir)+"-"+name)
	res.TargetDir = target

	// 1. Copy the source tree to the target. Skip node_modules, .next, .yaver/snapshots.
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.Command("rsync", "-a",
		"--exclude=node_modules", "--exclude=.next", "--exclude=.git",
		"--exclude=.yaver/snapshots", "--exclude=.yaver/backups",
		sourceDir+"/", target+"/")
	if out, err := cmd.CombinedOutput(); err != nil {
		return finishStaging(res, fmt.Errorf("rsync: %w (%s)", err, string(out)))
	}
	res.Steps = append(res.Steps, "copied source tree to "+target)

	// 2. Bump service ports in services.yaml so they don't clash with prod.
	sm := NewServicesManager(target)
	cfg, _ := sm.LoadConfig()
	if cfg != nil {
		portBump := 10000
		for n, svc := range cfg.Services {
			svc.Port += portBump
			if svc.ConsolePort > 0 {
				svc.ConsolePort += portBump
			}
			if svc.WebPort > 0 {
				svc.WebPort += portBump
			}
			cfg.Services[n] = svc
		}
		if err := sm.SaveConfig(cfg); err != nil {
			return finishStaging(res, fmt.Errorf("bump ports: %w", err))
		}
		res.Steps = append(res.Steps, fmt.Sprintf("bumped %d service ports by +%d", len(cfg.Services), portBump))
	}

	// 3. Start target services.
	if msg, err := sm.Start(); err != nil {
		return finishStaging(res, fmt.Errorf("start: %w (%s)", err, msg))
	} else {
		res.Steps = append(res.Steps, "services started: "+msg)
	}

	// 4. Clone data from source → target.
	if _, err := CloneEnvironment(sourceDir, target, 0); err != nil {
		res.Steps = append(res.Steps, "warn: data clone failed: "+err.Error())
	} else {
		res.Steps = append(res.Steps, "data cloned from source")
	}

	// 5. Attach domain through Caddy if one was provided.
	if domain != "" {
		upstream := "localhost:3000"
		if cfg != nil {
			// Pick the first app-looking port (bumped).
			for _, svc := range cfg.Services {
				if svc.Port == 3000+10000 || svc.Port == 5173+10000 || svc.Port == 4321+10000 {
					upstream = fmt.Sprintf("localhost:%d", svc.Port)
					break
				}
			}
		}
		if _, err := AddDomain(domain, upstream, "", ""); err != nil {
			res.Steps = append(res.Steps, "warn: caddy attach failed: "+err.Error())
		} else {
			res.Domain = domain
			res.Upstream = upstream
			res.Steps = append(res.Steps, "domain "+domain+" → "+upstream)
		}
	}
	return res, nil
}

func finishStaging(res *CreateStagingResult, err error) (*CreateStagingResult, error) {
	if err != nil {
		res.Error = err.Error()
	}
	return res, err
}

// ---- Queue (ElasticMQ / SQS) inspector ----

// ListQueues returns the queues visible to the local ElasticMQ. Uses the
// unsigned SQS ListQueues action which ElasticMQ accepts by default.
type QueueInfo struct {
	URL              string `json:"url"`
	ApproxMessages   int    `json:"approximateMessages"`
	ApproxInFlight   int    `json:"approximateInFlight"`
}

func ListQueues(endpoint string) ([]QueueInfo, error) {
	if endpoint == "" {
		endpoint = "http://127.0.0.1:9324"
	}
	res, err := provisionHTTP.Get(endpoint + "/?Action=ListQueues")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("sqs list: %d %s", res.StatusCode, string(data))
	}
	// Parse ListQueuesResponse XML.
	var parsed struct {
		XMLName xml.Name `xml:"ListQueuesResponse"`
		Result  struct {
			QueueURL []string `xml:"QueueUrl"`
		} `xml:"ListQueuesResult"`
	}
	if err := xml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	out := make([]QueueInfo, 0, len(parsed.Result.QueueURL))
	for _, u := range parsed.Result.QueueURL {
		q := QueueInfo{URL: u}
		// Best-effort GetQueueAttributes for depth.
		attrURL := fmt.Sprintf("%s?Action=GetQueueAttributes&AttributeName.1=ApproximateNumberOfMessages&AttributeName.2=ApproximateNumberOfMessagesNotVisible&QueueUrl=%s",
			endpoint, u)
		if ar, err := provisionHTTP.Get(attrURL); err == nil {
			body, _ := io.ReadAll(ar.Body)
			ar.Body.Close()
			var attrs struct {
				XMLName xml.Name `xml:"GetQueueAttributesResponse"`
				Result  struct {
					Attribute []struct {
						Name  string `xml:"Name"`
						Value string `xml:"Value"`
					} `xml:"Attribute"`
				} `xml:"GetQueueAttributesResult"`
			}
			if xml.Unmarshal(body, &attrs) == nil {
				for _, a := range attrs.Result.Attribute {
					switch a.Name {
					case "ApproximateNumberOfMessages":
						fmt.Sscanf(a.Value, "%d", &q.ApproxMessages)
					case "ApproximateNumberOfMessagesNotVisible":
						fmt.Sscanf(a.Value, "%d", &q.ApproxInFlight)
					}
				}
			}
		}
		out = append(out, q)
	}
	return out, nil
}

// PurgeQueue empties a queue by URL.
func PurgeQueue(endpoint, queueURL string) error {
	if endpoint == "" {
		endpoint = "http://127.0.0.1:9324"
	}
	url := fmt.Sprintf("%s?Action=PurgeQueue&QueueUrl=%s", endpoint, queueURL)
	res, err := provisionHTTP.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		data, _ := io.ReadAll(res.Body)
		return fmt.Errorf("purge: %d %s", res.StatusCode, string(data))
	}
	return nil
}

// ---- Secret rotation ----

// RotateSecret generates a new random value, updates .env.local, restarts
// dependent services. Returns old/new values (old only if asked — dangerous).
type SecretRotateResult struct {
	Key        string   `json:"key"`
	OldValue   string   `json:"oldValue,omitempty"`
	NewValue   string   `json:"newValue"`
	FilesPatched []string `json:"filesPatched"`
	ServicesRestarted []string `json:"servicesRestarted"`
}

func RotateSecret(projectDir, key string, restartServices []string) (*SecretRotateResult, error) {
	if key == "" {
		return nil, fmt.Errorf("key required")
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	newVal := hex.EncodeToString(buf)

	res := &SecretRotateResult{Key: key, NewValue: newVal}
	// Patch .env.local, .env, and services.yaml envs.
	for _, name := range []string{".env.local", ".env"} {
		path := filepath.Join(projectDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		patched := false
		for i, line := range lines {
			if strings.HasPrefix(line, key+"=") {
				res.OldValue = strings.TrimPrefix(line, key+"=")
				lines[i] = key + "=" + newVal
				patched = true
			}
		}
		if !patched {
			lines = append(lines, key+"="+newVal)
			patched = true
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err == nil {
			res.FilesPatched = append(res.FilesPatched, path)
		}
	}

	// Patch services.yaml env entries.
	sm := NewServicesManager(projectDir)
	cfg, _ := sm.LoadConfig()
	if cfg != nil {
		changed := false
		for name, svc := range cfg.Services {
			if svc.Env == nil {
				continue
			}
			if _, ok := svc.Env[key]; ok {
				svc.Env[key] = newVal
				cfg.Services[name] = svc
				changed = true
			}
		}
		if changed {
			_ = sm.SaveConfig(cfg)
			res.FilesPatched = append(res.FilesPatched, sm.yamlPath)
		}
	}

	// Rolling restart of requested services (or all if nil).
	if _, err := sm.Start(restartServices...); err == nil {
		res.ServicesRestarted = restartServices
		if len(restartServices) == 0 && cfg != nil {
			for n := range cfg.Services {
				res.ServicesRestarted = append(res.ServicesRestarted, n)
			}
		}
	}

	// Notify + audit.
	if globalNotifyManager != nil {
		globalNotifyManager.NotifyAgentEvent("Secret rotated", fmt.Sprintf("%s in %s", key, filepath.Base(projectDir)))
	}
	AuditLog("", "secret_rotate", projectDir, key, "success", "", "")
	return res, nil
}

// ---- HTTP handlers ----

func (s *HTTPServer) handleStagingCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Source string `json:"source"`
		Name   string `json:"name"`
		Domain string `json:"domain"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if b.Source == "" {
		b.Source = s.dirParam(r)
	}
	res, err := CreateStaging(b.Source, b.Name, b.Domain)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error(), "result": res})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *HTTPServer) handleQueueList(w http.ResponseWriter, r *http.Request) {
	ep := r.URL.Query().Get("endpoint")
	queues, err := ListQueues(ep)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"queues": queues})
}

func (s *HTTPServer) handleQueuePurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Endpoint string `json:"endpoint"`
		URL      string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	if err := PurgeQueue(b.Endpoint, b.URL); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *HTTPServer) handleSecretRotate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b struct {
		Key      string   `json:"key"`
		Services []string `json:"services"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	res, err := RotateSecret(s.dirParam(r), b.Key, b.Services)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"error": err.Error()})
		return
	}
	// Strip old value from the response — we don't want it lingering in logs.
	res.OldValue = ""
	writeJSON(w, http.StatusOK, res)
}

var _ = time.Now // prevent unused import if time isn't referenced above
