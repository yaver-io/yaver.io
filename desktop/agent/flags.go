package main

// flags.go — F1 — self-hosted feature flags on the dev's own
// relay. Solo-dev alternative to LaunchDarkly / Statsig / ConfigCat.
//
// Scope (v1):
//
//   - Boolean + string flags keyed by short string IDs,
//   - Per-flag percentage rollout using a stable SHA256 bucket of
//     the caller's userId (falls back to deviceId, falls back to
//     "in" for anonymous callers when rollout > 0),
//   - Per-user overrides (force ON/OFF for a specific userId),
//   - Atomic write-then-rename persistence to
//     ~/.yaver/flags/flags.json,
//   - No dashboards; the CLI + mobile Flags tab are the editor.
//
// The SDK calls GET /flags/eval?userId=x (optionally &flag=foo)
// and caches the response for ~30s. `yaver flags set` updates the
// ledger and bumps an updatedAt so clients know to refetch.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Flag is one entry in the ledger.
type Flag struct {
	Key             string            `json:"key"`
	Description     string            `json:"description,omitempty"`
	Type            string            `json:"type"` // "bool" | "string"
	DefaultBool     bool              `json:"defaultBool,omitempty"`
	DefaultString   string            `json:"defaultString,omitempty"`
	RolloutPercent  int               `json:"rolloutPercent"`  // 0..100, for bool flags
	StringVariant   string            `json:"stringVariant,omitempty"` // when in rollout, the string value returned
	Overrides       map[string]string `json:"overrides,omitempty"` // userId -> "on"/"off" for bool, or literal string value
	UpdatedAt       string            `json:"updatedAt"`
}

// flagStore is the persistent ledger.
type flagStore struct {
	mu        sync.Mutex
	path      string
	flags     map[string]*Flag
	updatedAt string
}

var (
	flagStoreOnce sync.Once
	flagStoreInst *flagStore
)

// globalFlagStore returns the package-wide ledger, lazily loading
// from disk on first access.
func globalFlagStore() *flagStore {
	flagStoreOnce.Do(func() {
		base, err := ConfigDir()
		if err != nil {
			flagStoreInst = &flagStore{flags: map[string]*Flag{}}
			return
		}
		dir := filepath.Join(base, "flags")
		_ = os.MkdirAll(dir, 0700)
		s := &flagStore{
			path:  filepath.Join(dir, "flags.json"),
			flags: map[string]*Flag{},
		}
		_ = s.loadLocked()
		flagStoreInst = s
	})
	return flagStoreInst
}

