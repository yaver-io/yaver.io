package main

// env.go — `yaver env` self-hosted secrets / env-var manager.
// Replaces Doppler / 1Password Connect / AWS Secrets Manager for
// the solo-dev case. Backed by the local LWW sync store so env
// vars stay coherent across the dev's laptop, phone, and Hetzner
// box — no Convex, no vendor, no central server.
//
// Every write is timestamped and origin-tagged; peer agents pull
// /sync/env?since=<ts> and POST their own deltas. Last-write-wins
// per key.
//
// Values are stored in the on-disk sync file. A later patch can
// add encryption-at-rest by routing through the existing
// vault.enc, but for a solo dev whose relay is already the trust
// boundary the file is good enough for v1.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

// --- store helpers ------------------------------------------------

func envStore() (*SyncStore, error) {
	return OpenSyncStore("env")
}

// envGet returns the plain-string value for a key.
func envGet(key string) (string, bool) {
	s, err := envStore()
	if err != nil {
		return "", false
	}
	it, ok := s.Get(key)
	if !ok {
		return "", false
	}
	var v string
	if err := json.Unmarshal(it.Value, &v); err != nil {
		return "", false
	}
	return v, true
}

// envSet upserts a string value.
func envSet(key, value string) error {
	s, err := envStore()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.Set(key, raw)
	return err
}

// envDelete tombstones a key.
func envDelete(key string) error {
	s, err := envStore()
	if err != nil {
		return err
	}
	return s.Delete(key)
}

// envList returns every live key + its value + metadata.
func envList() ([]envRow, error) {
	s, err := envStore()
	if err != nil {
		return nil, err
	}
	items := s.List()
	out := make([]envRow, 0, len(items))
	for _, it := range items {
		var val string
		_ = json.Unmarshal(it.Value, &val)
		out = append(out, envRow{
			Key:       it.Key,
			Value:     val,
			UpdatedAt: it.UpdatedAt,
			UpdatedBy: it.UpdatedBy,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

type envRow struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	UpdatedAt int64  `json:"updatedAt"`
	UpdatedBy string `json:"updatedBy"`
}

// --- CLI ----------------------------------------------------------

func runEnv(args []string) {
	if len(args) == 0 {
		printEnvUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "set":
		envSetCmd(args[1:])
	case "get":
		envGetCmd(args[1:])
	case "list", "ls":
		envListCmd()
	case "unset", "delete", "rm":
		envUnsetCmd(args[1:])
	case "export":
		envExportCmd(args[1:])
	case "import":
		envImportCmd(args[1:])
	case "help", "--help", "-h":
		printEnvUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown env subcommand: %s\n\n", args[0])
		printEnvUsage()
		os.Exit(1)
	}
}

func printEnvUsage() {
	fmt.Print(`Yaver env — self-hosted secrets / env-var manager.

Usage:
  yaver env set <key> <value>        Set or update a variable
  yaver env get <key>                Print a variable's value
  yaver env list                     List every variable + updatedAt
  yaver env unset <key>              Tombstone a variable
  yaver env export [--shell|--dotenv]  Export for source / .env
  yaver env import <.env file>       Bulk import from a dotenv file

Storage: ~/.yaver/sync/env.json — local-only, timestamped, LWW.
Every write is tagged with this agent's origin ID so peer syncs
(via /sync/env) converge across your laptop / phone / remote box
without a central server.
`)
}

func envSetCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver env set <key> <value>")
		os.Exit(1)
	}
	key := args[0]
	value := strings.Join(args[1:], " ")
	if err := envSet(key, value); err != nil {
		fmt.Fprintf(os.Stderr, "set: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s set (%d chars)\n", key, len(value))
}

func envGetCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver env get <key>")
		os.Exit(1)
	}
	v, ok := envGet(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "%s: not set\n", args[0])
		os.Exit(2)
	}
	fmt.Println(v)
}

func envListCmd() {
	rows, err := envList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		os.Exit(1)
	}
	if len(rows) == 0 {
		fmt.Println("No env vars yet. `yaver env set FOO bar` to create one.")
		return
	}
	for _, r := range rows {
		mask := r.Value
		if len(mask) > 8 {
			mask = mask[:4] + "…" + mask[len(mask)-2:]
		}
		fmt.Printf("  %-30s  %-12s  updated %d by %s\n",
			r.Key, mask, r.UpdatedAt, shortOrigin(r.UpdatedBy))
	}
}

func envUnsetCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver env unset <key>")
		os.Exit(1)
	}
	if err := envDelete(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "unset: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ %s tombstoned\n", args[0])
}

func envExportCmd(args []string) {
	mode := "shell"
	for _, a := range args {
		if a == "--dotenv" {
			mode = "dotenv"
		}
		if a == "--shell" {
			mode = "shell"
		}
	}
	rows, err := envList()
	if err != nil {
		fmt.Fprintf(os.Stderr, "export: %v\n", err)
		os.Exit(1)
	}
	for _, r := range rows {
		if mode == "shell" {
			fmt.Printf("export %s=%s\n", r.Key, shellQuote(r.Value))
		} else {
			fmt.Printf("%s=%s\n", r.Key, r.Value)
		}
	}
}

func envImportCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver env import <.env file>")
		os.Exit(1)
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		value = strings.TrimPrefix(value, "\"")
		value = strings.TrimSuffix(value, "\"")
		if key == "" {
			continue
		}
		if err := envSet(key, value); err == nil {
			count++
		}
	}
	fmt.Printf("✓ imported %d variable(s)\n", count)
}

func shellQuote(s string) string {
	if strings.ContainsAny(s, " \t\"'$`\\") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}

func shortOrigin(o string) string {
	if len(o) > 8 {
		return o[:8]
	}
	return o
}

// --- HTTP ---------------------------------------------------------

func (s *HTTPServer) handleEnvList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := envList()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"env":     rows,
			"origin":  syncOrigin(),
		})
	case http.MethodPost:
		var body struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Key == "" {
			jsonError(w, http.StatusBadRequest, "key required")
			return
		}
		if err := envSet(body.Key, body.Value); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	case http.MethodDelete:
		key := r.URL.Query().Get("key")
		if key == "" {
			jsonError(w, http.StatusBadRequest, "key required")
			return
		}
		if err := envDelete(key); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET/POST/DELETE")
	}
}

// SDK-facing single-key lookup. Token scopes this to the
// envReader scope so SDK clients can read but not write.
func (s *HTTPServer) handleEnvGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		jsonError(w, http.StatusBadRequest, "key required")
		return
	}
	val, ok := envGet(key)
	if !ok {
		jsonError(w, http.StatusNotFound, "not set")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":    true,
		"key":   key,
		"value": val,
	})
}
