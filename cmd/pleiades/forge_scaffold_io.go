// This file holds the pieces shared by forge_new_device.go and
// forge_new_collection.go: both scaffold packages
// (internal/inventory/devicescaffold, internal/forge/collectionscaffold)
// are pure, I/O-free generators, so the one place that touches disk, and
// the one place a --capabilities flag value gets parsed, lives here
// rather than being duplicated per subcommand.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// writeGeneratedFile writes content to filepath.Join(dir, relPath),
// refusing by default to overwrite anything that already exists there
// (naming the colliding path) rather than silently skipping it. This
// deliberately diverges from internal/inventory/project.go's Scaffold,
// which silently skips a file that already exists because re-running
// `pleiades init` in an existing project is expected and idempotent:
// `forge new-*` is a different kind of action, an explicit "generate
// this new, specific thing," so a collision is a real problem the caller
// should see. It returns the full path either way, plus whether it
// actually wrote, for the caller's own success message.
//
// Callers wanting the skip behaviour go through firstExistingFile first
// and skip the whole set, rather than passing a per-file flag here; see
// that function for why the distinction matters.
//
// Everything below dir goes through an os.Root opened on dir, and the
// file is created with O_EXCL. The earlier os.Stat check followed by
// os.WriteFile (FAILURE_PATTERNS 792) had two holes: os.WriteFile follows
// a symlink already sitting at the file's name and writes wherever it
// points, and a file created between the check and the write was silently
// overwritten. O_EXCL makes "it does not exist yet" and "create it" one
// step that refuses an existing file or symlink, and the root stops a
// symlinked directory on the way to the file from carrying the write
// outside dir.
func writeGeneratedFile(dir, relPath string, content []byte) (string, error) {
	full := filepath.Join(dir, relPath)

	// dir itself is the caller's choice (--dir, or the default under the
	// working directory), so it is created normally; everything below it
	// is created through the root.
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- generated source, not secret material
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", dir, err)
	}
	defer root.Close()

	// The scaffold packages build relPath with forward slashes.
	rel := filepath.FromSlash(relPath)
	if err := root.MkdirAll(filepath.Dir(rel), 0o755); err != nil { // #nosec G301 -- generated source, not secret material
		return "", fmt.Errorf("creating directory for %s: %w", full, err)
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G302 -- generated source, not secret material
	if errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("refusing to overwrite existing file: %s", full)
	}
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", full, err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("writing %s: %w", full, err)
	}
	// A failed Close can mean the data never reached the disk.
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("writing %s: %w", full, err)
	}
	return full, nil
}

// firstExistingFile returns the first of relPaths that already exists
// under dir, or "" when none of them do. It is how `--skip-existing`
// decides, and it takes the whole set rather than one path at a time on
// purpose.
//
// The unit a `forge new-*` subcommand generates is one entry, not one
// file: a collection method is a source file plus its starter test, and
// those two are only coherent together. Deciding per file means a method
// whose implementation was hand-completed and whose generated starter
// test was replaced by a real one under a different name gets that stub
// test written back underneath it, asserting the method is declared and
// returns "not implemented" against an implementation that is neither.
// That is not hypothetical: the first run of this with a per-file check
// resurrected fifteen such tests across svc.* and net.catalyst.*, and
// every one of them failed immediately. Either the whole entry is
// already on disk or none of it is.
//
// It uses Lstat so a symlink at a name counts as already there, even one
// whose target is missing: writeGeneratedFile would refuse to replace it,
// so the whole entry is skipped rather than half written.
func firstExistingFile(dir string, relPaths []string) (string, error) {
	for _, relPath := range relPaths {
		full := filepath.Join(dir, relPath)
		if _, err := os.Lstat(full); err == nil {
			return full, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("checking %s: %w", full, err)
		}
	}
	return "", nil
}

// parseDocJSONFlag decodes a --doc-json flag value into a
// pkg/collection.Doc. An empty value yields the zero Doc. A value
// beginning with "@" is a path to read the JSON from, the convention
// curl and gh already established, because a real Doc runs to
// paragraphs of prose and nobody types that on a command line.
//
// Unknown fields are rejected rather than ignored. The whole point of
// this flag is to carry documentation into a generated manifest that
// internal/archtest then compares for exact equality, so a mistyped key
// that decodes to "field absent" would produce a file that fails that
// comparison with no hint that the input was the problem.
func parseDocJSONFlag(value string) (collection.Doc, error) {
	if value == "" {
		return collection.Doc{}, nil
	}

	raw := []byte(value)
	source := "--doc-json"
	if strings.HasPrefix(value, "@") {
		path := strings.TrimPrefix(value, "@")
		contents, err := os.ReadFile(path) // #nosec G304 -- a path the operator typed on their own command line, in their own repository
		if err != nil {
			return collection.Doc{}, fmt.Errorf("reading --doc-json file: %w", err)
		}
		raw = contents
		source = path
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc collection.Doc
	if err := dec.Decode(&doc); err != nil {
		return collection.Doc{}, fmt.Errorf("parsing %s as a collection.Doc: %w", source, err)
	}
	return doc, nil
}

// parseSettingsJSONFlag decodes a --settings-json flag value into the
// per-deployment settings a generated sync plugin declares. It follows
// parseDocJSONFlag's conventions exactly, for the same reasons: an empty
// value yields no settings, a value beginning with "@" is a path to read
// the JSON from, and unknown fields are rejected rather than ignored, so
// a mistyped key fails here instead of generating a descriptor that
// silently declares nothing.
//
// The value is a JSON array of objects with name, description and
// required, matching syncplugin.SettingSpec's own field names:
//
//	--settings-json '[{"name":"region","description":"the AWS region to read from","required":true}]'
func parseSettingsJSONFlag(value string) ([]syncplugin.SettingSpec, error) {
	if value == "" {
		return nil, nil
	}

	raw := []byte(value)
	source := "--settings-json"
	if strings.HasPrefix(value, "@") {
		path := strings.TrimPrefix(value, "@")
		contents, err := os.ReadFile(path) // #nosec G304 -- a path the operator typed on their own command line, in their own repository
		if err != nil {
			return nil, fmt.Errorf("reading --settings-json file: %w", err)
		}
		raw = contents
		source = path
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var settings []syncplugin.SettingSpec
	if err := dec.Decode(&settings); err != nil {
		return nil, fmt.Errorf("parsing %s as a list of syncplugin.SettingSpec: %w", source, err)
	}
	return settings, nil
}

// parseCapabilitiesFlag splits a comma-separated --capabilities flag value
// into capability.Name values. An empty string yields no capabilities, not
// one containing a single empty Name.
func parseCapabilitiesFlag(flagValue string) []capability.Name {
	if flagValue == "" {
		return nil
	}
	parts := strings.Split(flagValue, ",")
	names := make([]capability.Name, 0, len(parts))
	for _, p := range parts {
		names = append(names, capability.Name(p))
	}
	return names
}
