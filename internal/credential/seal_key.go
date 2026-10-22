// The project's seal key, and opening a password sealed to the project or to
// a passphrase (internal/sealed).
package credential

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// sealKeyFileName holds the project's seal private key, encrypted under the
// master key like every stored credential, beside master.key.
const sealKeyFileName = "seal.key"

// sealKeyLabel opens the file, so a reader can tell what it holds.
const sealKeyLabel = "pleiades seal key v1"

// ErrNoSealKey reports a project that has never made a seal key, so nothing
// can have been sealed to it.
var ErrNoSealKey = errors.New("this project has no seal key")

func sealKeyPath(dir string) string {
	return filepath.Join(dir, masterKeyDirName, sealKeyFileName)
}

// LoadSealKey returns the project's seal key, decrypted with the master key
// key, or ErrNoSealKey when the project has none.
func LoadSealKey(dir string, key []byte) (*sealed.PrivateKey, error) {
	data, err := os.ReadFile(sealKeyPath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoSealKey
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the seal key: %w", err)
	}
	label, encoded, ok := strings.Cut(strings.TrimSpace(string(data)), "\n")
	if !ok || label != sealKeyLabel {
		return nil, fmt.Errorf("%s is not a seal key this pleiades reads", sealKeyPath(dir))
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, fmt.Errorf("%s is not a seal key this pleiades reads: %w", sealKeyPath(dir), err)
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return nil, err
	}
	raw, err := svc.Decrypt(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("the seal key does not decrypt with this project's master key: %w", err)
	}
	return sealed.ParsePrivateKey(raw)
}

// SealKey returns the project's seal key, making one on first use. created
// reports whether this call made it.
//
// The key is written whole to a temporary file beside its path and then
// linked into place, which never replaces what is already there. So two
// commands making the first key at once cannot each keep a different one,
// and neither can read the other's half written: whichever links second
// reads the first's complete file. A crash leaves at most a temporary file,
// never a seal.key that refuses to open.
func SealKey(dir string, key []byte) (priv *sealed.PrivateKey, created bool, err error) {
	priv, err = LoadSealKey(dir, key)
	if !errors.Is(err, ErrNoSealKey) {
		return priv, false, err
	}
	priv, err = sealed.GenerateKey()
	if err != nil {
		return nil, false, err
	}
	raw, err := priv.Bytes()
	if err != nil {
		return nil, false, fmt.Errorf("failed to serialize the seal key: %w", err)
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return nil, false, err
	}
	ciphertext, err := svc.Encrypt(raw)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Join(dir, masterKeyDirName), 0o700); err != nil {
		return nil, false, fmt.Errorf("failed to create %s: %w", masterKeyDirName, err)
	}
	tmp, err := writeTempBeside(sealKeyPath(dir), fmt.Sprintf("%s\n%s\n", sealKeyLabel, base64.StdEncoding.EncodeToString(ciphertext)))
	if err != nil {
		return nil, false, fmt.Errorf("failed to write the seal key: %w", err)
	}
	defer func() { _ = os.Remove(tmp) }()
	if err := os.Link(tmp, sealKeyPath(dir)); errors.Is(err, fs.ErrExist) {
		priv, err = LoadSealKey(dir, key)
		if errors.Is(err, ErrNoSealKey) {
			// Something is at the path that does not read as a file, such
			// as a link to nothing: never written through.
			return nil, false, fmt.Errorf("%s exists but is not a readable file; remove it to make a seal key", sealKeyPath(dir))
		}
		return priv, false, err
	} else if err != nil {
		return nil, false, fmt.Errorf("failed to create the seal key: %w", err)
	}
	return priv, true, nil
}

// writeTempBeside writes content to a new private temporary file in path's
// directory, synced to disk, and returns its name.
func writeTempBeside(path, content string) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(content)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	return f.Name(), nil
}

// OpenSealedPassword opens a sealed file holding a password. A file sealed to
// a key opens with the project's seal key, and passphrase must then be nil:
// a passphrase offered for it would be one nobody asked for. A file sealed
// to a passphrase calls passphrase for it, and refuses when that is nil.
func OpenSealedPassword(dir string, key []byte, file *sealed.File, passphrase func() (string, error)) (string, error) {
	if file.Purpose != sealed.PurposePassword {
		return "", fmt.Errorf("the sealed file holds a %s, not a password", file.Purpose)
	}
	var secret []byte
	switch file.Method {
	case sealed.MethodKey:
		if passphrase != nil {
			return "", fmt.Errorf("the file is sealed to a project's key and opens with it, so a passphrase does not apply")
		}
		priv, err := LoadSealKey(dir, key)
		if errors.Is(err, ErrNoSealKey) {
			return "", fmt.Errorf("the file is sealed to the key %s, and this project has no seal key: it was sealed to another project", file.Recipient)
		}
		if err != nil {
			return "", err
		}
		if secret, err = file.OpenWithKey(priv); err != nil {
			return "", err
		}
	case sealed.MethodPassphrase:
		if passphrase == nil {
			return "", fmt.Errorf("the file is sealed with a passphrase: add --passphrase to type it, or --passphrase-stdin to pipe it")
		}
		p, err := passphrase()
		if err != nil {
			return "", err
		}
		if secret, err = file.OpenWithPassphrase(p); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("the file is sealed with %s, which this pleiades does not open", file.Method)
	}
	return string(secret), nil
}
