// Connection reuse across tasks: a Pool keeps one authenticated SSH
// connection per device open between the tasks of one run, the way
// Ansible's ControlPersist does, so a runbook of ten tasks against a
// device pays for one login instead of ten.
package remoteexec

import (
	"context"
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

// DefaultPoolIdle is how long a pooled connection may sit unused before
// the Pool closes it. A run's next task against a device normally
// follows within a second; a minute covers a slow task on another device
// in between without keeping a login open for the length of a long run.
const DefaultPoolIdle = 60 * time.Second

// defaultKeepaliveTimeout bounds the liveness check made before a pooled
// connection is lent again. A live device answers in one round trip.
const defaultKeepaliveTimeout = 3 * time.Second

// errReleased is returned by a borrowed Conn used after its Close.
var errReleased = errors.New("remoteexec: connection used after Close")

// Pool lends connections to the tasks of one run, keyed by the device
// they reach. A borrowed Conn's Close gives the connection back rather
// than closing it, and the next Connect for the same device, address,
// credential and host key policy gets it again.
//
// Reuse is refused, and a fresh connection dialed in its place, whenever
// reuse could mean anything other than what a new login would have
// meant:
//
//   - a different address, credential, host key mode or known_hosts path
//     for the device closes the pooled connection and dials anew, so a
//     task never runs on a login made with another task's credential;
//   - a known_hosts file changed since the connection was made closes it,
//     so a host key an operator just removed is checked again at once;
//   - a connection that does not answer an SSH keepalive is closed before
//     any command is sent on it, and a device that never answers one is
//     not pooled again in this run;
//   - a connection that opened a terminal or a subsystem, started a
//     streamed process, or had a command cut off, is closed rather than
//     given back (see Conn.taint).
//
// A Pool is safe for concurrent use. Two tasks against the same device
// at once (which the engine's device lock normally prevents) do not
// share a connection: the second gets a fresh one of its own, closed on
// its Close.
type Pool struct {
	idle             time.Duration
	keepaliveTimeout time.Duration

	mu      sync.Mutex
	entries map[string]*pooled
	// silent is every device that did not answer a keepalive in time.
	// Such a device is connected to afresh for each task, exactly as
	// without a pool, rather than paying the timeout on every reuse.
	silent map[string]bool
	closed bool
}

// poolKey is everything a login's meaning depends on. Two Connects for
// one device share a connection only when their keys are equal.
type poolKey struct {
	addr       string
	identity   [sha256.Size]byte
	insecure   bool
	knownHosts string
}

// pooled is one open connection and what the Pool knows about it.
type pooled struct {
	key  poolKey
	conn *Conn
	// stamp is a digest of the known_hosts file as it was when the
	// connection was verified; zero when verification was skipped.
	stamp [sha256.Size]byte
	lent  bool
	timer *time.Timer
}

// lease is a borrowed Conn's tie to its Pool entry.
type lease struct {
	pool   *Pool
	device string
	entry  *pooled

	mu       sync.Mutex
	tainted  bool
	released bool
}

// NewPool returns an empty Pool whose connections close after idle
// without use, or after DefaultPoolIdle when idle is not positive.
func NewPool(idle time.Duration) *Pool {
	if idle <= 0 {
		idle = DefaultPoolIdle
	}
	return &Pool{
		idle:             idle,
		keepaliveTimeout: defaultKeepaliveTimeout,
		entries:          map[string]*pooled{},
		silent:           map[string]bool{},
	}
}

// Connect returns a connection to target for device, lent from the Pool
// when an equal one is open and still answers, and dialed through r
// otherwise. The caller must Close it, which gives it back.
//
// device names the inventory item the connection is for, and is the
// unit Discard acts on. Connections are never pooled through jump hosts:
// a caller that needs hops uses Runner.Connect.
func (p *Pool) Connect(ctx context.Context, r *Runner, device string, target Target, auth Auth) (*Conn, error) {
	key, ok := p.keyFor(r, target, auth)
	if !ok {
		return r.Connect(ctx, nil, target, auth)
	}
	if conn, ok := p.reuse(ctx, device, key); ok {
		return conn, nil
	}
	p.mu.Lock()
	skip := p.closed || p.silent[device] || p.entries[device] != nil
	p.mu.Unlock()
	if skip {
		return r.Connect(ctx, nil, target, auth)
	}

	// Stat before dialing, so an edit racing the dial reads as a change
	// at the next reuse and forces a fresh check, never the reverse.
	stamp, err := knownHostsStamp(key)
	if err != nil {
		return r.Connect(ctx, nil, target, auth)
	}
	conn, err := r.Connect(ctx, nil, target, auth)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.entries[device] != nil {
		return conn, nil
	}
	e := &pooled{key: key, conn: conn, stamp: stamp, lent: true}
	p.entries[device] = e
	return p.lend(device, e), nil
}

// keyFor returns the key a connection made by r to target with auth
// would carry, and false when it must not be pooled: an Auth with no
// identity, or a known_hosts path that cannot be resolved (the dial will
// report that itself).
func (p *Pool) keyFor(r *Runner, target Target, auth Auth) (poolKey, bool) {
	if auth.identity == ([sha256.Size]byte{}) {
		return poolKey{}, false
	}
	key := poolKey{addr: target.Addr(), identity: auth.identity, insecure: r.opts.InsecureSkipHostKeyVerify}
	if !key.insecure {
		path, err := knownHostsPath(r.opts)
		if err != nil {
			return poolKey{}, false
		}
		key.knownHosts = path
	}
	return key, true
}

// reuse lends device's pooled connection when its key equals key, its
// known_hosts file is unchanged and it answers a keepalive. Any entry
// that fails one of those is closed. It returns false when nothing was
// lent.
func (p *Pool) reuse(ctx context.Context, device string, key poolKey) (*Conn, bool) {
	p.mu.Lock()
	e := p.entries[device]
	if e == nil || e.lent || p.closed {
		p.mu.Unlock()
		return nil, false
	}
	if e.key != key || !stampUnchanged(e) {
		delete(p.entries, device)
		e.timer.Stop()
		p.mu.Unlock()
		_ = e.conn.closeChain() // replaced by a fresh dial, which reports its own errors
		return nil, false
	}
	e.lent = true
	e.timer.Stop()
	p.mu.Unlock()

	switch alive(ctx, e.conn, p.keepaliveTimeout) {
	case answered:
		return p.lendLocked(device, e), true
	case silent:
		p.mu.Lock()
		p.silent[device] = true
		p.mu.Unlock()
	}
	p.forget(device, e)
	_ = e.conn.closeChain() // dead or silent: a fresh dial follows
	return nil, false
}

// lendLocked is lend for an entry already marked lent outside the lock.
func (p *Pool) lendLocked(device string, e *pooled) *Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lend(device, e)
}

