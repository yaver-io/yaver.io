package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type appwriteAdapter struct {
	endpoint  string
	projectID string
	apiKey    string
	dbID      string // default database ID to browse
	http      *http.Client
}

func newAppwriteAdapter(dir string, cfg *YaverProjectConfig) *appwriteAdapter {
	ep := firstNonEmpty(cfg.DB, cfg.Env["APPWRITE_ENDPOINT"], os.Getenv("APPWRITE_ENDPOINT"), "http://localhost/v1")
	project := firstNonEmpty(cfg.Env["APPWRITE_PROJECT_ID"], os.Getenv("APPWRITE_PROJECT_ID"))
	key := firstNonEmpty(cfg.Env["APPWRITE_API_KEY"], os.Getenv("APPWRITE_API_KEY"))
	dbID := firstNonEmpty(cfg.Env["APPWRITE_DATABASE_ID"], os.Getenv("APPWRITE_DATABASE_ID"), "default")
	return &appwriteAdapter{
		endpoint:  strings.TrimRight(ep, "/"),
		projectID: project,
		apiKey:    key,
		dbID:      dbID,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *appwriteAdapter) Kind() BackendKind { return BackendAppwrite }

func (a *appwriteAdapter) request(method, path string, body interface{}) ([]byte, error) {
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.endpoint+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Appwrite-Project", a.projectID)
	if a.apiKey != "" {
		req.Header.Set("X-Appwrite-Key", a.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return data, fmt.Errorf("appwrite %s: %d %s", path, res.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (a *appwriteAdapter) Status() BackendStatus {
	st := BackendStatus{Kind: BackendAppwrite, URL: a.endpoint}
	res, err := a.http.Get(a.endpoint + "/health")
	if err != nil {
		st.Error = err.Error()
		st.Hint = "Start Appwrite (`docker compose up`) or set APPWRITE_ENDPOINT"
		return st
	}
	defer res.Body.Close()
	st.Running = res.StatusCode == 200
	if a.projectID == "" {
		st.Hint = "APPWRITE_PROJECT_ID not set — dashboard reads/writes will fail"
	}
	return st
}

func (a *appwriteAdapter) ListTables() ([]TableInfo, error) {
	path := fmt.Sprintf("/databases/%s/collections?limit=200", url.PathEscape(a.dbID))
	data, err := a.request("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Collections []struct {
			Name string `json:"name"`
			ID   string `json:"$id"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	res := make([]TableInfo, 0, len(out.Collections))
	for _, c := range out.Collections {
		res = append(res, TableInfo{Name: c.ID, Kind: "collection"})
	}
	return res, nil
}

func (a *appwriteAdapter) Browse(table, cursor string, limit int) (*BrowseResult, error) {
	if limit <= 0 {
		limit = 50
	}
	path := fmt.Sprintf("/databases/%s/collections/%s/documents?queries[]=limit(%d)",
		url.PathEscape(a.dbID), url.PathEscape(table), limit)
	if cursor != "" {
		path += fmt.Sprintf("&queries[]=cursorAfter(\"%s\")", url.QueryEscape(cursor))
	}
	data, err := a.request("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Documents []map[string]interface{} `json:"documents"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	next := ""
	if len(out.Documents) == limit {
		if id, ok := out.Documents[len(out.Documents)-1]["$id"].(string); ok {
			next = id
		}
	}
	return &BrowseResult{Rows: out.Documents, NextCursor: next}, nil
}

func (a *appwriteAdapter) Query(q string, args map[string]interface{}) (interface{}, error) {
	data, err := a.request("GET", "/"+strings.TrimPrefix(q, "/"), nil)
	if err != nil {
		return nil, err
	}
	var j interface{}
	_ = json.Unmarshal(data, &j)
	return j, nil
}

func (a *appwriteAdapter) Insert(table string, doc map[string]interface{}) (string, error) {
	path := fmt.Sprintf("/databases/%s/collections/%s/documents", url.PathEscape(a.dbID), url.PathEscape(table))
	id, hasID := doc["$id"].(string)
	if !hasID {
		id = "unique()"
	}
	body := map[string]interface{}{"documentId": id, "data": doc}
	data, err := a.request("POST", path, body)
	if err != nil {
		return "", err
	}
	var out struct{ ID string `json:"$id"` }
	_ = json.Unmarshal(data, &out)
	return out.ID, nil
}

func (a *appwriteAdapter) Update(table, id string, fields map[string]interface{}) error {
	path := fmt.Sprintf("/databases/%s/collections/%s/documents/%s",
		url.PathEscape(a.dbID), url.PathEscape(table), url.PathEscape(id))
	_, err := a.request("PATCH", path, map[string]interface{}{"data": fields})
	return err
}

func (a *appwriteAdapter) Delete(table, id string) error {
	path := fmt.Sprintf("/databases/%s/collections/%s/documents/%s",
		url.PathEscape(a.dbID), url.PathEscape(table), url.PathEscape(id))
	_, err := a.request("DELETE", path, nil)
	return err
}
