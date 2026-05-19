package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readTgz returns name→content for a gzip+tar bundle.
func readTgz(t *testing.T, data []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	out := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = string(b)
	}
	return out
}

// readZip returns name→content for a zip bundle (also proves it's a
// valid archive a coding agent / OS unzip can open).
func readZip(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip open: %v", err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("zip entry %s: %v", f.Name, err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func keysOf(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// The export must ship an AGENTS.md handoff and offer a .zip twin
// (identical contents) so the sandbox is droppable into a coding agent
// AND Yaver-Cloud-compatible (the handoff explains the hosted backend
// wiring). Found wiring the "export to coding agent" ask.
func TestPhoneExport_AgentsDocAndZipParity(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Agent Handoff", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	opts := PhoneExportOptions{IncludeData: true}

	tgz, err := ExportPhoneProjectWithOptions(p.Slug, opts)
	if err != nil {
		t.Fatalf("tgz export: %v", err)
	}
	zipb, err := ExportPhoneProjectZip(p.Slug, opts)
	if err != nil {
		t.Fatalf("zip export: %v", err)
	}
	tf := readTgz(t, tgz)
	zf := readZip(t, zipb)

	agentsKey := filepath.ToSlash(filepath.Join(p.Slug, "AGENTS.md"))
	tAgents, ok := tf[agentsKey]
	if !ok || tAgents == "" {
		t.Fatalf("AGENTS.md missing from .tgz (have %v)", keysOf(tf))
	}
	zAgents, ok := zf[agentsKey]
	if !ok || zAgents == "" {
		t.Fatalf("AGENTS.md missing from .zip (have %v)", keysOf(zf))
	}
	if tAgents != zAgents {
		t.Error("AGENTS.md differs between .tgz and .zip — formats drifted")
	}

	// Handoff must be Yaver-Cloud-compatible + describe the real app.
	for _, want := range []string{
		"EXPO_PUBLIC_CONVEX_URL",
		"Yaver Cloud",
		"yaver deploy --target=selfhosted",
		"todos",
	} {
		if !strings.Contains(tAgents, want) {
			t.Errorf("AGENTS.md missing %q:\n%s", want, tAgents)
		}
	}

	// Same file set in both archives.
	if len(tf) != len(zf) {
		t.Errorf("archive file count differs: tgz=%d zip=%d", len(tf), len(zf))
	}
	for k := range tf {
		if _, ok := zf[k]; !ok {
			t.Errorf("zip missing %q present in tgz", k)
		}
	}
}

func setupPhoneTestHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":      "hello-world",
		"MyProject!!":      "myproject",
		"  lots  spaces  ": "lots-spaces",
		"UPPER_case-1":     "upper-case-1",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Slugify(""); got != "" {
		t.Errorf("Slugify(\"\") should be empty so callers can fall through, got %q", got)
	}
}

func TestCreatePhoneProject_BlankTemplate(t *testing.T) {
	setupPhoneTestHome(t)

	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "My Blank App", Template: "blank"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Slug != "my-blank-app" {
		t.Errorf("slug = %q, want my-blank-app", p.Slug)
	}
	if p.Template != "blank" {
		t.Errorf("template = %q, want blank", p.Template)
	}
	// .yaver/config.yaml must exist so existing BackendAdapter picks this up.
	if _, err := os.Stat(filepath.Join(p.Dir, ".yaver", "config.yaml")); err != nil {
		t.Errorf(".yaver/config.yaml missing: %v", err)
	}
	// phone.yaml stores the metadata.
	if _, err := os.Stat(filepath.Join(p.Dir, ".yaver", "phone.yaml")); err != nil {
		t.Errorf(".yaver/phone.yaml missing: %v", err)
	}
}

func TestCreatePhoneProject_DuplicateSlug(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "dup"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "dup"}); err == nil {
		t.Fatalf("expected ErrPhoneProjectExists")
	}
}

