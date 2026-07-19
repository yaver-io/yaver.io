package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxSafeRelPath(t *testing.T) {
	ok := map[string]string{
		"app/index.tsx":    "app/index.tsx",
		"./a/b.ts":         "a/b.ts",
		"a/../b.ts":        "b.ts",
		"components/X.tsx": "components/X.tsx",
	}
	for in, want := range ok {
		got, err := sandboxSafeRelPath(in)
		if err != nil {
			t.Errorf("sandboxSafeRelPath(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("sandboxSafeRelPath(%q) = %q, want %q", in, got, want)
		}
	}
	bad := []string{"", "  ", "/etc/passwd", "../escape.ts", "a/../../b.ts", `a\b.ts`, "C:/Windows", ".."}
	for _, in := range bad {
		if _, err := sandboxSafeRelPath(in); err == nil {
			t.Errorf("sandboxSafeRelPath(%q) = nil error, want rejection", in)
		}
	}
}

func TestSandboxIgnoredPath(t *testing.T) {
	ignored := []string{".git/config", ".claude/x", "node_modules/react/index.js", "dist/bundle.js", ".expo/settings"}
	for _, p := range ignored {
		if !sandboxIgnoredPath(p) {
			t.Errorf("sandboxIgnoredPath(%q) = false, want true", p)
		}
	}
	kept := []string{"app/index.tsx", "src/lib/a.ts", "package.json", "README.md"}
	for _, p := range kept {
		if sandboxIgnoredPath(p) {
			t.Errorf("sandboxIgnoredPath(%q) = true, want false", p)
		}
	}
}

func TestWriteAndSnapshotRoundTrip(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"app/index.tsx": "export default 1;",
		"lib/util.ts":   "export const x = 2;",
		"README.md":     "# hi",
	}
	if err := writeSandboxFiles(root, files); err != nil {
		t.Fatalf("writeSandboxFiles: %v", err)
	}
	// Drop a junk dir that must be ignored on read-back.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "session.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := snapshotSandboxDir(root)
	if err != nil {
		t.Fatalf("snapshotSandboxDir: %v", err)
	}
	if len(snap) != len(files) {
		t.Fatalf("snapshot has %d files, want %d (%v)", len(snap), len(files), snap)
	}
	for k, v := range files {
		if snap[k] != v {
			t.Errorf("snapshot[%q] = %q, want %q", k, snap[k], v)
		}
	}
	if _, leaked := snap[".claude/session.json"]; leaked {
		t.Error("ignored .claude file leaked into snapshot")
	}
}

func TestDiffSandboxSnapshots(t *testing.T) {
	before := map[string]string{
		"keep.ts":   "same",
		"change.ts": "old",
		"delete.ts": "gone",
	}
	after := map[string]string{
		"keep.ts":    "same",
		"change.ts":  "new",
		"created.ts": "fresh",
	}
	edits := diffSandboxSnapshots(before, after)
	// Expect: update change.ts, create created.ts, delete delete.ts (sorted by path).
	want := []sandboxEdit{
		{Action: "update", Path: "change.ts", Content: "new"},
		{Action: "create", Path: "created.ts", Content: "fresh"},
		{Action: "delete", Path: "delete.ts"},
	}
	if len(edits) != len(want) {
		t.Fatalf("got %d edits, want %d: %+v", len(edits), len(want), edits)
	}
	for i := range want {
		if edits[i].Action != want[i].Action || edits[i].Path != want[i].Path || edits[i].Content != want[i].Content {
			t.Errorf("edit[%d] = %+v, want %+v", i, edits[i], want[i])
		}
	}
	// "keep.ts" must NOT produce an edit.
	for _, e := range edits {
		if e.Path == "keep.ts" {
			t.Error("unchanged file produced an edit")
		}
	}
}

