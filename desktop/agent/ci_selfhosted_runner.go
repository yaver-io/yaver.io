package main

// ci_selfhosted_runner.go — Model 1 "self-hosted runner adapter": register a
// Yaver box as a GitHub Actions / GitLab self-hosted runner so the user's
// EXISTING workflows (`runs-on: [self-hosted, yaver]`) run on Yaver hardware
// and GitHub bills $0 for the minutes. The managed-cloud CI-absorption wedge —
// see docs/yaver-managed-cloud-ci-absorption.md.
//
// Distinct from ci_runner.go: that is Yaver-NATIVE CI (`.yaver/ci.yaml`,
// docker-per-step — Model 2). THIS file keeps the user on GitHub/GitLab and
// redirects the compute, capturing the whole bill with zero workflow
// re-authoring. It is the already-reserved RunnerJobWorkflow ("workflow") kind
// from runner.go made live.
//
// Lifecycle — EPHEMERAL SUPERVISOR. GitHub's documented pattern for untrusted /
// autoscaled compute is `--ephemeral`: a runner claims exactly one job, runs
// it, then deregisters. A CISupervisor per registration loops:
//   mint registration token → run ONE ephemeral runner (host or container) →
//   on exit: meter + teardown → re-register.
// So: one job = one ephemeral runner = one isolated work dir = one RunnerRun.
//
// SECURITY. In Model 1, secrets live on GitHub's side — GitHub injects repo/org
// Actions secrets into the job env at claim time, so Yaver's vault is NOT on
// this path. That is exactly why isolation matters: those secrets land in the
// job env in plaintext. `--ephemeral` + a fresh per-run work dir +
// teardown-on-Finish (kill procs, wipe work dir) is the safety story; the
// operator-fleet "gap C" container jail is the stronger form (isolation:
// container). Default: PRIVATE repos only; public/fork PRs stay disabled.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// githubRunnerVersion is the pinned actions/runner release downloaded on first
// use. Bump deliberately; the runner self-updates against GitHub's required
// version on connect, so a slightly old pin still works.
const githubRunnerVersion = "2.321.0"

// CIProvider (the upstream forge) + its CIGitHub / CIGitLab consts are declared
// in ci.go and reused here.

// CIRunWhere records the hardware class a CI run executed on. It selects both
// the COGS (free on owned/operator hardware) and the `provider` label on the
// managedUsage meter row.
type CIRunWhere string

const (
	CIWhereOwn      CIRunWhere = "self-hosted"    // the user's own registered box — free
	CIWhereOperator CIRunWhere = "operator-fleet" // Yaver-operated free public fleet — free
	CIWhereCloud    CIRunWhere = "yaver-cloud"    // Yaver-rented Hetzner / Mac colo — metered
)

// CIIsolation selects how a claimed job is sandboxed. Container is the safe
// default; host is opt-in for trusted PRIVATE work on a dedicated box.
type CIIsolation string

const (
	CIIsolationContainer CIIsolation = "container"
	CIIsolationHost      CIIsolation = "host"
)

// CIRunnerRegistration is one repo/org bound to this box as a self-hosted
// runner. Persisted to ~/.yaver/runner/ci-registrations.json (mirrors
// runner.go's jobs.json layout). HOST-LOCAL only — carries a forge target +
// labels, never a token, never crosses the Convex boundary.
type CIRunnerRegistration struct {
	Provider      CIProvider  `json:"provider"`
	Scope         string      `json:"scope"`          // "repo" | "org"
	Target        string      `json:"target"`         // "owner/repo" | "org-name" | gitlab project id
	Host          string      `json:"host,omitempty"` // GHES / self-managed GitLab host; empty = SaaS
	Labels        []string    `json:"labels,omitempty"`
	Isolation     CIIsolation `json:"isolation,omitempty"`
	Where         CIRunWhere  `json:"where,omitempty"`
	MaxConcurrent int         `json:"maxConcurrent,omitempty"`
	PrivateOnly   bool        `json:"privateOnly,omitempty"` // refuse public/fork-PR jobs (default true)
	CreatedAt     int64       `json:"createdAt,omitempty"`
	UpdatedAt     int64       `json:"updatedAt,omitempty"`
}