func TestCreatePhoneProject_FromPromptUsesGeneratedSpec(t *testing.T) {
	setupPhoneTestHome(t)
	old := runPhonePromptGenerator
	t.Cleanup(func() { runPhonePromptGenerator = old })
	runPhonePromptGenerator = func(spec AIGeneratorSpec) (string, error) {
		return `{
  "name": "Prompt Todos",
  "schema": {
    "tables": [
      {
        "name": "users",
        "columns": [
          {"name":"id","type":"text","primary":true},
          {"name":"email","type":"text","required":true,"unique":true}
        ]
      },
      {
        "name": "todos",
        "columns": [
          {"name":"id","type":"text","primary":true,"default":"uuid"},
          {"name":"title","type":"text","required":true},
          {"name":"done","type":"bool","default":"false"},
          {"name":"owner_id","type":"text"}
        ]
      }
    ],
    "relations": [{"from":"todos.owner_id","to":"users.id","onDelete":"cascade"}]
  },
  "auth": {
    "personas": [{"id":"owner","email":"owner@example.com","name":"Owner","role":"owner"}]
  },
  "seed": {
    "todos": [{"id":"welcome","title":"Welcome","done":false,"owner_id":"owner"}]
  },
  "app": {
    "summary": "A compact todo app for phone-first capture.",
    "primaryEntity": "todos",
    "screens": [
      {
        "id": "todo_list",
        "title": "Todos",
        "kind": "list",
        "table": "todos",
        "actions": [{"label":"Add task","kind":"create","table":"todos"}]
      }
    ]
  }
}`, nil
	}

	p, err := CreatePhoneProject(PhoneCreateSpec{
		Name:   "prompt-app",
		Prompt: "todo app with login",
	})
	if err != nil {
		t.Fatalf("create from prompt: %v", err)
	}
	if p.Template != "prompt" {
		t.Fatalf("template = %q, want prompt", p.Template)
	}
	if p.Schema == nil || len(p.Schema.Tables) != 2 {
		t.Fatalf("expected generated schema, got %+v", p.Schema)
	}
	if p.Stats == nil || p.Stats.PerTable["todos"] != 1 {
		t.Fatalf("expected generated seed row, got %+v", p.Stats)
	}
	if p.App == nil || p.App.PrimaryEntity != "todos" || len(p.App.Screens) != 1 {
		t.Fatalf("expected generated app spec, got %+v", p.App)
	}
}

func TestCreatePhoneProject_FromImportedConversationUsesAnalysis(t *testing.T) {
	setupPhoneTestHome(t)
	oldImport := runConversationImportGenerator
	oldPrompt := runPhonePromptGenerator
	t.Cleanup(func() {
		runConversationImportGenerator = oldImport
		runPhonePromptGenerator = oldPrompt
	})
	runConversationImportGenerator = func(spec AIGeneratorSpec) (string, error) {
		return `{
			"suggestedName":"Imported Planner",
			"productGoal":"Turn a pasted thread into a real app plan.",
			"technicalPlan":["analyze thread","generate prompt"],
			"nextPrompt":"Build the imported-thread intake flow."
		}`, nil
	}
	runPhonePromptGenerator = func(spec AIGeneratorSpec) (string, error) {
		if !strings.Contains(spec.Prompt, "Build the imported-thread intake flow.") {
			t.Fatalf("expected analyzed import prompt, got: %s", spec.Prompt)
		}
		return `{
  "name": "Imported Planner",
  "schema": {
    "tables": [
      {"name":"users","columns":[{"name":"id","type":"text","primary":true}]},
      {"name":"ideas","columns":[{"name":"id","type":"text","primary":true,"default":"uuid"},{"name":"title","type":"text","required":true}]}
    ]
  },
  "auth": { "personas": [] },
  "seed": { "ideas": [{"id":"welcome","title":"Imported brief ready"}] },
  "app": {
    "summary": "Import-driven planner.",
    "primaryEntity": "ideas",
    "screens": [{"id":"ideas","title":"Ideas","kind":"list","table":"ideas"}]
  }
}`, nil
	}

	p, err := CreatePhoneProject(PhoneCreateSpec{
		ImportContent: "user pasted a long Claude thread",
	})
	if err != nil {
		t.Fatalf("create from import: %v", err)
	}
	if p.Name != "Imported Planner" {
		t.Fatalf("name = %q", p.Name)
	}
	if p.Template != "prompt" {
		t.Fatalf("template = %q", p.Template)
	}
}

func TestExtractJSONObject_StripsFences(t *testing.T) {
	got := extractJSONObject("```json\n{\"name\":\"demo\"}\n```")
	if got != "{\"name\":\"demo\"}" {
		t.Fatalf("unexpected extracted JSON: %q", got)
	}
}

