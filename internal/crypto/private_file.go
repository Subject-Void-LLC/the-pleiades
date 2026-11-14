// Package crypto: reading a file that holds a secret only when it is its
// owner's alone, as OpenSSH reads a private key.
//
// The owner decided on 2026-11-13 that a master key, a credential file or a
// seal key that another account could read or change is refused rather than
// used, naming the file and the command that fixes it. A file this project
// writes is already 0600, so only a hand-loosened or foreign file is refused.
// The check is made on the open file (fstat), so what is checked is what is
// read, with no window between the two for the path to be swapped.
package crypto

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrLooseSecretFile marks a secret file refused because another account
// could read or change it, or because another account owns it.
var ErrLooseSecretFile = errors.New("a file holding a secret must be its owner's alone")

// maxSecretFileSize bounds what ReadPrivateFile reads: every secret file this
// module writes is far smaller, and a larger one is not one of them.
const maxSecretFileSize = 64 << 20

// ReadPrivateFile reads path, a file holding a secret, refusing it with
// ErrLooseSecretFile when its permissions let group or others read or write
// it, or another user owns it (checkPrivate, per platform). A file that does
// not exist returns the open error unchanged, so a caller still tells a
// missing file by errors.Is(err, fs.ErrNotExist).
func ReadPrivateFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- a secret file under the caller's own configured directory
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to stat %s: %w", path, err)
	}
	if err := checkPrivate(path, info); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSecretFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	if len(data) > maxSecretFileSize {
		return nil, fmt.Errorf("%s is larger than any secret file this pleiades writes", path)
	}
	return data, nil
}