func (r CIRunnerRegistration) key() string {
	return string(r.Provider) + ":" + r.Target
}

// runnerLabels is the label set advertised to the forge — the right-hand side
// of `runs-on: [self-hosted, yaver, ...]`. self-hosted + yaver, then this box's
// LocalCapabilities() (os:*, arch:*, host:*), then operator extras. De-duped,
// order-preserving.
func (r CIRunnerRegistration) runnerLabels() []string {
	all := []string{"self-hosted", "yaver"}
	all = append(all, LocalCapabilities()...)
	all = append(all, r.Labels...)
	seen := map[string]bool{}
	uniq := make([]string, 0, len(all))
	for _, l := range all {
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		uniq = append(uniq, l)
	}
	return uniq
}

// forgeURL is the --url config arg: the repo/org/project web URL on the host.
func (r CIRunnerRegistration) forgeURL() string {
	host := strings.TrimSpace(r.Host)
	switch r.Provider {
	case CIGitLab:
		if host == "" {
			host = "gitlab.com"
		}
		return "https://" + host
	default: // GitHub
		if host == "" {
			host = "github.com"
		}
		return "https://" + host + "/" + r.Target
	}
}

// --- Registration store --------------------------------------------------

type CIRegistrationStore struct {
	mu   sync.Mutex
	regs map[string]*CIRunnerRegistration
	path string
}

func NewCIRegistrationStore() *CIRegistrationStore {
	s := &CIRegistrationStore{regs: map[string]*CIRunnerRegistration{}}
	if dir, err := ConfigDir(); err == nil {
		root := filepath.Join(dir, "runner")
		if err := os.MkdirAll(root, 0700); err == nil {
			s.path = filepath.Join(root, "ci-registrations.json")
		}
	}
	s.load()
	return s
}

func (s *CIRegistrationStore) load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var regs map[string]*CIRunnerRegistration
	if err := json.Unmarshal(data, &regs); err != nil {
		log.Printf("[ci] failed to parse ci-registrations.json: %v — starting empty", err)
		return
	}
	s.regs = regs
}

func (s *CIRegistrationStore) saveLocked() {
	if s.path == "" {
		return
	}
	data, err := json.MarshalIndent(s.regs, "", "  ")
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.Printf("[ci] write %s failed: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("[ci] rename %s failed: %v", tmp, err)
	}
}

// Add upserts a registration with safe defaults (container isolation,
// private-only, single-concurrency).
func (s *CIRegistrationStore) Add(r CIRunnerRegistration) (CIRunnerRegistration, error) {
	if r.Provider == "" {
		return CIRunnerRegistration{}, fmt.Errorf("ci registration requires a provider")
	}
	if strings.TrimSpace(r.Target) == "" {
		return CIRunnerRegistration{}, fmt.Errorf("ci registration requires a target (owner/repo or org)")
	}
	if r.Scope == "" {
		r.Scope = "repo"
	}
	if r.Isolation == "" {
		r.Isolation = CIIsolationContainer
	}
	if r.Where == "" {
		r.Where = CIWhereOwn
	}
	if r.MaxConcurrent <= 0 {
		r.MaxConcurrent = 1
	}
	// Safe default: refuse public/fork-PR jobs until the jail + approval gate
	// exist. Operators opt OUT explicitly (private+host only).
	if !r.PrivateOnly {
		r.PrivateOnly = true
	}
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.regs[r.key()]; ok {
		r.CreatedAt = existing.CreatedAt
	} else {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	stored := r
	s.regs[r.key()] = &stored
	s.saveLocked()
	return stored, nil
}

func (s *CIRegistrationStore) List() []CIRunnerRegistration {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CIRunnerRegistration, 0, len(s.regs))
	for _, r := range s.regs {
		out = append(out, *r)
	}
	return out
}

