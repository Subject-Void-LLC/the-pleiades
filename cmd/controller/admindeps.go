// Wiring for the administrative subcommands.
//
// Kept apart from admin.go so the commands themselves read as what they do
// rather than as how they are assembled, and kept apart from main.go
// because this deliberately builds a much smaller stack than the server:
// a database client and three stores, with no NATS connection, no HTTP
// listener, no leader election and no telemetry exporter. A command that
// cannot reach the message bus cannot dispatch a job by accident, and one
// that never listens cannot be reached from the network at all.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/access"
	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/localauth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
)

// openAdminDeps opens the database and builds the stores a subcommand
// needs, returning a cleanup the caller defers. The database and the key
// both come from the environment, exactly as the server reads them.
func openAdminDeps(ctx context.Context) (*adminDeps, func(), error) {
	dsn, err := resolveDatabaseDSN()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve database configuration: %w", err)
	}
	// Loaded before the database is opened, because opening it migrates it:
	// a command run without a usable key stops having changed nothing.
	envelopeSvc, err := loadEnvelopeService()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init envelope encryption: %w", err)
	}
	return openAdminDepsWith(ctx, dsn, envelopeSvc)
}

// openAdminDepsWith is openAdminDeps with the database and the envelope
// service supplied rather than read from the environment.
//
// The setup command is the caller that needs it: it has just generated a
// master key and written it to a file, and has to open the database under
// that key to record it and to create the first administrator. Handing it
// the key through the environment instead would put the key where every
// child process inherits it and /proc publishes it, which is the one place
// this command is built never to put a secret.
func openAdminDepsWith(ctx context.Context, dsn string, envelopeSvc *crypto.EnvelopeService) (*adminDeps, func(), error) {
	// The masking ruleset is installed on this path too. An admin command
	// logs a subject and an outcome and never a password, but
	// internal/archtest's TestEverySlogHandlerCarriesTheMaskingRuleset
	// exists because "this particular writer never logs a secret" is a
	// claim that stops being true the first time somebody adds a field.
	logger := slog.New(slog.NewTextHandler(os.Stderr, redact.Shared().HandlerOptions(slog.LevelWarn)))

	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open the controller database: %w", err)
	}

	// The same envelope hooks the server installs, through the same
	// function, so the two paths cannot drift into encrypting an entity on
	// one and storing it in plaintext on the other. None of the entities
	// these commands touch is encrypted today, which is exactly why this
	// would be easy to omit and expensive to have omitted later.
	installCryptoHooks(client, envelopeSvc)

	// Through the AUDITED store, not the bare one. A break-glass path that
	// creates an administrator invisibly is the path used to create a
	// second one nobody notices, so a bootstrap leaves the same activity
	// trail an administrative change through the API would.
	//
	// The actor is the host rather than an HTTP request, and it is recorded
	// as such: somebody with shell access to the controller, which is a
	// materially different and stronger claim than any web session.
	activityStream := activity.NewEntStore(client)
	accessStore := access.NewAuditedStore(
		access.NewEntStore(client),
		activityStream,
		func(context.Context) string { return "controller-cli" },
		logger,
	)

	deps := &adminDeps{
		client: client,
		access: accessStore,
		// Audited on this path too, through the same decorator and the same
		// actor rule. A break-glass command that changed a password
		// invisibly is exactly the one used to change a second password
		// nobody notices.
		passwords: localauth.NewAuditedStore(
			localauth.NewEntStore(client, logger), activityStream,
			func(context.Context) string { return "controller-cli" }, logger),
		sessions: session.NewEntStore(client),
		logger:   logger,
	}
	return deps, func() { _ = client.Close() }, nil
}
