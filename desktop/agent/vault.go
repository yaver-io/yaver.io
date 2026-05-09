package main

// vault.go — Yaver's on-device encrypted secret store. File format:
//
//   [salt(16B)][nonce(24B)][nacl_secretbox(JSON)]
//
// Key derived from passphrase (default: the Yaver auth token hashed via
// DerivePassphraseFromToken) using Argon2id. The plaintext JSON is a sorted
// slice of VaultEntry — v2 format. For backward compatibility we also
// accept the v1 format (object keyed by name, no Project / DeviceID /
// Deleted fields) on load and automatically upgrade on the next save.
//
// Project grouping: each entry has a Project ("" = global). The same key
// name is allowed across projects (e.g. APP_STORE_KEY_PATH for "yaver"
// and "sfmg"). Internally we key on "project\x00name" so the same Name
// can live in multiple projects without collision.
//
// Sync-ready fields (DeviceID / UpdatedAt / Deleted) exist for the P2P
// vault sync layer — merging is last-writer-wins by UpdatedAt. Deletes
// are soft (Deleted=true + UpdatedAt bump) so a delete on one machine
// propagates as a tombstone to peers. Tombstones are purged after
// vaultTombstoneTTL.
//
// Privacy contract: entries NEVER leave the agent's machine except via
// an explicit owner-authenticated sync to another device the same user
// owns. Convex is never involved (see convex_privacy_test.go).

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/nacl/secretbox"
)

// VaultEntry holds a single secret in the vault.
type VaultEntry struct {
	Name      string `json:"name"`
	Project   string `json:"project,omitempty"`  // "" = global; else app/project name
	Category  string `json:"category,omitempty"` // api-key, signing-key, ssh-key, git-credential, custom
	Value     string `json:"value,omitempty"`    // plaintext in-memory; omitted for tombstones
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	DeviceID  string `json:"device_id,omitempty"` // who last wrote this revision
	Deleted   bool   `json:"deleted,omitempty"`   // tombstone
}

// VaultEntrySummary is returned by List — never exposes Value.
type VaultEntrySummary struct {
	Name      string `json:"name"`
	Project   string `json:"project,omitempty"`
	Category  string `json:"category,omitempty"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	DeviceID  string `json:"device_id,omitempty"`
}

// VaultDigestEntry is the minimal per-entry record shared during sync.
// It contains no secret value — just enough metadata for a peer to decide
// whether it has fresher data.
type VaultDigestEntry struct {
	Name      string `json:"name"`
	Project   string `json:"project,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
	Deleted   bool   `json:"deleted,omitempty"`
}

// VaultStore manages the encrypted vault file at ~/.yaver/vault.enc.
type VaultStore struct {
	mu       sync.RWMutex
	path     string
	key      [32]byte // derived from passphrase or auth token
	deviceID string   // stamped into UpdatedAt on every write
	entries  map[string]VaultEntry
	unlocked bool
}

// Argon2id parameters for key derivation.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
	nonceLen     = 24
)

// vaultTombstoneTTL is how long a delete marker lingers before GC. Long
// enough that the slowest peer has plenty of opportunity to sync.
const vaultTombstoneTTL = 30 * 24 * time.Hour

// vaultKey builds the internal map key from a (project, name) pair.
// Using \x00 as separator guarantees no collision with legitimate names.
func vaultKey(project, name string) string {
	return project + "\x00" + name
}

// validateVaultName enforces a conservative charset so a Name can always
// be used as a shell env var and a URL query parameter without encoding.
// Projects follow the same rule.
func validateVaultName(s, field string) error {
	if s == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	if len(s) > 128 {
		return fmt.Errorf("%s too long (max 128 chars)", field)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.'
		if !ok {
			return fmt.Errorf("%s %q contains invalid character %q (allowed: A-Z a-z 0-9 _ - .)", field, s, c)
		}
	}
	return nil
}

func validateVaultProject(s string) error {
	if s == "" {
		return nil // global is allowed
	}
	return validateVaultName(s, "project")
}

// VaultPath returns the default vault file path.
func VaultPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vault.enc"), nil
}

