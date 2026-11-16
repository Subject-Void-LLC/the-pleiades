// The --tls-keylog flag run and adhoc take: the run's TLS session secrets,
// written to a new file for a packet tool to decrypt a capture of the run's
// device sessions with (pkg/devicetls' KeyLog). It is a command line flag and
// nothing else: no environment variable, project setting or launch field
// sets it, and a Runner has no way to.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// tlsKeyLogFlagName is the flag's name.
const tlsKeyLogFlagName = "tls-keylog"

// tlsKeyLogFlag is --tls-keylog as parsed: the file named, or "" when the
// flag was not given.
type tlsKeyLogFlag struct {
	path string
}

// addTLSKeyLogFlag registers --tls-keylog on fs. run and adhoc call it;
// rollback and onboarding do not take the flag.
func addTLSKeyLogFlag(fs *flag.FlagSet) *tlsKeyLogFlag {
	f := &tlsKeyLogFlag{}
	fs.Func(tlsKeyLogFlagName, "write this run's TLS session secrets (the NSS key log format) to a NEW file, mode 0600, so a packet tool can decrypt a capture of the run's gNMI, RESTCONF and http.request device sessions; the file then decrypts them, credentials included, so guard and delete it", func(v string) error {
		switch {
		case v == "":
			return errors.New("needs a file name")
		case v == "-":
			return errors.New("writes to a new file, never to standard output: name a file")
		case f.path != "":
			return errors.New("is given twice")
		}
		f.path = v
		return nil
	})
	return f
}

// option returns the file named, refusing one that already exists, or one
// inside projectDir, before anything is loaded, so a run that could only be
// refused later is refused first. The file is created only as the run
// starts (startTLSKeyLog), where creating it refuses an existing name again,
// atomically.
//
// A key log inside the project is refused because methods read project
// files (file.copy's src, a template) and send them to devices: a runbook in
// the same project could then carry the run's own session secrets off the
// machine.
func (f *tlsKeyLogFlag) option(projectDir string) (string, error) {
	if f.path == "" {
		return "", nil
	}
	inside, err := insideDirResolved(f.path, projectDir)
	if err != nil {
		return "", fmt.Errorf("--%s: %w", tlsKeyLogFlagName, err)
	}
	if inside {
		return "", fmt.Errorf("--%s: %s is inside the project %s, whose files a run's methods can read and send to devices: write the key log outside the project", tlsKeyLogFlagName, f.path, projectDir)
	}
	if _, err := os.Lstat(f.path); err == nil {
		return "", fmt.Errorf("--%s: %s already exists: a key log is never written over or added to, so name a file that does not exist yet", tlsKeyLogFlagName, f.path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("--%s: %w", tlsKeyLogFlagName, err)
	}
	return f.path, nil
}

// insideDirResolved reports whether path, which need not exist, lies in dir or
// below it, comparing both with symbolic links resolved where they exist
// (the file's directory, and dir), so a link cannot carry the key log into
// the project unnoticed. (ai.go's insideDir compares without resolving
// links, which is enough where it is used and not here.)
func insideDirResolved(path, dir string) (bool, error) {
	absDir, err := resolvedAbs(dir)
	if err != nil {
		return false, err
	}
	absParent, err := resolvedAbs(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(absDir, filepath.Join(absParent, filepath.Base(path)))
	if err != nil {
		return false, nil
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

// resolvedAbs is p made absolute, with its symbolic links resolved when p
// exists; a p that does not exist is only made absolute.
func resolvedAbs(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// startTLSKeyLog creates the run's key log when path names one, recording
// the warning every such run gives on rep, and returns it (nil for none).
func startTLSKeyLog(path string, rep *runReport) (*devicetls.KeyLog, error) {
	if path == "" {
		return nil, nil
	}
	keyLog, err := devicetls.CreateKeyLog(path)
	if err != nil {
		return nil, fmt.Errorf("--%s: %w", tlsKeyLogFlagName, err)
	}
	rep.Warnings = append(rep.Warnings, clireport.RunWarning{Kind: clireport.WarnTLSKeyLog, When: "start", Message: keyLog.Warning()})
	return keyLog, nil
}

// closeTLSKeyLog closes the run's key log, saying on standard error when the
// file could not be closed, since its last lines may then be missing.
func closeTLSKeyLog(keyLog *devicetls.KeyLog) {
	if keyLog == nil {
		return
	}
	if err := keyLog.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: closing the TLS key log %s: %v; its last lines may be missing\n", keyLog.Path(), err)
	}
}
