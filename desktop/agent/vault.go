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
	key      [32]byte // v1: Argon2id(passphrase, salt). v2: master key from keychain.
	deviceID string   // stamped into UpdatedAt on every write
	entries  map[string]VaultEntry
	unlocked bool
	// formatV2 controls the on-disk layout. True ⇒ write `[magic][nonce][ct]`
	// and key is the master key directly. False ⇒ legacy v1
	// `[salt][nonce][ct]` with the key derived from a passphrase. New
	// vaults default to v2; v1 vaults opened via the passphrase chain
	// flip this on migration (RekeyToMasterKey).
	formatV2 bool
}

// Argon2id parameters for key derivation (legacy v1 format only — v2 uses
// the master key from vault_keychain.go directly, no derivation needed).
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
	nonceLen     = 24
)

// vaultV2Magic distinguishes the new master-key file format from the
// legacy Argon2id-passphrase one. Random salt bytes match this exact
// sequence with probability 1/2^32 — negligible vs the simpler 1-byte
// check, and the trailing NUL makes diff/grep output unambiguous.
const vaultV2Magic = "YV2\x00"

// ErrVaultIsLegacyV1 is returned by NewVaultStoreV2 when the on-disk file
// is in legacy v1 format (no magic header). Callers handle migration:
// open via the v1 passphrase chain, then call RekeyToMasterKey to flip
// the file to v2 under the stable master key.
var ErrVaultIsLegacyV1 = fmt.Errorf("vault is in legacy v1 format (passphrase-encrypted); use the v1 chain + RekeyToMasterKey to migrate")

// ErrVaultIsV2 is the converse: callers using the legacy
// NewVaultStoreWithDevice (passphrase-based) hit a file with the v2
// magic header. Cleaner than letting Argon2id silently produce garbage
// + a "wrong passphrase" misdirection. Callers should retry via
// NewVaultStoreV2 with the master key.
var ErrVaultIsV2 = fmt.Errorf("vault is in v2 format (master-key encrypted); use NewVaultStoreV2 instead")

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

	// Refuse v2 files explicitly so callers using the legacy passphrase
	// constructor get a routable error instead of a misleading "wrong
	// passphrase" (Argon2id on the magic + nonce bytes would silently
	// produce a bogus key).
	if hasV2Magic(data) {
		return nil, ErrVaultIsV2
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

// NewVaultStoreV2 opens (or creates) a vault under the new file format —
// `[magic][nonce][ciphertext]` with the master key from
// vault_keychain.EnsureMasterKey as the AEAD key (not Argon2id from a
// rotating passphrase). Returns ErrVaultIsLegacyV1 when the file exists
// but lacks the v2 magic; the caller is expected to open it via the v1
// passphrase chain, then call RekeyToMasterKey to migrate.
//
// The vs.formatV2 flag is set so all subsequent persists write the new
// layout.
func NewVaultStoreV2(masterKey [32]byte, deviceID string) (*VaultStore, error) {
	vaultPath, err := VaultPath()
	if err != nil {
		return nil, err
	}

	vs := &VaultStore{
		path:     vaultPath,
		deviceID: deviceID,
		entries:  make(map[string]VaultEntry),
		key:      masterKey,
		formatV2: true,
	}

	// Same crash-recovery hint as v1.
	if st, sErr := os.Stat(vaultPath + ".tmp"); sErr == nil {
		log.Printf("[vault] stale %s (size %d, mtime %s) — a previous save likely crashed before commit. Inspect + remove manually once you've confirmed the live vault is OK.",
			vaultPath+".tmp", st.Size(), st.ModTime().UTC().Format(time.RFC3339))
	}

	data, err := os.ReadFile(vaultPath)
	if err != nil {
		if os.IsNotExist(err) {
			// First run under v2 — create an empty file in the new
			// format so subsequent opens skip the legacy fallback.
			vs.unlocked = true
			if err := vs.save(nil); err != nil {
				return nil, fmt.Errorf("create v2 vault: %w", err)
			}
			return vs, nil
		}
		return nil, fmt.Errorf("read vault: %w", err)
	}

	if !hasV2Magic(data) {
		return nil, ErrVaultIsLegacyV1
	}
	header := len(vaultV2Magic)
	if len(data) < header+nonceLen+secretbox.Overhead {
		return nil, fmt.Errorf("v2 vault file too small — corrupted?")
	}
	var nonce [nonceLen]byte
	copy(nonce[:], data[header:header+nonceLen])
	plaintext, ok := secretbox.Open(nil, data[header+nonceLen:], &nonce, &vs.key)
	if !ok {
		// Wrong master key — usually means the file was encrypted on
		// another machine OR ~/.yaver/master.key was swapped. Surface
		// the same error string the v1 path uses so callers can pattern-
		// match if they need to ("wrong passphrase or corrupted vault").
		return nil, fmt.Errorf("wrong passphrase or corrupted vault")
	}
	if err := vs.loadEntries(plaintext); err != nil {
		return nil, err
	}
	vs.unlocked = true
	return vs, nil
}

// RekeyToMasterKey re-encrypts the (already-decrypted) vault under the
// supplied master key + the v2 file format. Called by the migration
// path: openVaultE successfully decrypts a v1 vault via the
// passphrase chain, then this flips the file to v2 so future opens go
// through NewVaultStoreV2 + are no longer tied to the rotating auth
// token. Idempotent — calling it on an already-v2 store just rotates
// the key + nonce (cheap, useful for `yaver vault migrate` or rotation).
func (vs *VaultStore) RekeyToMasterKey(masterKey [32]byte) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if !vs.unlocked {
		return fmt.Errorf("vault is locked — open it first")
	}
	vs.key = masterKey
	vs.formatV2 = true
	return vs.save(nil)
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
	out.Value = expandHomeRef(out.Value)
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
	// Portable path storage: if the value is an absolute path under
	// the writer's HOME, rewrite to `~/...` so the same vault entry
	// resolves correctly on every machine the user signs into. Read
	// paths (Get / EnvExport) expand `~/` and `$HOME/` back to the
	// runtime HOME. Non-path values pass through untouched.
	entry.Value = normalizeHomeRefForStorage(entry.Value)

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
		v := expandHomeRef(picked[n].Value)
		sb.WriteString("export ")
		sb.WriteString(n)
		sb.WriteString("='")
		sb.WriteString(strings.ReplaceAll(v, "'", `'"'"'`))
		sb.WriteString("'\n")
	}
	return sb.String()
}

