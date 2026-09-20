// Package user implements the three "identity.user.*" Collection
// methods: create, modify and remove.
//
// # No new pkg/ primitive
//
// Exactly like pkg.apt.* and pkg.dnf.*, this talks to the target
// entirely through pkg/remoteexec (via sdk.Connect): account state is
// read with getent, and changed with useradd/usermod/userdel, all as
// plain SSH commands. Nothing here needed a shared primitive of its
// own.
//
// # Read first, always
//
// Every method here reads getent's own record of the account before
// deciding whether to act, and converges only the attributes the
// runbook actually named against the attributes getent actually
// reports, the same discipline pkg.apt.install already established: a
// method that ran usermod unconditionally would work, and would report
// changed on every run forever.
//
// # Deliberately out of scope for this pass
//
// Supplementary group membership (ansible.builtin.user's groups and
// append) and account passwords are not managed here. Both are real,
// separately-scoped follow-up work, not oversights: password handling
// needs a real answer for idempotency against a hash the runbook did
// not generate, and supplementary-group convergence needs its own
// query-then-diff design the same way this file's primary-group
// handling already has. A task naming either concept today has no
// parameter to spend it on; this package's own Doc says so.
//
// # The capability this cannot reach yet
//
// capability.PosixAccountCapable exists (pkg/capability/capabilities_posix.go)
// and is what RequiredCapabilities below names, but no device type in
// this repository structurally implements it today, the same gap
// pkg/apt/apt.go documents for capability.AptCapable: PasswdPath has no
// real accessor anywhere. That is settled, intentional architecture
// (internal/inventory/devices/linux/server_test.go's
// TestNewServer_UnionsClassificationCapabilities is the regression
// proof, for a sibling capability), not an oversight this package's own
// tests can or should paper over. The practical consequence:
// identity.user.create, identity.user.modify and identity.user.remove
// are implemented and tested here, against a real in-process SSH server
// with fake getent/useradd/usermod/userdel on PATH, exactly the tier
// pkg.apt.* already ships at, but are not yet reachable against a real
// inventory device through the platform end to end.
package user

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Ansible's own ansible.builtin.user spelling, reused rather than
// invented: name, uid, group (primary group, name or numeric gid),
// shell, home, comment (the GECOS field), create_home and system.
// remove mirrors ansible.builtin.user's own remove parameter (also
// delete the home directory and mail spool), spent only by Remove.
const (
	paramName       = "name"
	paramUID        = "uid"
	paramGroup      = "group"
	paramShell      = "shell"
	paramHome       = "home"
	paramComment    = "comment"
	paramCreateHome = "create_home"
	paramSystem     = "system"
	paramRemove     = "remove"
)

// statName is the one stat this namespace names directly; every other
// fact worth reporting lives in the before/after account maps sdk.Diff
// already records, the same way pkg.apt.* names statName and statVersion
// but leaves everything else to the diff. An account has five fields
// worth comparing and no single one of them is the equivalent of a
// package's version, so none gets its own top-level stat here.
const statName = "name"

// account is what getent passwd reports about one user.
type account struct {
	exists  bool
	uid     int
	gid     int
	comment string
	home    string
	shell   string
}

// Map renders account the way sdk.Diff records it.
func (a account) Map() map[string]any {
	return map[string]any{
		"exists":  a.exists,
		"uid":     a.uid,
		"gid":     a.gid,
		"comment": a.comment,
		"home":    a.home,
		"shell":   a.shell,
	}
}

// queryUser asks getent directly for an account's record, rather than
// reading /etc/passwd, so a name resolved through any NSS-backed source
// (LDAP, SSSD) answers exactly the way useradd/usermod themselves would
// see it, not just a local file.
func queryUser(ctx context.Context, conn *remoteexec.Conn, name string) (account, error) {
	result, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"getent", "passwd", name}))
	if err != nil {
		return account{}, err
	}
	if result.ExitCode == 2 {
		// getent's own convention for "no such entry in that database":
		// a name useradd has never heard of, not a failure worth
		// surfacing.
		return account{}, nil
	}
	if result.ExitCode != 0 {
		return account{}, fmt.Errorf("getent passwd %s exited %d: %s", name, result.ExitCode, failureDetail(result))
	}
	fields := strings.Split(strings.TrimRight(result.Stdout, "\n"), ":")
	if len(fields) < 7 {
		return account{}, fmt.Errorf("getent passwd %s: unexpected output %q", name, result.Stdout)
	}
	uid, err := strconv.Atoi(fields[2])
	if err != nil {
		return account{}, fmt.Errorf("getent passwd %s: uid field %q is not a number", name, fields[2])
	}
	gid, err := strconv.Atoi(fields[3])
	if err != nil {
		return account{}, fmt.Errorf("getent passwd %s: gid field %q is not a number", name, fields[3])
	}
	return account{exists: true, uid: uid, gid: gid, comment: fields[4], home: fields[5], shell: fields[6]}, nil
}

