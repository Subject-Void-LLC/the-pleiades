// Writing a key that came with a backup into a compose env file that holds
// none.
package setup

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// ImportKey writes key, with its version tag, into the compose env file in
// dir, and returns the file's name as messages give it.
//
// It exists for one caller: a restore onto a machine with no key, which has
// just proved that key opens every sealed value in the backup it restored,
// under that tag. It is not a way to change a key. It refuses a file that
// already holds a key or a previous key, so replacing one still goes through
// setup's own guards, and it never generates anything. Every other line of
// an existing file is kept exactly as it was.
func ImportKey(dir *Dir, key []byte, version string) (string, error) {
	display := dir.Show(ComposeFile)
	data, exists, err := dir.ReadExisting(ComposeFile)
	if err != nil {
		return "", err
	}
	file, err := ParseEnvFile(data)
	if err != nil {
		return "", fmt.Errorf("%w (in %s)", err, display)
	}
	for _, name := range []string{VarMasterKey, VarPreviousKey} {
		if _, held := file.Get(name); held {
			return "", refuse(fmt.Sprintf("%s already holds a %s, and a restore writes a key only where there is none. The key there was not changed.", display, name))
		}
	}
	if err := validateVersion(version); err != nil {
		return "", refuse(fmt.Sprintf("The version tag %q cannot be written to %s: %v", version, display, err))
	}

	encoded := crypto.EncodeKey(key)
	redact.Shared().Literals().Add(encoded)
	file.Set(VarMasterKey, encoded)
	if _, set := file.Get(VarMasterKeyVersion); set || version != crypto.DefaultKeyVersion {
		file.Set(VarMasterKeyVersion, version)
	}

	if !exists {
		file.AddComment(composeFileNote()...)
		err = dir.CreateNew(ComposeFile, file.Bytes())
	} else {
		err = dir.Replace(ComposeFile, data, file.Bytes())
	}
	if err != nil {
		return "", err
	}
	return display, nil
}
