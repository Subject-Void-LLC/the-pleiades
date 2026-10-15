// Package promptsign: the trusted keys file, and the register of versions
// already used.
package promptsign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// FileVersions is a version register kept in one JSON file, written whole
// and renamed into place, so a run that dies midway leaves the last
// complete register.
type FileVersions struct {
	Path string
	mu   sync.Mutex
}

// read reads the register, empty when the file does not exist.
func (f *FileVersions) read() (map[string]int64, error) {
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", f.Path, err)
	}
	return out, nil
}

// registerKey is one name of one kind in the register.
func registerKey(kind, name string) string { return kind + "/" + name }

// Newest implements Versions.
func (f *FileVersions) Newest(kind, name string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return 0, false, err
	}
	v, ok := m[registerKey(kind, name)]
	return v, ok, nil
}

// Use records that version of name was used, keeping the newest.
func (f *FileVersions) Use(kind, name string, version int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.read()
	if err != nil {
		return err
	}
	if m[registerKey(kind, name)] >= version {
		return nil
	}
	m[registerKey(kind, name)] = version
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.Path), ".prompt-versions-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.Path)
}