func TestTodosTemplate_EndToEnd(t *testing.T) {
	setupPhoneTestHome(t)

	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Todo App", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Schema == nil || len(p.Schema.Tables) != 2 {
		t.Fatalf("expected 2 tables (users + todos), got %+v", p.Schema)
	}
	if p.Auth == nil || len(p.Auth.Personas) < 2 {
		t.Fatalf("expected >= 2 personas, got %+v", p.Auth)
	}

	adapter, err := PhoneAdapter(p.Slug)
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	tables, err := adapter.ListTables()
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	names := map[string]bool{}
	for _, x := range tables {
		names[x.Name] = true
	}
	if !names["users"] || !names["todos"] {
		t.Errorf("expected users + todos in %v", names)
	}

	// Personas should have been mirrored into the users table.
	res, err := adapter.Browse("users", "", 50)
	if err != nil {
		t.Fatalf("browse users: %v", err)
	}
	if len(res.Rows) < 2 {
		t.Errorf("expected 2+ users from personas, got %d", len(res.Rows))
	}

	// Seeded todos should be present.
	res, err = adapter.Browse("todos", "", 50)
	if err != nil {
		t.Fatalf("browse todos: %v", err)
	}
	if len(res.Rows) < 3 {
		t.Errorf("expected 3+ seeded todos, got %d", len(res.Rows))
	}

	// Insert a new todo and verify.
	id, err := adapter.Insert("todos", map[string]interface{}{
		"id": "t4", "title": "test-inserted", "done": 0, "owner_id": "alice",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id == "" {
		t.Errorf("expected non-empty id")
	}
	res, err = adapter.Browse("todos", "", 50)
	if err != nil {
		t.Fatalf("browse after insert: %v", err)
	}
	if len(res.Rows) < 4 {
		t.Errorf("expected 4+ todos after insert, got %d", len(res.Rows))
	}

	// Update.
	if err := adapter.Update("todos", "t4", map[string]interface{}{"title": "test-updated"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	r, err := adapter.Query(`SELECT title FROM "todos" WHERE id='t4'`, nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows, ok := r.([]map[string]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("unexpected query result %T %v", r, r)
	}
	if rows[0]["title"] != "test-updated" {
		t.Errorf("title = %v, want test-updated", rows[0]["title"])
	}

	// Delete.
	if err := adapter.Delete("todos", "t4"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// Regression: an insert WITHOUT an explicit id must return the real
// generated primary key (the uuid TEXT default), not the integer
// rowid — callers update/delete by the returned id. And Query must
// actually bind named args (previously silently dropped). Both were
// found driving the phone-sandbox todos flow end-to-end.
func TestPhoneInsertReturnsRealPK_AndQueryBindsArgs(t *testing.T) {
	setupPhoneTestHome(t)

	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "PK App", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	adapter, err := PhoneAdapter(p.Slug)
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}

	// No id supplied → todos.id has DEFAULT uuid. Returned id must be
	// that uuid, not "4"/rowid.
	id, err := adapter.Insert("todos", map[string]interface{}{
		"title": "no-id-row", "done": 0, "owner_id": "alice",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if len(id) < 16 {
		t.Fatalf("returned id %q looks like a rowid, expected the real uuid PK", id)
	}

	// The returned id must actually address the row: update by it,
	// then read it back.
	if err := adapter.Update("todos", id, map[string]interface{}{"title": "by-real-pk"}); err != nil {
		t.Fatalf("update by returned id: %v", err)
	}

	// Query with named args must bind (was dropped before the fix).
	r, err := adapter.Query(`SELECT title FROM "todos" WHERE id = :pk`,
		map[string]interface{}{"pk": id})
	if err != nil {
		t.Fatalf("query with named args: %v", err)
	}
	rows, ok := r.([]map[string]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("named-arg query returned %T %v (expected exactly the updated row)", r, r)
	}
	if rows[0]["title"] != "by-real-pk" {
		t.Errorf("title = %v, want by-real-pk (update by returned id did not hit the row)", rows[0]["title"])
	}

	// Delete by the returned id must remove exactly that row.
	if err := adapter.Delete("todos", id); err != nil {
		t.Fatalf("delete by returned id: %v", err)
	}
	r, _ = adapter.Query(`SELECT COUNT(*) c FROM "todos" WHERE id = :pk`,
		map[string]interface{}{"pk": id})
	if rows, ok := r.([]map[string]interface{}); ok && len(rows) == 1 {
		if c := rows[0]["c"]; c != int64(0) && c != float64(0) && fmt.Sprintf("%v", c) != "0" {
			t.Errorf("row still present after delete-by-returned-id: count=%v", c)
		}
	}
}

func TestApplyPhoneSchema_Additive(t *testing.T) {
	setupPhoneTestHome(t)
	_, err := CreatePhoneProject(PhoneCreateSpec{
		Name: "schema-test",
		Schema: &PhoneSchema{Tables: []PhoneTable{{
			Name: "widgets",
			Columns: []PhoneColumn{
				{Name: "id", Type: "text", Primary: true},
				{Name: "name", Type: "text"},
			},
		}}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Add a column — should succeed without dropping data.
	if err := ApplyPhoneSchema("schema-test", &PhoneSchema{
		Tables: []PhoneTable{{
			Name: "widgets",
			Columns: []PhoneColumn{
				{Name: "id", Type: "text", Primary: true},
				{Name: "name", Type: "text"},
				{Name: "created_at", Type: "timestamp", Default: "now"},
			},
		}},
	}); err != nil {
		t.Fatalf("re-apply schema: %v", err)
	}
	adapter, _ := PhoneAdapter("schema-test")
	if _, err := adapter.Insert("widgets", map[string]interface{}{"id": "w1", "name": "a"}); err != nil {
		t.Fatalf("insert after alter: %v", err)
	}
	res, _ := adapter.Browse("widgets", "", 10)
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	if _, ok := res.Rows[0]["created_at"]; !ok {
		t.Errorf("new column created_at missing from row")
	}
}

func TestApplyPhoneSchema_RejectsBadType(t *testing.T) {
	setupPhoneTestHome(t)
	_, err := CreatePhoneProject(PhoneCreateSpec{Name: "bad-type"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	err = ApplyPhoneSchema("bad-type", &PhoneSchema{Tables: []PhoneTable{{
		Name:    "x",
		Columns: []PhoneColumn{{Name: "y", Type: "blob"}},
	}}})
	if err == nil {
		t.Fatalf("expected error for unknown column type")
	}
	if !strings.Contains(err.Error(), "unsupported column type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExportPhoneProject_TarRoundTrip(t *testing.T) {
	setupPhoneTestHome(t)

	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "Export Test", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	data, err := ExportPhoneProject(p.Slug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if len(data) < 200 {
		t.Errorf("tgz too small: %d bytes", len(data))
	}
	names, err := readTarNames(data)
	if err != nil {
		t.Fatalf("read tar: %v", err)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[strings.TrimPrefix(n, p.Slug+"/")] = true
	}
	for _, must := range []string{
		".yaver/config.yaml",
		".yaver/project.yaml",
		"schema.yaml",
		"auth.yaml",
		"seed.json",
		"app.yaml",
		"schema.sql",
		"schema.postgres.sql",
		"README.md",
	} {
		if !have[must] {
			t.Errorf("export missing %s (got %v)", must, names)
		}
	}
}

func TestImportPhoneProject_PreservesAppSpec(t *testing.T) {
	setupPhoneTestHome(t)

	src, err := CreatePhoneProject(PhoneCreateSpec{Name: "Import App", Template: "todos"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	data, err := ExportPhoneProject(src.Slug)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	imported, err := ImportPhoneProject(data, PhoneImportOptions{SlugOverride: "imported-app"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imported.App == nil {
		t.Fatalf("expected imported app spec")
	}
	if imported.App.PrimaryEntity != "todos" {
		t.Fatalf("primaryEntity = %q, want todos", imported.App.PrimaryEntity)
	}
	if len(imported.App.Screens) == 0 || imported.App.Screens[0].ID != "todo_list" {
		t.Fatalf("unexpected app screens: %+v", imported.App.Screens)
	}
}

func TestListPhoneProjects(t *testing.T) {
	setupPhoneTestHome(t)
	for _, n := range []string{"one", "two", "three"} {
		if _, err := CreatePhoneProject(PhoneCreateSpec{Name: n}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}
	projs, err := ListPhoneProjects()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(projs) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projs))
	}
}

func TestDeletePhoneProject(t *testing.T) {
	setupPhoneTestHome(t)
	if _, err := CreatePhoneProject(PhoneCreateSpec{Name: "del-test"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := DeletePhoneProject("del-test"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := LoadPhoneProject("del-test"); err == nil {
		t.Fatalf("expected load to fail after delete")
	}
	// Deleting missing slug must be a no-op.
	if err := DeletePhoneProject("never-existed"); err != nil {
		t.Errorf("delete of missing should be no-op, got %v", err)
	}
}

func TestPromotePhoneProject_DryRun(t *testing.T) {
	setupPhoneTestHome(t)
	p, err := CreatePhoneProject(PhoneCreateSpec{Name: "promote-test", Template: "crud"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	engine := NewSwitchEngine()
	state, err := engine.Plan(p.Dir, "postgres-neon", true)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if state.FromBackend != BackendSQLite {
		t.Errorf("from = %s, want sqlite", state.FromBackend)
	}
	if len(state.Steps) == 0 {
		t.Errorf("expected steps, got none")
	}
	if err := engine.Persist(state); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if err := engine.Run(state); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
}

func TestTemplatesAreJSONSerialisable(t *testing.T) {
	for _, tpl := range ListPhoneTemplates() {
		schema := templateSchema(tpl["id"])
		auth := templateAuth(tpl["id"])
		seed := templateSeed(tpl["id"])
		buf := new(bytes.Buffer)
		enc := json.NewEncoder(buf)
		if err := enc.Encode(map[string]interface{}{
			"schema": schema, "auth": auth, "seed": seed,
		}); err != nil {
			t.Errorf("template %s not JSON-encodable: %v", tpl["id"], err)
		}
	}
}
