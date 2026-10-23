// Package ovs implements the net.ovs Collection methods, which manage an
// Open vSwitch switch's bridges and ports through ovs-vsctl over SSH, as
// the account onboarding proved may use its database
// (capability.OpenVSwitchCapable). This file holds what both share.
package ovs

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ifaceName is what a bridge or port name may be: what Linux names an
// interface (at most 15 bytes), and nothing ovs-vsctl could read as an
// option or a second command.
var ifaceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,14}$`)

// session is one SSH connection to a switch, for one method's run.
type session struct {
	conn *remoteexec.Conn
	fqcn string
}

// open connects to device for fqcn.
func open(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*session, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}
	return &session{conn: conn, fqcn: fqcn}, nil
}

// close closes the connection.
func (s *session) close() { _ = s.conn.Close() }

// vsctl runs ovs-vsctl with args and returns its exit code and output,
// failing only when it could not be run at all.
func (s *session) vsctl(ctx context.Context, args ...string) (int, string, error) {
	res, err := s.conn.Run(ctx, remoteexec.QuoteCommand(append([]string{"ovs-vsctl"}, args...)))
	if err != nil {
		return 0, "", fmt.Errorf("%s: ovs-vsctl: %w", s.fqcn, err)
	}
	return res.ExitCode, strings.TrimSpace(res.Stdout + "\n" + res.Stderr), nil
}

// must runs ovs-vsctl and fails on a non-zero exit.
func (s *session) must(ctx context.Context, args ...string) (string, error) {
	code, out, err := s.vsctl(ctx, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("%s: ovs-vsctl %s exited %d: %s", s.fqcn, args[0], code, bounded(out))
	}
	return out, nil
}

// bounded is device text fit for an error: at most 300 bytes, quoted so a
// control character is shown rather than acted on.
func bounded(text string) string {
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return strconv.Quote(text)
}

// nonDefault returns, of the settings named (each table, record column and
// its default value as ovs-vsctl get prints it), those a record holds set,
// as column=value: what an undo that recreates the record would not put
// back.
func (s *session) nonDefault(ctx context.Context, table, record string, columns map[string][]string) ([]string, error) {
	var set []string
	for _, col := range sortedKeys(columns) {
		v, err := s.must(ctx, "get", table, record, col)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(columns[col], v) {
			set = append(set, col+"="+v)
		}
	}
	return set, nil
}

// sortedKeys returns m's keys, sorted.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkName refuses a name that is not an interface name.
func checkName(fqcn, what, name string) error {
	if !ifaceName.MatchString(name) {
		return fmt.Errorf("%s: %s must be an interface name, got %q", fqcn, what, name)
	}
	return nil
}

// stateParam reads state: present (the default) or absent.
func stateParam(fqcn string, params map[string]any) (bool, error) {
	state, err := sdk.OptionalStringParam(params, "state")
	if err != nil {
		return false, err
	}
	switch state {
	case "", "present":
		return true, nil
	case "absent":
		return false, nil
	}
	return false, fmt.Errorf("%s: state must be present or absent, got %q", fqcn, state)
}

// unquote reads one ovs-vsctl get value: a string in double quotes or a
// bare word.
func unquote(v string) string {
	if u, err := strconv.Unquote(v); err == nil {
		return u
	}
	return v
}
