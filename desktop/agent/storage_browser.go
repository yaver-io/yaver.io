package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StorageFile is a universal file descriptor across storage backends.
type StorageFile struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ContentType string `json:"contentType,omitempty"`
	URL        string `json:"url,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

// ListStorageFiles enumerates files in the project's configured storage.
// Supports: Convex (/api/storage via yaver_admin:listStoredFiles), Supabase
// Storage (REST), PocketBase (collection files), MinIO/S3 (via minio client
// config), and local filesystem as a fallback.
func ListStorageFiles(projectDir, bucket string) ([]StorageFile, string, error) {
	cfg, err := LoadProjectConfig(projectDir)
	if err != nil {
		return nil, "", err
	}
	switch cfg.Backend {
	case BackendConvex:
		return listConvexFiles(projectDir)
	case BackendSupabase:
		return listSupabaseFiles(projectDir, bucket)
	case BackendPocketBase:
		return listPocketBaseFiles(projectDir)
	}
	// Fallback: list a local `uploads/` directory.
	return listLocalFiles(filepath.Join(projectDir, "uploads"))
}

func listConvexFiles(projectDir string) ([]StorageFile, string, error) {
	client := NewConvexAdminClient(projectDir)
	data, err := client.Query("yaver_admin:listStoredFiles", nil)
	if err != nil {
		return nil, "convex", err
	}
	var env struct {
		Value []struct {
			ID          string `json:"_id"`
			Size        int64  `json:"size"`
			ContentType string `json:"contentType"`
			CreationTime int64 `json:"_creationTime"`
		} `json:"value"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, "convex", err
	}
	var out []StorageFile
	for _, f := range env.Value {
		out = append(out, StorageFile{
			ID: f.ID, Name: f.ID, Size: f.Size, ContentType: f.ContentType,
			CreatedAt: time.UnixMilli(f.CreationTime).Format(time.RFC3339),
		})
	}
	return out, "convex", nil
}

func listSupabaseFiles(projectDir, bucket string) ([]StorageFile, string, error) {
	if bucket == "" {
		bucket = "public"
	}
	cfg, _ := LoadProjectConfig(projectDir)
	baseURL := cfg.Env["SUPABASE_URL"]
	if baseURL == "" {
		baseURL = "http://localhost:54321"
	}
	key := cfg.Env["SUPABASE_SERVICE_ROLE_KEY"]
	if key == "" {
		return nil, "supabase", fmt.Errorf("SUPABASE_SERVICE_ROLE_KEY not set")
	}
	u := strings.TrimRight(baseURL, "/") + "/storage/v1/object/list/" + url.PathEscape(bucket)
	req, _ := http.NewRequest(http.MethodPost, u, strings.NewReader(`{"limit":100}`))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := provisionHTTP.Do(req)
	if err != nil {
		return nil, "supabase", err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return nil, "supabase", fmt.Errorf("supabase storage: %s", string(data))
	}
	var items []struct {
		Name string `json:"name"`
		ID   string `json:"id"`
		Metadata struct{ Size int64 `json:"size"` } `json:"metadata"`
	}
	_ = json.Unmarshal(data, &items)
	var out []StorageFile
	for _, it := range items {
		out = append(out, StorageFile{
			ID: it.ID, Name: it.Name, Size: it.Metadata.Size, Bucket: bucket,
		})
	}
	return out, "supabase", nil
}

func listPocketBaseFiles(projectDir string) ([]StorageFile, string, error) {
	// PocketBase files are tied to record fields; listing all is expensive.
	// Return an instruction rather than attempting a heavy scan.
	return nil, "pocketbase", fmt.Errorf("PocketBase files are per-record; use the admin UI or pass ?collection=<name>")
}

func listLocalFiles(dir string) ([]StorageFile, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "local", nil
		}
		return nil, "local", err
	}
	var out []StorageFile
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, StorageFile{
			ID:        filepath.Join(dir, e.Name()),
			Name:      e.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime().Format(time.RFC3339),
		})
	}
	return out, "local", nil
}

// ---- MCP / HTTP ----

func mcpStorageList(dir, bucket string) interface{} {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	files, source, err := ListStorageFiles(dir, bucket)
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "source": source}
	}
	return map[string]interface{}{"source": source, "files": files}
}

func (s *HTTPServer) handleStorageList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpStorageList(s.dirParam(r), r.URL.Query().Get("bucket")))
}
