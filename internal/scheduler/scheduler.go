package scheduler

import (
	"context"
	"time"

	"go.uber.org/zap"
)

// Scheduler wraps a time.Ticker to execute a job at a fixed interval.
// It supports graceful shutdown via outer context cancellation or explicit
// Shutdown() call. Each job run inherits the parent context with the same
// deadline/cancellation.
//
// The job function should be idempotent and handle its own internal timeouts;
// Scheduler does NOT create a per-run timeout to keep flexibility.
// You may wrap fn in a context.WithTimeout in main if desired.

type Scheduler struct {
	interval time.Duration
	fn       func(ctx context.Context)
	log      *zap.SugaredLogger
	stopCh   chan struct{}
}

// New constructs a Scheduler. If interval <1s, it is clamped to 1s to avoid
// busy-loops.
func New(interval time.Duration, fn func(ctx context.Context), logger *zap.SugaredLogger) *Scheduler {
	if interval < time.Second {
		interval = time.Second
	}
	if logger == nil {
		logger = zap.NewNop().Sugar()
	}
	return &Scheduler{
		interval: interval,
		fn:       fn,
		log:      logger,
		stopCh:   make(chan struct{}),
	}
}

// Run starts the ticker loop. It blocks until the parent context is done or
// Shutdown() is called. Safe to call in its own goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	s.log.Info("scheduler started", "interval", s.interval.String())

	// Immediate execution at start (optional; comment if not needed)
	s.fn(ctx)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("scheduler: parent context cancelled")
			return
		case <-s.stopCh:
			s.log.Info("scheduler: shutdown signal received")
			return
		case <-ticker.C:
			s.fn(ctx)
		}
	}
}

// Shutdown signals the Run loop to exit as soon as possible.
// It is idempotent.
func (s *Scheduler) Shutdown() {
	select {
	case <-s.stopCh:
		// already closed
	default:
		close(s.stopCh)
	}
}
