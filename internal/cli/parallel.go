package cli

import (
	"context"
	"sync"
)

// DefaultFanout bounds how many per-guest API calls a cluster-wide audit has
// in flight at once.
//
// These commands are N+1 by nature: one cluster-resources scan, then one
// config read per guest. Run serially, a 200-guest cluster pays 200 round
// trips end to end, which is what makes `security list` feel broken on a real
// fleet. Run unbounded, the same command opens 200 connections to a single
// pvedaemon, which is a denial of service against the operator's own cluster.
//
// Eight is chosen to be well inside what a default pveproxy accepts while
// still turning the wait from minutes into seconds.
const DefaultFanout = 8

// ForEachIndex calls fn once for every index in [0, n), at most limit calls at
// a time, and returns when all have finished.
//
// fn receives the index rather than an element so callers can write their
// result into a pre-sized slice at that position: nothing is shared, no lock
// is needed, and the output order does not depend on which call finished
// first — a report whose row order changed run to run would be useless for
// diffing.
//
// fn is expected to record its own per-item failure. That is the shape these
// audits want: one unreadable guest should cost that guest's row, not the
// whole report. Cancellation is the caller's to handle inside fn via ctx;
// pending calls still start, so fn should return early when ctx is done.
func ForEachIndex(ctx context.Context, n, limit int, fn func(ctx context.Context, i int)) {
	if n <= 0 {
		return
	}
	if limit < 1 {
		limit = 1
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)
		sem <- struct{}{}

		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(ctx, i)
		}()
	}

	wg.Wait()
}