// normalizeHomeRefForStorage rewrites absolute paths under the writer's
// HOME to `~/...` so the entry is portable across every machine the
// user signs into. Leaves already-portable paths (`~/`, `$HOME/`),
// non-path values, and absolute paths NOT under HOME untouched.
func normalizeHomeRefForStorage(value string) string {
	if value == "" {
		return value
	}
	// Already portable — pass through.
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, "$HOME/") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	home = strings.TrimRight(home, "/")
	if strings.HasPrefix(value, home+"/") {
		return "~/" + value[len(home)+1:]
	}
	return value
}

// expandHomeRef resolves `~/` or `$HOME/` prefixes against the runtime
// HOME so consumers receive a usable absolute path. Other values pass
// through unchanged.
func expandHomeRef(value string) string {
	if value == "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	home = strings.TrimRight(home, "/")
	if strings.HasPrefix(value, "~/") {
		return home + "/" + value[2:]
	}
	if strings.HasPrefix(value, "$HOME/") {
		return home + "/" + value[6:]
	}
	return value
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

	// v2 format has no salt — the master key is stable and the magic
	// header replaces the salt's role at offset 0. v1 preserves the
	// existing salt so the key derivation stays consistent across
	// writes; new v1 vaults get a fresh salt.
	if vs.formatV2 {
		return vs.save(nil)
	}
	salt := make([]byte, saltLen)
	existing, err := os.ReadFile(vs.path)
	if err == nil && len(existing) >= saltLen && !hasV2Magic(existing) {
		copy(salt, existing[:saltLen])
	} else {
		if _, err := io.ReadFull(rand.Reader, salt); err != nil {
			return fmt.Errorf("generate salt: %w", err)
		}
	}
	return vs.save(salt)
}

// hasV2Magic returns true iff data begins with vaultV2Magic. Cheap probe
// used by both the reader (decide which format) and the v1 persist path
// (refuse to read v2 bytes as v1 salt).
func hasV2Magic(data []byte) bool {
	return len(data) >= len(vaultV2Magic) && string(data[:len(vaultV2Magic)]) == vaultV2Magic
}

// save encrypts entries and writes atomically. v2 format ⇒
// `[magic 4B][nonce 24B][secretbox(JSON)]`; v1 ⇒ `[salt 16B][nonce 24B]
// [secretbox(JSON)]`. salt is ignored when formatV2 is true (pass nil).
// The plaintext is a JSON array sorted by (Project, Name) for reproducible
// diffs across syncs.
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

	var out []byte
	if vs.formatV2 {
		out = make([]byte, 0, len(vaultV2Magic)+nonceLen+len(ciphertext))
		out = append(out, vaultV2Magic...)
	} else {
		out = make([]byte, 0, saltLen+nonceLen+len(ciphertext))
		out = append(out, salt...)
	}
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
