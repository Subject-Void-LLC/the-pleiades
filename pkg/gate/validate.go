// The rules a gate manifest meets before anything in it runs.
package gate

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var (
	// namePattern is a check's or a declared kind's name: what selection
	// and results print, so nothing a terminal or a shell acts on.
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// commandNamePattern is a required command: a bare name or a path,
	// nothing a shell would expand.
	commandNamePattern = regexp.MustCompile(`^[A-Za-z0-9._+/-]{1,255}$`)
	// envNamePattern is a required environment variable's name.
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// pinnedImagePattern is an image named by digest, with or without a tag.
	pinnedImagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)
	// networkPattern is a container network's name.
	networkPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
)

// supportedOS are the operating systems a check may require, as uname -s
// names them in lower case.
var supportedOS = []string{"darwin", "freebsd", "linux"}

// reservedExit are exit codes a check may not call a skip: the timeout
// wrapper's own (124, and 137 and 143 for a check killed by KILL or TERM),
// which would turn a check that ran out of time into one that skipped.
var reservedExit = []int{124, 137, 143}

// maxShellBytes bounds one shell string or command vector.
const maxShellBytes = 64 << 10

// maxTimeout bounds one check's timeout.
const maxTimeout = 24 * time.Hour

// Validate reports the first rule m breaks, naming the check or field.
func (m *Manifest) Validate() error {
	if m.Version != 1 {
		return fmt.Errorf("version %d: this platform reads gate manifest version 1 (write version: 1)", m.Version)
	}
	kinds, err := m.kindSet()
	if err != nil {
		return err
	}
	if m.Defaults.Parallelism < 0 || m.Defaults.Parallelism > MaxParallelism {
		return fmt.Errorf("defaults.parallelism %d: it is 1 to %d", m.Defaults.Parallelism, MaxParallelism)
	}
	if err := checkTimeout("defaults.timeout", m.Defaults.Timeout, true); err != nil {
		return err
	}
	if len(m.Checks) == 0 {
		return errors.New("the manifest declares no checks")
	}
	if len(m.Checks) > MaxChecks {
		return fmt.Errorf("the manifest declares %d checks, over the %d one may", len(m.Checks), MaxChecks)
	}
	seen := map[string]bool{}
	for i, c := range m.Checks {
		if err := m.validateCheck(c, kinds); err != nil {
			if c.Name == "" || !namePattern.MatchString(c.Name) {
				return fmt.Errorf("checks[%d]: %w", i, err)
			}
			return fmt.Errorf("check %s: %w", c.Name, err)
		}
		if seen[c.Name] {
			return fmt.Errorf("check %s is declared twice", c.Name)
		}
		seen[c.Name] = true
	}
	return nil
}

// kindSet is every kind m may use: the built-in ones and its own.
func (m *Manifest) kindSet() (map[string]bool, error) {
	kinds := map[string]bool{}
	for _, k := range BuiltinKinds() {
		kinds[k] = true
	}
	for i, k := range m.Kinds {
		switch {
		case !namePattern.MatchString(k.Name):
			return nil, fmt.Errorf("kinds[%d]: %q is not a kind name (lower-case letters, digits, '.', '_' and '-', at most 64)", i, k.Name)
		case kinds[k.Name]:
			return nil, fmt.Errorf("kind %s is declared twice, or is built in", k.Name)
		}
		kinds[k.Name] = true
	}
	return kinds, nil
}

