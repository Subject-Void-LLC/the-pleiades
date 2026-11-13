// Waiting for a host to serve WinRM again, after a restart or a change
// that cut its connection: the one way to reach a host that the endpoint's
// circuit breaker neither counts nor refuses.
package winrmexec

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// WaitIntervalFloor is the least time between the starts of two attempts of
// one Waiter, whatever its caller asks for, so no wait can hammer a host.
const WaitIntervalFloor = time.Second

// ErrNotServing is wrapped by an attempt that found the host not serving
// yet: nothing answered, the listener refused or was not WinRM's, or the
// command failed short of an answer. A wait tries again later. Any other
// error from a Waiter ends the wait.
var ErrNotServing = errors.New("not serving yet")

// CredentialRefusedError is the host refusing the wait's credential. It ends
// the wait, and the Waiter never sends the credential again: a wait that
// retried a wrong password every few seconds would lock the account, and a
// domain account locks everywhere at once.
type CredentialRefusedError struct {
	// Err is the refusal as the transport reported it.
	Err error
}

// Error implements error.
func (e *CredentialRefusedError) Error() string {
	return "winrm: the host refused the credential, so the wait stops here rather than send it again: " + e.Err.Error()
}

// Unwrap exposes Err to errors.Is and errors.As.
func (e *CredentialRefusedError) Unwrap() error { return e.Err }

// Waiter is one wait for a WinRM endpoint to serve again. Build one per
// wait with NewWaiter and call Run or Probe once per try.
//
// A wait differs from every other call in two ways, and both are why it
// is its own entry. Its failures are expected by the dozen, so they are
// not counted: a counted wait would open the endpoint's circuit for every
// task on the host during a reboot. And it is not refused: a refused wait
// would see the host only at probe times, or lose the probe to another
// task. What it does report is its success. A command that ran closes the
// circuit, so the task after the wait is sent at once rather than refused
// for the rest of a cooldown that other callers' failures started, and a
// listener heard answering ends the cooldown, so the next counted call is
// the probe.
//
// Each try first asks the listener with no credential at all, and sends
// the credential only once the listener answers with the 401 a WinRM
// listener gives a request with no Authorization header. That request is
// not a logon attempt, so a host coming back, or one with the wrong
// password on record, costs the account nothing until the host is ready to
// judge the credential, and the first refusal ends the wait: a wrong
// password is sent once per wait.
//
// Tries never overlap and start at least WaitIntervalFloor apart. The
// waits are the only callers: internal/archtest's
// TestOnlyWaitsUseTheUncountedWinRMEntry holds everything else to Execute.
type Waiter struct {
	target Target
	auth   Auth
	opts   Options
	addr   string

	// mu is held for a whole try, so two never overlap.
	mu sync.Mutex
	// last is when the previous try began, for the floor.
	last time.Time
	// refused is the first credential refusal, returned from then on
	// without sending anything.
	refused error
	// sends counts the tries that sent the credential.
	sends int

	// listen, run, sleep and now are how a try reaches the host, waits and
	// reads the clock; this package's tests replace them.
	listen func(ctx context.Context) error
	run    func(ctx context.Context, cmd Command) (Result, error)
	sleep  func(ctx context.Context, d time.Duration) error
	now    func() time.Time
}

// NewWaiter returns a Waiter for target, authenticating with auth. opts
// bounds each half of a try (the listener check and the command) by its
// Timeout, as it bounds any call.
func NewWaiter(target Target, auth Auth, opts Options) *Waiter {
	w := &Waiter{target: target, auth: auth, opts: opts, addr: Addr(target, auth, opts), sleep: sleepCtx, now: time.Now}
	w.listen = func(ctx context.Context) error { return listenerAnswers(ctx, target, auth, opts) }
	w.run = func(ctx context.Context, cmd Command) (Result, error) {
		return execute(ctx, target, auth, cmd, opts, false)
	}
	return w
}

// Sends reports how many tries so far sent the credential.
func (w *Waiter) Sends() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sends
}

// Probe is Run with the cheapest command that proves a session: it logs
// in, opens a shell, runs, and returns output.
func (w *Waiter) Probe(ctx context.Context) error {
	_, err := w.Run(ctx, Command{Shell: ShellPowerShell, Script: probeScript})
	return err
}

