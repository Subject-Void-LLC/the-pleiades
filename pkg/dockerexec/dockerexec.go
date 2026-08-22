// Package dockerexec runs one command inside one running container
// through the Docker Engine API's exec endpoints, reached over the
// daemon's own control socket. It is the single place this platform
// speaks to that socket at all.
//
// # Exec-only, by construction, not by discipline
//
// Docker daemon socket access is root-equivalent on the host: whoever
// can reach it can, in general, ask the daemon to create a privileged
// container with the host's own root filesystem bind-mounted in, and
// read or write anything from inside it. This package makes that
// impossible structurally, not by convention: every request it can
// possibly send is checked against a fixed, positive allowlist of
// exactly three (method, path) pairs — POST /containers/{id}/exec,
// POST /exec/{id}/start, GET /exec/{id}/json — in checkAllowed, the ONE
// function every request in this package funnels through, before a
// single byte reaches the socket. There is no exported, or internally
// reachable, "send this request to the daemon" method a future caller
// (in this package or another) could widen into a passthrough.
//
// Two mitigations that do NOT work, and are deliberately not attempted
// here:
//
//   - A read-only socket mount. The Docker API is HTTP carried over that
//     socket; file permissions restrict who can OPEN the socket, not
//     which HTTP verbs a connection that has already opened it may send.
//   - Running the calling process as non-root. The privilege that
//     matters is the DAEMON's, not the caller's: a completely
//     unprivileged process that can merely open the socket can still ask
//     the (root) daemon to do anything the API allows on its behalf.
//
// The only real defense is the one this package actually applies: never
// construct a request outside the three exec-lifecycle endpoints, ever.
// Phase 73's own bastion-proof workstream proves this by attempting
// escalation through every public entry point this package exposes.
//
// # Real exit status, unlike this phase's other byte-stream transports
//
// Docker's own exec-inspect endpoint (GET /exec/{id}/json) reports a
// genuine ExitCode once the command has finished. Result carries no
// ExitStatusUnknown field at all, unlike pkg/serialexec, pkg/serialtcp,
// and pkg/telnetexec: this transport has a real exit status, always.
//
// # The command runs through the container's own shell, like every other adapter's target
//
// Cmd is sent as ["/bin/sh", "-c", command] — Docker's API takes Cmd as
// an argv array and does not itself interpret shell syntax (pipes,
// redirection, quoting) the way a remote sshd's own shell does for
// ssh_exec. Wrapping command in "/bin/sh -c" is what makes a Docker exec
// command behave the same way every other Transport in this module
// already does: the string arrives at whatever normally interprets
// commands on the target verbatim, as ONE argument, never re-split or
// locally shell-expanded by this package itself. This assumes the target
// image has /bin/sh, true of essentially every Linux container image.
//
// # The Docker API version is pinned
//
// Every request names a fixed API version (apiVersion below) rather than
// the version-less endpoint form the daemon also accepts, which silently
// resolves to whatever the daemon's own minimum supported version
// happens to be — a moving target this package could not pin or test
// against. Bumping apiVersion is a reviewed decision, mirroring the
// Makefile's own GOSEC_VERSION/GOVULNCHECK_VERSION precedent, not a
// default a newer daemon should be allowed to silently renegotiate. Pinned
// to v1.43 (Docker Engine 24.x+), the earliest version with no known gaps
// against the three endpoints this package speaks.
//
// # Cancellation closes the connection, not just checks between reads
//
// Unlike pkg/serialexec/pkg/serialtcp/pkg/telnetexec/pkg/rfc2217's own
// documented "checked only between reads" limitation, every request this
// package makes is guarded by a goroutine that closes the underlying
// net.Conn the instant ctx is done, unblocking any in-flight Read or
// Write immediately rather than waiting out a per-call timeout. This is
// a genuine improvement available here specifically because the hijacked
// exec-start stream already requires holding one raw net.Conn open
// across write-then-read, making the watcher-goroutine idiom the natural
// fit; it was not retrofitted onto the sibling packages' already-shipped,
// separately-verified read loops.
//
// # No "quiet period" heuristic, unlike this phase's other byte-stream transports
//
// Docker's own exec-start stream is real length-prefixed framing
// (RFC-less, but a stable, documented Docker protocol: an 8-byte header
// per frame — stream type byte, 3 reserved bytes, 4-byte big-endian
// payload length — repeated until the connection closes), not an
// undelimited byte stream. Completion is therefore a genuine EOF, not a
// guess based on a quiet period the way pkg/serialexec, pkg/serialtcp,
// and pkg/telnetexec must infer one. There is no ReadTimeout option here
// for that reason: nothing to tune, because there is no heuristic.
//
// A handful of branches — json.Marshal failing on this package's own
// fixed request structs, http.NewRequestWithContext failing on a fixed,
// well-formed URL, and req.Write/http.ReadResponse failing against a
// freshly dialed, working connection — are real defensive coverage but
// not reachable from this package's own test suite without fault
// injection this module does not fabricate, the identical class of gap
// pkg/serialexec, pkg/serialtcp, pkg/telnetexec, and pkg/rfc2217 each
// already document for the same reason.
package dockerexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// apiVersion is the Docker Engine API version every request in this
// package names explicitly. See the package doc comment.
const apiVersion = "v1.43"