func TestProcessSandboxRun_FakeRunnerEdits(t *testing.T) {
	req := sandboxRunRequest{
		Prompt: "make the heading green",
		Files: []sandboxFile{
			{Path: "app/index.tsx", Content: "color: red"},
			{Path: "app/old.tsx", Content: "delete me"},
		},
	}
	// Fake runner: edits one file, creates one, deletes one — mutating the workdir
	// exactly as a real agent would, so we exercise the full diff path.
	fake := func(ctx context.Context, workDir, prompt string) (sandboxRunMeta, error) {
		if err := os.WriteFile(filepath.Join(workDir, "app", "index.tsx"), []byte("color: green"), 0o644); err != nil {
			return sandboxRunMeta{}, err
		}
		if err := os.WriteFile(filepath.Join(workDir, "app", "new.tsx"), []byte("// new"), 0o644); err != nil {
			return sandboxRunMeta{}, err
		}
		if err := os.Remove(filepath.Join(workDir, "app", "old.tsx")); err != nil {
			return sandboxRunMeta{}, err
		}
		return sandboxRunMeta{rationale: "made it green", model: "glm-4.7"}, nil
	}
	resp := processSandboxRun(context.Background(), req, fake)
	if !resp.OK {
		t.Fatalf("resp not OK: %+v", resp)
	}
	if resp.Rationale != "made it green" || resp.Model != "glm-4.7" {
		t.Errorf("meta not threaded: rationale=%q model=%q", resp.Rationale, resp.Model)
	}
	byPath := map[string]sandboxEdit{}
	for _, e := range resp.Edits {
		byPath[e.Path] = e
	}
	if e := byPath["app/index.tsx"]; e.Action != "update" || e.Content != "color: green" {
		t.Errorf("index.tsx edit = %+v, want update→green", e)
	}
	if e := byPath["app/new.tsx"]; e.Action != "create" || e.Content != "// new" {
		t.Errorf("new.tsx edit = %+v, want create", e)
	}
	if e := byPath["app/old.tsx"]; e.Action != "delete" {
		t.Errorf("old.tsx edit = %+v, want delete", e)
	}
}

func TestProcessSandboxRun_TodoRNAppDevelopmentFixture(t *testing.T) {
	req := sandboxRunRequest{
		Prompt:    "turn this into a todo app with add, toggle, and delete support",
		Framework: "React Native (Expo)",
		Files: []sandboxFile{
			{
				Path: "package.json",
				Content: `{
  "scripts": {
    "start": "expo start"
  },
  "dependencies": {
    "expo": "~53.0.0",
    "react": "19.0.0",
    "react-native": "0.79.0"
  }
}`,
			},
			{
				Path: "App.tsx",
				Content: `import React from "react";
import { Text, View } from "react-native";

export default function App() {
  return (
    <View>
      <Text>Hello sandbox</Text>
    </View>
  );
}
`,
			},
		},
	}

	fakeOpenCodeTodo := func(ctx context.Context, workDir, prompt string) (sandboxRunMeta, error) {
		if !strings.Contains(prompt, "todo app") {
			t.Fatalf("prompt did not include user request:\n%s", prompt)
		}
		app := `import React, { useState } from "react";
import { Button, FlatList, Pressable, StyleSheet, Text, TextInput, View } from "react-native";

type Todo = { id: string; title: string; done: boolean };

export default function App() {
  const [text, setText] = useState("");
  const [todos, setTodos] = useState<Todo[]>([]);

  const addTodo = () => {
    const title = text.trim();
    if (!title) return;
    setTodos((items) => [{ id: Date.now().toString(), title, done: false }, ...items]);
    setText("");
  };

  return (
    <View style={styles.container}>
      <Text style={styles.title}>Todos</Text>
      <View style={styles.row}>
        <TextInput value={text} onChangeText={setText} placeholder="New todo" style={styles.input} />
        <Button title="Add" onPress={addTodo} />
      </View>
      <FlatList
        data={todos}
        keyExtractor={(item) => item.id}
        renderItem={({ item }) => (
          <View style={styles.todo}>
            <Pressable onPress={() => setTodos((items) => items.map((todo) => todo.id === item.id ? { ...todo, done: !todo.done } : todo))}>
              <Text style={[styles.todoText, item.done && styles.done]}>{item.title}</Text>
            </Pressable>
            <Button title="Delete" onPress={() => setTodos((items) => items.filter((todo) => todo.id !== item.id))} />
          </View>
        )}
      />
    </View>
  );
}

const styles = StyleSheet.create({
  container: { flex: 1, padding: 24, paddingTop: 72 },
  title: { fontSize: 32, fontWeight: "700", marginBottom: 16 },
  row: { flexDirection: "row", gap: 8, marginBottom: 16 },
  input: { borderWidth: 1, borderColor: "#999", flex: 1, padding: 8 },
  todo: { alignItems: "center", flexDirection: "row", justifyContent: "space-between", paddingVertical: 8 },
  todoText: { fontSize: 18 },
  done: { textDecorationLine: "line-through", color: "#777" },
});
`
		if err := os.WriteFile(filepath.Join(workDir, "App.tsx"), []byte(app), 0o644); err != nil {
			return sandboxRunMeta{}, err
		}
		return sandboxRunMeta{rationale: "implemented todo flow", model: "zai-coding-plan/glm-4.7"}, nil
	}

	resp := processSandboxRun(context.Background(), req, fakeOpenCodeTodo)
	if !resp.OK {
		t.Fatalf("resp not OK: %+v", resp)
	}
	if resp.Runner != "opencode" || resp.Model != "zai-coding-plan/glm-4.7" {
		t.Fatalf("runner/model not threaded: %+v", resp)
	}
	if len(resp.Edits) != 1 {
		t.Fatalf("expected one App.tsx update, got %+v", resp.Edits)
	}
	edit := resp.Edits[0]
	if edit.Action != "update" || edit.Path != "App.tsx" {
		t.Fatalf("unexpected edit: %+v", edit)
	}
	for _, want := range []string{"useState", "FlatList", "addTodo", "Delete", "textDecorationLine"} {
		if !strings.Contains(edit.Content, want) {
			t.Fatalf("todo App.tsx missing %q:\n%s", want, edit.Content)
		}
	}
}

