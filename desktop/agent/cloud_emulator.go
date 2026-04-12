package main

import (
	"fmt"
	"strings"
)

// splitCSV splits a comma-separated string, trims whitespace, drops empties.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// cloudEmulatorPresets returns the service names that each cloud provider maps to.
var cloudEmulatorPresets = map[string]map[string]string{
	"aws": {
		"s3":       "minio",
		"dynamodb": "dynamodb-local",
		"sqs":      "elasticmq",
	},
	"azure": {
		"blob":  "azurite",
		"queue": "azurite",
		"table": "azurite",
	},
	"gcp": {
		// Firebase emulators run as a binary via `firebase emulators:start`.
		"firestore": "firebase-emulator",
		"storage":   "firebase-emulator",
		"auth":      "firebase-emulator",
	},
}

func cloudServicesFor(provider string, services []string) ([]string, error) {
	presets, ok := cloudEmulatorPresets[provider]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (supported: aws, gcp, azure)", provider)
	}
	seen := map[string]bool{}
	var out []string
	if len(services) == 0 {
		for _, name := range presets {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
		return out, nil
	}
	for _, svc := range services {
		name, ok := presets[strings.ToLower(svc)]
		if !ok {
			return nil, fmt.Errorf("%s has no emulator for %q", provider, svc)
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

func mcpCloudEmuStart(dir, provider string, services []string) interface{} {
	targets, err := cloudServicesFor(provider, services)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	sm := NewServicesManager(dir)
	// Add any missing services with defaults
	for _, name := range targets {
		if _, addErr := sm.Add(name, nil); addErr != nil && !strings.Contains(addErr.Error(), "already") {
			// soft-fail: may already be present
			_ = addErr
		}
	}
	msg, err := sm.Start(targets...)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": msg}
	}
	return map[string]interface{}{"provider": provider, "services": targets, "output": msg, "config": cloudEmuSDKConfig(provider)}
}

func mcpCloudEmuStop(dir, provider string, services []string) interface{} {
	targets, err := cloudServicesFor(provider, services)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	sm := NewServicesManager(dir)
	msg, err := sm.Stop(targets...)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": msg}
	}
	return map[string]interface{}{"provider": provider, "services": targets, "output": msg}
}

func mcpCloudEmuStatus(dir string) interface{} {
	sm := NewServicesManager(dir)
	all, err := sm.Status()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	knownEmu := map[string]string{}
	for provider, presets := range cloudEmulatorPresets {
		for _, svc := range presets {
			knownEmu[svc] = provider
		}
	}
	var out []map[string]interface{}
	for _, st := range all {
		if provider, ok := knownEmu[st.Name]; ok {
			out = append(out, map[string]interface{}{
				"name":     st.Name,
				"provider": provider,
				"running":  st.Running,
				"port":     st.Port,
				"health":   st.Health,
			})
		}
	}
	return map[string]interface{}{"emulators": out}
}

func mcpCloudEmuConfig(provider string) interface{} {
	return map[string]interface{}{"provider": provider, "config": cloudEmuSDKConfig(provider)}
}

// cloudEmuSDKConfig returns SDK config snippets the user can drop into their app.
func cloudEmuSDKConfig(provider string) map[string]interface{} {
	switch provider {
	case "aws":
		return map[string]interface{}{
			"region":      "us-east-1",
			"credentials": map[string]string{"accessKeyId": "test", "secretAccessKey": "test"},
			"s3":          map[string]string{"endpoint": "http://localhost:9000", "forcePathStyle": "true"},
			"dynamodb":    map[string]string{"endpoint": "http://localhost:8000"},
			"sqs":         map[string]string{"endpoint": "http://localhost:9324"},
			"notes":       "Stock MinIO (S3), DynamoDB Local, ElasticMQ (SQS). LocalStack is not used.",
		}
	case "azure":
		return map[string]interface{}{
			"blob":  "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=Eby8vdM02xNOcqFlqUwJPLlmEh8zchiLRN+QBM4JgxlqcpiP7F6eCZbqx8vbPhBIV3DLp8Af1s2SdwTWeK8VL4Lg==;BlobEndpoint=http://localhost:10000/devstoreaccount1;",
			"queue": "http://localhost:10001/devstoreaccount1",
			"table": "http://localhost:10002/devstoreaccount1",
			"notes": "Azurite (official MIT emulator). Account name + key are Microsoft's documented defaults.",
		}
	case "gcp":
		return map[string]interface{}{
			"firestore": "localhost:8080",
			"auth":      "http://localhost:9099",
			"storage":   "http://localhost:9199",
			"pubsub":    "localhost:8085",
			"ui":        "http://localhost:4000",
			"notes":     "Firebase Emulator Suite. Run `firebase emulators:start` in the project dir.",
		}
	}
	return map[string]interface{}{"error": "unknown provider"}
}
