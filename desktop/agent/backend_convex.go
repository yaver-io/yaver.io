package main

import (
	"encoding/json"
	"fmt"
)

type convexAdapter struct {
	client *ConvexAdminClient
	dir    string
}

func (a *convexAdapter) Kind() BackendKind { return BackendConvex }

func (a *convexAdapter) Status() BackendStatus {
	st := BackendStatus{Kind: BackendConvex, URL: a.client.URL}
	if err := a.client.Health(); err != nil {
		st.Error = err.Error()
		st.Hint = "Run `yaver services add convex && yaver services start convex`"
		return st
	}
	st.Running = true
	return st
}

func (a *convexAdapter) ListTables() ([]TableInfo, error) {
	data, err := a.client.Query("yaver_admin:listTables", nil)
	if err != nil {
		return nil, fmt.Errorf("%w (install with `convex_install_helper`)", err)
	}
	// Convex wraps responses as {"status":"success","value":...}
	var envelope struct {
		Status string          `json:"status"`
		Value  json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	var rows []struct{ Name string `json:"name"` }
	if err := json.Unmarshal(envelope.Value, &rows); err != nil {
		return nil, err
	}
	out := make([]TableInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, TableInfo{Name: r.Name, Kind: "collection"})
	}
	return out, nil
}

func (a *convexAdapter) Browse(table, cursor string, limit int) (*BrowseResult, error) {
	args := map[string]interface{}{"tableName": table, "limit": limit}
	if cursor != "" {
		args["cursor"] = cursor
	}
	data, err := a.client.Query("yaver_admin:browseTable", args)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Value struct {
			Page           []map[string]interface{} `json:"page"`
			ContinueCursor string                   `json:"continueCursor"`
			IsDone         bool                     `json:"isDone"`
		} `json:"value"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	next := envelope.Value.ContinueCursor
	if envelope.Value.IsDone {
		next = ""
	}
	return &BrowseResult{Rows: envelope.Value.Page, NextCursor: next}, nil
}

func (a *convexAdapter) Query(fn string, args map[string]interface{}) (interface{}, error) {
	data, err := a.client.Query(fn, args)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func (a *convexAdapter) Insert(table string, doc map[string]interface{}) (string, error) {
	data, err := a.client.Mutation("yaver_admin:insertDocument", map[string]interface{}{
		"tableName": table, "document": doc,
	})
	if err != nil {
		return "", err
	}
	var env struct{ Value string `json:"value"` }
	_ = json.Unmarshal(data, &env)
	return env.Value, nil
}

func (a *convexAdapter) Update(table, id string, fields map[string]interface{}) error {
	_, err := a.client.Mutation("yaver_admin:patchDocument", map[string]interface{}{
		"tableName": table, "id": id, "fields": fields,
	})
	return err
}

func (a *convexAdapter) Delete(table, id string) error {
	_, err := a.client.Mutation("yaver_admin:deleteDocument", map[string]interface{}{
		"tableName": table, "id": id,
	})
	return err
}
