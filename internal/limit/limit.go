package limit

import (
	"context"
	"errors"
	"sync/atomic"
)

var ErrSaturated = errors.New("queue is full")

type Stats struct {
	Active      int64 `json:"active"`
	Waiting     int64 `json:"waiting"`
	Concurrency int   `json:"concurrency"`
	MaxWaiting  int64 `json:"max_waiting"`
}

type Limiter struct {
	slots      chan struct{}
	maxWaiting int64
	active     atomic.Int64
	waiting    atomic.Int64
}

func New(concurrency int, maxWaiting int) *Limiter {
	return &Limiter{slots: make(chan struct{}, concurrency), maxWaiting: int64(maxWaiting)}
}

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