// resolveGroupGID asks getent for the numeric gid a group name or
// numeric gid string resolves to, so a primary-group drift check
// compares like with like regardless of which form the runbook wrote:
// useradd/usermod's own -g accepts either, but getent passwd only ever
// reports the numeric form back.
func resolveGroupGID(ctx context.Context, conn *remoteexec.Conn, group string) (int, error) {
	result, err := conn.Run(ctx, remoteexec.QuoteCommand([]string{"getent", "group", group}))
	if err != nil {
		return 0, err
	}
	if result.ExitCode == 2 {
		return 0, fmt.Errorf("group %q does not exist", group)
	}
	if result.ExitCode != 0 {
		return 0, fmt.Errorf("getent group %s exited %d: %s", group, result.ExitCode, failureDetail(result))
	}
	fields := strings.Split(strings.TrimRight(result.Stdout, "\n"), ":")
	if len(fields) < 3 {
		return 0, fmt.Errorf("getent group %s: unexpected output %q", group, result.Stdout)
	}
	gid, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, fmt.Errorf("getent group %s: gid field %q is not a number", group, fields[2])
	}
	return gid, nil
}

// desired is whichever of uid/group/shell/home/comment the runbook
// actually set, each nil when the task left it unspecified. A pointer,
// not a zero value, is what lets converge tell "not requested" apart
// from "requested as empty", the same reason sdk.StringSlice reports
// presence separately from its value.
type desired struct {
	uid     *int
	group   *string
	shell   *string
	home    *string
	comment *string
}

func parseDesired(params map[string]any) (desired, error) {
	var d desired
	if uid, ok, err := sdk.IntParam(params, paramUID); err != nil {
		return desired{}, fmt.Errorf("%s: %w", paramUID, err)
	} else if ok {
		d.uid = &uid
	}
	if v := sdk.StringParam(params, paramGroup); v != "" {
		d.group = &v
	}
	if v := sdk.StringParam(params, paramShell); v != "" {
		d.shell = &v
	}
	if v := sdk.StringParam(params, paramHome); v != "" {
		d.home = &v
	}
	if v := sdk.StringParam(params, paramComment); v != "" {
		d.comment = &v
	}
	return d, nil
}

// useraddArgs builds a fresh account's full useradd invocation,
// including the trailing login name, from whichever of
// uid/group/shell/home/comment the runbook set. createHome and system
// are creation-only concepts with no usermod equivalent, so they never
// appear in converge's own argument building below.
func useraddArgs(name string, d desired, createHome, system bool) []string {
	args := []string{"useradd"}
	if d.uid != nil {
		args = append(args, "-u", strconv.Itoa(*d.uid))
	}
	if d.group != nil {
		args = append(args, "-g", *d.group)
	}
	if d.shell != nil {
		args = append(args, "-s", *d.shell)
	}
	if d.home != nil {
		args = append(args, "-d", *d.home)
	}
	if d.comment != nil {
		args = append(args, "-c", *d.comment)
	}
	if system {
		args = append(args, "-r")
	}
	if createHome {
		args = append(args, "-m")
	} else {
		args = append(args, "-M")
	}
	return append(args, name)
}