// lend wraps e's connection in a borrowed Conn. p.mu must be held.
func (p *Pool) lend(device string, e *pooled) *Conn {
	return &Conn{client: e.conn.client, chain: e.conn.chain, addr: e.conn.addr, lease: &lease{pool: p, device: device, entry: e}}
}

// forget removes e from the Pool when it is still device's entry.
func (p *Pool) forget(device string, e *pooled) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.entries[device] == e {
		delete(p.entries, device)
	}
}

// giveBack returns e to the Pool after a borrower's Close, or closes it
// when it was tainted, discarded or replaced, or the Pool is closed.
func (p *Pool) giveBack(device string, e *pooled, tainted bool) error {
	p.mu.Lock()
	if p.closed || tainted || p.entries[device] != e {
		if p.entries[device] == e {
			delete(p.entries, device)
		}
		p.mu.Unlock()
		return e.conn.closeChain()
	}
	e.lent = false
	e.timer = time.AfterFunc(p.idle, func() { p.expire(device, e) })
	p.mu.Unlock()
	return nil
}

// expire closes e when it has sat unused for the idle period.
func (p *Pool) expire(device string, e *pooled) {
	p.mu.Lock()
	if p.entries[device] != e || e.lent {
		p.mu.Unlock()
		return
	}
	delete(p.entries, device)
	p.mu.Unlock()
	_ = e.conn.closeChain() // nothing is waiting on this close to report to
}

// Discard closes device's pooled connection, now when it is idle or at
// its borrower's Close when it is lent, so the device's next Connect
// logs in again. It is what a task that changes the login itself (a
// user's groups, the account's own shell) calls, since a connection made
// before that change still carries the old login's state.
func (p *Pool) Discard(device string) {
	p.mu.Lock()
	e := p.entries[device]
	delete(p.entries, device)
	p.mu.Unlock()
	if e == nil || e.lent {
		return
	}
	e.timer.Stop()
	_ = e.conn.closeChain() // the device's next Connect dials afresh either way
}

// Close closes every idle connection at once and every lent one at its
// borrower's Close. A Connect after Close dials a connection of its own
// that is not pooled. It returns the first error closing any
// connection.
func (p *Pool) Close() error {
	p.mu.Lock()
	p.closed = true
	var idle []*pooled
	for device, e := range p.entries {
		if !e.lent {
			e.timer.Stop()
			idle = append(idle, e)
		}
		delete(p.entries, device)
	}
	p.mu.Unlock()
	var first error
	for _, e := range idle {
		if err := e.conn.closeChain(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// taint marks the lease's connection as not to be given back.
func (l *lease) taint() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tainted = true
}

// isReleased reports whether the borrower has already called Close.
func (l *lease) isReleased() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.released
}

// release is a borrowed Conn's Close: the first call gives the
// connection back, and a second is an error, as it would be for a Conn
// that was really closed.
func (l *lease) release() error {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return errReleased
	}
	l.released = true
	tainted := l.tainted
	l.mu.Unlock()
	return l.pool.giveBack(l.device, l.entry, tainted)
}
