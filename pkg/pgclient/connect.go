// Package pgclient: every connect bounded, on a stream that may refuse
// deadlines.
package pgclient

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sync"
	"time"

	"github.com/lib/pq"
)

// defaultConnectTimeout bounds opening a connection when the target sets
// no bound of its own.
const defaultConnectTimeout = 30 * time.Second

// connector is the driver's connector with every connect bounded by its
// context and by bound. The driver reads the server's answers to its
// startup and authentication with no context, and bounds them only with a
// deadline on the stream, which an SSH channel (pkg/remoteexec's DialTCP
// and DialUnix) refuses; a server that accepted and stayed silent would
// hold the connect forever. So the stream a connect dials is closed when
// either ends first, until the connection is open.
type connector struct {
	*pq.Connector
	bound time.Duration
}

// Connect implements driver.Connector.
func (c connector) Connect(ctx context.Context) (driver.Conn, error) {
	bounded, cancel := context.WithTimeout(ctx, c.bound)
	defer cancel()
	w := &watch{}
	conn, err := c.Connector.Connect(context.WithValue(bounded, watchKey{}, w))
	if w.release() {
		return conn, err
	}
	// The stream was closed under the driver, so whatever it returned,
	// there is no connection to keep.
	if err == nil {
		_ = conn.Close()
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("opening the connection: %w", ctx.Err())
	}
	return nil, fmt.Errorf("the connection did not open within %s: %w", c.bound, context.DeadlineExceeded)
}

// watchKey is the context key a connect's watch reaches the dialer under.
type watchKey struct{}

// watch holds what disarms each stream closer the dialer armed for one
// connect, so the connect can disarm them once its connection is open.
type watch struct {
	mu    sync.Mutex
	stops []func() bool
}

// add keeps stop, which disarms one stream's closer.
func (w *watch) add(stop func() bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stops = append(w.stops, stop)
}

// release disarms every closer, reporting false when one had already
// closed its stream.
func (w *watch) release() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	disarmed := true
	for _, stop := range w.stops {
		// stop reports false when its closer has already run.
		if !stop() {
			disarmed = false
		}
	}
	w.stops = nil
	return disarmed
}