// Options configures how Exec reaches the daemon.
type Options struct {
	// DialTimeout bounds the initial connection to the daemon socket for
	// each of the three requests Exec makes. Zero takes
	// DefaultDialTimeout.
	DialTimeout time.Duration

	// MaxOutputBytes bounds the combined stdout+stderr accumulated from
	// the exec-start stream before a run is refused outright, so a
	// command stuck emitting output forever cannot exhaust memory. Zero
	// takes DefaultMaxOutputBytes.
	MaxOutputBytes int
}

const (
	// DefaultDialTimeout is how long Exec waits for the initial
	// connection to the daemon socket before giving up.
	DefaultDialTimeout = 10 * time.Second

	// DefaultMaxOutputBytes bounds accumulated stdout+stderr. 1 MiB, the
	// same order of magnitude pkg/sdk's own structured-input caps use
	// elsewhere in this module for document-shaped content.
	DefaultMaxOutputBytes = 1 << 20
)

// Result is what running one command inside one container produced.
type Result struct {
	// Stdout is everything the command wrote to its standard output.
	Stdout string

	// Stderr is everything the command wrote to its standard error.
	Stderr string

	// ExitCode is the command's real process exit status, reported by
	// the daemon's own exec-inspect endpoint. Always meaningful, unlike
	// this phase's other byte-stream transports — see the package doc
	// comment.
	ExitCode int
}

// idPattern is what a Docker container ID, container name, or exec
// instance ID may look like: this package's own, deliberately strict
// definition, independent of whatever the daemon itself would accept,
// since the point is refusing anything that could alter a URL path's
// shape (an extra "/", a "?", a "#", whitespace, a NUL byte, a
// traversal sequence) BEFORE it is ever interpolated into one.
var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func validateID(kind, id string) error {
	if id == "" {
		return fmt.Errorf("%s must not be empty", kind)
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%s %q contains characters not allowed in a Docker identifier", kind, id)
	}
	return nil
}

// allowedRequests is the fixed, positive allowlist checkAllowed enforces:
// exactly the three (method, path-shape) pairs this package will ever
// send. The path patterns re-validate the ID segment's shape
// independently of validateID (called earlier, at each ID's own point of
// origin), so this chokepoint is a real, self-contained safety net —
// not a formality that trusts every caller already sanitized its input.
var allowedRequests = []struct {
	method  string
	pattern *regexp.Regexp
}{
	{http.MethodPost, regexp.MustCompile(`^/` + apiVersion + `/containers/[a-zA-Z0-9][a-zA-Z0-9_.-]*/exec$`)},
	{http.MethodPost, regexp.MustCompile(`^/` + apiVersion + `/exec/[a-zA-Z0-9][a-zA-Z0-9_.-]*/start$`)},
	{http.MethodGet, regexp.MustCompile(`^/` + apiVersion + `/exec/[a-zA-Z0-9][a-zA-Z0-9_.-]*/json$`)},
}