// Run makes one try: it asks the listener without a credential, and once
// the listener answers it runs cmd once, authenticated. It returns the
// command's Result when it ran; an error wrapping ErrNotServing when the
// host is not serving yet; and any other error when the wait should stop:
// a *CredentialRefusedError, returned again by every later call without
// sending anything, or a mistake in cmd or the credential that asking
// again cannot fix.
func (w *Waiter) Run(ctx context.Context, cmd Command) (Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.refused != nil {
		return Result{}, w.refused
	}
	// What the caller can get wrong is refused before anything is sent, and
	// ends the wait: it will not change by being asked again.
	if err := checkHost(w.target.Host); err != nil {
		return Result{}, err
	}
	if err := w.auth.Validate(); err != nil {
		return Result{}, err
	}
	if cmd.Script == "" {
		return Result{}, fmt.Errorf("winrm: empty script")
	}
	if err := CheckScript(cmd.Shell, cmd.Script, w.opts.resolve(w.auth)); err != nil {
		return Result{}, err
	}

	if !w.last.IsZero() {
		if gap := WaitIntervalFloor - w.now().Sub(w.last); gap > 0 {
			if err := w.sleep(ctx, gap); err != nil {
				return Result{}, w.notYet(ctx, err)
			}
		}
	}
	w.last = w.now()

	if err := w.listen(ctx); err != nil {
		return Result{}, w.notYet(ctx, err)
	}
	// The listener answered: the host is there, though not yet shown to
	// serve, so the cooldown ends and nothing closes.
	endpoints.answered(w.addr)

	w.sends++
	res, err := w.run(ctx, cmd)
	switch {
	case err == nil:
		endpoints.served(w.addr)
		return res, nil
	case credentialRefused(err):
		w.refused = &CredentialRefusedError{Err: err}
		return Result{}, w.refused
	}
	return Result{}, w.notYet(ctx, err)
}

// notYet is the error for a try that did not reach a serving host. A try
// whose own deadline ran out is one more try that did not answer, and the
// wait goes on; a canceled context is the caller giving up, and is
// returned as it is so the wait stops.
func (w *Waiter) notYet(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %w", ErrNotServing, err)
}

// credentialRefused reports whether err is the host refusing the credential,
// as each of this package's three transports reports it: the sealed NTLM
// transport's handshake refused with 401 (errLoginRefused), the library's
// NTLM over HTTPS ("http response error: 401", its own wording, read from
// its source), and a client certificate refused with a TLS alert, which Go
// reports as a *net.OpError with Op "remote error", or with a 401 or 403
// that is not a WS-Man reply.
func credentialRefused(err error) bool {
	if errors.Is(err, errLoginRefused) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "remote error" {
		return true
	}
	text := err.Error()
	return strings.Contains(text, "http response error: 401") ||
		strings.Contains(text, " answered 401 with ") || strings.Contains(text, " answered 403 with ")
}

// listenerAnswers sends target's WS-Man listener one request with no
// Authorization header and reports nil when it answers 401, which is what
// the WinRM service sends a request with no credential once it serves.
// Anything else is an error: nothing answered, or something that is not
// WinRM's listener did (http.sys answers 503 for a URL whose service has
// not started yet).
//
// Over HTTPS the server's certificate is verified against the pinned
// authority or the system's roots and the pinned name, as the real call
// verifies it, and nothing is presented: a check that skipped verification
// would report a listener the authenticated call then cannot reach.
// Options.Insecure is not honored here; nothing in the platform sets it.
func listenerAnswers(ctx context.Context, target Target, auth Auth, opts Options) error {
	opts = opts.resolve(auth)
	ctx, cancel := withOperationDeadline(ctx, opts.Timeout)
	defer cancel()

	scheme := "http"
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DisableKeepAlives: true}
	if opts.HTTPS {
		scheme = "https"
		config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: opts.ServerName}
		if len(opts.CACert) > 0 {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(opts.CACert) {
				return fmt.Errorf("winrm: the CA bundle holds no certificate this platform could read")
			}
			config.RootCAs = pool
		}
		transport.TLSClientConfig = config
	}
	defer transport.CloseIdleConnections()

	u := url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(unbracket(target.Host), strconv.Itoa(ResolvePort(target.Port, opts.HTTPS))),
		Path:   "/wsman",
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), nil)
	if err != nil {
		return fmt.Errorf("winrm: building the listener check for %s: %w", u.String(), err)
	}
	request.Header.Set("Content-Type", soapContentType)
	client := &http.Client{
		Transport: transport,
		// A redirect would carry the next request somewhere this device's
		// record does not name.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("winrm: the listener at %s did not answer: %w", u.String(), err)
	}
	// Drained a little and closed: only the status matters.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("winrm: %s answered HTTP %d to a request with no credential, not the 401 a serving WinRM listener sends", u.String(), response.StatusCode)
	}
	return nil
}

// sleepCtx waits for d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
