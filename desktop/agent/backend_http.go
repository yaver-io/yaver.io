package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// ---------- Universal backend MCP tool handlers ----------

func mcpBackendStatus(dir string) interface{} {
	a, err := NewBackendAdapter(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return a.Status()
}

func mcpDataTables(dir string) interface{} {
	a, err := NewBackendAdapter(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	tables, err := a.ListTables()
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"backend": a.Kind(), "tables": tables}
}

func mcpDataBrowse(dir, table, cursor string, limit int) interface{} {
	a, err := NewBackendAdapter(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	res, err := a.Browse(table, cursor, limit)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return res
}

func mcpDataQuery(dir, q, argsJSON string) interface{} {
	a, err := NewBackendAdapter(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	res, err := a.Query(q, parseJSONArgs(argsJSON))
	if err != nil {
		return map[string]interface{}{"error": err.Error(), "output": res}
	}
	return map[string]interface{}{"result": res}
}

func mcpDataInsert(dir, table, docJSON string) interface{} {
	a, err := NewBackendAdapter(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	id, err := a.Insert(table, parseJSONArgs(docJSON))
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"id": id}
}

func mcpDataUpdate(dir, table, id, fieldsJSON string) interface{} {
	a, err := NewBackendAdapter(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := a.Update(table, id, parseJSONArgs(fieldsJSON)); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true}
}

func mcpDataDelete(dir, table, id string) interface{} {
	a, err := NewBackendAdapter(dir)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	if err := a.Delete(table, id); err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	return map[string]interface{}{"ok": true}
}

// ---------- HTTP handlers ----------

func (s *HTTPServer) handleBackendStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpBackendStatus(s.dirParam(r)))
}

func (s *HTTPServer) handleDBTables(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mcpDataTables(s.dirParam(r)))
}

func (s *HTTPServer) handleDBBrowse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	writeJSON(w, http.StatusOK, mcpDataBrowse(s.dirParam(r), q.Get("table"), q.Get("cursor"), limit))
}

type dbCallBody struct {
	Query    string                 `json:"query"`
	Table    string                 `json:"table"`
	ID       string                 `json:"id"`
	Doc      map[string]interface{} `json:"doc"`
	Fields   map[string]interface{} `json:"fields"`
	Args     map[string]interface{} `json:"args"`
}

func (s *HTTPServer) handleDBQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b dbCallBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpDataQuery(s.dirParam(r), b.Query, argsToJSON(b.Args)))
}

func (s *HTTPServer) handleDBInsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b dbCallBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpDataInsert(s.dirParam(r), b.Table, argsToJSON(b.Doc)))
}

func (s *HTTPServer) handleDBUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b dbCallBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpDataUpdate(s.dirParam(r), b.Table, b.ID, argsToJSON(b.Fields)))
}

func (s *HTTPServer) handleDBDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var b dbCallBody
	_ = json.NewDecoder(r.Body).Decode(&b)
	writeJSON(w, http.StatusOK, mcpDataDelete(s.dirParam(r), b.Table, b.ID))
}
