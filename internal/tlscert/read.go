// Reading what is on disk: the guards that keep a path a caller pointed at
// from stopping this process instead of failing it.
//
// Every file this package reads is named by configuration, which means a
// mistake in that configuration reaches an open(2) call. Most mistakes fail
// immediately and say why. One does not: a named pipe with no writer makes
// open(2) block forever, so a controller pointed at one stays alive, holds
// its start-up, and logs nothing at all. That is the worst failure shape
// available, because every signal an operator has says the process is fine.
//
// So nothing in this package calls tls.LoadX509KeyPair, which opens the files
// itself. Every byte is read through readPEMFile, which refuses anything that
// is not an ordinary file first, and the parsing is done from memory
// (load.go's parsePair).
package tlscert

import (
	"fmt"
	"io/fs"
	"os"
)

// maxPEMBytes caps how much of a file this package will read.
//
// A certificate is about a kilobyte and a P-256 key is a few hundred bytes,
// so a megabyte is four orders of magnitude of headroom for the largest
// bundle anybody would plausibly store here. The cap exists so that a path
// pointed at something enormous (a log, a disk image, /dev/zero if it ever
// got past the regular-file check) fails with a message naming the file
// rather than by exhausting memory.
const maxPEMBytes = 1 << 20

// requireRegularFile refuses a path that is not an ordinary file, and does
// so without opening it.
//
// os.Stat is the whole trick. It follows symlinks, which is what a
// Kubernetes secret mount needs, and it never blocks: stat(2) on a named
// pipe returns immediately whether or not a writer exists, while open(2) on
// the same pipe does not return at all. Checking first turns "the process
// hangs with no log line" into "the process refuses to start and names the
// file".
//
// The remaining window is a path swapped for a pipe between this call and
// the read below it. Closing that would need an O_NONBLOCK open, which the
// os package does not expose portably, and it is a different threat
// (something hostile already running beside this process) from the one this
// guards, which is an operator's mistake or a stale mount.
func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		// Returned unwrapped past this point by every caller, so that
		// errors.Is(err, fs.ErrNotExist) still answers "the file is not
		// there yet", which is how the container healthcheck tells a cold
		// start from a fault.
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is %s rather than an ordinary file, and reading it could block this process with no way to recover", path, describeMode(info.Mode()))
	}
	if info.Size() > maxPEMBytes {
		return fmt.Errorf("%s is %d bytes, larger than the %d byte limit this reads, so it is not the certificate material it is configured as", path, info.Size(), maxPEMBytes)
	}
	return nil
}

// describeMode names what a non-regular file is, in words an operator can
// act on.
//
// "cert.pem is a named pipe" tells somebody what to go and look at.
// "unexpected file mode 0x2000000" does not.
func describeMode(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "a directory"
	case mode&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case mode&fs.ModeSocket != 0:
		return "a socket"
	case mode&fs.ModeDevice != 0:
		return "a device"
	case mode&fs.ModeIrregular != 0:
		return "not a file this can read"
	default:
		return "not an ordinary file"
	}
}

// readPEMFile reads path once it is known to be safe to open.
func readPEMFile(path string) ([]byte, error) {
	if err := requireRegularFile(path); err != nil {
		return nil, err
	}
	// #nosec G304 -- the path is this deployment's own certificate material:
	// either a file an operator named in TLS_CERT_FILE, or one inside the
	// directory this package was told to manage. It is never a value from a
	// request. gosec cannot see the requireRegularFile guard above it, which
	// is the check that actually matters here, and reading the file is not
	// optional: the alternative is not verifying the certificate at all.
	// This is the same justification internal/crypto's key_resolve.go
	// records for reading a configured key path.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) > maxPEMBytes {
		// The size was checked before the read, so reaching this means the
		// file grew in between. Refusing is cheaper than reasoning about
		// what half of it means.
		return nil, fmt.Errorf("%s grew past the %d byte limit while it was being read", path, maxPEMBytes)
	}
	return data, nil
}
