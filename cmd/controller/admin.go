// The controller's administrative subcommands: bootstrap-admin,
// reset-password and unlock.
//
// # Why these are a subcommand and not an HTTP route or an email
//
// PLAN.md Section 18.1 requires local password authentication. Password
// reset needs a channel that does not depend on already being able to log
// in, and the obvious one is email, which would put this work behind Phase
// 28's Notification Engine and would make the first login on a clean
// machine depend on working outbound SMTP. That is the wrong thing to put
// between an operator and their own control plane, so the channel is the
// host shell they already have.
//
// bootstrap-admin in particular is the answer to a question the rest of the
// phase cannot answer on its own: the password path works, and on a clean
// machine no account HAS a password, so there is still no way in. This is
// what closes that, and with it Phase 20's clean-machine release gate.
//
// # Why main() is not restructured into a dispatcher
//
// cmd/controller has never had a subcommand. The obvious shape is to turn
// main() into a dispatch table, and it is the wrong one here: main()'s own
// header comment records a known residual risk about every fatal path
// skipping deferred cleanup, and rewriting those paths is unowned work that
// an authentication phase should not drag in. Instead main() gains a
// three-line argument guard at the top. With no arguments the binary is the
// server, byte-identically to before. With arguments it is an admin tool
// that never opens NATS, never starts an HTTP listener and never elects a
// leader.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
)

// adminCommand is one subcommand's entry point.
type adminCommand func(ctx context.Context, deps *adminDeps, args []string) error

// adminCommands maps each subcommand name to its handler (Command pattern),
// copying cmd/pleiades's own dispatch map. Adding a subcommand means adding
// an entry here, never branching inside an existing one.
var adminCommands = map[string]adminCommand{
	"bootstrap-admin": runBootstrapAdmin,
	"reset-password":  runResetPassword,
	"unlock":          runUnlock,
}

// adminDeps is the minimum a subcommand needs.
//
// Deliberately not the server's full wiring: an admin command touches the
// database and nothing else, so it opens no NATS connection, starts no
// listener and joins no election. A command that cannot reach the message
// bus cannot accidentally dispatch a job.
type adminDeps struct {
	client    *ent.Client
	access    access.Store
	passwords localauth.Store
	sessions  sessionRevoker
	logger    *slog.Logger
}

// sessionRevoker is the one session operation these commands need.
//
// A narrow interface rather than session.Store, because an administrative
// reset revokes sessions and has no business creating or resolving one.
type sessionRevoker interface {
	DeleteForSubject(ctx context.Context, subject, keepToken string) (int, error)
}

// runAdmin dispatches one subcommand and returns a process exit code.
func runAdmin(args []string) int {
	name := args[0]
	command, ok := adminCommands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "controller: unknown command %q\n\n", name)
		printAdminUsage()
		return 2
	}

	ctx := context.Background()
	deps, cleanup, err := openAdminDeps(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "controller %s: %v\n", name, err)
		return 1
	}
	defer cleanup()

	if err := command(ctx, deps, args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "controller %s: %v\n", name, err)
		return 1
	}
	return 0
}

// isAdminCommand reports whether the first argument names a subcommand.
//
// Checked rather than assuming any argument means admin mode, so an unknown
// flag intended for the server produces a usage message instead of silently
// starting something else.
func isAdminCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	_, ok := adminCommands[args[0]]
	return ok || !strings.HasPrefix(args[0], "-")
}

// printAdminUsage lists the subcommands.
//
// Written here rather than transcribed into internal/clispec, and that is a
// decision rather than an omission: clispec.Root is the `pleiades` tree, and
// tools/gendocs walks that one tree to produce docs/reference/cli.md. These
// three commands are therefore outside the generated CLI reference, and are
// documented in prose in the security book instead. Adding a second root to
// clispec for three commands would mean a second traversal in gendocs and a
// second consistency test, for a surface an operator reaches once per
// deployment.
func printAdminUsage() {
	fmt.Fprint(os.Stderr, `Usage: controller <command> [flags]

Administrative commands, run on the host rather than over HTTP:

  bootstrap-admin --email <address>   create the first administrator, or set
                                      a password on an existing account and
                                      grant it system-scope admin
  reset-password  --email <address>   replace an account's password and
                                      revoke all of its sessions
  unlock          --email <address>   clear a lockout without changing the
                                      password

One operational command, which touches no database at all:

  healthcheck                         ask this controller's own /readyz on
                                      LISTEN_ADDR and exit 0 only if it
                                      reports ready (see healthcheck.go for
                                      why the binary probes itself)

With no command, the controller starts the server.

Each command reads the same database and encryption-key configuration the
server does, and prompts for the password with echo disabled. For automation,
--password-stdin reads it as one line on standard input instead:

  echo "$PASSWORD" | controller bootstrap-admin --email you@example.com --password-stdin

A password is never accepted as a flag value on either path. A flag is
visible in shell history and in this process's argument list to every other
user on the machine for as long as it runs.
`)
}

