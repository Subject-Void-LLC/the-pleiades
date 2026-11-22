// What a project holds sealed under its master key, which ResolveMasterKey
// reads before it makes a key: a key is made only where nothing is sealed
// under one yet (FAILURE_PATTERNS 1344).
package credential

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// factsDirName is the fact store's directory under masterKeyDirName, where
// the Crawl CLI keeps each device's facts sealed under keys derived from the
// master key (cmd/pleiades' factsDir, internal/facts' atrest.go).
const factsDirName = "facts"

// ErrSealedWithoutKey marks a project that has no master key and holds
// something sealed under one: a new key would open none of it.
var ErrSealedWithoutKey = errors.New("the project has no master key, and it holds what one sealed")

// sealedMaterial is one file or directory under masterKeyDirName that holds
// something sealed under the master key, with what it holds in words.
type sealedMaterial struct {
	// path is where it is, under the directory the key resolves for.
	path string
	// what is its name and what it holds, never a value: "credentials.yaml
	// (2 device credentials, 1 named secret)".
	what string
}

// sealedUnderMasterKey returns everything under dir's masterKeyDirName that is
// sealed under the master key: the credential file's ciphertext (device
// credentials, registry accounts, database connections, named secrets), the
// seal key, and the fact store's files (each device's facts, baselines and
// state, sealed under keys derived from the master key). The journal and the
// inventory hold nothing sealed. An error means whether something is sealed
// cannot be told, which a caller about to make a key takes as a refusal.
func sealedUnderMasterKey(dir string) ([]sealedMaterial, error) {
	var found []sealedMaterial
	creds, err := credentialFileSealed(credentialsPath(dir))
	if err != nil {
		return nil, err
	}
	if creds != "" {
		found = append(found, sealedMaterial{path: credentialsPath(dir), what: credentialsFileName + " (" + creds + ")"})
	}
	switch _, err := os.Lstat(sealKeyPath(dir)); {
	case err == nil:
		found = append(found, sealedMaterial{path: sealKeyPath(dir), what: sealKeyFileName})
	case !errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%s: %w", sealKeyPath(dir), err)
	}
	factsPath := filepath.Join(dir, masterKeyDirName, factsDirName)
	files, err := factFiles(factsPath)
	if err != nil {
		return nil, err
	}
	if files > 0 {
		found = append(found, sealedMaterial{path: factsPath, what: fmt.Sprintf("%s (%s)", factsDirName, plural(files, "fact file"))})
	}
	return found, nil
}

// credentialFileSealed says what path's credential file holds sealed, in
// words ("1 device credential, 2 named secrets"), or "" when it is missing or
// holds no ciphertext. A field holds ciphertext by the file's one naming rule,
// a key ending in _encrypted (TestEveryCiphertextFieldIsNamedEncrypted holds
// the structs to it), and every value under secrets: is ciphertext. It reads
// the file as Lookup does, so a file another account may read is refused.
func credentialFileSealed(path string) (string, error) {
	data, err := crypto.ReadPrivateFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	var doc map[string]map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("%s does not parse: %w", path, err)
	}
	var parts []string
	for _, section := range []struct{ key, noun string }{
		{"devices", "device credential"}, {"registries", "registry account"},
		{"databases", "database connection"}, {"secrets", "named secret"},
	} {
		n := 0
		for _, entry := range doc[section.key] {
			if holdsCiphertext(entry, section.key == "secrets") {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, plural(n, section.noun))
		}
	}
	return strings.Join(parts, ", "), nil
}

// holdsCiphertext reports whether one entry of the credential file holds a
// sealed value: a non-empty field whose key ends in _encrypted, or, for a
// named secret (every field ciphertext), any non-empty field.
func holdsCiphertext(entry any, everyField bool) bool {
	fields, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	for k, v := range fields {
		if s, ok := v.(string); ok && s != "" && (everyField || strings.HasSuffix(k, "_encrypted")) {
			return true
		}
	}
	return false
}

// factFiles counts the files in the fact store at path that hold sealed
// facts: a device's facts and baselines (.jsonl) and its state (.state.json),
// the names internal/facts' FileStore.HoldsAny counts. A lock file holds none.
func factFiles(path string) (int, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
	}
	n := 0
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".state.json") {
			n++
		}
	}
	return n, nil
}

// plural is n and noun, with an s when n is not one.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// lostKeyError is the refusal to make a key beside found: it names each thing
// sealed, the key to restore, and what a person does when the key is truly
// gone. It names files and counts, never a value.
func lostKeyError(dir string, found []sealedMaterial) error {
	whats := make([]string, len(found))
	paths := make([]string, len(found))
	for i, m := range found {
		whats[i], paths[i] = m.what, m.path
	}
	return fmt.Errorf("%w in %s: %s. "+
		"Restore %s or set %s to the key that sealed them; no new key is made beside them, since it would open none of them. "+
		"If that key is truly gone, nothing can open what it sealed: remove what you accept losing (%s) and run the command again",
		ErrSealedWithoutKey, filepath.Join(dir, masterKeyDirName), strings.Join(whats, ", "),
		filepath.Join(dir, masterKeyDirName, masterKeyFileName), masterKeyEnvVar, strings.Join(paths, ", "))
}
