package main

// autorun_store_test.go — real SQLite in a temp dir, no mocks (repo convention).
// Covers the load-bearing lease behaviour from AUTORUN_STORE.md §12: the deploy
// race (§12.8), quota (§12.9), code-lock ancestor/descendant (§12.10), plus
// expiry, heartbeat, release→history, and the 7-day sweep.

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *AutorunStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "autoruns.db")
	db, err := sqlOpenAutorunStoreAt(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// sqlOpenAutorunStoreAt opens a store at an explicit path (tests pass a per-test
// DB rather than ~/.yaver — the spec forbids a global mock-store mode).
func sqlOpenAutorunStoreAt(dbPath string) (*AutorunStore, error) {
	db, err := openSQLiteAt(dbPath)
	if err != nil {
		return nil, err
	}
	s := &AutorunStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func TestDeployLeaseRace(t *testing.T) {
	s := testStore(t)
	// Two "autoruns" acquire testflight concurrently against the SAME DB file.
	var wg sync.WaitGroup
	results := make([]error, 2)
	ids := []string{"autorun-A", "autorun-B"}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = s.AcquireDeployLease("testflight", ids[i], "seat-"+ids[i], "/w/"+ids[i], "main", "450", time.Hour)
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, e := range results {
		if e == nil {
			wins++
		} else if _, ok := e.(*LeaseHeld); !ok {
			t.Fatalf("unexpected acquire error: %v", e)
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d (%v)", wins, results)
	}
	// The loser retries and is still blocked while the winner is live.
	if err := s.AcquireDeployLease("testflight", "autorun-C", "seat-C", "/w/C", "main", "450", time.Hour); err == nil {
		t.Fatal("third acquire should be blocked while lease is live")
	} else if _, ok := err.(*LeaseHeld); !ok {
		t.Fatalf("want *LeaseHeld, got %T", err)
	}
	// Winner re-acquiring its OWN lease succeeds (crash-retry continuity).
	winner := "autorun-A"
	if results[1] == nil {
		winner = "autorun-B"
	}
	if err := s.AcquireDeployLease("testflight", winner, "seat", "/w", "main", "450", time.Hour); err != nil {
		t.Fatalf("holder re-acquiring own lease should succeed, got %v", err)
	}
}

func TestDeployLeaseExpiryHandoff(t *testing.T) {
	s := testStore(t)
	// A held-but-expired lease is treated as absent; another autorun takes it.
	if err := s.AcquireDeployLease("testflight", "dead", "seat", "/w", "main", "1", time.Millisecond); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	// Force expiry deterministically (TTL was 1ms; expires_at == started second).
	if _, err := s.db.Exec(`UPDATE deploy_leases SET expires_at = ? WHERE target='testflight'`, nowUnix()-10); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.CurrentDeployLease("testflight"); got != nil {
		t.Fatalf("expired lease should read as free, got %+v", got)
	}
	if err := s.AcquireDeployLease("testflight", "live", "seat", "/w", "main", "451", time.Hour); err != nil {
		t.Fatalf("acquire after expiry should succeed, got %v", err)
	}
}

func TestDeployQuota(t *testing.T) {
	s := testStore(t)
	// 18 completed testflight deploys in the last 24h → 19th acquire refuses.
	now := nowUnix()
	for i := 0; i < 18; i++ {
		if _, err := s.db.Exec(`INSERT INTO deploy_history(id, target, autorun_id, holder, workdir, started_at, ended_at, outcome)
		  VALUES(?, 'testflight', 'a', 'seat', '/w', ?, ?, 'success')`, storeID(), now-100, now-50); err != nil {
			t.Fatal(err)
		}
	}
	err := s.AcquireDeployLease("testflight", "next", "seat", "/w", "main", "460", time.Hour)
	if _, ok := err.(*QuotaExceeded); !ok {
		t.Fatalf("want *QuotaExceeded at cap, got %v", err)
	}
}

func TestReleaseWritesHistory(t *testing.T) {
	s := testStore(t)
	if err := s.AcquireDeployLease("testflight", "a", "seat", "/w", "main", "450", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatDeployLease("testflight", "a", "uploading", time.Hour); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := s.ReleaseDeployLease("testflight", "a", "success"); err != nil {
		t.Fatalf("release: %v", err)
	}
	used, _, err := s.DeployQuotaUsed("testflight")
	if err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatalf("released success should count as 1 upload today, got %d", used)
	}
	// Lease is now free (terminal) — a new deploy can acquire.
	if err := s.AcquireDeployLease("testflight", "b", "seat", "/w", "main", "451", time.Hour); err != nil {
		t.Fatalf("acquire after release should succeed, got %v", err)
	}
}

func TestCodeLockAncestorDescendant(t *testing.T) {
	s := testStore(t)
	// A build lock over mobile/ refuses an edit lock over mobile/ios/Info.plist.
	if err := s.AcquireCodeLock("/repo/mobile", "builder", "seat", "build", time.Hour); err != nil {
		t.Fatalf("acquire mobile/: %v", err)
	}
	if err := s.AcquireCodeLock("/repo/mobile/ios/Info.plist", "editor", "seat", "edit", time.Hour); err == nil {
		t.Fatal("edit under a locked subtree should be refused")
	}
	// Two unrelated files coexist.
	if err := s.AcquireCodeLock("/repo/web/a.ts", "e1", "seat", "edit", time.Hour); err != nil {
		t.Fatalf("web edit should succeed: %v", err)
	}
	if err := s.AcquireCodeLock("/repo/backend/b.ts", "e2", "seat", "edit", time.Hour); err != nil {
		t.Fatalf("backend edit should succeed: %v", err)
	}
}

func TestSweepOld(t *testing.T) {
	s := testStore(t)
	old := nowUnix() - int64((8 * 24 * time.Hour).Seconds())
	// An old terminal deploy + an old expired code lock should be swept.
	if _, err := s.db.Exec(`INSERT INTO deploy_leases(target, autorun_id, holder, workdir, stage, started_at, updated_at, expires_at, ended_at, outcome)
	  VALUES('playstore','a','seat','/w','finished',?,?,?,?,'success')`, old, old, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO code_locks(path, autorun_id, holder, purpose, started_at, expires_at)
	  VALUES('/old','a','seat','edit',?,?)`, old, old); err != nil {
		t.Fatal(err)
	}
	n, err := s.SweepOld(autorunStoreRetention)
	if err != nil {
		t.Fatal(err)
	}
	if n < 2 {
		t.Fatalf("expected >=2 old rows swept, got %d", n)
	}
}