func TestProcessSandboxRun_NoChangeNoEdits(t *testing.T) {
	req := sandboxRunRequest{
		Prompt: "do nothing",
		Files:  []sandboxFile{{Path: "a.ts", Content: "x"}},
	}
	noop := func(ctx context.Context, workDir, prompt string) (sandboxRunMeta, error) {
		return sandboxRunMeta{}, nil
	}
	resp := processSandboxRun(context.Background(), req, noop)
	if !resp.OK {
		t.Fatalf("resp not OK: %+v", resp)
	}
	if len(resp.Edits) != 0 {
		t.Errorf("expected no edits, got %+v", resp.Edits)
	}
}

func TestProcessSandboxRun_RunnerErrorWithPartialEdits(t *testing.T) {
	req := sandboxRunRequest{
		Prompt: "x",
		Files:  []sandboxFile{{Path: "a.ts", Content: "x"}},
	}
	partial := func(ctx context.Context, workDir, prompt string) (sandboxRunMeta, error) {
		_ = os.WriteFile(filepath.Join(workDir, "a.ts"), []byte("y"), 0o644)
		return sandboxRunMeta{}, context.DeadlineExceeded
	}
	resp := processSandboxRun(context.Background(), req, partial)
	if resp.Error == "" {
		t.Error("expected error surfaced")
	}
	if !resp.OK { // partial edit present → OK true so the phone can still preview it
		t.Error("expected OK=true with partial edits")
	}
	if len(resp.Edits) != 1 || resp.Edits[0].Action != "update" {
		t.Errorf("expected one update edit, got %+v", resp.Edits)
	}
}

func TestProcessSandboxRun_RejectsUnsafeInputPath(t *testing.T) {
	req := sandboxRunRequest{
		Prompt: "x",
		Files:  []sandboxFile{{Path: "../escape.ts", Content: "boom"}},
	}
	called := false
	fake := func(ctx context.Context, workDir, prompt string) (sandboxRunMeta, error) {
		called = true
		return sandboxRunMeta{}, nil
	}
	resp := processSandboxRun(context.Background(), req, fake)
	if called {
		t.Error("runner should not be called when an input path is unsafe")
	}
	if resp.OK || resp.Error == "" {
		t.Errorf("expected rejection, got %+v", resp)
	}
}

func TestBuildSandboxRemotePrompt(t *testing.T) {
	req := sandboxRunRequest{
		Prompt:    "add a settings screen",
		Framework: "react-native",
		Schema:    []byte(`{"tables":[{"name":"todos"}]}`),
	}
	p := buildSandboxRemotePrompt(req)
	for _, want := range []string{"react-native", "add a settings screen", "CURRENT WORKING DIRECTORY", "todos"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}
