// Package promptsign: the trusted keys file, and the register of versions
// already used.
package promptsign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// MaxKeysFileBytes bounds a trusted keys file.
const MaxKeysFileBytes = 256 << 10

// keysFile is the trusted keys file as YAML holds it.
type keysFile struct {
	Keys []struct {
		PublicKey string   `yaml:"public_key"`
		Uses      []string `yaml:"uses"`
		Expires   string   `yaml:"expires"`
		Comment   string   `yaml:"comment"`
	} `yaml:"keys"`
}

// ParseTrustedKeys reads a trusted keys file:
//
//	keys:
//	  - public_key: <base64 Ed25519 public key>
//	    uses: [triage, summarize]
//	    expires: 2027-01-01T00:00:00Z
//	    comment: who holds it
//
// A key with no uses could sign for nothing and is refused, as is a key
// listed twice, so a file says each key's role once.
func ParseTrustedKeys(data []byte) ([]TrustedKey, error) {
	if len(data) > MaxKeysFileBytes {
		return nil, fmt.Errorf("the trusted keys file is over %d bytes", MaxKeysFileBytes)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f keysFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("the trusted keys file: %w", err)
	}
	var out []TrustedKey
	seen := map[string]bool{}
	for i, k := range f.Keys {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.PublicKey))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("key %d: public_key is not a base64 Ed25519 public key", i+1)
		}
		key := TrustedKey{Public: ed25519.PublicKey(raw), Uses: k.Uses}
		if len(key.Uses) == 0 {
			return nil, fmt.Errorf("key %s has no uses, so it could sign for nothing", key.ID())
		}
		for _, u := range key.Uses {
			if strings.TrimSpace(u) == "" {
				return nil, fmt.Errorf("key %s lists an empty use", key.ID())
			}
		}
		if k.Expires != "" {
			if key.Expires, err = time.Parse(time.RFC3339, k.Expires); err != nil {
				return nil, fmt.Errorf("key %s: expires %q is not an RFC 3339 time", key.ID(), k.Expires)
			}
		}
		if seen[key.ID()] {
			return nil, fmt.Errorf("key %s is listed twice", key.ID())
		}
		seen[key.ID()] = true
		out = append(out, key)
	}
	return out, nil
}

// FileVersions is a version register kept as a directory: one empty file
// for each version of each name that was used, created exclusively, so two
// processes recording versions at once cannot lose each other's (a single
// file read, changed and renamed would let the later rename drop the
// earlier). The newest version of a name is the highest one that has a
// file, found by listing; the names are hex, so nothing a document holds
// reaches the file system as a path.
type FileVersions struct {
	// Path is the directory the register lives in.
	Path string
}

// maxVersionDigits is the width a version is written to, so file names sort
// as the numbers do; versions are bounded below 2^53 by the document parser.
const maxVersionDigits = 20

// dirOf is the directory holding the versions of one name of one kind.
func (f *FileVersions) dirOf(kind, name string) string {
	return filepath.Join(f.Path, hex.EncodeToString([]byte(kind)), hex.EncodeToString([]byte(name)))
}

// Newest implements Versions.
func (f *FileVersions) Newest(kind, name string) (int64, bool, error) {
	entries, err := os.ReadDir(f.dirOf(kind, name))
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var newest int64
	found := false
	for _, e := range entries {
		v, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil || v < 0 || len(e.Name()) != maxVersionDigits {
			continue
		}
		if !found || v > newest {
			newest, found = v, true
		}
	}
	return newest, found, nil
}

// Use records that version of name was used. Recording a version already
// recorded is no change; an older one than the newest is recorded too and
// changes nothing, since the newest is what counts.
func (f *FileVersions) Use(kind, name string, version int64) error {
	if version < 0 {
		return fmt.Errorf("version %d is negative", version)
	}
	dir := f.dirOf(kind, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%0*d", maxVersionDigits, version)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
