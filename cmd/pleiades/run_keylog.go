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

// option returns the file named, refusing one that already exists before
// anything is loaded, so a run that could only be refused later is refused
// first. The file is created only as the run starts (startTLSKeyLog), where
// creating it refuses an existing name again, atomically.
func (f *tlsKeyLogFlag) option() (string, error) {
	if f.path == "" {
		return "", nil
	}
	if _, err := os.Lstat(f.path); err == nil {
		return "", fmt.Errorf("--%s: %s already exists: a key log is never written over or added to, so name a file that does not exist yet", tlsKeyLogFlagName, f.path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("--%s: %w", tlsKeyLogFlagName, err)
	}
	return f.path, nil
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