// checkAllowed is the one function every request this package sends
// funnels through. See the package doc comment.
func checkAllowed(method, path string) error {
	for _, a := range allowedRequests {
		if a.method == method && a.pattern.MatchString(path) {
			return nil
		}
	}
	return fmt.Errorf("dockerexec: refusing disallowed request %s %s", method, path)
}

// Exec creates an exec instance for command inside containerID, runs it
// attached (so its output can be captured), and reports its real exit
// code, all over the daemon socket at socket (a Unix socket path on
// POSIX; see the note on Windows named pipes below).
//
// Windows named pipe sockets ("npipe:...") are refused outright with a
// clear error rather than silently misdialed: this package only
// implements the POSIX Unix-socket path. Running the Controller on
// Windows at all is an explicit stretch goal this Part is allowed to
// miss (see CLAUDE.md), and half-implementing named pipe support instead
// of stating the gap plainly would be worse than refusing it.
func Exec(ctx context.Context, socket, containerID string, opts Options, command string) (Result, error) {
	if strings.HasPrefix(socket, "npipe:") {
		return Result{}, errors.New("dockerexec: Windows named pipe sockets are not supported by this platform's Docker exec client")
	}
	if err := validateID("container id", containerID); err != nil {
		return Result{}, fmt.Errorf("dockerexec: %w", err)
	}

	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = DefaultDialTimeout
	}
	maxOutput := opts.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}
	c := &client{socket: socket, dialTimeout: dialTimeout, maxOutput: maxOutput}

	execID, err := c.createExec(ctx, containerID, command)
	if err != nil {
		return Result{}, fmt.Errorf("dockerexec: %w", err)
	}
	if err := validateID("exec instance id", execID); err != nil {
		return Result{}, fmt.Errorf("dockerexec: daemon returned an unusable exec id: %w", err)
	}

	stdout, stderr, err := c.startExec(ctx, execID)
	if err != nil {
		return Result{}, fmt.Errorf("dockerexec: %w", err)
	}

	exitCode, err := c.inspectExec(ctx, execID)
	if err != nil {
		return Result{}, fmt.Errorf("dockerexec: %w", err)
	}

	return Result{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}, nil
}

type client struct {
	socket      string
	dialTimeout time.Duration
	maxOutput   int
}