func (s *flagStore) loadLocked() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var payload struct {
		Flags     map[string]*Flag `json:"flags"`
		UpdatedAt string           `json:"updatedAt"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	if payload.Flags != nil {
		s.flags = payload.Flags
	}
	s.updatedAt = payload.UpdatedAt
	return nil
}

func (s *flagStore) saveLocked() error {
	s.updatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(map[string]interface{}{
		"flags":     s.flags,
		"updatedAt": s.updatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if werr := os.WriteFile(tmp, data, 0600); werr != nil {
		return werr
	}
	return os.Rename(tmp, s.path)
}

// Set upserts a flag definition.
func (s *flagStore) Set(f Flag) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.Type == "" {
		f.Type = "bool"
	}
	f.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	existing := s.flags[f.Key]
	if existing != nil && existing.Overrides != nil && f.Overrides == nil {
		f.Overrides = existing.Overrides
	}
	s.flags[f.Key] = &f
	_ = s.saveLocked()
}

// Override pins a userId to a specific value on a flag.
func (s *flagStore) Override(key, userID, value string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flags[key]
	if f == nil {
		return false
	}
	if f.Overrides == nil {
		f.Overrides = map[string]string{}
	}
	f.Overrides[userID] = value
	f.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_ = s.saveLocked()
	return true
}

// ClearOverride removes a per-user override.
func (s *flagStore) ClearOverride(key, userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flags[key]
	if f == nil || f.Overrides == nil {
		return false
	}
	delete(f.Overrides, userID)
	f.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	_ = s.saveLocked()
	return true
}

// Delete removes a flag entirely.
func (s *flagStore) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.flags[key]; !ok {
		return false
	}
	delete(s.flags, key)
	_ = s.saveLocked()
	return true
}

// List returns a stable copy sorted alphabetically for the CLI /
// mobile UI.
func (s *flagStore) List() []*Flag {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Flag, 0, len(s.flags))
	for _, f := range s.flags {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Evaluate returns the effective value for a userId. For bool
// flags: (bool, "bool"). For string flags: (string, "string").
func (s *flagStore) Evaluate(key, userID string) (value interface{}, kind string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flags[key]
	if f == nil {
		return nil, "", false
	}
	// Per-user override wins.
	if over, has := f.Overrides[userID]; has {
		if f.Type == "bool" {
			return strings.EqualFold(over, "on") || over == "true", "bool", true
		}
		return over, "string", true
	}
	// Percentage rollout.
	hit := flagBucket(userID, f.Key, f.RolloutPercent)
	if f.Type == "bool" {
		// rollout hit → f.DefaultBool flipped vs not; we keep the
		// convention that DefaultBool is the value *outside* the
		// rollout, StringVariant = "on" means "on inside the
		// rollout". Simpler model: rollout% of users get the
		// opposite of DefaultBool.
		val := f.DefaultBool
		if hit {
			val = !f.DefaultBool
		}
		return val, "bool", true
	}
	// string flag
	if hit && f.StringVariant != "" {
		return f.StringVariant, "string", true
	}
	return f.DefaultString, "string", true
}

// EvaluateAll returns every flag's effective value for a userId.
// Used by the mobile Flags tab to show "this is what user X sees".
func (s *flagStore) EvaluateAll(userID string) map[string]interface{} {
	list := s.List()
	out := map[string]interface{}{}
	for _, f := range list {
		val, _, _ := s.Evaluate(f.Key, userID)
		out[f.Key] = val
	}
	return out
}

// flagBucket is the pure-local hash bucket: sha256(userId + "|" +
// flagKey) mod 100 < rolloutPercent. Stable per user per flag so
// the same dev rolls the dice deterministically on every poll.
func flagBucket(userID, key string, rolloutPercent int) bool {
	if rolloutPercent >= 100 {
		return true
	}
	if rolloutPercent <= 0 {
		return false
	}
	h := sha256.New()
	h.Write([]byte(userID))
	h.Write([]byte{'|'})
	h.Write([]byte(key))
	sum := h.Sum(nil)
	n := uint32(sum[0])<<24 | uint32(sum[1])<<16 | uint32(sum[2])<<8 | uint32(sum[3])
	return int(n%100) < rolloutPercent
}

// --- CLI -----------------------------------------------------------

func runFlags(args []string) {
	if len(args) == 0 {
		printFlagsUsage()
		os.Exit(0)
	}
	switch args[0] {
	case "list", "ls":
		flagListCmd()
	case "set":
		flagSetCmd(args[1:])
	case "delete", "rm":
		flagDeleteCmd(args[1:])
	case "override":
		flagOverrideCmd(args[1:])
	case "rollout":
		flagRolloutCmd(args[1:])
	case "eval":
		flagEvalCmd(args[1:])
	case "help", "--help", "-h":
		printFlagsUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown flags subcommand %q\n", args[0])
		printFlagsUsage()
		os.Exit(1)
	}
}

func printFlagsUsage() {
	fmt.Print(`Yaver feature flags — self-hosted LaunchDarkly alternative.

Usage:
  yaver flags list
  yaver flags set <key> <value> [--type bool|string] [--rollout N] [--desc text]
  yaver flags rollout <key> <percent>
  yaver flags override <key> <userId> <value>
  yaver flags delete <key>
  yaver flags eval <key> [--user <userId>]

Examples:
  yaver flags set checkout_v2 false --type bool --rollout 20
  yaver flags override checkout_v2 alice@example.com on
  yaver flags eval checkout_v2 --user alice@example.com

All state lives in ~/.yaver/flags/flags.json. SDKs poll
/flags/eval?userId=<id> through the existing P2P transport.
`)
}

func flagListCmd() {
	list := globalFlagStore().List()
	if len(list) == 0 {
		fmt.Println("No flags yet. `yaver flags set <key> <default>` to create one.")
		return
	}
	for _, f := range list {
		val := "false"
		if f.Type == "bool" {
			val = strconv.FormatBool(f.DefaultBool)
		} else {
			val = "\"" + f.DefaultString + "\""
		}
		fmt.Printf("  %-24s  %-6s  default=%-10s  rollout=%d%%  %s\n",
			f.Key, f.Type, val, f.RolloutPercent, f.Description)
	}
}

func flagSetCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver flags set <key> <value> [--type bool|string] [--rollout N] [--desc text] [--variant s]")
		os.Exit(1)
	}
	key := args[0]
	raw := args[1]
	typ := "bool"
	rollout := 100
	desc := ""
	variant := ""
	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--type":
			if i+1 < len(args) {
				typ = args[i+1]
				i++
			}
		case "--rollout":
			if i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err == nil {
					rollout = n
				}
				i++
			}
		case "--desc":
			if i+1 < len(args) {
				desc = args[i+1]
				i++
			}
		case "--variant":
			if i+1 < len(args) {
				variant = args[i+1]
				i++
			}
		}
	}
	f := Flag{
		Key:            key,
		Description:    desc,
		Type:           typ,
		RolloutPercent: rollout,
		StringVariant:  variant,
	}
	if typ == "bool" {
		f.DefaultBool = strings.EqualFold(raw, "true") || strings.EqualFold(raw, "on")
	} else {
		f.DefaultString = raw
	}
	globalFlagStore().Set(f)
	fmt.Printf("✓ %s = %v (rollout %d%%)\n", key, raw, rollout)
}

func flagDeleteCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver flags delete <key>")
		os.Exit(1)
	}
	if globalFlagStore().Delete(args[0]) {
		fmt.Printf("✓ deleted %s\n", args[0])
	} else {
		fmt.Fprintf(os.Stderr, "flag %q not found\n", args[0])
		os.Exit(2)
	}
}

func flagOverrideCmd(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: yaver flags override <key> <userId> <value>")
		os.Exit(1)
	}
	if !globalFlagStore().Override(args[0], args[1], args[2]) {
		fmt.Fprintf(os.Stderr, "flag %q not found\n", args[0])
		os.Exit(2)
	}
	fmt.Printf("✓ %s override for %s = %s\n", args[0], args[1], args[2])
}

func flagRolloutCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: yaver flags rollout <key> <percent>")
		os.Exit(1)
	}
	pct, err := strconv.Atoi(args[1])
	if err != nil || pct < 0 || pct > 100 {
		fmt.Fprintln(os.Stderr, "percent must be 0..100")
		os.Exit(2)
	}
	store := globalFlagStore()
	list := store.List()
	for _, f := range list {
		if f.Key == args[0] {
			nf := *f
			nf.RolloutPercent = pct
			store.Set(nf)
			fmt.Printf("✓ %s rollout %d%%\n", args[0], pct)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "flag %q not found\n", args[0])
	os.Exit(2)
}

func flagEvalCmd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: yaver flags eval <key> [--user <id>]")
		os.Exit(1)
	}
	key := args[0]
	user := "anonymous"
	for i := 1; i < len(args); i++ {
		if args[i] == "--user" && i+1 < len(args) {
			user = args[i+1]
			i++
		}
	}
	val, kind, ok := globalFlagStore().Evaluate(key, user)
	if !ok {
		fmt.Fprintf(os.Stderr, "flag %q not found\n", key)
		os.Exit(2)
	}
	fmt.Printf("%s  type=%s  userId=%s  value=%v\n", key, kind, user, val)
}

// --- HTTP ----------------------------------------------------------

// handleFlags serves list/get/set via REST. GET returns every
// flag; POST upserts; DELETE removes.
func (s *HTTPServer) handleFlags(w http.ResponseWriter, r *http.Request) {
	store := globalFlagStore()
	switch r.Method {
	case http.MethodGet:
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":        true,
			"flags":     store.List(),
			"updatedAt": store.updatedAt,
		})
	case http.MethodPost:
		var body Flag
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if body.Key == "" {
			jsonError(w, http.StatusBadRequest, "key required")
			return
		}
		store.Set(body)
		jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
	default:
		jsonError(w, http.StatusMethodNotAllowed, "use GET or POST")
	}
}

// handleFlagsEval is the SDK-facing evaluation endpoint. Mobile
// + web SDKs poll this on startup + every 30s and cache the
// result. userId is optional — anonymous callers get rollout
// results seeded on the empty string.
func (s *HTTPServer) handleFlagsEval(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	userID := r.URL.Query().Get("userId")
	single := r.URL.Query().Get("flag")
	store := globalFlagStore()
	if single != "" {
		val, kind, ok := store.Evaluate(single, userID)
		if !ok {
			jsonError(w, http.StatusNotFound, "flag not found")
			return
		}
		jsonReply(w, http.StatusOK, map[string]interface{}{
			"ok":    true,
			"flag":  single,
			"kind":  kind,
			"value": val,
		})
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"userId":    userID,
		"flags":     store.EvaluateAll(userID),
		"updatedAt": store.updatedAt,
	})
}

// handleFlagOverride POST /flags/override {key, userId, value}.
// Separate endpoint so the mobile UI can hit it without touching
// the full Flag struct.
func (s *HTTPServer) handleFlagOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Key    string `json:"key"`
		UserID string `json:"userId"`
		Value  string `json:"value"`
		Clear  bool   `json:"clear,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.Key == "" || body.UserID == "" {
		jsonError(w, http.StatusBadRequest, "key and userId required")
		return
	}
	store := globalFlagStore()
	if body.Clear {
		store.ClearOverride(body.Key, body.UserID)
	} else {
		store.Override(body.Key, body.UserID, body.Value)
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// handleFlagDelete accepts POST /flags/delete?key=foo so the
// mobile UI can wipe a flag.
func (s *HTTPServer) handleFlagDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !globalFlagStore().Delete(body.Key) {
		jsonError(w, http.StatusNotFound, "flag not found")
		return
	}
	jsonReply(w, http.StatusOK, map[string]interface{}{"ok": true})
}
