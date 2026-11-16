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
	fs.Func(tlsKeyLogFlagName, "write this run's TLS session secrets (the NSS key log format) to a NEW file outside the project, mode 0600 (a name that exists, or one inside the project, is refused; not on Windows), so a packet tool can decrypt a capture of the run's gNMI, RESTCONF and http.request device sessions; the file then decrypts them, credentials included, so guard and delete it", func(v string) error {
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

// option returns the file named, refusing before anything is loaded what
// the run would refuse as it starts (devicetls.CheckKeyLogPath): Windows, a
// name that exists, a directory that does not exist or is not one, and a
// directory in or below one keyLogKeepOut lists for projectDir. The file is
// created only as the run starts (startTLSKeyLog), where everything is
// checked again, in the directory held open from that check to the file's
// creation.
func (f *tlsKeyLogFlag) option(projectDir string) (string, error) {
	if f.path == "" {
		return "", nil
	}
	if err := devicetls.CheckKeyLogPath(f.path, keyLogKeepOut(projectDir)...); err != nil {
		return "", fmt.Errorf("--%s: %w", tlsKeyLogFlagName, err)
	}
	return f.path, nil
}

// keyLogKeepOut lists the directories a run's key log may not be made in or
// below, for projectDir's run, each compared by identity
// (devicetls.KeepOut): the project, because methods read project files
// (file.copy's src, a template) and send them to devices, so a runbook in it
// could carry the run's own session secrets off the machine; and, when
// external Collections are loaded, their directory and each path they are
// granted, since those third-party programs run during the run and can read
// both.
func keyLogKeepOut(projectDir string) []devicetls.KeepOut {
	keep := []devicetls.KeepOut{{Dir: projectDir, What: "the project", Why: "whose files a run's methods can read and send to devices"}}
	collections := os.Getenv(collectionsDirEnv)
	if collections == "" {
		return keep
	}
	keep = append(keep, devicetls.KeepOut{Dir: collections, What: "the external Collections directory (" + collectionsDirEnv + ")", Why: "whose programs run during the run and can read it"})
	for _, p := range readPaths(os.Getenv(collectionsReadPathsEnv)) {
		keep = append(keep, devicetls.KeepOut{Dir: p, What: "a path external Collections may read (" + collectionsReadPathsEnv + ")", Why: "which every loaded program can read during the run"})
	}
	return keep
}

// startTLSKeyLog creates the run's key log when path names one, kept out of
// keepOut, recording the warning every such run gives on rep, and returns it
// (nil for none).
func startTLSKeyLog(path string, keepOut []devicetls.KeepOut, rep *runReport) (*devicetls.KeyLog, error) {
	if path == "" {
		return nil, nil
	}
	keyLog, err := devicetls.CreateKeyLog(path, keepOut...)
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