// dial connects to c.socket and arranges for the connection to close the
// instant ctx is done, so a blocking Write or Read on it is interrupted
// promptly rather than only checked between calls. The returned cleanup
// func must be deferred by the caller once the connection itself is no
// longer needed.
func (c *client) dial(ctx context.Context) (net.Conn, func(), error) {
	dialCtx, cancel := context.WithTimeout(ctx, c.dialTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", c.socket)
	if err != nil {
		return nil, nil, fmt.Errorf("dialing docker socket %s: %w", c.socket, err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	return conn, func() { close(done); _ = conn.Close() }, nil
}

type createExecRequest struct {
	Cmd          []string `json:"Cmd"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
}

type createExecResponse struct {
	ID string `json:"Id"`
}

func (c *client) createExec(ctx context.Context, containerID, command string) (string, error) {
	path := fmt.Sprintf("/%s/containers/%s/exec", apiVersion, containerID)
	reqBody := createExecRequest{Cmd: []string{"/bin/sh", "-c", command}, AttachStdout: true, AttachStderr: true}

	var respBody createExecResponse
	if err := c.doJSON(ctx, http.MethodPost, path, reqBody, &respBody); err != nil {
		return "", fmt.Errorf("creating exec instance: %w", err)
	}
	return respBody.ID, nil
}

type inspectExecResponse struct {
	ExitCode int `json:"ExitCode"`
}

func (c *client) inspectExec(ctx context.Context, execID string) (int, error) {
	path := fmt.Sprintf("/%s/exec/%s/json", apiVersion, execID)

	var respBody inspectExecResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &respBody); err != nil {
		return 0, fmt.Errorf("inspecting exec instance: %w", err)
	}
	return respBody.ExitCode, nil
}

// doJSON sends one ordinary (non-hijacked) JSON request/response over a
// fresh connection: createExec and inspectExec's own shape.
func (c *client) doJSON(ctx context.Context, method, path string, reqBody, respBody any) error {
	if err := checkAllowed(method, path); err != nil {
		return err
	}

	conn, cleanup, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer cleanup()

	var bodyReader io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, bodyReader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if err := req.Write(conn); err != nil {
		return fmt.Errorf("writing request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.maxOutput)))
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("docker daemon returned %s: %s", resp.Status, bytes.TrimSpace(body))
	}
	if respBody != nil && len(body) > 0 {
		if err := json.Unmarshal(body, respBody); err != nil {
			return fmt.Errorf("decoding response body: %w", err)
		}
	}
	return nil
}

type startExecRequest struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

// startExec starts execID attached (Detach: false, Tty: false) and reads
// its multiplexed stdout/stderr stream to completion.
func (c *client) startExec(ctx context.Context, execID string) (stdout, stderr string, err error) {
	path := fmt.Sprintf("/%s/exec/%s/start", apiVersion, execID)
	if err := checkAllowed(http.MethodPost, path); err != nil {
		return "", "", err
	}

	conn, cleanup, err := c.dial(ctx)
	if err != nil {
		return "", "", err
	}
	defer cleanup()

	reqBodyJSON, err := json.Marshal(startExecRequest{Detach: false, Tty: false})
	if err != nil {
		return "", "", fmt.Errorf("encoding request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+path, bytes.NewReader(reqBodyJSON))
	if err != nil {
		return "", "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if err := req.Write(conn); err != nil {
		return "", "", fmt.Errorf("writing request: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return "", "", fmt.Errorf("reading response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("docker daemon returned %s: %s", resp.Status, bytes.TrimSpace(body))
	}

	outBytes, errBytes, err := readDemux(resp.Body, c.maxOutput)
	if err != nil {
		return "", "", fmt.Errorf("reading exec output: %w", err)
	}
	return string(outBytes), string(errBytes), nil
}

// readDemux reads Docker's own exec-attach stream framing from r until a
// clean EOF (the daemon closing the connection once the command has
// finished — see the package doc comment for why this is a real
// completion signal here, not a heuristic): a repeating 8-byte header
// (stream type byte, 3 reserved bytes, 4-byte big-endian payload
// length) followed by that many payload bytes, stream type 1 meaning
// stdout and 2 meaning stderr. Refuses outright, rather than silently
// truncating, once accumulated stdout+stderr exceeds maxOutput.
func readDemux(r io.Reader, maxOutput int) (stdout, stderr []byte, err error) {
	header := make([]byte, 8)
	for {
		n, err := io.ReadFull(r, header)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return stdout, stderr, nil
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return stdout, stderr, fmt.Errorf("stream ended mid-frame after %d of %d header bytes", n, len(header))
			}
			return stdout, stderr, fmt.Errorf("reading frame header: %w", err)
		}

		streamType := header[0]
		size := binary.BigEndian.Uint32(header[4:8])

		// Checked against the remaining budget BEFORE allocating payload,
		// not after appending it: size is a 4-byte field this package
		// reads directly off the wire, and make([]byte, size) first would
		// let one frame claiming close to 4 GiB force that allocation
		// regardless of maxOutput -- the daemon is presumed non-hostile,
		// but a corrupted or truncated stream produces the identical
		// bytes a hostile one would, the same "cap the decoded side, not
		// just the encoded side" gap FAILURE_PATTERNS.md already records
		// once for this module's own GzipDecompress filter.
		if int64(len(stdout))+int64(len(stderr))+int64(size) > int64(maxOutput) {
			return nil, nil, fmt.Errorf("output exceeded %d bytes", maxOutput)
		}

		payload := make([]byte, size)
		if _, err := io.ReadFull(r, payload); err != nil {
			return stdout, stderr, fmt.Errorf("reading %d-byte frame payload: %w", size, err)
		}

		switch streamType {
		case 1:
			stdout = append(stdout, payload...)
		case 2:
			stderr = append(stderr, payload...)
		}
	}
}
