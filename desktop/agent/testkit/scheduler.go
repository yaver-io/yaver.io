package testkit

import (
	"context"
	"sync"
)

// RunSuite executes every spec in the slice and returns a Suite. When
// concurrency > 1, specs run in parallel across that many worker
// goroutines. Each worker spawns its own Chromium instance, so the
// only shared state is the per-spec on-disk artifact dir (already
// segregated by spec name in artifactDirFor).
//
// Solo dev value: a 6-spec suite that takes 30s sequentially drops to
// ~6s on a 6-core laptop, which is the difference between "I run tests
// before every save" and "I run them once a day."
func RunSuite(ctx context.Context, specs []*Spec, opts RunOptions, concurrency int) *Suite {
	suite := &Suite{StartedAt: nowFunc()}
	defer func() { suite.FinishedAt = nowFunc() }()

	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(specs) {
		concurrency = len(specs)
	}

	results := make([]*Result, len(specs))
	type job struct {
		idx  int
		spec *Spec
	}
	jobs := make(chan job)

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				results[j.idx] = Run(ctx, j.spec, opts)
			}
		}()
	}
	for i, s := range specs {
		select {
		case jobs <- job{idx: i, spec: s}:
		case <-ctx.Done():
			break
		}
	}
	close(jobs)
	wg.Wait()

	for _, r := range results {
		if r != nil {
			suite.Results = append(suite.Results, r)
		}
	}
	return suite
}

// nowFunc is overridable for tests; defaults to time.Now in scheduler_now.go.
var nowFunc = realNow
