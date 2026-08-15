// Tests for the administrative subcommands.
//
// These run against a real SQLite database through the same adminDeps
// production builds, per RULE 0. The claim worth proving is not that the
// flag parsing works; it is that bootstrap-admin produces an account that
// can actually SIGN IN, which means a User row, a Team, a system-scope
// admin RoleBinding and a verifiable password hash all being written
// consistently enough that auth.IdentityBuilder resolves them back to an
// admin identity. A double anywhere in that chain would prove nothing,
// because every link is a real query.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
)

const testAdminPassword = "a-real-admin-password"

func newAdminDeps(t *testing.T) (*adminDeps, *ent.Client) {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	activityStream := activity.NewEntStore(client)

	return &adminDeps{
		client: client,
		access: access.NewAuditedStore(
			access.NewEntStore(client), activityStream,
			func(context.Context) string { return "controller-cli" }, logger,
		),
		passwords: localauth.NewEntStore(client, logger),
		sessions:  session.NewEntStore(client),
		logger:    logger,
	}, client
}

// withStdin replaces os.Stdin with a pipe carrying body, so the
// --password-stdin path can be exercised without a terminal.
func withStdin(t *testing.T, body string) {
	t.Helper()

	r, w, err := osPipe()
	if err != nil {
		t.Fatalf("creating a pipe: %v", err)
	}
	original := swapStdin(r)
	t.Cleanup(func() { swapStdin(original); _ = r.Close() })

	go func() {
		_, _ = io.WriteString(w, body)
		_ = w.Close()
	}()
}

// TestBootstrapAdmin_ProducesAnAccountThatCanSignIn is the release gate's
// core claim, minus the HTTP layer: after one command on a clean database,
// an email and a password resolve to an admin identity.
//
// No JWT is minted, pasted or configured anywhere in it. That is the whole
// point: before this phase the only way into the UI was a token a fresh
// operator had no way to obtain.
func TestBootstrapAdmin_ProducesAnAccountThatCanSignIn(t *testing.T) {
	deps, client := newAdminDeps(t)
	ctx := context.Background()
	withStdin(t, testAdminPassword+"\n")

	if err := runBootstrapAdmin(ctx, deps, []string{
		"--email", "admin@example.test", "--password-stdin",
	}); err != nil {
		t.Fatalf("runBootstrapAdmin() error = %v", err)
	}

	// 1. The password verifies through the real store.
	account, err := deps.passwords.Authenticate(ctx, "admin@example.test", testAdminPassword)
	if err != nil {
		t.Fatalf("the bootstrapped password does not authenticate: %v", err)
	}
	if account.Subject != "admin@example.test" {
		t.Errorf("Subject = %q", account.Subject)
	}

	// 2. And the identity derived from stored RoleBindings is an admin.
	// This is the link that would silently break if the command wrote a
	// user without a team, or a team without a binding: the password would
	// still work and the account would reach nothing.
	builder := auth.NewIdentityBuilder(
		auth.NewEntTeamLookup(client),
		auth.NewScopeResolver(auth.NewEntRoleBindingRepository(client)),
	)
	identity, err := builder.Build(ctx, account.Subject)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if identity.Role != auth.RoleAdmin {
		t.Fatalf("Role = %q, want admin; the bootstrap wrote an account that can sign in and do nothing",
			identity.Role)
	}
	if !identity.HasScope(auth.ScopeAccessWrite) {
		t.Error("the bootstrapped administrator cannot administer access")
	}
}

// TestBootstrapAdmin_StoresNoPlaintext checks the column, not the API.
func TestBootstrapAdmin_StoresNoPlaintext(t *testing.T) {
	deps, client := newAdminDeps(t)
	ctx := context.Background()
	withStdin(t, testAdminPassword+"\n")

	if err := runBootstrapAdmin(ctx, deps, []string{
		"--email", "admin@example.test", "--password-stdin",
	}); err != nil {
		t.Fatalf("runBootstrapAdmin() error = %v", err)
	}

	cred, err := client.LocalCredential.Query().Only(ctx)
	if err != nil {
		t.Fatalf("reading the credential row: %v", err)
	}
	if strings.Contains(cred.PasswordHash, testAdminPassword) {
		t.Fatal("the stored hash contains the plaintext password")
	}
	if !strings.HasPrefix(cred.PasswordHash, "$argon2id$v=19$") {
		t.Errorf("stored hash = %q, want a PHC-encoded Argon2id string", cred.PasswordHash)
	}
}