// converge compares an existing account against whichever attributes the
// runbook actually requested and returns the usermod flags needed to
// close the gap, together with the old value of exactly the attributes
// it decided to change, keyed by this namespace's own param names. Both
// are empty when every requested attribute already matches: re-running
// create or modify against an account already in the requested state
// must not report a change forever, the same rule pkg.apt.install
// already follows for a package.
//
// oldValues is captured here, from current, rather than by diffing a
// before/after requery once usermod has run: the account this runs
// against is the only truthful source for what a value was about to
// become, and a caller building an inverse from it needs no assumption
// that a later query will report the mutation accurately.
func converge(ctx context.Context, conn *remoteexec.Conn, current account, d desired) (args []string, oldValues map[string]any, err error) {
	oldValues = map[string]any{}
	if d.uid != nil && *d.uid != current.uid {
		args = append(args, "-u", strconv.Itoa(*d.uid))
		oldValues[paramUID] = current.uid
	}
	if d.group != nil {
		gid, err := resolveGroupGID(ctx, conn, *d.group)
		if err != nil {
			return nil, nil, err
		}
		if gid != current.gid {
			args = append(args, "-g", *d.group)
			// The numeric gid is exactly what usermod's own -g accepts,
			// alongside a name; using it here needs no second
			// resolution to build a working inverse.
			oldValues[paramGroup] = strconv.Itoa(current.gid)
		}
	}
	if d.shell != nil && *d.shell != current.shell {
		args = append(args, "-s", *d.shell)
		oldValues[paramShell] = current.shell
	}
	if d.home != nil && *d.home != current.home {
		args = append(args, "-d", *d.home)
		oldValues[paramHome] = current.home
	}
	if d.comment != nil && *d.comment != current.comment {
		args = append(args, "-c", *d.comment)
		oldValues[paramComment] = current.comment
	}
	return args, oldValues, nil
}

// runUseradd, runUsermod and runUserdel each run one invocation and
// treat a non-zero exit as a real error.
func runUseradd(ctx context.Context, conn *remoteexec.Conn, args ...string) (remoteexec.Result, error) {
	return runIdentityCmd(ctx, conn, args)
}

func runUsermod(ctx context.Context, conn *remoteexec.Conn, name string, args []string) (remoteexec.Result, error) {
	return runIdentityCmd(ctx, conn, append(append([]string{"usermod"}, args...), name))
}

func runUserdel(ctx context.Context, conn *remoteexec.Conn, name string, removeHome bool) (remoteexec.Result, error) {
	args := []string{"userdel"}
	if removeHome {
		args = append(args, "-r")
	}
	return runIdentityCmd(ctx, conn, append(args, name))
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
// non-zero exit: stderr when the command wrote one, stdout otherwise,
// and a plain statement when it said nothing at all. Mirrors
// internal/catalog/pkg/apt.failureDetail; useradd/usermod/userdel/getent
// almost always explain themselves on stderr.
func failureDetail(result remoteexec.Result) string {
	if detail := strings.TrimSpace(result.Stderr); detail != "" {
		return detail
	}
	if detail := strings.TrimSpace(result.Stdout); detail != "" {
		return detail
	}
	return "no output"
}

// recordState writes the account name and the before/after diff, the
// two things every method in this namespace reports regardless of
// which one ran.
// predictAccount is what useradd or usermod would leave, for a check,
// without running either. For an account that exists it is the account
// with every requested attribute applied (converge decides which differ;
// the group is resolved to its gid the way usermod resolves it). For a
// new one it is only what the task names: a uid, gid, home or shell the
// task leaves out is the system's to assign, so it is left out rather
// than guessed.
func predictAccount(ctx context.Context, conn *remoteexec.Conn, current account, d desired) (map[string]any, error) {
	after := map[string]any{"exists": true}
	if current.exists {
		after = current.Map()
	}
	if d.uid != nil {
		after["uid"] = *d.uid
	}
	if d.group != nil {
		gid, err := resolveGroupGID(ctx, conn, *d.group)
		if err != nil {
			return nil, err
		}
		after["gid"] = gid
	}
	if d.shell != nil {
		after["shell"] = *d.shell
	}
	if d.home != nil {
		after["home"] = *d.home
	}
	if d.comment != nil {
		after["comment"] = *d.comment
	}
	return after, nil
}

// recordPrediction is recordState for a check, with a predicted after.
func recordPrediction(rc sdk.RunbookContext, name string, before account, after map[string]any) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after})
}

func recordState(rc sdk.RunbookContext, name string, before, after account) error {
	if err := rc.SetStat(statName, name); err != nil {
		return err
	}
	return sdk.RecordDiff(rc, sdk.Diff{Before: before.Map(), After: after.Map()})
}
