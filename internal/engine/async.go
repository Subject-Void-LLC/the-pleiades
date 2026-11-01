// Package engine: async and poll, Ansible's way of running a long task under
// its own time limit, or in the background while the run goes on.
//
// async: N is a hard limit of N seconds per device: past it the task's
// context is cancelled, so the method stops, and the task fails, which a
// rescue: can answer. With poll: M (10 when async is set and poll is not),
// the task runs where it stands and the run reports it still running every M
// seconds. With poll: 0 it runs in the background: the node returns at once,
// its register holding the job's id (ansible_job_id), and later tasks run
// while it does. An async_status task, given that id, waits for the job and
// returns its real result as its own. validate keeps a poll: 0 task from
// being written without one (validateAsyncFlow).
//
// A background job keeps its device's lock for as long as it runs, held as
// a counted hold the rest of its run may join (heldLeases), so the run's next
// task on that device is not refused by its own job, and another run is
// still kept out. The lease is kept alive while it is held. The job's change
// is journaled when it is collected, under its own node, so a rollback sees
// it; a run never ends with a job nobody collected: Run waits for each one,
// up to its limit, and journals it before returning (drainJobs).
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
)

// The names of the async_status action, bare and namespaced.
const (
	AsyncStatusName = "async_status"
	AsyncStatusFQCN = "pleiades.builtin.async_status"
)

// The stats an async task's start and its async_status carry, Ansible's.
const (
	StatJobID    = "ansible_job_id"
	StatStarted  = "started"
	StatFinished = "finished"
)

// defaultPoll is the poll interval when async is set and poll is not,
// Ansible's default.
const defaultPoll = 10 * time.Second

// isAsyncStatus reports whether fqcn names the async_status action.
func isAsyncStatus(fqcn string) bool { return fqcn == AsyncStatusName || fqcn == AsyncStatusFQCN }

// background reports whether task runs in the background: async with poll 0.
func (t *Task) background() bool { return t.Async > 0 && t.Poll != nil && *t.Poll == 0 }

// pollInterval is how often a task running where it stands is reported as
// still running, zero for a task that is not async.
func (t *Task) pollInterval() time.Duration {
	switch {
	case t.Async <= 0:
		return 0
	case t.Poll == nil:
		return defaultPoll
	}
	return time.Duration(*t.Poll) * time.Second
}

// asyncJob is one background run of one task on one device.
type asyncJob struct {
	id, nodeID, device string
	done               chan struct{}
	result             NodeResult
	collected          bool
}

// asyncJobs is a run's background jobs, by id and in the order they started.
type asyncJobs struct {
	mu    sync.Mutex
	byID  map[string]*asyncJob
	order []*asyncJob
}

// newJobID returns an unguessable job id.
func newJobID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// add records job.
func (j *asyncJobs) add(job *asyncJob) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.byID == nil {
		j.byID = map[string]*asyncJob{}
	}
	j.byID[job.id] = job
	j.order = append(j.order, job)
}

// claim returns the job with id for device, marking it collected, or why it
// cannot be: unknown, another device's, or already collected.
func (j *asyncJobs) claim(id, device string) (*asyncJob, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.byID[id]
	switch {
	case !ok:
		return nil, fmt.Errorf("async_status: no background job %q was started in this run", id)
	case job.device != device:
		return nil, fmt.Errorf("async_status: job %q belongs to another device, and a device collects only its own job", id)
	case job.collected:
		return nil, fmt.Errorf("async_status: job %q was already collected", id)
	}
	job.collected = true
	return job, nil
}

// uncollected returns, and marks collected, every job nobody collected.
func (j *asyncJobs) uncollected() []*asyncJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []*asyncJob
	for _, job := range j.order {
		if !job.collected {
			job.collected = true
			out = append(out, job)
		}
	}
	return out
}

// heldLease is a device lease a background job holds and the rest of its run
// may join: refs counts the holders, and the lease is released, and its
// keep-alive stopped, when the last one lets go.
type heldLease struct {
	lease lock.Lease
	refs  int
	stop  chan struct{}
}