func (s *CIRegistrationStore) Get(key string) (CIRunnerRegistration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.regs[key]
	if !ok {
		return CIRunnerRegistration{}, false
	}
	return *r, true
}

func (s *CIRegistrationStore) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.regs[key]; !ok {
		return fmt.Errorf("ci registration %q not found", key)
	}
	delete(s.regs, key)
	s.saveLocked()
	return nil
}

// --- Metering + savings ledger ------------------------------------------

// CIRunResult is the per-run fact handed to the meter callback on Finish.
type CIRunResult struct {
	RunID                      string
	RegistrationKey            string
	RunnerOS                   string // "linux" | "macos" | "windows"
	Where                      CIRunWhere
	DurationMin                float64
	ProviderCostCents          int // raw COGS — 0 on owned/operator hardware
	WouldHaveCostUpstreamCents int // GitHub Actions equivalent — the savings anchor
	OK                         bool
}

// CIMeterFunc receives one CIRunResult per completed run.
type CIMeterFunc func(CIRunResult)

// githubActionsCentsPerMin is GitHub's published per-minute price (USD cents)
// per runner OS. The 10x macOS / 2x Windows multipliers are already baked in.
var githubActionsCentsPerMin = map[string]float64{
	"linux":   0.8, // $0.008/min
	"macos":   8.0, // $0.080/min
	"windows": 1.6, // $0.016/min
}

func githubActionsUpstreamCents(runnerOS string, minutes float64) int {
	per, ok := githubActionsCentsPerMin[strings.ToLower(runnerOS)]
	if !ok {
		per = githubActionsCentsPerMin["linux"]
	}
	return int(math.Ceil(per * minutes))
}

// ciCogsCentsPerMin is the raw COGS per wall-clock runner-minute by hardware
// class. Owned + operator hardware is FREE; only Yaver-rented cloud is metered.
func ciCogsCentsPerMin(where CIRunWhere, runnerOS string) float64 {
	switch where {
	case CIWhereOwn, CIWhereOperator:
		return 0
	case CIWhereCloud:
		if strings.ToLower(runnerOS) == "macos" {
			return 0.25 // Mac colo ~$0.15/hr
		}
		return 0.015 // Hetzner-class Linux box, wall-clock minute
	}
	return 0
}

// defaultCIMeter is the wired meter: it always appends to a HOST-LOCAL savings
// ledger (~/.yaver/runner/ci-savings.jsonl) — the durable "you saved $X"
// artifact that works with zero backend — and best-effort debits the Convex
// wallet via the public managedMeter:recordCIUsageFromAgent mutation (dormant
// until YAVER_MANAGED_METER_LIVE + the per-user `ci` opt-in; callMutation is
// silent-on-failure so this is a no-op when not signed in / not launched).
func defaultCIMeter(res CIRunResult) {
	saved := res.WouldHaveCostUpstreamCents - res.ProviderCostCents
	if saved < 0 {
		saved = 0
	}
	log.Printf("[ci] run %s on %s (%s) %.2fmin — charged %d¢, GitHub would bill %d¢, saved %d¢",
		res.RunID, res.RunnerOS, res.Where, res.DurationMin,
		res.ProviderCostCents, res.WouldHaveCostUpstreamCents, saved)

	appendCISavingsLedger(res, saved)

	if globalConvexSync != nil {
		globalConvexSync.callMutation("managedMeter:recordCIUsageFromAgent", map[string]interface{}{
			"deviceId":                   localDeviceID(),
			"provider":                   string(res.Where),
			"unit":                       ciMeterUnit(res.RunnerOS),
			"quantity":                   res.DurationMin,
			"providerCostCents":          res.ProviderCostCents,
			"wouldHaveCostUpstreamCents": res.WouldHaveCostUpstreamCents,
			"ref":                        res.RunID,
		})
	}
}

