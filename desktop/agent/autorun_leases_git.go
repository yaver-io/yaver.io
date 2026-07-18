package main

// autorun_leases_git.go — the cross-machine tier of the lease layer.
//
// autorun_leases.go excludes runs on ONE box. This extends that across the
// fleet, and it does so without a coordinator, a broker, or an election.
//
// Why git refs rather than a lock service:
//
//   - `git update-ref <ref> <new> <old>` is a genuine atomic compare-and-swap.
//     The ARBITER is the remote, not any node, so there is no leader to elect
//     and no leader to die.
//   - The namespace is already replicated, already authenticated, and already
//     owned by the user. No new server, no new port, nothing to host.
//   - It keeps work-derived data OUT of Convex, which the privacy contract
//     requires: a lease carries a repo hash, a device alias and a timestamp —
//     never a path, a prompt, or a diff.
//   - A human can inspect and break a stuck claim with plain git, instead of
//     needing an admin verb we would have had to invent.
//
// The honest limits, stated where they will be read rather than discovered:
//
//   - CAS is atomic per-remote, not instantaneous. Two machines can both believe
//     they hold a lease until one PUSHES; the push is the serialization point.
//     Good enough for "do not compile the same subsystem"; wrong for anything
//     needing sub-second mutual exclusion.
//   - A lease is only as fresh as your last fetch, so the local tier
//     (autorun_leases.go) stays the fast path and this is the fleet tier.
//   - Clock skew makes TTL approximate. Reap generously, never aggressively.
//
// See docs/architecture/AUTORUN_COLLECTIVE_SYNC_AUDIT.md Part 5.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// autorunLeaseRefPrefix namespaces claims away from branches and tags so a
// `git push --all` or a branch-protection rule can never touch them.
const autorunLeaseRefPrefix = "refs/yaver/lease/"

// gitLeaseRecord is what a lease ref points at. Metadata only — the privacy
// contract forbids anything work-derived here, and a lease genuinely does not
// need it: exclusion is about WHICH key is held, not what is being done with it.
type gitLeaseRecord struct {
	Key        string `json:"key"`
	Holder     string `json:"holder"`     // autorun session ID
	Slot       string `json:"slot"`       // task:seat
	MachineID  string `json:"machineId"`  // device id, not a hostname or a path
	Phase      string `json:"phase"`      //_edit / build / land
	AcquiredAt int64  `json:"acquiredAt"` // unix seconds
	TTLSeconds int64  `json:"ttlSeconds"`
}

func (r gitLeaseRecord) expired(now time.Time) bool {
	if r.TTLSeconds <= 0 {
		return false
	}
	return now.Unix() > r.AcquiredAt+r.TTLSeconds
}

func autorunLeaseRef(k leaseKey) string {
	// leaseKey.String() is already ref-safe by construction (autorun_leases.go),
	// which is why the two tiers cannot disagree about what a claim is called.
	return autorunLeaseRefPrefix + k.String()
}

// gitLeaseZeroOID is git's "this ref must not exist" sentinel for a CAS.
const gitLeaseZeroOID = "0000000000000000000000000000000000000000"

// gitLeaseClient runs the git plumbing. An interface so the CAS logic is
// testable against a real temp repository rather than a mock — the whole value
// of this file is that the CAS is REAL, so a mocked test would assert nothing.
type gitLeaseClient struct {
	repoDir string
	exec    func(ctx context.Context, name string, args []string, dir string) autorunCommandResult
}

func newGitLeaseClient(repoDir string) *gitLeaseClient {
	return &gitLeaseClient{repoDir: repoDir, exec: autorunExec}
}

func (g *gitLeaseClient) git(ctx context.Context, args ...string) autorunCommandResult {
	return g.exec(ctx, "git", append([]string{"-C", g.repoDir}, args...), g.repoDir)
}

// writeRecord stores the record as a blob and returns its OID.
func (g *gitLeaseClient) writeRecord(ctx context.Context, rec gitLeaseRecord) (string, error) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	res := g.exec(ctx, "sh", []string{"-c",
		fmt.Sprintf("printf %%s %s | git -C %s hash-object -w --stdin", shellQuoteSingle(string(payload)), shellQuoteSingle(g.repoDir))},
		g.repoDir)
	if res.Err != nil {
		return "", fmt.Errorf("hash-object: %w: %s", res.Err, strings.TrimSpace(res.Output))
	}
	oid := strings.TrimSpace(res.Output)
	if oid == "" {
		return "", fmt.Errorf("hash-object returned no oid")
	}
	return oid, nil
}