// validateCheck reports the first rule c breaks.
func (m *Manifest) validateCheck(c Check, kinds map[string]bool) error {
	switch {
	case c.Name == "":
		return errors.New("a check has no name")
	case !namePattern.MatchString(c.Name):
		return fmt.Errorf("%q is not a check name (lower-case letters, digits, '.', '_' and '-', at most 64)", c.Name)
	case c.Kind == "":
		return errors.New("no kind: every check says which kind it is (pure, db, network, model, or one the manifest declares)")
	case !kinds[c.Kind]:
		return fmt.Errorf("kind %q is neither built in (pure, db, network, model) nor declared under kinds", c.Kind)
	}
	if err := checkRunnable(c); err != nil {
		return err
	}
	if err := checkPlacement(c); err != nil {
		return err
	}
	if err := checkTimeout("timeout", c.Timeout, true); err != nil {
		return err
	}
	if c.Timeout == 0 && m.Defaults.Timeout == 0 {
		return errors.New("no timeout: set timeout on the check or defaults.timeout; there is no implicit one")
	}
	for _, code := range c.SkipExit {
		switch {
		case code < 1 || code > 255:
			return fmt.Errorf("skip_exit %d: an exit code a skip may use is 1 to 255", code)
		case slices.Contains(reservedExit, code):
			return fmt.Errorf("skip_exit %d: that is how a check that ran out of time ends, so it cannot mean skipped", code)
		}
	}
	return checkRequires(c.Requires)
}

// checkRunnable checks that c says what to run, once.
func checkRunnable(c Check) error {
	switch {
	case len(c.Command) > 0 && c.Shell != "":
		return errors.New("both command and shell: a check runs one of them")
	case len(c.Command) == 0 && strings.TrimSpace(c.Shell) == "":
		return errors.New("nothing to run: set command (an argument vector) or shell (one shell string)")
	}
	size := len(c.Shell)
	for i, arg := range c.Command {
		if i == 0 && arg == "" {
			return errors.New("command's first element, the program, is empty")
		}
		if strings.ContainsRune(arg, 0) {
			return fmt.Errorf("command[%d] holds a NUL byte", i)
		}
		size += len(arg)
	}
	if strings.ContainsRune(c.Shell, 0) {
		return errors.New("shell holds a NUL byte")
	}
	if size > maxShellBytes {
		return fmt.Errorf("the command is %d bytes, over the %d one may be", size, maxShellBytes)
	}
	return nil
}

// checkPlacement checks where c runs: a directory, or an image.
func checkPlacement(c Check) error {
	if strings.ContainsAny(c.Dir, "\x00\n\r") {
		return errors.New("dir holds a NUL or a line break")
	}
	if c.Image == "" {
		if c.Network != "" || c.Writable || c.Pull {
			return errors.New("network, writable and pull apply to an image check, and this one names no image")
		}
		return nil
	}
	switch {
	case c.Dir != "":
		return errors.New("both image and dir: an image check runs in its container")
	case !pinnedImagePattern.MatchString(c.Image):
		return fmt.Errorf("image %q is not pinned by digest (name@sha256:...): a check runs exactly the image that was reviewed", c.Image)
	case c.Network != "" && !networkPattern.MatchString(c.Network):
		return fmt.Errorf("network %q is not a network name", c.Network)
	}
	return nil
}

// checkTimeout checks one timeout; zero is allowed where optional is.
func checkTimeout(field string, d Duration, optional bool) error {
	switch {
	case d == 0 && optional:
		return nil
	case d <= 0:
		return fmt.Errorf("%s must be longer than zero", field)
	case time.Duration(d) > maxTimeout:
		return fmt.Errorf("%s %v is over the %v a check may run", field, time.Duration(d), maxTimeout)
	}
	return nil
}

// checkRequires checks a check's preconditions.
func checkRequires(r Requires) error {
	for _, name := range r.Commands {
		if !commandNamePattern.MatchString(name) {
			return fmt.Errorf("requires.commands: %q is not a command name or path", name)
		}
	}
	for _, name := range r.Env {
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("requires.env: %q is not an environment variable name", name)
		}
	}
	for _, path := range r.Files {
		if path == "" || strings.ContainsAny(path, "\x00\n\r") {
			return fmt.Errorf("requires.files: %q is not a path", path)
		}
	}
	if r.OS != "" && !slices.Contains(supportedOS, r.OS) {
		return fmt.Errorf("requires.os %q: it is one of %s", r.OS, strings.Join(supportedOS, ", "))
	}
	for _, endpoint := range r.TCP {
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil || host == "" || strings.ContainsAny(host, " \x00\n\r") {
			return fmt.Errorf("requires.tcp: %q is not a host:port", endpoint)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("requires.tcp: %q has no port from 1 to 65535", endpoint)
		}
	}
	return nil
}
