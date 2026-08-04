package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// keySize is the required key size in bytes for AES-256, matching
// NewAESService's own requirement (aes.go).
const keySize = 32

// ResolveKey resolves a 32-byte AES-256 key in priority order:
//
//  1. The envVar environment variable, if set: its value is base64-decoded
//     (standard encoding) and must decode to exactly 32 bytes. A malformed
//     value is a hard, explicit error; ResolveKey never silently falls back
//     to the file when a caller's operator has deliberately set this
//     variable, since silently using a different key than the one they
//     asked for would be a much more dangerous failure mode than simply
//     refusing to start.
//  2. filepath.Join(dir, fileName), if it exists: also base64-decoded and
//     required to be exactly 32 bytes. A corrupt or wrong-length file is
//     likewise a hard, explicit error, never silently regenerated:
//     regenerating would produce a new key that cannot decrypt anything
//     already encrypted under the old one, silently losing access to it.
//  3. Otherwise, a fresh 32-byte key is generated with crypto/rand,
//     base64-encoded, and written to filepath.Join(dir, fileName) (creating
//     its parent directory with mode 0o700 if needed, tightening it to
//     0o700 even if it already existed with looser permissions) with mode
//     0o600, then the raw key bytes are returned.
//
// This is the generic form of the resolution logic internal/credential's
// ResolveMasterKey originally implemented directly; that package now calls
// through here with its own envVar/fileName, and any other caller needing
// the identical env-var/file/generate-and-persist shape (for example, the
// envelope encryption KEK) uses this directly with its own distinct
// envVar/fileName so the two never collide on the same key material.
func ResolveKey(dir, envVar, fileName string) ([]byte, error) {
	if envVal, ok := os.LookupEnv(envVar); ok {
		return decodeKey(envVal, "environment variable "+envVar)
	}

	keyPath := filepath.Join(dir, fileName)
	data, err := os.ReadFile(keyPath) // #nosec G304 -- keyPath is derived from the caller's own configured directory, not untrusted input
	if err == nil {
		return decodeKey(string(data), keyPath)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read key file %s: %w", keyPath, err)
	}

	return generateAndSaveKey(keyPath)
}

// decodeKey base64-decodes raw (standard encoding, trimming surrounding
// whitespace since a hand-edited or shell-exported value commonly carries a
// trailing newline) and requires the result to be exactly keySize bytes.
// source names where raw came from, purely to make the returned error
// message actionable.
func decodeKey(raw, source string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("key from %s is not valid base64: %w", source, err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("key from %s must decode to exactly %d bytes, got %d", source, keySize, len(key))
	}
	return key, nil
}

// generateAndSaveKey creates a fresh random key, persists it at keyPath,
// and returns the raw key bytes. It is only reached when no env var is set
// and no key file exists yet.
func generateAndSaveKey(keyPath string) ([]byte, error) {
	key := make([]byte, keySize)
	// crypto/rand, not math/rand: this is real key material. math/rand's
	// output is predictable from its seed, which would make every value
	// encrypted under a generated key recoverable by anyone who could guess
	// or observe that seed.
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	keyDir := filepath.Dir(keyPath)
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create %s: %w", keyDir, err)
	}
	// MkdirAll only applies 0o700 to a directory it actually creates, so
	// this Chmod is what actually guarantees it here too, not just when
	// this call happens to be the one creating the directory fresh.
	// 0o700 is a directory permission (owner rwx; the executable bit is
	// required to traverse it), not a file permission: gosec's G302 check
	// flags anything above its 0600 threshold without distinguishing the
	// two, which makes this a false positive rather than a real gap.
	if err := os.Chmod(keyDir, 0o700); err != nil { // #nosec G302 -- intentional, see comment above
		return nil, fmt.Errorf("failed to set permissions on %s: %w", keyDir, err)
	}

	encoded := base64.StdEncoding.EncodeToString(key)

	// Write to a temp file in keyDir first, then commit it into place with
	// os.Link, not a plain os.WriteFile or a bare O_EXCL open: an
	// adversarial review of this phase caught a real TOCTOU race in each
	// of those simpler approaches in turn. Two callers reaching this
	// function concurrently against the same not-yet-existing keyPath
	// (each having already passed ResolveKey's own "does the file exist
	// yet" read, both getting ENOENT) would otherwise either (a) each
	// generate a different key and race to write it, with only the last
	// writer's key ever surviving on disk while the other kept using its
	// own, different, unpersisted key in memory -- or (b), even with an
	// exclusive O_CREATE|O_EXCL open, leave a real window where the
	// destination file exists but is still empty or partially written,
	// which a concurrent reader can observe (caught for real by
	// TestResolveKey_ConcurrentGenerationConverges failing against that
	// intermediate version, not by inspection alone). Writing the full
	// content to a temp file and only then linking it to keyPath means no
	// reader can ever observe keyPath in a partial state: it is either
	// absent or fully written, never in between. os.Link, not os.Rename,
	// for the commit step specifically because Link fails with
	// ErrExist if keyPath already exists, letting the loser detect it and
	// defer to the winner's key; Rename would instead silently replace an
	// existing destination, which is exactly the multiple-different-keys
	// hazard this function exists to prevent. The temp file must be
	// created inside keyDir itself (not the OS temp dir): Link requires
	// both names to be on the same filesystem.
	tmp, err := os.CreateTemp(keyDir, ".key-*.tmp") // #nosec G304 -- keyDir is derived from the caller's own configured path, not untrusted input
	if err != nil {
		return nil, fmt.Errorf("failed to create temp key file in %s: %w", keyDir, err)
	}
	tmpPath := tmp.Name()
	// Removes the temp name in every case once this function returns: on
	// the success path below, keyPath is already a second hard link to
	// the same inode's content by then, so this only ever unlinks the
	// now-redundant temp name, never the actual persisted key.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write([]byte(encoded)); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("failed to write temp key file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp key file %s: %w", tmpPath, err)
	}

	if err := os.Link(tmpPath, keyPath); err != nil {
		if os.IsExist(err) {
			data, readErr := os.ReadFile(keyPath) // #nosec G304 -- keyPath is the caller's own configured path, not untrusted input
			if readErr != nil {
				return nil, fmt.Errorf("lost the race to create key file %s and failed to read the winning key: %w", keyPath, readErr)
			}
			return decodeKey(string(data), keyPath)
		}
		return nil, fmt.Errorf("failed to save key file %s: %w", keyPath, err)
	}

	return key, nil
}