func ciMeterUnit(runnerOS string) string {
	if strings.ToLower(runnerOS) == "macos" {
		return "mac-min"
	}
	return "cpu-min"
}

// appendCISavingsLedger writes one JSON line per run. Non-secret counters only
// (id/label/timestamp/cents) — same privacy class as the Convex meter row.
func appendCISavingsLedger(res CIRunResult, savedCents int) {
	dir, err := ConfigDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "runner", "ci-savings.jsonl")
	row, _ := json.Marshal(map[string]interface{}{
		"runId":                      res.RunID,
		"registration":               res.RegistrationKey,
		"runnerOS":                   res.RunnerOS,
		"where":                      string(res.Where),
		"durationMin":                res.DurationMin,
		"chargedCents":               res.ProviderCostCents,
		"wouldHaveCostUpstreamCents": res.WouldHaveCostUpstreamCents,
		"savedCents":                 savedCents,
		"ok":                         res.OK,
		"at":                         time.Now().UnixMilli(),
	})
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(row, '\n'))
}

// CISavingsSummary aggregates the local ledger for the "you saved $X" UI.
type CISavingsSummary struct {
	Runs          int `json:"runs"`
	ChargedCents  int `json:"chargedCents"`
	UpstreamCents int `json:"wouldHaveCostUpstreamCents"`
	SavedCents    int `json:"savedCents"`
}

func readCISavingsSummary() CISavingsSummary {
	var sum CISavingsSummary
	dir, err := ConfigDir()
	if err != nil {
		return sum
	}
	f, err := os.Open(filepath.Join(dir, "runner", "ci-savings.jsonl"))
	if err != nil {
		return sum
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var row struct {
			ChargedCents  int `json:"chargedCents"`
			UpstreamCents int `json:"wouldHaveCostUpstreamCents"`
			SavedCents    int `json:"savedCents"`
		}
		if json.Unmarshal(sc.Bytes(), &row) != nil {
			continue
		}
		sum.Runs++
		sum.ChargedCents += row.ChargedCents
		sum.UpstreamCents += row.UpstreamCents
		sum.SavedCents += row.SavedCents
	}
	return sum
}

// --- Manager -------------------------------------------------------------

// CIManager owns the registration store + one supervisor per active
// registration. A process-wide singleton; the run lifecycle uses the shared
// HTTPServer RunnerStore so CI runs surface in the same "Runs" tab.
type CIManager struct {
	mu      sync.Mutex
	regs    *CIRegistrationStore
	runs    *RunnerStore
	limiter *runnerLimiter
	meter   CIMeterFunc
	sups    map[string]*CISupervisor
	ctx     context.Context
}

var (
	globalCIManager   *CIManager
	globalCIManagerMu sync.Mutex
)

// ensureCIManager lazily builds the singleton bound to the given shared run
// store. Subsequent calls return the same manager (the first run store wins).
func ensureCIManager(runs *RunnerStore) *CIManager {
	globalCIManagerMu.Lock()
	defer globalCIManagerMu.Unlock()
	if globalCIManager == nil {
		globalCIManager = &CIManager{
			regs:    NewCIRegistrationStore(),
			runs:    runs,
			limiter: newRunnerLimiter(),
			meter:   defaultCIMeter,
			sups:    map[string]*CISupervisor{},
			ctx:     context.Background(),
		}
	}
	if globalCIManager.runs == nil {
		globalCIManager.runs = runs
	}
	return globalCIManager
}

// Register persists a registration and starts (or restarts) its supervisor.
func (m *CIManager) Register(r CIRunnerRegistration) (CIRunnerRegistration, error) {
	stored, err := m.regs.Add(r)
	if err != nil {
		return CIRunnerRegistration{}, err
	}
	m.startSupervisor(stored)
	return stored, nil
}