// heldLeases is a run's held leases by device id.
type heldLeases struct {
	mu   sync.Mutex
	byID map[string]*heldLease
}

// sharedLease is one holder's share of a heldLease; Release lets go of it.
type sharedLease struct {
	owner  *heldLeases
	device string
	once   sync.Once
}

// ID implements lock.Lease.
func (s *sharedLease) ID() string { return s.owner.leaseOf(s.device).ID() }

// KeepAlive implements lock.Lease: the held lease keeps itself alive.
func (s *sharedLease) KeepAlive(context.Context) error { return nil }

// Release implements lock.Lease, letting go of this holder's share once.
func (s *sharedLease) Release(ctx context.Context) error {
	var err error
	s.once.Do(func() { err = s.owner.release(ctx, s.device) })
	return err
}

// leaseOf returns device's held lease.
func (h *heldLeases) leaseOf(device string) lock.Lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	if held := h.byID[device]; held != nil {
		return held.lease
	}
	return nil
}

// join returns a share of device's held lease, or nil when no background job
// of this run holds one.
func (h *heldLeases) join(device string) lock.Lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	held := h.byID[device]
	if held == nil {
		return nil
	}
	held.refs++
	return &sharedLease{owner: h, device: device}
}

// adopt turns lease, which a starting background job took, into device's
// held lease, keeping it alive every ttl/3 until it is released or ctx (the
// run's) ends, and returns the job's share. A lease already held for device
// (two jobs on one device) is shared, and the new one released.
func (h *heldLeases) adopt(ctx context.Context, device string, lease lock.Lease, ttl time.Duration) lock.Lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byID == nil {
		h.byID = map[string]*heldLease{}
	}
	if held := h.byID[device]; held != nil {
		held.refs++
		_ = lease.Release(context.Background())
		return &sharedLease{owner: h, device: device}
	}
	held := &heldLease{lease: lease, refs: 1, stop: make(chan struct{})}
	h.byID[device] = held
	go keepAlive(ctx, lease, ttl, held.stop)
	return &sharedLease{owner: h, device: device}
}

// release lets go of one share of device's held lease, releasing the lease
// when it was the last.
func (h *heldLeases) release(ctx context.Context, device string) error {
	h.mu.Lock()
	held := h.byID[device]
	if held == nil {
		h.mu.Unlock()
		return nil
	}
	held.refs--
	if held.refs > 0 {
		h.mu.Unlock()
		return nil
	}
	delete(h.byID, device)
	h.mu.Unlock()
	close(held.stop)
	return held.lease.Release(ctx)
}

// keepAlive renews lease every ttl/3 until stop closes or ctx ends, so a
// task running longer than the lease's ttl keeps its device. Each renewal is
// bounded by the interval, so a lock backend that stalls cannot hold it past
// the next one.
func keepAlive(ctx context.Context, lease lock.Lease, ttl time.Duration, stop <-chan struct{}) {
	every := max(ttl/3, time.Second)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			renew, cancel := context.WithTimeout(ctx, every)
			_ = lease.KeepAlive(renew)
			cancel()
		}
	}
}

