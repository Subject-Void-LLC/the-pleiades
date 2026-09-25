// Package group implements the three "identity.group.*" Collection
// methods: create, modify and remove.
//
// # No new pkg/ primitive
//
// Exactly like identity.user.*, this talks to the target entirely
// through pkg/remoteexec (via sdk.Connect): group state is read with
// getent, and changed with groupadd/groupmod/groupdel, all as plain SSH
// commands.
//
// # A group has one mutable attribute
//
// A POSIX group's identity IS its name; there is no rename operation
// (ansible.builtin.group has none either), and its membership list is
// identity.user.*'s own concern (the groups/append parameters that
// namespace deliberately does not implement yet), not this one's. That
// leaves gid as the only attribute create or modify can converge, which
// is why, unlike identity.user.*, this package has no desired/converge
// pair: the comparison is a single int.
//
// # The capability, and the devices that satisfy it
//
// The same as identity.user.*, for the same capability
// (capability.PosixAccountCapable); see internal/catalog/identity/user/user.go.
package group

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Ansible's own ansible.builtin.group spelling, reused rather than
// invented.
const (
	paramName   = "name"
	paramGID    = "gid"
	paramSystem = "system"
)

// statName is the one stat this namespace names directly; gid itself
// lives in the before/after maps sdk.Diff already records.
const statName = "name"

// groupAccount is what getent group reports about one group.
type groupAccount struct {
	exists bool
	gid    int
}

// Map renders groupAccount the way sdk.Diff records it.
func (a groupAccount) Map() map[string]any {
	return map[string]any{"exists": a.exists, "gid": a.gid}
}

// queryGroup asks getent directly for a group's record, the same reason
// identity.user's queryUser does: an NSS-backed source answers exactly
// the way groupadd/groupmod themselves would see it.
func queryGroup(ctx context.Context, conn *remoteexec.Conn, name string) (groupAccount, error) {
	result, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"getent", "group", name}))
	if err != nil {
		return groupAccount{}, err
	}
	if result.ExitCode == 2 {
		// getent's own convention for "no such entry in that database".
		return groupAccount{}, nil
	}
	if result.ExitCode != 0 {
		return groupAccount{}, fmt.Errorf("getent group %s exited %d: %s", name, result.ExitCode, failureDetail(result))
	}
	fields := strings.Split(strings.TrimRight(result.Stdout, "\n"), ":")
	if len(fields) < 3 {
		return groupAccount{}, fmt.Errorf("getent group %s: unexpected output %q", name, result.Stdout)
	}
	gid, err := strconv.Atoi(fields[2])
	if err != nil {
		return groupAccount{}, fmt.Errorf("getent group %s: gid field %q is not a number", name, fields[2])
	}
	return groupAccount{exists: true, gid: gid}, nil
}

// runGroupadd, runGroupmod and runGroupdel each run one invocation and
// treat a non-zero exit as a real error.
func runGroupadd(ctx context.Context, conn *remoteexec.Conn, args ...string) (remoteexec.Result, error) {
	return runIdentityCmd(ctx, conn, args)
}

func runGroupmod(ctx context.Context, conn *remoteexec.Conn, name string, gid int) (remoteexec.Result, error) {
	return runIdentityCmd(ctx, conn, []string{"groupmod", "-g", strconv.Itoa(gid), name})
}

func runGroupdel(ctx context.Context, conn *remoteexec.Conn, name string) (remoteexec.Result, error) {
	return runIdentityCmd(ctx, conn, []string{"groupdel", name})
}

func runIdentityCmd(ctx context.Context, conn *remoteexec.Conn, argv []string) (remoteexec.Result, error) {
	command := remoteexec.QuoteCommand(argv)
	result, err := conn.Run(ctx, command)
	if err != nil {
		return remoteexec.Result{}, err
	}
	if result.ExitCode != 0 {
		return remoteexec.Result{}, fmt.Errorf("%s exited %d: %s", command, result.ExitCode, failureDetail(result))
	}
	return result, nil
}

// failureDetail picks the stream an operator should read after a
// non-zero exit. Mirrors internal/catalog/identity/user.failureDetail.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return detail
	}
	return "no output"
}

// recordState writes the group name and the before/after diff, the two
// things every method in this namespace reports regardless of which one
// ran.
// recordPrediction is recordState for a check: the diff from before to
// the predicted after, given as a map so a gid the system would choose
// can be left out rather than guessed.
func recordPrediction(rc sdk.RunbookContext, name string, before groupAccount, after map[string]any) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after})
}

func recordState(rc sdk.RunbookContext, name string, before, after groupAccount) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()})
}
