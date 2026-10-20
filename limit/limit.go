// Package limit bounds concurrent work with a fixed number of slots and a
// capped wait queue, rejecting work rather than queueing without limit.
package limit

import (
	"context"
	"errors"
	"sync/atomic"
)

// ErrSaturated is returned by Limiter.Run when the wait queue is full.
var ErrSaturated = errors.New("queue is full")

// Stats is a point-in-time snapshot of a Limiter's load and capacity.
type Stats struct {
	Active      int64 `json:"active"`
	Waiting     int64 `json:"waiting"`
	Concurrency int   `json:"concurrency"`
	MaxWaiting  int64 `json:"max_waiting"`
}

// Limiter runs at most a fixed number of jobs at once and lets at most
// maxWaiting callers wait for a slot. It is safe for concurrent use.
type Limiter struct {
	slots      chan struct{}
	maxWaiting int64
	active     atomic.Int64
	waiting    atomic.Int64
}

// New returns a Limiter with concurrency slots and room for maxWaiting
// waiting callers.
func New(concurrency int, maxWaiting int) *Limiter {
	return &Limiter{slots: make(chan struct{}, concurrency), maxWaiting: int64(maxWaiting)}
}

// Run calls work once a slot is free and returns its error. It returns
// ErrSaturated without waiting when the queue is full, or ctx.Err() if ctx
// ends before a slot frees.
func (l *Limiter) Run(ctx context.Context, work func() error) error {
	select {
	case l.slots <- struct{}{}:
		l.active.Add(1)
		defer l.release()
		return work()
	default:
	}

	waiting := l.waiting.Add(1)
	if waiting > l.maxWaiting {
		l.waiting.Add(-1)
		return ErrSaturated
	}
	defer l.waiting.Add(-1)

	select {
	case l.slots <- struct{}{}:
		l.active.Add(1)
		defer l.release()
		return work()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Snapshot returns the Limiter's current load and configured capacity.
func (l *Limiter) Snapshot() Stats {
	return Stats{
		Active:      l.active.Load(),
		Waiting:     l.waiting.Load(),
		Concurrency: cap(l.slots),
		MaxWaiting:  l.maxWaiting,
	}
}

func (l *Limiter) release() {
	<-l.slots
	l.active.Add(-1)
}