// startJob starts cmd's task in the background on its device, holding lease
// (nil for a task with no device) for as long as it runs, and returns the
// started result: not a change yet, never journaled, its register holding the
// job's id. run is the task's own run, called in the job's goroutine.
func (r *run) startJob(ctx context.Context, cmd nodeExecution, result NodeResult, host string, lease lock.Lease, run func(context.Context, nodeExecution, NodeResult) NodeResult) NodeResult {
	job := &asyncJob{id: newJobID(), nodeID: cmd.NodeID, device: result.Device, done: make(chan struct{})}
	// The job id is recorded before the job starts: a record that fails then
	// fails a task that started nothing, where after the start it would report
	// a failure while the job ran on, uncollectable.
	stats := map[string]interface{}{StatJobID: job.id, StatStarted: 1, StatFinished: 0}
	if cmd.Task.Register != "" && !cmd.Task.noMerge {
		if err := r.x.workflow.Merge(cmd.Task.Register, result.Device, stats); err != nil {
			if lease != nil {
				// runOne handed the lease to this job, which will not run.
				_ = lease.Release(context.Background())
			}
			result.fail(FailureStageRecord, fmt.Errorf("failed to record the job id of task %s: %w", taskLabel(cmd.NodeID, cmd.Task), err))
			r.publish(cmd.NodeID, cmd.Task, host, "failed", result.Err.Error())
			return finish(result)
		}
	}
	if lease != nil {
		lease = r.leases.adopt(ctx, result.Device, lease, defaultLockTTL)
	}
	// The job's own run records nothing in the register: it holds the id,
	// and async_status's register gets the result.
	bg := cmd
	task := *cmd.Task
	task.noMerge = true
	bg.Task = &task
	r.jobs.add(job)
	// The job starts from its own copy: result goes on being written below.
	start := result
	go func() {
		defer close(job.done)
		if lease != nil {
			defer func() { _ = lease.Release(context.Background()) }()
		}
		// The job runs past the level that started it, bounded by its own async
		// limit and by ctx, the run's own context: nothing in the engine ends
		// ctx at a level's end, so it ends only when the run is cancelled (an
		// interrupt, a cancelled job), and that cancel must stop the job, or
		// Run's drain would hold the run open for the job's whole limit.
		job.result = run(ctx, bg, start)
	}()

	result.Stats = stats
	result.background = true
	r.publish(cmd.NodeID, cmd.Task, host, "started", "running in the background as job "+job.id)
	return finish(result)
}

// runAsyncStatus waits, under ctx (the async_status task's own limit), for
// the background job its jid parameter names on device, and returns that
// job's result as its own, the job's own result beside it for the run to
// journal under the job's node.
func (r *run) runAsyncStatus(ctx context.Context, cmd nodeExecution, device string) (ActionResult, *NodeResult, error) {
	id, _ := cmd.Task.Params["jid"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return ActionResult{}, nil, fmt.Errorf("async_status: jid, the job id a background task registered (ansible_job_id), is required")
	}
	job, err := r.jobs.claim(id, device)
	if err != nil {
		return ActionResult{}, nil, err
	}
	select {
	case <-job.done:
	case <-ctx.Done():
		// Not collected after all: Run's drain still waits for it and
		// journals it.
		r.jobs.mu.Lock()
		job.collected = false
		r.jobs.mu.Unlock()
		return ActionResult{}, nil, fmt.Errorf("async_status: job %s had not finished: %w", id, ctx.Err())
	}
	collected := job.result
	collected.fromJob = true
	stats := map[string]interface{}{StatJobID: id, StatStarted: 1, StatFinished: 1}
	for k, v := range job.result.Stats {
		stats[k] = v
	}
	out := ActionResult{Changed: job.result.Changed, Stats: stats}
	if job.result.Err != nil {
		return out, &collected, fmt.Errorf("async_status: the background task %s failed: %w", taskLabel(job.nodeID, r.dag.Nodes[job.nodeID]), job.result.Err)
	}
	return out, &collected, nil
}

// drainJobs waits for every job nobody collected, each bounded by its own
// async limit, and returns their results to be journaled and reported, so a
// run never ends with a change outside its journal.
func (r *run) drainJobs() []NodeResult {
	var out []NodeResult
	for _, job := range r.jobs.uncollected() {
		<-job.done
		out = append(out, job.result)
	}
	return out
}

// reportProgress publishes that cmd's task is still running every interval
// until stop closes, for an async task running where it stands.
func (r *run) reportProgress(cmd nodeExecution, host string, interval, limit time.Duration, stop <-chan struct{}) {
	started := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			r.publish(cmd.NodeID, cmd.Task, host, "running", fmt.Sprintf("still running after %s of its async limit %s", time.Since(started).Round(time.Second), limit))
		}
	}
}