// ReadLease returns the current record for a key, or ok=false when unheld.
func (g *gitLeaseClient) ReadLease(ctx context.Context, k leaseKey) (gitLeaseRecord, string, bool) {
	ref := autorunLeaseRef(k)
	res := g.git(ctx, "rev-parse", "--verify", "--quiet", ref)
	oid := strings.TrimSpace(res.Output)
	if res.Err != nil || oid == "" {
		return gitLeaseRecord{}, "", false
	}
	cat := g.git(ctx, "cat-file", "blob", oid)
	if cat.Err != nil {
		return gitLeaseRecord{}, oid, false
	}
	var rec gitLeaseRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(cat.Output)), &rec); err != nil {
		// An unparseable record is treated as unheld rather than as a hard
		// error: a claim we cannot read must not be able to wedge the fleet.
		return gitLeaseRecord{}, oid, false
	}
	return rec, oid, true
}

// AcquireLease takes a key by compare-and-swap, returning false when someone
// else holds it.
//
// Three cases, and the third is what makes a crashed machine survivable:
//
//	unheld            → CAS from the zero OID (must-not-exist)
//	held by us        → CAS from our own OID (renewal)
//	held and EXPIRED  → CAS from the expired OID (any actor may reap)
//
// Reaping via CAS rather than a plain delete is the important part: two machines
// racing to reap the same dead claim cannot both win, so the reap cannot become
// its own race.
func (g *gitLeaseClient) AcquireLease(ctx context.Context, k leaseKey, rec gitLeaseRecord, now time.Time) (bool, error) {
	ref := autorunLeaseRef(k)
	oldOID := gitLeaseZeroOID

	if existing, oid, ok := g.ReadLease(ctx, k); ok {
		switch {
		case existing.Holder == rec.Holder:
			oldOID = oid // renewal
		case existing.expired(now):
			oldOID = oid // reap
		default:
			return false, nil // genuinely held by someone else
		}
	} else if oid != "" {
		// The ref exists but did not parse. Swap from whatever is there rather
		// than from zero, or the CAS is guaranteed to fail forever.
		oldOID = oid
	}

	newOID, err := g.writeRecord(ctx, rec)
	if err != nil {
		return false, err
	}
	res := g.git(ctx, "update-ref", ref, newOID, oldOID)
	if res.Err != nil {
		// A failed CAS is a LOST RACE, not an error: someone updated the ref
		// between our read and our write. The caller retries later.
		return false, nil
	}
	return true, nil
}

// ReleaseLease drops a key we hold. Scoped by holder so one run can never free
// another's claim — the same rule the local tier enforces.
func (g *gitLeaseClient) ReleaseLease(ctx context.Context, k leaseKey, holder string) bool {
	existing, oid, ok := g.ReadLease(ctx, k)
	if !ok || existing.Holder != holder {
		return false
	}
	res := g.git(ctx, "update-ref", "-d", autorunLeaseRef(k), oid)
	return res.Err == nil
}

// PublishLeases pushes the lease namespace. This is the moment a local claim
// becomes a fleet-wide one — before it, two machines can both believe they hold
// the key, which is exactly why the push result is the answer and not the CAS.
func (g *gitLeaseClient) PublishLeases(ctx context.Context, remote string) error {
	res := g.git(ctx, "push", remote,
		"--force-with-lease", autorunLeaseRefPrefix+"*:"+autorunLeaseRefPrefix+"*")
	if res.Err != nil {
		return fmt.Errorf("publish leases: %w: %s", res.Err, strings.TrimSpace(res.Output))
	}
	return nil
}

// FetchLeases refreshes the local view of the namespace. A lease is only as
// fresh as the last fetch, so callers that care about exclusion must fetch
// before they trust a "free" answer.
func (g *gitLeaseClient) FetchLeases(ctx context.Context, remote string) error {
	res := g.git(ctx, "fetch", remote,
		"--prune", "+"+autorunLeaseRefPrefix+"*:"+autorunLeaseRefPrefix+"*")
	if res.Err != nil {
		return fmt.Errorf("fetch leases: %w: %s", res.Err, strings.TrimSpace(res.Output))
	}
	return nil
}

// ListLeases returns every live claim, reaping expired ones from the answer.
// Expired records are filtered rather than deleted here: reading is done on
// every poll and must stay side-effect free, while deletion is a write that
// belongs to whoever wants the key.
func (g *gitLeaseClient) ListLeases(ctx context.Context, now time.Time) []gitLeaseRecord {
	res := g.git(ctx, "for-each-ref", "--format=%(refname)", autorunLeaseRefPrefix)
	if res.Err != nil {
		return nil
	}
	out := []gitLeaseRecord{}
	for _, line := range strings.Split(strings.TrimSpace(res.Output), "\n") {
		ref := strings.TrimSpace(line)
		if !strings.HasPrefix(ref, autorunLeaseRefPrefix) {
			continue
		}
		name := strings.TrimPrefix(ref, autorunLeaseRefPrefix)
		parts := strings.SplitN(name, "/", 2)
		if len(parts) != 2 {
			continue
		}
		rec, _, ok := g.ReadLease(ctx, leaseKey{Class: leaseClass(parts[0]), Name: parts[1]})
		if !ok || rec.expired(now) {
			continue
		}
		out = append(out, rec)
	}
	return out
}
