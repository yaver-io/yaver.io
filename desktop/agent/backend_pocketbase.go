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
	"sync"
	"time"
)

type pocketBaseAdapter struct {
	url      string
	email    string
	password string
	token    string
	tokenExp time.Time
	http     *http.Client
	mu       sync.Mutex
}

func newPocketBaseAdapter(dir string, cfg *YaverProjectConfig) *pocketBaseAdapter {
	u := cfg.DB
	if u == "" {
		u = os.Getenv("POCKETBASE_URL")
	}
	if u == "" {
		u = "http://127.0.0.1:8090"
	}
	email := firstNonEmpty(cfg.Env["POCKETBASE_ADMIN_EMAIL"], os.Getenv("POCKETBASE_ADMIN_EMAIL"))
	pw := firstNonEmpty(cfg.Env["POCKETBASE_ADMIN_PASSWORD"], os.Getenv("POCKETBASE_ADMIN_PASSWORD"))
	return &pocketBaseAdapter{
		url:      strings.TrimRight(u, "/"),
		email:    email,
		password: pw,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *pocketBaseAdapter) Kind() BackendKind { return BackendPocketBase }

func (a *pocketBaseAdapter) Status() BackendStatus {
	st := BackendStatus{Kind: BackendPocketBase, URL: a.url}
	res, err := a.http.Get(a.url + "/api/health")
	if err != nil {
		st.Error = err.Error()
		st.Hint = "Start PocketBase: `./pocketbase serve` or run the pocketbase service"
		return st
	}
	defer res.Body.Close()
	st.Running = res.StatusCode == 200
	if !st.Running {
		st.Error = fmt.Sprintf("unexpected status %d", res.StatusCode)
	}
	return st
}

func (a *pocketBaseAdapter) authHeader() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.tokenExp) {
		return "Bearer " + a.token, nil
	}
	if a.email == "" || a.password == "" {
		return "", fmt.Errorf("PocketBase admin creds not set (POCKETBASE_ADMIN_EMAIL / _PASSWORD)")
	}
	body, _ := json.Marshal(map[string]string{"identity": a.email, "password": a.password})
	res, err := a.http.Post(a.url+"/api/admins/auth-with-password", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		b, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("pb auth: %d %s", res.StatusCode, string(b))
	}
	var out struct{ Token string `json:"token"` }
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	a.token = out.Token
	a.tokenExp = time.Now().Add(10 * time.Minute)
	return "Bearer " + a.token, nil
}

func (a *pocketBaseAdapter) request(method, path string, body interface{}) ([]byte, error) {
	auth, err := a.authHeader()
	if err != nil {
		return nil, err
	}
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.url+path, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
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
		return data, fmt.Errorf("pb %s: %d %s", path, res.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (a *pocketBaseAdapter) ListTables() ([]TableInfo, error) {
	data, err := a.request("GET", "/api/collections?perPage=200", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	res := make([]TableInfo, 0, len(out.Items))
	for _, c := range out.Items {
		res = append(res, TableInfo{Name: c.Name, Kind: c.Type})
	}
	return res, nil
}

func (a *pocketBaseAdapter) Browse(table, cursor string, limit int) (*BrowseResult, error) {
	if limit <= 0 {
		limit = 50
	}
	page := 1
	fmt.Sscanf(cursor, "%d", &page)
	if page < 1 {
		page = 1
	}
	path := fmt.Sprintf("/api/collections/%s/records?page=%d&perPage=%d", url.PathEscape(table), page, limit)
	data, err := a.request("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Page       int                      `json:"page"`
		TotalPages int                      `json:"totalPages"`
		Items      []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	next := ""
	if out.Page < out.TotalPages {
		next = fmt.Sprintf("%d", out.Page+1)
	}
	return &BrowseResult{Rows: out.Items, NextCursor: next}, nil
}

// Query treats `q` as a REST path (e.g. "collections/users/records").
func (a *pocketBaseAdapter) Query(q string, args map[string]interface{}) (interface{}, error) {
	path := q
	if !strings.HasPrefix(path, "/api/") {
		path = "/api/" + strings.TrimPrefix(path, "/")
	}
	data, err := a.request("GET", path, nil)
	if err != nil {
		return nil, err
	}
	var j interface{}
	_ = json.Unmarshal(data, &j)
	return j, nil
}

func (a *pocketBaseAdapter) Insert(table string, doc map[string]interface{}) (string, error) {
	data, err := a.request("POST", "/api/collections/"+url.PathEscape(table)+"/records", doc)
	if err != nil {
		return "", err
	}
	var out struct{ ID string `json:"id"` }
	_ = json.Unmarshal(data, &out)
	return out.ID, nil
}

func (a *pocketBaseAdapter) Update(table, id string, fields map[string]interface{}) error {
	_, err := a.request("PATCH", "/api/collections/"+url.PathEscape(table)+"/records/"+url.PathEscape(id), fields)
	return err
}

func (a *pocketBaseAdapter) Delete(table, id string) error {
	_, err := a.request("DELETE", "/api/collections/"+url.PathEscape(table)+"/records/"+url.PathEscape(id), nil)
	return err
}
