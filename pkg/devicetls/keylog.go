// Package devicetls: a run's TLS key log, the one file that decrypts the
// TLS sessions a run's device clients open, written only when a person asks
// for it on the command line (pleiades run and adhoc --tls-keylog).
//
// A key log is how a person reads a capture of an encrypted session: Go's
// crypto/tls writes each session's secrets in the NSS key log format
// (CLIENT_RANDOM for TLS 1.2, CLIENT_TRAFFIC_SECRET_0 and its kin for TLS
// 1.3), and a packet tool given the file decrypts the capture. That makes the
// file as secret as every credential the sessions carry, since a gNMI login
// rides the gRPC metadata and an HTTP one the Authorization header, both
// inside the encryption the file undoes. So it is never written unless asked
// for, never written over or added to, created readable by its owner alone,
// and nothing here reads it back: what reaches it goes nowhere else.
//
// The key log travels on a run's context (WithKeyLog), and only the clients
// built with Settings.ConfigContext under that context write to it. The
// command line is the one place that puts it there; a Runner and the
// Controller never do, which internal/archtest holds.
package devicetls

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// KeyLog is one run's TLS key log: a file created for the run alone, shared
// by every device session in it. It is safe for concurrent use.
type KeyLog struct {
	// path is the file's absolute path, which may be shown; its contents may
	// never be.
	path string

	// mu serializes writes and Close. crypto/tls already serializes its own
	// writes to a KeyLogWriter, but a KeyLog does not rely on that.
	mu sync.Mutex
	// f is the open file, nil once closed.
	f *os.File
}

// keyLogMode is the key log's permission: its owner reads and writes it, and
// nobody else can do either.
const keyLogMode fs.FileMode = 0o600

// CreateKeyLog creates path as a new key log, mode 0600. A path that already
// exists, a file, a directory or a symbolic link (dangling or not), is refused
// naming it, since a key log never overwrites or adds to a file it did not
// create, and "-" is refused because a key log is never standard output.
func CreateKeyLog(path string) (*KeyLog, error) {
	switch path {
	case "":
		return nil, errors.New("the TLS key log needs a file name")
	case "-":
		return nil, errors.New("the TLS key log is written to a new file, never to standard output: name a file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving the TLS key log %s: %w", path, err)
	}
	// O_EXCL with O_CREATE refuses any name that exists, a symbolic link
	// included, so the file opened is always the one created here.
	f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, keyLogMode) // #nosec G304 -- the path the person running the command named
	switch {
	case errors.Is(err, fs.ErrExist):
		return nil, fmt.Errorf("the TLS key log %s already exists: a key log is never written over or added to, so name a file that does not exist yet", abs)
	case err != nil:
		return nil, fmt.Errorf("creating the TLS key log %s: %w", abs, err)
	}
	return &KeyLog{path: abs, f: f}, nil
}

// Path is the key log's absolute path.
func (k *KeyLog) Path() string { return k.path }

// Write appends one key log line. It is what crypto/tls calls with each
// session's secrets (tls.Config.KeyLogWriter); an error it returns ends that
// handshake, so a key log that cannot be written never lets a session run
// that the file would not decrypt. The error names the file, never the line.
func (k *KeyLog) Write(line []byte) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.f == nil {
		return 0, fmt.Errorf("the TLS key log %s is closed: the run that wrote it has ended", k.path)
	}
	return k.f.Write(line)
}

// Close closes the file. Writing after it fails.
func (k *KeyLog) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.f == nil {
		return nil
	}
	err := k.f.Close()
	k.f = nil
	return err
}

// Warning is what every run writing the key log says, once, at its start and
// in its report.
func (k *KeyLog) Warning() string {
	return fmt.Sprintf("this run writes a TLS key log to %s: that file decrypts every TLS session the run's gNMI, RESTCONF and http.request device clients open, "+
		"credentials in gRPC metadata and HTTP headers included; keep it as secret as those credentials, and delete it once the capture is read", k.path)
}

// keyLogKey is the context key a run's key log travels under.
type keyLogKey struct{}

// WithKeyLog returns ctx carrying k, the key log every client built with
// ConfigContext under ctx writes to. Only the command line calls it.
func WithKeyLog(ctx context.Context, k *KeyLog) context.Context {
	if k == nil {
		return ctx
	}
	return context.WithValue(ctx, keyLogKey{}, k)
}

// keyLogFrom returns the key log ctx carries, nil when it carries none.
func keyLogFrom(ctx context.Context) *KeyLog {
	k, _ := ctx.Value(keyLogKey{}).(*KeyLog)
	return k
}

// ConfigContext is Config for a client a run opens under ctx: the same
// configuration, writing its sessions' secrets to the run's key log when ctx
// carries one (WithKeyLog), and to nothing otherwise.
func (s Settings) ConfigContext(ctx context.Context, secrets map[string]string) (*tls.Config, error) {
	cfg, err := s.Config(secrets)
	if err != nil {
		return nil, err
	}
	if k := keyLogFrom(ctx); k != nil {
		cfg.KeyLogWriter = k
	}
	return cfg, nil
}
