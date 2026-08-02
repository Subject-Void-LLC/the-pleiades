package engine

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
)

const schedulerLockKey = "pleiades-scheduler-leader"

// Scheduler handles background Cron jobs. Only one instance across the cluster
// can be active at a time to prevent duplicate workflow triggers.
type Scheduler struct {
	manager  lock.Manager
	isLeader atomic.Bool
	interval time.Duration
}

func NewScheduler(mgr lock.Manager) *Scheduler {
	return &Scheduler{
		manager:  mgr,
		interval: 2 * time.Second,
	}
}

// Run starts the background leader election loop. It blocks until ctx is canceled.
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	var currentLease lock.Lease

	for {
		select {
		case <-ctx.Done():
			// Graceful Handover: If we are shutting down and hold the lease, release it immediately.
			if currentLease != nil {
				// Use a background context since the parent ctx is already dead
				_ = currentLease.Release(context.Background())
			}
			s.isLeader.Store(false)
			return

		case <-ticker.C:
			if currentLease != nil {
				// We are the leader, try to keep the lease alive
				err := currentLease.KeepAlive(ctx)
				if err != nil {
					// We lost the lease (maybe network partition or someone else hijacked it)
					currentLease = nil
					s.isLeader.Store(false)
				}
				continue
			}

			// We are not the leader, attempt to acquire the lock
			lease, err := s.manager.Acquire(ctx, schedulerLockKey, s.interval*2)
			if err == nil {
				// We won the election!
				currentLease = lease
				s.isLeader.Store(true)
			} else if err != lock.ErrLockHeld {
				// An actual error occurred (not just losing the race)
				// In a real app we would log this via structured logging
			}
		}
	}
}

// IsLeader returns true if this instance currently holds the scheduler lease.
func (s *Scheduler) IsLeader() bool {
	return s.isLeader.Load()
}
