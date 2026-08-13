// Package clock is the injectable time source (determinism tools).
// Production code takes a Clock; tests steer lease and backoff
// races instead of sleeping for them.
//
// Database-clock decisions (lease validity at commit, operation deadlines)
// deliberately do not use this clock — they use clock_timestamp() inside
// SQL so a skewed process cannot extend its own lease. This package covers
// process-local scheduling: tickers, backoff waits, poll intervals.
package clock

import (
	"sync"
	"time"
)

// Clock is the minimal time surface the control plane uses.
type Clock interface {
	Now() time.Time
	// After behaves like time.After.
	After(d time.Duration) <-chan time.Time
}

// Real is the wall clock.
type Real struct{}

func (Real) Now() time.Time                         { return time.Now() }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake is a manually-stepped clock for tests.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

// NewFake starts a fake clock at the given time.
func NewFake(start time.Time) *Fake {
	return &Fake{now: start}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{at: f.now.Add(d), ch: ch})
	return ch
}

// Advance moves the clock forward, firing any waiters that come due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	var rest []waiter
	for _, w := range f.waiters {
		if !w.at.After(f.now) {
			w.ch <- f.now
		} else {
			rest = append(rest, w)
		}
	}
	f.waiters = rest
}
