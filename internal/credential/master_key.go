package credential

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// masterKeyEnvVar is the environment variable an operator can set to
// supply the AES-256 master key directly, bypassing the on-disk key file
// entirely. It takes precedence over the file so an operator who
// deliberately set it (for example, injecting a key from a secrets
// manager in a container environment) always gets that exact key, never
// a silently different one read from disk.
const masterKeyEnvVar = "PLEIADES_MASTER_KEY"

// masterKeyDirName is the fixed subdirectory, relative to the dir
// ResolveMasterKey and NewFileStore (file_store.go) are given, that
// holds both the master key file and the credentials file. Both live
// under one hidden directory so a caller only has to know one path.
const masterKeyDirName = ".pleiades"

// masterKeyFileName is the fixed filename for the generated master key
// within masterKeyDirName.
const masterKeyFileName = "master.key"

// ResolveMasterKey resolves the AES-256 master key used to encrypt and
// decrypt the credential file, in priority order:
//
//  1. The PLEIADES_MASTER_KEY environment variable, if set: its value is
//     base64-decoded (standard encoding) and must decode to exactly 32
//     bytes. A malformed value is a hard, explicit error; ResolveMasterKey
//     never silently falls back to the file when an operator has
//     deliberately set this variable, since silently using a different
//     key than the one they asked for would be a much more dangerous
//     failure mode than simply refusing to start.
//  2. <dir>/.pleiades/master.key, if it exists: also base64-decoded and
//     required to be exactly 32 bytes. A corrupt or wrong-length file is
//     likewise a hard, explicit error, never silently regenerated:
//     regenerating would produce a new key that cannot decrypt any
//     secret already encrypted under the old one, silently losing access
//     to it.
//  3. Otherwise, only while <dir>/.pleiades holds nothing sealed under a
//     master key (sealedUnderMasterKey: the credential file's ciphertext, the
//     seal key, the fact store's files), a fresh 32-byte key is generated
//     with crypto/rand, base64-encoded, and written to
//     <dir>/.pleiades/master.key (creating the .pleiades directory with mode
//     0o700 if needed) with mode 0o600, then the raw key bytes are returned.
//     A project that holds something sealed and has no key is refused with
//     ErrSealedWithoutKey, naming what it holds and the key to restore: a new
//     key would open none of it and would seal what comes next under a
//     second key, leaving a store no one key reads (FAILURE_PATTERNS 1344).
//     So is a project where whether anything is sealed cannot be told.
//
// A key file that is there and cannot be used (another account may read it,
// it is not a key) is refused at step 2 and never replaced, and the refusal
// names what the project holds sealed under it too.
//
// This is the one function that makes a project's master key, so every
// writer (add-credential, add-secret, seal-key, the importers, a fact write)
// and every reader that resolves the key on first use takes the rule;
// internal/archtest's TestOnlyResolveMasterKeyMakesTheMasterKey keeps it so.
//
// This is a thin wrapper around crypto.ResolveKey (internal/crypto's own
// generic env-var/file/generate-and-persist resolution, extracted from
// what used to be this function's own body during Phase 5), pinned to
// this package's own env var and file name so its behavior, including
// every case master_key_test.go covers, is unchanged.
func ResolveMasterKey(dir string) ([]byte, error) {
	key, err := ExistingMasterKey(dir)
	if err == nil {
		return key, nil
	}
	found, scanErr := sealedUnderMasterKey(dir)
	if !errors.Is(err, ErrNoMasterKey) {
		// A key that is there and cannot be used is never replaced; say what
		// it sealed, so nobody removes it thinking it guards nothing.
		if scanErr == nil && len(found) > 0 {
			whats := make([]string, len(found))
			for i, m := range found {
				whats[i] = m.what
			}
			return nil, fmt.Errorf("%w; %s holds %s sealed under it, so the key is never replaced: fix it or set %s", err, filepath.Join(dir, masterKeyDirName), strings.Join(whats, ", "), masterKeyEnvVar)
		}
		return nil, err
	}
	if scanErr != nil {
		// As below: a key made meanwhile by another caller is the answer.
		if key, err := ExistingMasterKey(dir); !errors.Is(err, ErrNoMasterKey) {
			return key, err
		}
		return nil, fmt.Errorf("the project has no master key, and whether %s holds what one sealed cannot be told (%w): no new key is made", filepath.Join(dir, masterKeyDirName), scanErr)
	}
	if len(found) > 0 {
		// Look once more before refusing. Another command, or another caller
		// in this process, may have made the project's first key and sealed
		// something under it between the first look and the scan. Nothing is
		// sealed under a key file before that file is in place: the key is
		// written whole to a temporary file and only then linked to its name
		// (crypto's generateAndSaveKey), and only a caller holding the linked
		// key seals. So material sealed under the file's key always finds the
		// file here; material that still finds none was sealed under a key
		// this project does not hold.
		if key, err := ExistingMasterKey(dir); !errors.Is(err, ErrNoMasterKey) {
			return key, err
		}
		return nil, lostKeyError(dir, found)
	}
	return crypto.ResolveKey(dir, masterKeyEnvVar, filepath.Join(masterKeyDirName, masterKeyFileName))
}

// ErrNoMasterKey marks a project with no master key: none in the environment
// and no key file.
var ErrNoMasterKey = crypto.ErrNoKey

// ExistingMasterKey is the project's master key when it has one, and
// ErrNoMasterKey when it has none. Unlike ResolveMasterKey it never makes
// one, so reading what a key sealed cannot leave a new key behind that opens
// none of it.
func ExistingMasterKey(dir string) ([]byte, error) {
	return crypto.ExistingKey(dir, masterKeyEnvVar, filepath.Join(masterKeyDirName, masterKeyFileName))
}
