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
// for, never written over or added to, created readable by its owner alone
// (and so not made at all on Windows, which ignores the mode), never placed
// where the caller keeps it out of (keylog_place.go), and nothing here reads
// it back: what reaches it goes nowhere else.
//
// The key log travels on a run's context (WithKeyLog), and only the clients
// built with Settings.ConfigContext under that context write to it. The
// command line is the one place that puts it there; a Runner and the
// Controller never do, which internal/archtest holds.
package devicetls

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/fs"
	"os"
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
// carries one (WithKeyLog), and to nothing otherwise, and recording the
// certificate each verified handshake presents on the Presented ctx carries
// (WithPresented), when it carries one.
func (s Settings) ConfigContext(ctx context.Context, secrets map[string]string) (*tls.Config, error) {
	cfg, err := s.Config(secrets)
	if err != nil {
		return nil, err
	}
	if k := keyLogFrom(ctx); k != nil {
		cfg.KeyLogWriter = k
	}
	if p := presentedFrom(ctx); p != nil {
		cfg.VerifyConnection = p.watching(cfg.VerifyConnection)
	}
	return cfg, nil
}
