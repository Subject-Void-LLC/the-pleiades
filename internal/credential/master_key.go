package credential

import (
	"path/filepath"

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
//  3. Otherwise, a fresh 32-byte key is generated with crypto/rand,
//     base64-encoded, and written to <dir>/.pleiades/master.key (creating
//     the .pleiades directory with mode 0o700 if needed) with mode
//     0o600, then the raw key bytes are returned.
//
// This is a thin wrapper around crypto.ResolveKey (internal/crypto's own
// generic env-var/file/generate-and-persist resolution, extracted from
// what used to be this function's own body during Phase 5), pinned to
// this package's own env var and file name so its behavior, including
// every case master_key_test.go covers, is unchanged.
func ResolveMasterKey(dir string) ([]byte, error) {
	return crypto.ResolveKey(dir, masterKeyEnvVar, filepath.Join(masterKeyDirName, masterKeyFileName))
}