// deriveKey uses Argon2id to derive a 32-byte key from a passphrase and salt.
func deriveKey(passphrase []byte, salt []byte) [32]byte {
	raw := argon2.IDKey(passphrase, salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	var key [32]byte
	copy(key[:], raw)
	return key
}

// NewVaultStore opens or creates a vault. The passphrase is used to derive
// the encryption key. If the vault file doesn't exist, an empty vault is
// created.
func NewVaultStore(passphrase string) (*VaultStore, error) {
	return NewVaultStoreWithDevice(passphrase, "")
}

// NewVaultStoreWithDevice is the variant used by the agent — it stamps
// deviceID into every write so sync can attribute a revision to its source.
func NewVaultStoreWithDevice(passphrase, deviceID string) (*VaultStore, error) {
	vaultPath, err := VaultPath()
	if err != nil {
		return nil, err
	}

	vs := &VaultStore{
		path:     vaultPath,
		deviceID: deviceID,
		entries:  make(map[string]VaultEntry),
	}

	// Warn if a stale .tmp from a crashed save sits next to the
	// vault. We don't auto-delete — a forensic operator may want
	// to inspect — but loud logging is better than silent confusion.
	if st, err := os.Stat(vaultPath + ".tmp"); err == nil {
		log.Printf("[vault] stale %s (size %d, mtime %s) — a previous save likely crashed before commit. Inspect + remove manually once you've confirmed the live vault is OK.",
			vaultPath+".tmp", st.Size(), st.ModTime().UTC().Format(time.RFC3339))
	}

	data, err := os.ReadFile(vaultPath)
	if err != nil {
		if os.IsNotExist(err) {
			salt := make([]byte, saltLen)
			if _, err := io.ReadFull(rand.Reader, salt); err != nil {
				return nil, fmt.Errorf("generate salt: %w", err)
			}
			vs.key = deriveKey([]byte(passphrase), salt)
			vs.unlocked = true
			if err := vs.save(salt); err != nil {
				return nil, fmt.Errorf("create vault: %w", err)
			}
			return vs, nil
		}
		return nil, fmt.Errorf("read vault: %w", err)
	}

	if len(data) < saltLen+nonceLen+secretbox.Overhead {
		return nil, fmt.Errorf("vault file too small — corrupted?")
	}

	salt := data[:saltLen]
	vs.key = deriveKey([]byte(passphrase), salt)

	var nonce [nonceLen]byte
	copy(nonce[:], data[saltLen:saltLen+nonceLen])

	plaintext, ok := secretbox.Open(nil, data[saltLen+nonceLen:], &nonce, &vs.key)
	if !ok {
		return nil, fmt.Errorf("wrong passphrase or corrupted vault")
	}

	if err := vs.loadEntries(plaintext); err != nil {
		return nil, err
	}

	vs.unlocked = true
	return vs, nil
}

// loadEntries accepts either v2 format (JSON array of VaultEntry) or v1
// format (JSON object keyed by name, no Project field). On v1 read, the
// next write re-serialises as v2.
func (vs *VaultStore) loadEntries(plaintext []byte) error {
	trimmed := trimLeadingWhitespace(plaintext)
	if len(trimmed) == 0 {
		return nil
	}
	switch trimmed[0] {
	case '[':
		var arr []VaultEntry
		if err := json.Unmarshal(plaintext, &arr); err != nil {
			return fmt.Errorf("parse vault (v2): %w", err)
		}
		for _, e := range arr {
			if e.Name == "" {
				continue
			}
			vs.entries[vaultKey(e.Project, e.Name)] = e
		}
		return nil
	case '{':
		var obj map[string]VaultEntry
		if err := json.Unmarshal(plaintext, &obj); err != nil {
			return fmt.Errorf("parse vault (v1): %w", err)
		}
		for name, e := range obj {
			if e.Name == "" {
				e.Name = name
			}
			// v1 had no Project — everything becomes global.
			e.Project = ""
			vs.entries[vaultKey("", e.Name)] = e
		}
		return nil
	default:
		return fmt.Errorf("parse vault: unrecognized format (root byte %q)", trimmed[0])
	}
}

func trimLeadingWhitespace(b []byte) []byte {
	for i, c := range b {
		if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
			return b[i:]
		}
	}
	return nil
}