// TestBootstrapAdmin_IsIdempotentExceptForThePassword covers re-running a
// provisioning script.
//
// The identity chain is created if missing and left alone if present; the
// PASSWORD is refused rather than replaced, because overwriting a working
// credential because somebody re-ran a script is a lockout with a
// helpful-sounding cause.
func TestBootstrapAdmin_IsIdempotentExceptForThePassword(t *testing.T) {
	deps, client := newAdminDeps(t)
	ctx := context.Background()

	withStdin(t, testAdminPassword+"\n")
	if err := runBootstrapAdmin(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	withStdin(t, "a-completely-different-password\n")
	err := runBootstrapAdmin(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"})
	if err == nil {
		t.Fatal("a second bootstrap silently replaced the existing password")
	}
	if !strings.Contains(err.Error(), "already has a password") {
		t.Errorf("error = %v, want a refusal naming the existing password", err)
	}

	// The original password still works, and nothing was duplicated.
	if _, err := deps.passwords.Authenticate(ctx, "admin@example.test", testAdminPassword); err != nil {
		t.Errorf("the original password stopped working after a refused re-run: %v", err)
	}
	assertCount(t, ctx, client.User.Query().Count, 1, "user")
	assertCount(t, ctx, client.Team.Query().Count, 1, "team")
	assertCount(t, ctx, client.RoleBinding.Query().Count, 1, "role binding")
	assertCount(t, ctx, client.Organization.Query().Count, 1, "organization")
}

// TestBootstrapAdmin_ForceReplacesThePassword is the other half.
func TestBootstrapAdmin_ForceReplacesThePassword(t *testing.T) {
	deps, _ := newAdminDeps(t)
	ctx := context.Background()

	withStdin(t, testAdminPassword+"\n")
	if err := runBootstrapAdmin(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	const replacement = "the-replacement-password"
	withStdin(t, replacement+"\n")
	if err := runBootstrapAdmin(ctx, deps, []string{
		"--email", "admin@example.test", "--password-stdin", "--force",
	}); err != nil {
		t.Fatalf("forced run: %v", err)
	}

	if _, err := deps.passwords.Authenticate(ctx, "admin@example.test", replacement); err != nil {
		t.Errorf("the replacement password does not work: %v", err)
	}
	if _, err := deps.passwords.Authenticate(ctx, "admin@example.test", testAdminPassword); err == nil {
		t.Error("the previous password still works after --force")
	}
}

// TestResetPassword_RevokesEverySession is the property that makes a reset
// mean something.
//
// A reset happens because a password is believed compromised or was
// forgotten. In the first case, leaving live sessions alive means the
// attacker keeps their access for up to the absolute deadline while the
// owner believes they have just locked them out.
func TestResetPassword_RevokesEverySession(t *testing.T) {
	deps, client := newAdminDeps(t)
	ctx := context.Background()

	withStdin(t, testAdminPassword+"\n")
	if err := runBootstrapAdmin(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Two live sessions, as if the operator were signed in on two machines.
	sessions := session.NewEntStore(client)
	identity := &auth.Identity{Subject: "admin@example.test", Role: auth.RoleAdmin}
	first, err := sessions.Create(ctx, identity, session.DefaultIdleTimeout, session.DefaultAbsoluteTimeout)
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}
	second, err := sessions.Create(ctx, identity, session.DefaultIdleTimeout, session.DefaultAbsoluteTimeout)
	if err != nil {
		t.Fatalf("creating a session: %v", err)
	}

	withStdin(t, "the-reset-password\n")
	if err := runResetPassword(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"}); err != nil {
		t.Fatalf("runResetPassword() error = %v", err)
	}

	for name, token := range map[string]string{"first": first, "second": second} {
		if _, err := sessions.Resolve(ctx, token); err == nil {
			t.Errorf("the %s session survived a password reset", name)
		}
	}
}

// TestResetPassword_MarksThePasswordAsNeedingAChange covers the difference
// between a password an administrator chose and one its owner did.
func TestResetPassword_MarksThePasswordAsNeedingAChange(t *testing.T) {
	deps, _ := newAdminDeps(t)
	ctx := context.Background()

	withStdin(t, testAdminPassword+"\n")
	if err := runBootstrapAdmin(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// Bootstrap is typed by the account's own owner, so nothing to hand on.
	account, err := deps.passwords.Account(ctx, "admin@example.test")
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if account.MustChange {
		t.Error("a bootstrapped password is marked must-change; its owner chose it")
	}

	withStdin(t, "the-reset-password\n")
	if err := runResetPassword(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"}); err != nil {
		t.Fatalf("runResetPassword() error = %v", err)
	}
	account, err = deps.passwords.Account(ctx, "admin@example.test")
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if !account.MustChange {
		t.Error("an administratively reset password is not marked must-change, so it silently becomes permanent")
	}
}

// TestUnlock_IsTheOutOfBandRecoveryPath covers the reason lockout is safe to
// have at all: locking out the last administrator must never require a
// second administrator who may not exist.
func TestUnlock_IsTheOutOfBandRecoveryPath(t *testing.T) {
	deps, client := newAdminDeps(t)
	ctx := context.Background()

	withStdin(t, testAdminPassword+"\n")
	if err := runBootstrapAdmin(ctx, deps, []string{"--email", "admin@example.test", "--password-stdin"}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Lock the account through the real store, using a policy a test can
	// reach without ten derivations.
	locking := localauth.NewEntStoreWithPolicy(client, deps.logger,
		localauth.LockoutPolicy{MaxAttempts: 2, Duration: localauth.DefaultLockoutPolicy.Duration})
	for range 2 {
		_, _ = locking.Authenticate(ctx, "admin@example.test", "wrong")
	}
	if _, err := deps.passwords.Authenticate(ctx, "admin@example.test", testAdminPassword); err == nil {
		t.Fatal("the account did not lock, so this test proves nothing")
	}

	if err := runUnlock(ctx, deps, []string{"--email", "admin@example.test"}); err != nil {
		t.Fatalf("runUnlock() error = %v", err)
	}
	if _, err := deps.passwords.Authenticate(ctx, "admin@example.test", testAdminPassword); err != nil {
		t.Errorf("the correct password still fails after unlock: %v", err)
	}
}

// TestAdminCommands_RefuseAMissingEmail covers the shared flag contract.
func TestAdminCommands_RefuseAMissingEmail(t *testing.T) {
	deps, _ := newAdminDeps(t)
	ctx := context.Background()

	for name, run := range adminCommands {
		t.Run(name, func(t *testing.T) {
			if err := run(ctx, deps, nil); err == nil {
				t.Errorf("%s with no --email returned nil", name)
			}
		})
	}
}

// TestResetPassword_DistinguishesAnUnknownAccount is the deliberate
// counterpart to the login path's single error value.
//
// An operator running a reset needs to know they typed the address wrong.
// An unauthenticated caller at a login form does not, which is why
// localauth.Authenticate refuses to make that distinction and these
// commands do.
func TestResetPassword_DistinguishesAnUnknownAccount(t *testing.T) {
	deps, _ := newAdminDeps(t)

	err := runResetPassword(context.Background(), deps, []string{"--email", "nobody@example.test"})
	if err == nil {
		t.Fatal("resetting an unknown account returned nil")
	}
	if !strings.Contains(err.Error(), "no local password") {
		t.Errorf("error = %v, want one naming the missing account", err)
	}
}

// TestIsAdminCommand keeps the main() guard honest: a flag intended for the
// server must not be mistaken for a subcommand.
func TestIsAdminCommand(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"bootstrap-admin"}, true},
		{[]string{"reset-password", "--email", "x@y.z"}, true},
		{[]string{"unlock"}, true},
		{[]string{"-help"}, false},
		{[]string{"--version"}, false},
	}

	for _, tt := range tests {
		if got := isAdminCommand(tt.args); got != tt.want {
			t.Errorf("isAdminCommand(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func assertCount(t *testing.T, ctx context.Context, count func(context.Context) (int, error), want int, what string) {
	t.Helper()
	got, err := count(ctx)
	if err != nil {
		t.Fatalf("counting %ss: %v", what, err)
	}
	if got != want {
		t.Errorf("%s count = %d, want %d; a re-run duplicated it", what, got, want)
	}
}

// osPipe and swapStdin isolate the two lines of global-state manipulation
// the --password-stdin tests need, so the rest of the file reads as
// ordinary test code.
//
// Replacing os.Stdin is genuinely global, so these tests cannot run in
// parallel with each other. That is stated here rather than discovered by a
// flake: none of them calls t.Parallel().
func osPipe() (*os.File, *os.File, error) { return os.Pipe() }

func swapStdin(f *os.File) *os.File {
	previous := os.Stdin
	os.Stdin = f
	return previous
}