// Unregister stops the supervisor and forgets the registration.
func (m *CIManager) Unregister(key string) error {
	m.mu.Lock()
	if sv, ok := m.sups[key]; ok {
		delete(m.sups, key)
		m.mu.Unlock()
		sv.Stop()
	} else {
		m.mu.Unlock()
	}
	return m.regs.Remove(key)
}

// ResumeAll starts a supervisor for every persisted registration. Called once
// on agent boot so registered runners survive a restart.
func (m *CIManager) ResumeAll(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	for _, r := range m.regs.List() {
		m.startSupervisor(r)
	}
}

func (m *CIManager) startSupervisor(r CIRunnerRegistration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.sups[r.key()]; ok {
		go old.Stop()
		delete(m.sups, r.key())
	}
	if m.runs == nil {
		log.Printf("[ci] no run store yet — supervisor for %s deferred", r.key())
		return
	}
	sv := NewCISupervisor(r, m.runs, m.limiter, m.meter)
	m.sups[r.key()] = sv
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go sv.Run(ctx)
}

// Status reports each registration + whether its supervisor is live, plus the
// local savings summary.
func (m *CIManager) Status() map[string]interface{} {
	m.mu.Lock()
	live := map[string]bool{}
	for k := range m.sups {
		live[k] = true
	}
	m.mu.Unlock()
	regs := m.regs.List()
	rows := make([]map[string]interface{}, 0, len(regs))
	for _, r := range regs {
		rows = append(rows, map[string]interface{}{
			"key":           r.key(),
			"provider":      string(r.Provider),
			"target":        r.Target,
			"scope":         r.Scope,
			"labels":        r.runnerLabels(),
			"isolation":     string(r.Isolation),
			"where":         string(r.Where),
			"maxConcurrent": r.MaxConcurrent,
			"live":          live[r.key()],
		})
	}
	return map[string]interface{}{
		"registrations": rows,
		"savings":       readCISavingsSummary(),
	}
}

// resumeCIRunnersOnBoot is the single boot hook (called from main.go) so
// persisted CI runners come back after an agent restart.
func resumeCIRunnersOnBoot(ctx context.Context, runs *RunnerStore) {
	ensureCIManager(runs).ResumeAll(ctx)
}

// --- Supervisor ----------------------------------------------------------

type CISupervisor struct {
	reg     CIRunnerRegistration
	store   *RunnerStore
	limiter *runnerLimiter
	meter   CIMeterFunc

	stop chan struct{}
	done chan struct{}
}