// List returns summaries of vault entries, excluding tombstones. If
// project is the empty string, only global entries are returned. If
// project is "*", every entry is returned. Otherwise, entries in that
// specific project.
func (vs *VaultStore) List(project string) []VaultEntrySummary {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	result := make([]VaultEntrySummary, 0, len(vs.entries))
	for _, e := range vs.entries {
		if e.Deleted {
			continue
		}
		if project != "*" && e.Project != project {
			continue
		}
		result = append(result, VaultEntrySummary{
			Name:      e.Name,
			Project:   e.Project,
			Category:  e.Category,
			Notes:     e.Notes,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
			DeviceID:  e.DeviceID,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Project != result[j].Project {
			return result[i].Project < result[j].Project
		}
		return result[i].Name < result[j].Name
	})
	return result
}

// ListProjects returns distinct project names (excluding the global "")
// that have at least one live entry.
func (vs *VaultStore) ListProjects() []string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	seen := map[string]struct{}{}
	for _, e := range vs.entries {
		if e.Deleted || e.Project == "" {
			continue
		}
		seen[e.Project] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Get returns a single vault entry by (project, name). Returns an error
// if the entry is absent or tombstoned.
func (vs *VaultStore) Get(project, name string) (*VaultEntry, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	e, ok := vs.entries[vaultKey(project, name)]
	if !ok || e.Deleted {
		label := name
		if project != "" {
			label = project + "/" + name
		}
		return nil, fmt.Errorf("vault entry %q not found", label)
	}
	out := e
	return &out, nil
}

// Set creates or updates a vault entry and persists to disk. Name (and
// optional Project) are validated. CreatedAt is preserved on update;
// UpdatedAt + DeviceID are stamped automatically.
func (vs *VaultStore) Set(entry VaultEntry) error {
	if err := validateVaultName(entry.Name, "name"); err != nil {
		return err
	}
	if err := validateVaultProject(entry.Project); err != nil {
		return err
	}
	if entry.Value == "" {
		return fmt.Errorf("value cannot be empty")
	}
	if entry.Category == "" {
		entry.Category = "custom"
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	now := time.Now().UnixMilli()
	key := vaultKey(entry.Project, entry.Name)
	if existing, ok := vs.entries[key]; ok && !existing.Deleted {
		entry.CreatedAt = existing.CreatedAt
	} else {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	entry.DeviceID = vs.deviceID
	entry.Deleted = false

	vs.entries[key] = entry
	return vs.persist()
}

// Delete soft-deletes a vault entry (leaves a tombstone) and persists.
// Returns an error if the entry does not exist.
func (vs *VaultStore) Delete(project, name string) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	key := vaultKey(project, name)
	existing, ok := vs.entries[key]
	if !ok || existing.Deleted {
		label := name
		if project != "" {
			label = project + "/" + name
		}
		return fmt.Errorf("vault entry %q not found", label)
	}
	tomb := VaultEntry{
		Name:      existing.Name,
		Project:   existing.Project,
		Category:  existing.Category,
		CreatedAt: existing.CreatedAt,
		UpdatedAt: time.Now().UnixMilli(),
		DeviceID:  vs.deviceID,
		Deleted:   true,
	}
	vs.entries[key] = tomb
	vs.gcTombstonesLocked()
	return vs.persist()
}

// gcTombstonesLocked drops delete markers older than vaultTombstoneTTL.
// Caller must hold the write lock.
func (vs *VaultStore) gcTombstonesLocked() {
	cutoff := time.Now().Add(-vaultTombstoneTTL).UnixMilli()
	for key, e := range vs.entries {
		if e.Deleted && e.UpdatedAt < cutoff {
			delete(vs.entries, key)
		}
	}
}

// Digest returns the per-entry (project, name, updatedAt, deleted)
// records — no secret values — for use by the sync protocol.
func (vs *VaultStore) Digest() []VaultDigestEntry {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	out := make([]VaultDigestEntry, 0, len(vs.entries))
	for _, e := range vs.entries {
		out = append(out, VaultDigestEntry{
			Name:      e.Name,
			Project:   e.Project,
			UpdatedAt: e.UpdatedAt,
			Deleted:   e.Deleted,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// EntriesNewerThan returns full entries (including Value, including
// tombstones) where UpdatedAt > peer's UpdatedAt for that (project, name).
// Entries the peer doesn't have yet are included with their full value.
// Used by /vault/sync to answer "give me everything I'm missing or stale on".
func (vs *VaultStore) EntriesNewerThan(peer []VaultDigestEntry) []VaultEntry {
	peerIdx := make(map[string]int64, len(peer))
	for _, d := range peer {
		peerIdx[vaultKey(d.Project, d.Name)] = d.UpdatedAt
	}
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	out := make([]VaultEntry, 0)
	for key, e := range vs.entries {
		if pu, ok := peerIdx[key]; ok && pu >= e.UpdatedAt {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Upsert applies an inbound sync revision. It accepts the entry if its
// UpdatedAt is strictly greater than what we have locally. Returns
// (accepted, error) so the caller can log stats. The sync path never
// re-stamps DeviceID or UpdatedAt — the inbound revision already carries
// the original source's attribution.
func (vs *VaultStore) Upsert(entry VaultEntry) (bool, error) {
	if err := validateVaultName(entry.Name, "name"); err != nil {
		return false, err
	}
	if err := validateVaultProject(entry.Project); err != nil {
		return false, err
	}
	if entry.UpdatedAt == 0 {
		return false, fmt.Errorf("upsert requires updated_at")
	}
	if !entry.Deleted && entry.Value == "" {
		// Non-tombstone with no value is invalid — reject.
		return false, fmt.Errorf("upsert requires value or deleted=true")
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	key := vaultKey(entry.Project, entry.Name)
	existing, have := vs.entries[key]
	if have && existing.UpdatedAt >= entry.UpdatedAt {
		return false, nil
	}
	if entry.CreatedAt == 0 {
		if have {
			entry.CreatedAt = existing.CreatedAt
		} else {
			entry.CreatedAt = entry.UpdatedAt
		}
	}
	vs.entries[key] = entry
	vs.gcTombstonesLocked()
	return true, vs.persist()
}

// EnvExport returns (project-scoped + global) env-var-style lines suitable
// for shell sourcing:
//
//	export APP_STORE_KEY_ID='abc'
//	export APP_STORE_KEY_ISSUER='def'
//
// If includeGlobal is true, global entries (Project="") are also emitted,
// with project-scoped entries winning on a name collision. Values are
// single-quote-escaped.
func (vs *VaultStore) EnvExport(project string, includeGlobal bool) string {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	picked := map[string]VaultEntry{}
	if includeGlobal {
		for _, e := range vs.entries {
			if e.Deleted || e.Project != "" {
				continue
			}
			picked[e.Name] = e
		}
	}
	for _, e := range vs.entries {
		if e.Deleted || e.Project != project {
			continue
		}
		picked[e.Name] = e
	}
	names := make([]string, 0, len(picked))
	for n := range picked {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		v := picked[n].Value
		sb.WriteString("export ")
		sb.WriteString(n)
		sb.WriteString("='")
		sb.WriteString(strings.ReplaceAll(v, "'", `'"'"'`))
		sb.WriteString("'\n")
	}
	return sb.String()
}

// persist encrypts and atomically writes the vault to disk, taking an
// advisory cross-process file lock on <vault>.lock while the write +
// rename happens. Without this, two `yaver vault add` invocations in
// separate terminals each load the current file, make their edit,
// and save — last save silently wins and drops the other's entry.
// Caller must hold vs.mu write lock (for in-process protection).
func (vs *VaultStore) persist() error {
	lock, err := vaultFileLock(vs.path + ".lock")
	if err != nil {
		// Fail soft: in-process mutex is still protecting us, this
		// just means sibling-process protection is off. Log once so
		// the issue is visible without blocking the write.
		log.Printf("[vault] flock(%s) failed: %v — writes are not safe across parallel processes", vs.path+".lock", err)
	}
	defer vaultFileUnlock(lock)

	salt := make([]byte, saltLen)
	existing, err := os.ReadFile(vs.path)
	if err == nil && len(existing) >= saltLen {
		copy(salt, existing[:saltLen])
	} else {
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return fmt.Errorf("generate salt: %w", err)
		}
	}
	return vs.save(salt)
}

// save encrypts entries and writes atomically with the given salt. The
// on-disk format is a JSON array (v2), sorted by (Project, Name) for
// reproducibility.
func (vs *VaultStore) save(salt []byte) error {
	arr := make([]VaultEntry, 0, len(vs.entries))
	for _, e := range vs.entries {
		arr = append(arr, e)
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].Project != arr[j].Project {
			return arr[i].Project < arr[j].Project
		}
		return arr[i].Name < arr[j].Name
	})
	plaintext, err := json.Marshal(arr)
	if err != nil {
		return fmt.Errorf("marshal vault: %w", err)
	}

	var nonce [nonceLen]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext := secretbox.Seal(nil, plaintext, &nonce, &vs.key)

	out := make([]byte, 0, saltLen+nonceLen+len(ciphertext))
	out = append(out, salt...)
	out = append(out, nonce[:]...)
	out = append(out, ciphertext...)

	tmpPath := vs.path + ".tmp"
	if err := os.WriteFile(tmpPath, out, 0600); err != nil {
		return fmt.Errorf("write vault tmp: %w", err)
	}
	if err := os.Rename(tmpPath, vs.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename vault: %w", err)
	}
	return nil
}

// RekeyTo re-encrypts the in-memory entries under a new passphrase
// using a fresh salt + nonce, then atomically replaces vault.enc.
//
// Called from SetAuthToken on every auth-token rotation so the vault
// stays openable under the new token without forcing the user to
// re-enter YAVER_VAULT_PASSPHRASE. The vault must already be
// unlocked — the caller is expected to have opened it under the
// previous passphrase.
func (vs *VaultStore) RekeyTo(newPassphrase string) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if !vs.unlocked {
		return fmt.Errorf("vault is locked — open it first")
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("generate salt: %w", err)
	}
	vs.key = deriveKey([]byte(newPassphrase), salt)
	return vs.save(salt)
}

// ExportPlaintext exports all vault entries as plaintext JSON. Use with
// caution — this is unencrypted.
func (vs *VaultStore) ExportPlaintext() ([]byte, error) {
	vs.mu.RLock()
	defer vs.mu.RUnlock()

	arr := make([]VaultEntry, 0, len(vs.entries))
	for _, e := range vs.entries {
		if e.Deleted {
			continue
		}
		arr = append(arr, e)
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].Project != arr[j].Project {
			return arr[i].Project < arr[j].Project
		}
		return arr[i].Name < arr[j].Name
	})
	return json.MarshalIndent(arr, "", "  ")
}

// ImportPlaintext imports vault entries from a plaintext JSON array.
// Existing entries at the same (project, name) are overwritten (and
// stamped with the current device / timestamp).
func (vs *VaultStore) ImportPlaintext(data []byte) (int, error) {
	var entries []VaultEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return 0, fmt.Errorf("parse import data: %w", err)
	}

	vs.mu.Lock()
	defer vs.mu.Unlock()

	now := time.Now().UnixMilli()
	for _, e := range entries {
		if e.Name == "" || e.Value == "" {
			continue
		}
		if err := validateVaultName(e.Name, "name"); err != nil {
			return 0, err
		}
		if err := validateVaultProject(e.Project); err != nil {
			return 0, err
		}
		if e.Category == "" {
			e.Category = "custom"
		}
		if e.CreatedAt == 0 {
			e.CreatedAt = now
		}
		e.UpdatedAt = now
		e.DeviceID = vs.deviceID
		e.Deleted = false
		vs.entries[vaultKey(e.Project, e.Name)] = e
	}

	if err := vs.persist(); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// DerivePassphraseFromToken derives a vault passphrase from a Yaver auth
// token. This provides seamless vault unlock without the user needing a
// separate passphrase.
func DerivePassphraseFromToken(token string) string {
	h := sha256.Sum256([]byte("yaver-vault:" + token))
	return fmt.Sprintf("%x", h[:])
}