// runBootstrapAdmin creates the first administrator.
//
// Idempotent in the parts that can be and refusing in the part that must
// not be: a missing User, Organization, Team or system-scope binding is
// created, and an EXISTING password is not silently replaced without
// --force. Overwriting a working credential because somebody re-ran a
// bootstrap script is a lockout with a helpful-sounding cause.
func runBootstrapAdmin(ctx context.Context, deps *adminDeps, args []string) error {
	fs := flag.NewFlagSet("bootstrap-admin", flag.ContinueOnError)
	email := fs.String("email", "", "the administrator's email address, which is also their login")
	force := fs.Bool("force", false, "replace an existing password rather than refusing")
	stdinPassword := fs.Bool("password-stdin", false,
		"read the password as one line on standard input instead of prompting, for automation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}

	subject, err := localauth.NormalizeEmail(*email)
	if err != nil {
		return err
	}

	// Refuse before prompting, so somebody who typed the wrong address is
	// not asked for a password first and told afterwards.
	if !*force {
		if _, err := deps.passwords.Account(ctx, subject); err == nil {
			return fmt.Errorf("%q already has a password; use reset-password, or --force to overwrite", subject)
		} else if !errors.Is(err, localauth.ErrNoSuchAccount) {
			return err
		}
	}

	user, err := ensureAdminUser(ctx, deps, subject)
	if err != nil {
		return err
	}

	password, err := readNewPassword(subject, *stdinPassword)
	if err != nil {
		return err
	}
	// mustChange is false: the person typing this at a terminal IS the
	// account's owner, so there is nobody to hand it on to.
	if err := deps.passwords.SetPassword(ctx, subject, password, false); err != nil {
		return err
	}

	fmt.Printf("administrator %q is ready (user id %d)\n", subject, user.ID)
	fmt.Println("sign in at /ui/login with that address and the password you just set")
	return nil
}

// ensureAdminUser creates whatever part of the identity chain is missing and
// returns the User.
//
// The chain is Organization, then Team, then User, then a system-scope
// RoleBinding on the Team. All four, because PLAN.md Section 18.2 forbids
// granting a role directly to a User: a grant lives on a Team's RoleBinding
// rows, so a bootstrapped administrator needs a Team to hold the grant even
// when they are the only member.
//
// Everything here goes through access.Store rather than through ent
// directly, so a bootstrap appears in the activity stream exactly like any
// other administrative change. A break-glass path that creates an
// administrator invisibly is the one that gets used to create a second one
// nobody notices.
func ensureAdminUser(ctx context.Context, deps *adminDeps, subject string) (access.User, error) {
	const bootstrapName = "bootstrap"

	users, err := deps.access.ListUsers(ctx, access.Query{Search: subject, Limit: 2})
	if err != nil {
		return access.User{}, fmt.Errorf("listing users: %w", err)
	}
	for _, existing := range users {
		if existing.Email == subject {
			if err := ensureSystemAdminBinding(ctx, deps, existing, bootstrapName); err != nil {
				return access.User{}, err
			}
			return existing, nil
		}
	}

	created, err := deps.access.CreateUser(ctx, access.User{Email: subject})
	if err != nil {
		return access.User{}, fmt.Errorf("creating user: %w", err)
	}
	if err := ensureSystemAdminBinding(ctx, deps, created, bootstrapName); err != nil {
		return access.User{}, err
	}
	return created, nil
}

// ensureSystemAdminBinding gives the user a Team holding a system-scope
// admin grant, creating the Organization and Team if this is the first run.
func ensureSystemAdminBinding(ctx context.Context, deps *adminDeps, user access.User, name string) error {
	// Already a member of a team with a system-scope admin Allow? Then
	// there is nothing to do, and re-running is a no-op rather than a
	// second grant.
	if len(user.TeamIDs) > 0 {
		bindings, err := deps.access.ListBindings(ctx, access.BindingQuery{
			TeamIDs:    user.TeamIDs,
			ScopeTypes: []auth.ScopeType{auth.ScopeSystem},
		})
		if err != nil {
			return fmt.Errorf("listing bindings: %w", err)
		}
		for _, b := range bindings {
			if b.Role == auth.RoleAdmin {
				return nil
			}
		}
	}

	org, err := ensureOrganization(ctx, deps, name)
	if err != nil {
		return err
	}
	team, err := ensureTeam(ctx, deps, name, org.ID)
	if err != nil {
		return err
	}

	// Membership is written from the TEAM side, not by setting TeamIDs on
	// the User and calling UpdateUser. access.User.TeamIDs is a read
	// projection that UpdateUser silently ignores, so doing it the other
	// way round returns nil having changed nothing, and produces an account
	// that signs in successfully and reaches nothing at all. This test
	// suite's first run did exactly that.
	//
	// access.Team.UserIDs is replaced WHOLESALE rather than merged (its own
	// doc explains why: a merge makes removing the last member
	// inexpressible), so the existing membership is read and extended
	// rather than overwritten. Re-running a bootstrap must not evict
	// whoever else is on the team.
	current, err := deps.access.GetTeam(ctx, team.ID)
	if err != nil {
		return fmt.Errorf("reading the bootstrap team's membership: %w", err)
	}
	current.UserIDs = appendMissing(current.UserIDs, user.ID)
	if err := deps.access.UpdateTeam(ctx, current); err != nil {
		return fmt.Errorf("adding the user to the bootstrap team: %w", err)
	}

	if _, err := deps.access.CreateBinding(ctx, access.Binding{
		TeamID:    team.ID,
		Role:      auth.RoleAdmin,
		ScopeType: auth.ScopeSystem,
		// Effect is set explicitly rather than left to a zero value. The
		// store refuses an empty effect, which is what caught this: an
		// unset Effect is not "allow by default", and a binding type whose
		// zero value silently meant Allow would be a permission granted by
		// forgetting to type one.
		Effect: auth.EffectAllow,
	}); err != nil {
		return fmt.Errorf("granting system-scope admin: %w", err)
	}
	return nil
}

func ensureOrganization(ctx context.Context, deps *adminDeps, name string) (access.Organization, error) {
	orgs, err := deps.access.ListOrganizations(ctx, access.Query{Search: name, Limit: 2})
	if err != nil {
		return access.Organization{}, fmt.Errorf("listing organizations: %w", err)
	}
	for _, org := range orgs {
		if org.Name == name {
			return org, nil
		}
	}
	org, err := deps.access.CreateOrganization(ctx, access.Organization{
		Name:        name,
		Description: "Created by controller bootstrap-admin for the first administrator.",
	})
	if err != nil {
		return access.Organization{}, fmt.Errorf("creating the bootstrap organization: %w", err)
	}
	return org, nil
}

func ensureTeam(ctx context.Context, deps *adminDeps, name string, orgID int) (access.Team, error) {
	teams, err := deps.access.ListTeams(ctx, access.TeamQuery{
		Query:           access.Query{Search: name, Limit: 10},
		OrganizationIDs: []int{orgID},
	})
	if err != nil {
		return access.Team{}, fmt.Errorf("listing teams: %w", err)
	}
	for _, team := range teams {
		if team.Name == name && team.OrganizationID == orgID {
			return team, nil
		}
	}
	created, err := deps.access.CreateTeam(ctx, access.Team{
		Name:           name,
		OrganizationID: orgID,
		Description:    "Holds the system-scope admin grant created by controller bootstrap-admin.",
	})
	if err != nil {
		return access.Team{}, fmt.Errorf("creating the bootstrap team: %w", err)
	}
	return created, nil
}

// runResetPassword replaces an account's password and revokes its sessions.
//
// Revoking is not optional and not a flag. A reset happens because the
// password is believed compromised or was forgotten, and in the first case
// leaving live sessions alive means the attacker keeps their access for up
// to the absolute deadline while the owner believes they have locked them
// out.
func runResetPassword(ctx context.Context, deps *adminDeps, args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	email := fs.String("email", "", "the account to reset")
	keep := fs.Bool("keep-sessions", false, "do not revoke the account's existing sessions")
	stdinPassword := fs.Bool("password-stdin", false,
		"read the password as one line on standard input instead of prompting, for automation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}

	subject, err := localauth.NormalizeEmail(*email)
	if err != nil {
		return err
	}
	// Distinguishing "no such account" is correct HERE and wrong on the
	// login path. An operator running a reset needs to know they typed the
	// address wrong; an unauthenticated caller does not.
	if _, err := deps.passwords.Account(ctx, subject); err != nil {
		if errors.Is(err, localauth.ErrNoSuchAccount) {
			return fmt.Errorf("%q has no local password; use bootstrap-admin to create one", subject)
		}
		return err
	}

	password, err := readNewPassword(subject, *stdinPassword)
	if err != nil {
		return err
	}
	// mustChange is true: an administrator chose this password, so its
	// owner has not, and it should not silently become permanent.
	if err := deps.passwords.SetPassword(ctx, subject, password, true); err != nil {
		return err
	}

	if *keep {
		fmt.Printf("password reset for %q; existing sessions were left alive by --keep-sessions\n", subject)
		return nil
	}
	revoked, err := deps.sessions.DeleteForSubject(ctx, subject, "")
	if err != nil {
		return fmt.Errorf("password was reset but sessions could not be revoked: %w", err)
	}
	fmt.Printf("password reset for %q, %d session(s) revoked\n", subject, revoked)
	return nil
}

// runUnlock clears a lockout without touching the password.
//
// This is the recovery path LockoutPolicy.Duration's doc refers to. It runs
// over the operator's own shell access rather than over HTTP, so locking
// out the last administrator never requires a second administrator who may
// not exist.
func runUnlock(ctx context.Context, deps *adminDeps, args []string) error {
	fs := flag.NewFlagSet("unlock", flag.ContinueOnError)
	email := fs.String("email", "", "the account to unlock")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *email == "" {
		return errors.New("--email is required")
	}

	subject, err := localauth.NormalizeEmail(*email)
	if err != nil {
		return err
	}
	if err := deps.passwords.Unlock(ctx, subject); err != nil {
		return err
	}
	fmt.Printf("%q unlocked\n", subject)
	return nil
}

// readNewPassword obtains a new password, interactively or from a pipe.
//
// The interactive path asks twice and checks the two agree, because there
// is no way to recover a mistyped password that was never displayed: the
// account simply stops working, with no signal distinguishing "I typed it
// wrong then" from "I am typing it wrong now".
//
// The piped path asks once, because a script has nothing to mistype and a
// second read would consume a line the caller never meant to send. It is
// reached only through an explicit --password-stdin, never by sniffing
// whether stdin happens to be a terminal, so the same command never
// silently behaves two ways on two machines.
//
// Neither path accepts the password as a flag VALUE. A flag is visible in
// shell history and in this process's argument list to every other user on
// the machine for as long as it runs, which is a worse exposure than the
// one the whole credential store exists to prevent.
func readNewPassword(subject string, fromStdin bool) (string, error) {
	if fromStdin {
		password, err := prompt.SecretFromStdin()
		if err != nil {
			return "", err
		}
		if err := localauth.ValidatePassword(subject, password); err != nil {
			return "", err
		}
		return password, nil
	}

	first, err := prompt.Secret(fmt.Sprintf("New password for %s: ", subject))
	if err != nil {
		return "", err
	}
	second, err := prompt.Secret("Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("the two passwords do not match")
	}
	// Validated here as well as in the store, so a terminal user is told
	// what is wrong before anything is written rather than seeing a
	// wrapped error from two layers down.
	if err := localauth.ValidatePassword(subject, first); err != nil {
		return "", err
	}
	return first, nil
}

// appendMissing adds id to ids unless it is already present.
func appendMissing(ids []int, id int) []int {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}