func NewCISupervisor(reg CIRunnerRegistration, store *RunnerStore, limiter *runnerLimiter, meter CIMeterFunc) *CISupervisor {
	return &CISupervisor{
		reg:     reg,
		store:   store,
		limiter: limiter,
		meter:   meter,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Run drives the ephemeral loop until Stop() or ctx cancellation. Each pass
// executes one isolated job. Transient errors back off; a misconfiguration
// (no token, missing toolchain) backs off to the cap rather than spinning.
func (sv *CISupervisor) Run(ctx context.Context) {
	defer close(sv.done)
	key := sv.reg.key()
	limiterKey := "ci:" + key
	log.Printf("[ci=%s] supervisor up — labels=%v isolation=%s where=%s",
		key, sv.reg.runnerLabels(), sv.reg.Isolation, sv.reg.Where)

	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-sv.stop:
			return
		default:
		}

		if !sv.limiter.tryAcquire(limiterKey, sv.reg.MaxConcurrent) {
			if !sleepCtx(ctx, 500*time.Millisecond) { // sleepCtx: false == cancelled
				return
			}
			continue
		}

		err := sv.runOneEphemeralRunner(ctx)
		sv.limiter.release(limiterKey)

		if err != nil {
			log.Printf("[ci=%s] ephemeral cycle failed: %v (backoff %s)", key, err, backoff)
			if !sleepCtx(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

func (sv *CISupervisor) Stop() {
	select {
	case <-sv.stop:
	default:
		close(sv.stop)
	}
	<-sv.done
}

// runOneEphemeralRunner performs exactly one claim→execute→teardown→meter
// cycle.
func (sv *CISupervisor) runOneEphemeralRunner(ctx context.Context) error {
	run := sv.store.Start(RunnerRun{
		JobName:     "ci:" + sv.reg.Target,
		Kind:        RunnerJobWorkflow,
		Pool:        strings.Join(sv.reg.runnerLabels(), ","),
		TriggeredBy: "webhook",
	})

	token, err := mintRunnerRegistrationToken(ctx, sv.reg)
	if err != nil {
		sv.store.Append(run.ID, "[ci] mint registration token: "+err.Error())
		sv.store.Finish(run.ID, -1, false)
		return err
	}

	started := time.Now()
	runnerOS, exitCode, execErr := runEphemeralRunner(ctx, sv.reg, token, run.ID, sv.store)
	durMin := time.Since(started).Minutes()
	timedOut := errors.Is(execErr, context.DeadlineExceeded)
	sv.store.Finish(run.ID, exitCode, timedOut)

	if sv.meter != nil {
		cogs := int(math.Ceil(ciCogsCentsPerMin(sv.reg.Where, runnerOS) * durMin))
		sv.meter(CIRunResult{
			RunID:                      run.ID,
			RegistrationKey:            sv.reg.key(),
			RunnerOS:                   runnerOS,
			Where:                      sv.reg.Where,
			DurationMin:                durMin,
			ProviderCostCents:          cogs,
			WouldHaveCostUpstreamCents: githubActionsUpstreamCents(runnerOS, durMin),
			OK:                         exitCode == 0 && !timedOut,
		})
	}
	return execErr
}

// --- SEAM 1: registration token ------------------------------------------

// mintRunnerRegistrationToken obtains a short-lived self-hosted-runner
// registration token from the forge using the locally-stored git provider
// credentials. The token is never persisted.
func mintRunnerRegistrationToken(ctx context.Context, reg CIRunnerRegistration) (string, error) {
	switch reg.Provider {
	case CIGitLab:
		token := detectGitLabToken(reg.Host)
		if token == "" {
			return "", fmt.Errorf("no GitLab token — run `yaver vault add gitlab-token --category git-credential --value <token>` or `git_connect`")
		}
		host := strings.TrimSpace(reg.Host)
		if host == "" {
			host = "gitlab.com"
		}
		// /user/runners needs a numeric project_id; we only carry the path
		// unless the operator passed an id. Honest error rather than a wrong
		// call.
		if !isNumeric(reg.Target) {
			return "", fmt.Errorf("gitlab self-hosted runner registration needs a numeric project_id as target (got %q)", reg.Target)
		}
		apiURL := fmt.Sprintf("https://%s/api/v4/user/runners?runner_type=project_type&project_id=%s", host, reg.Target)
		return fetchRegistrationToken(ctx, apiURL, "PRIVATE-TOKEN", token)
	default: // GitHub
		token := detectGitHubToken()
		if token == "" {
			return "", fmt.Errorf("no GitHub token — run `yaver vault add github-token --category git-credential --value <token>` or `git_connect`")
		}
		apiURL := githubRegistrationTokenURL(reg.Host, reg.Scope, reg.Target)
		return fetchRegistrationToken(ctx, apiURL, "Authorization", "Bearer "+token)
	}
}

// githubRegistrationTokenURL builds the registration-token endpoint. Repo scope
// needs only `repo` (already requested in git_oauth_device.go); org scope needs
// `admin:org`. GHES uses the /api/v3 base.
func githubRegistrationTokenURL(host, scope, target string) string {
	base := "https://api.github.com"
	if h := strings.TrimSpace(host); h != "" && !strings.EqualFold(h, "github.com") {
		base = "https://" + h + "/api/v3"
	}
	if strings.EqualFold(strings.TrimSpace(scope), "org") {
		return fmt.Sprintf("%s/orgs/%s/actions/runners/registration-token", base, target)
	}
	return fmt.Sprintf("%s/repos/%s/actions/runners/registration-token", base, target)
}

// fetchRegistrationToken POSTs to the registration-token endpoint and returns
// the `token` field. Factored out so it is exercisable against an httptest
// server.
func fetchRegistrationToken(ctx context.Context, apiURL, headerKey, headerVal string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(headerKey, headerVal)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("registration-token API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("parse registration-token response: %w", err)
	}
	if out.Token == "" {
		return "", fmt.Errorf("registration-token response had no token")
	}
	return out.Token, nil
}

func isNumeric(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// --- SEAM 2: ephemeral runner exec ---------------------------------------

// runEphemeralRunner downloads (once) and runs a single `--ephemeral` GitHub
// runner, piping output into the run log and tearing down the per-run work dir
// on return. Host mode runs directly; container mode runs the same flow inside
// the sandbox image with the runner dir mounted. Returns (runnerOS, exitCode,
// err). GitLab uses gitlab-runner (not yet wired) — host-runner path is GitHub.
func runEphemeralRunner(ctx context.Context, reg CIRunnerRegistration, token, runID string, store *RunnerStore) (string, int, error) {
	runnerOS := normalizeRunnerOS(runtime.GOOS)
	if reg.Provider == CIGitLab {
		return runnerOS, -1, fmt.Errorf("gitlab-runner exec not yet wired (registration token minted ok); use a GitHub repo or run gitlab-runner out of band")
	}

	runnerDir, err := ensureGitHubRunnerExtracted(ctx, githubRunnerVersion, store, runID)
	if err != nil {
		store.Append(runID, "[ci] runner download/extract: "+err.Error())
		return runnerOS, -1, err
	}

	work, err := ciPerRunWorkDir(runID)
	if err != nil {
		return runnerOS, -1, err
	}
	defer os.RemoveAll(work) // teardown: wipe the per-run work dir

	name := "yaver-" + safeRunSuffix(runID)
	labels := strings.Join(reg.runnerLabels(), ",")
	cfgArgs := ghRunnerConfigArgs(reg.forgeURL(), token, name, labels, work)

	if reg.Isolation == CIIsolationContainer {
		code, cerr := runGitHubRunnerInContainer(ctx, runnerDir, work, cfgArgs, runID, store)
		return runnerOS, code, cerr
	}
	code, herr := runGitHubRunnerOnHost(ctx, runnerDir, cfgArgs, runID, store)
	return runnerOS, code, herr
}

// ghRunnerConfigArgs builds the config.sh arg list for a one-shot ephemeral
// runner. Pure — unit-tested.
func ghRunnerConfigArgs(forgeURL, token, name, labels, workDir string) []string {
	return []string{
		"--url", forgeURL,
		"--token", token,
		"--ephemeral",
		"--unattended",
		"--replace",
		"--name", name,
		"--labels", labels,
		"--work", workDir,
	}
}

// runGitHubRunnerOnHost configures then runs the runner once, directly on the
// host. config.sh writes .runner/.credentials into runnerDir; ephemeral mode
// auto-deregisters after the single job.
func runGitHubRunnerOnHost(ctx context.Context, runnerDir string, cfgArgs []string, runID string, store *RunnerStore) (int, error) {
	cfg := exec.CommandContext(ctx, filepath.Join(runnerDir, "config.sh"), cfgArgs...)
	cfg.Dir = runnerDir
	if code, err := streamCmdToRun(store, runID, cfg); err != nil || code != 0 {
		return code, fmt.Errorf("config.sh failed (exit %d): %w", code, err)
	}
	// --ephemeral (in cfgArgs) makes run.sh process exactly one job then exit;
	// the old --once flag is deprecated in favour of it.
	run := exec.CommandContext(ctx, filepath.Join(runnerDir, "run.sh"))
	run.Dir = runnerDir
	return streamCmdToRun(store, runID, run)
}

// runGitHubRunnerInContainer runs config + run inside the fat sandbox image
// with the runner dir + work dir mounted. The image must carry the runner's
// runtime deps (libicu, etc.); the fat sandbox does.
func runGitHubRunnerInContainer(ctx context.Context, runnerDir, work string, cfgArgs []string, runID string, store *RunnerStore) (int, error) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return -1, fmt.Errorf("isolation=container needs docker (not found); register with isolation=host on a dedicated private box, or install docker: %w", err)
	}
	// Quote the config args into a shell line; run config then run.sh inside.
	script := "set -e; cd /runner; ./config.sh " + ciShellJoin(cfgArgs) + "; ./run.sh"
	args := []string{
		"run", "--rm",
		"-v", runnerDir + ":/runner",
		"-v", work + ":" + work,
		"-w", "/runner",
		sandboxImage,
		"sh", "-c", script,
	}
	cmd := exec.CommandContext(ctx, dockerPath, args...)
	return streamCmdToRun(store, runID, cmd)
}

// ensureGitHubRunnerExtracted downloads + unpacks actions/runner once into
// ~/.yaver/runner/gha/<version>/ and returns that dir.
func ensureGitHubRunnerExtracted(ctx context.Context, version string, store *RunnerStore, runID string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "runner", "gha", version)
	if fileExistsCI(filepath.Join(target, "config.sh")) || fileExistsCI(filepath.Join(target, "config.cmd")) {
		return target, nil
	}
	url, err := githubRunnerDownloadURL(version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, 0700); err != nil {
		return "", err
	}
	store.Append(runID, "[ci] downloading actions/runner "+version+" ("+runtime.GOOS+"/"+runtime.GOARCH+") ...")
	tarball := filepath.Join(target, "runner.tar.gz")
	if err := downloadFileCI(ctx, url, tarball); err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer os.Remove(tarball)
	// tar is present on macOS + Linux; the runner tarball is gzip.
	ex := exec.CommandContext(ctx, "tar", "xzf", tarball, "-C", target)
	if out, err := ex.CombinedOutput(); err != nil {
		return "", fmt.Errorf("extract runner: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return target, nil
}

// githubRunnerDownloadURL maps GOOS/GOARCH to the actions/runner release asset.
// Pure — unit-tested.
func githubRunnerDownloadURL(version, goos, goarch string) (string, error) {
	var osLabel string
	switch goos {
	case "linux":
		osLabel = "linux"
	case "darwin":
		osLabel = "osx"
	case "windows":
		osLabel = "win"
	default:
		return "", fmt.Errorf("unsupported runner OS %q", goos)
	}
	var archLabel string
	switch goarch {
	case "amd64":
		archLabel = "x64"
	case "arm64":
		archLabel = "arm64"
	case "arm":
		archLabel = "arm"
	default:
		return "", fmt.Errorf("unsupported runner arch %q", goarch)
	}
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("https://github.com/actions/runner/releases/download/v%s/actions-runner-%s-%s-%s.%s",
		version, osLabel, archLabel, version, ext), nil
}

func normalizeRunnerOS(goos string) string {
	switch goos {
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func ciPerRunWorkDir(runID string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	w := filepath.Join(dir, "runner", "gha-work", safeRunSuffix(runID))
	if err := os.MkdirAll(w, 0700); err != nil {
		return "", err
	}
	return w, nil
}

func safeRunSuffix(runID string) string {
	if len(runID) > 12 {
		return runID[:12]
	}
	return runID
}

// streamCmdToRun runs cmd, piping combined stdout+stderr line-by-line into the
// run log via store.Append, and returns the exit code.
func streamCmdToRun(store *RunnerStore, runID string, cmd *exec.Cmd) (int, error) {
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return -1, err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			store.Append(runID, sc.Text())
		}
	}()
	err := cmd.Wait()
	_ = pw.Close()
	wg.Wait()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	return 0, nil
}

func ciShellJoin(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

func fileExistsCI(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func downloadFileCI(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}
