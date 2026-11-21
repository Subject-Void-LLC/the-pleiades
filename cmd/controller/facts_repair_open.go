// This file is how `controller facts` (facts_repair.go) reaches its keys and
// its database: the facts keys from the environment, at least one of the
// two; a database that already exists and is where this build leaves it,
// opened without OpenDatabase, which would create a missing SQLite file,
// migrate an empty one and run any reclamation owed; and a master key the
// database's key registry records, since a wrong one makes every stored
// fact read as not opening, which is not a device's fault. What the registry
// says of the other master key settings (a version, a previous key, a newer
// key) is a warning: a row that does not open is held back from the repairs
// by the store itself, however the keys are set.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/encryptionkey"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// loadFactsCommandKeys reads the facts keys the command checks with: the
// current, the previous, or both, at least one. A key that does not decode,
// or a previous key that is the current one, is refused by name, as the
// server refuses it.
func loadFactsCommandKeys() (*facts.Keys, *facts.Keys, error) {
	var currentSecret []byte
	var current, previous *facts.Keys
	if os.Getenv(factDigestKeyEnv) != "" {
		secret, err := decodeEnvelopeKey(factDigestKeyEnv)
		if err != nil {
			return nil, nil, err
		}
		if current, err = entstore.AtRestKeys(secret); err != nil {
			return nil, nil, err
		}
		currentSecret = secret
	}
	previousSecret, err := previousFactSecret(currentSecret)
	if err != nil {
		return nil, nil, err
	}
	if previousSecret != nil {
		if previous, err = entstore.AtRestKeys(previousSecret); err != nil {
			return nil, nil, err
		}
	}
	if current == nil && previous == nil {
		return nil, nil, fmt.Errorf("set %s, %s, or both: stored facts are checked under the facts keys", factDigestKeyEnv, factDigestKeyPreviousEnv)
	}
	return current, previous, nil
}

// openFactStore opens the database without changing it and returns the
// fact store the command works through, holding current and previous, and
// its cleanup. A warning it has for the operator goes to stderr.
func openFactStore(ctx context.Context, current, previous *facts.Keys, stderr io.Writer) (*entstore.Store, func(), error) {
	dsn, err := resolveDatabaseDSN()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve database configuration: %w", err)
	}
	envelopeSvc, err := loadEnvelopeService()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init envelope encryption: %w", err)
	}
	existing, err := ent.OpenExisting(ctx, dsn)
	if errors.Is(err, ent.ErrNoDatabase) {
		return nil, nil, errors.New("the database DB_PATH (or DB_DSN) names does not exist, so it holds no facts; this command creates none: point it at the database the server runs with")
	}
	if err != nil {
		return nil, nil, err
	}
	if err := servable(ctx, existing); err != nil {
		_ = existing.Close()
		return nil, nil, err
	}
	client := existing.Client()
	// The same hooks the server installs: a device's properties read back
	// as the server reads them.
	installCryptoHooks(client, envelopeSvc)
	warnings, err := masterKeyKnown(ctx, client)
	if err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	for _, warning := range warnings {
		fmt.Fprintln(stderr, "controller facts: warning: "+termsafe.EscapeLine(warning))
	}
	return entstore.New(client, current, envelopeSvc, entstore.WithPreviousKeys(previous)), func() { _ = client.Close() }, nil
}

// servable refuses a database this build would have to create or upgrade
// before serving it, or must not use: only one where this build leaves it,
// or a newer build within this one's window, is read.
func servable(ctx context.Context, existing *ent.ExistingDatabase) error {
	plan, err := existing.SchemaPlan(ctx)
	if err != nil {
		return err
	}
	switch plan.Verdict {
	case migrate.VerdictCurrent, migrate.VerdictNewerWithinWindow:
		return nil
	case migrate.VerdictFresh:
		return fmt.Errorf("%s has no migration history: no controller has run against it, so it holds no facts, and this command migrates nothing", existing.Describe())
	case migrate.VerdictPending:
		return fmt.Errorf("%s records migrations through %s and this build ends at %s: start the controller from this build first, which upgrades it, then run this again", existing.Describe(), plan.DatabaseHead, plan.BuildHead)
	}
	return fmt.Errorf("%s cannot be used by this build: %s", existing.Describe(), plan.Refusal)
}

// masterKeyKnown refuses a MASTER_ENCRYPTION_KEY the database's key registry
// does not record: a key no controller has run this database with, under
// which every sealed fact would read as not opening, and which a truncate
// would re-key what it keeps under. A registry holding no key at all (a
// database whose controllers could never write it: recording a key never
// stops a start) has nothing to check against, so that is a warning, and the
// inspection's own state for a device no row of which opens says to check
// the key. For a key it records, it returns registryWarnings.
func masterKeyKnown(ctx context.Context, client *ent.Client) ([]string, error) {
	key, err := crypto.DecodeKey(os.Getenv("MASTER_ENCRYPTION_KEY"), "environment variable MASTER_ENCRYPTION_KEY")
	if err != nil {
		return nil, err
	}
	fingerprint := crypto.Fingerprint(key)
	record, err := keyregistry.NewEntStore(client).Get(ctx, fingerprint)
	if err == nil {
		return registryWarnings(ctx, client, record)
	}
	if !errors.Is(err, keyregistry.ErrNotRegistered) {
		return nil, fmt.Errorf("reading the database's key registry: %w", err)
	}
	recorded, err := client.EncryptionKey.Query().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the database's key registry: %w", err)
	}
	if recorded == 0 {
		return []string{"the database's key registry records no master key, so MASTER_ENCRYPTION_KEY cannot be checked against it; a device no stored fact of which opens reads as unopened"}, nil
	}
	return nil, fmt.Errorf("MASTER_ENCRYPTION_KEY (%s) is not a key this database's registry records: no controller has run it with that key, and every sealed fact would read as not opening. Run this with the server's MASTER_ENCRYPTION_KEY and MASTER_ENCRYPTION_KEY_PREVIOUS", keyregistry.Short(fingerprint))
}

// registryWarnings holds the master key settings to what the key registry
// records, current being MASTER_ENCRYPTION_KEY's record, and warns of each
// that would make some sealed facts not open: a key given as another
// version than the registry records it under (a fact names the version it
// was sealed as), a MASTER_ENCRYPTION_KEY_PREVIOUS the registry does not
// record, and a recorded key newer than any given (the environment from
// before a rotation). Each is a warning, not a refusal: the registry says
// which keys a controller ran with, not which rows still need them, and a
// row that does not open is refused as proof of damage by the repairs
// themselves (internal/facts/entstore's Unverifiable).
func registryWarnings(ctx context.Context, client *ent.Client, current keyregistry.Record) ([]string, error) {
	var warnings []string
	if given := getenv("MASTER_ENCRYPTION_KEY_VERSION", crypto.DefaultKeyVersion); given != current.Version {
		warnings = append(warnings, versionWarning("MASTER_ENCRYPTION_KEY", current, given))
	}
	given := map[string]bool{current.Fingerprint: true}
	if os.Getenv("MASTER_ENCRYPTION_KEY_PREVIOUS") != "" {
		// loadEnvelopeService has already accepted this key and its version.
		key, err := decodeEnvelopeKey("MASTER_ENCRYPTION_KEY_PREVIOUS")
		if err != nil {
			return nil, err
		}
		fingerprint, version := crypto.Fingerprint(key), os.Getenv("MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION")
		given[fingerprint] = true
		previous, err := keyregistry.NewEntStore(client).Get(ctx, fingerprint)
		switch {
		case errors.Is(err, keyregistry.ErrNotRegistered):
			warnings = append(warnings, fmt.Sprintf("MASTER_ENCRYPTION_KEY_PREVIOUS (%s) is not a key this database's registry records: no controller has run it with that key, so a fact sealed as version %s opens under neither key given", keyregistry.Short(fingerprint), termsafe.Quote(version)))
		case err != nil:
			return nil, fmt.Errorf("reading the database's key registry: %w", err)
		case previous.Version != version:
			warnings = append(warnings, versionWarning("MASTER_ENCRYPTION_KEY_PREVIOUS", previous, version))
		}
	}
	newest, err := client.EncryptionKey.Query().Order(ent.Desc(encryptionkey.FieldID)).First(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading the database's key registry: %w", err)
	}
	if !given[newest.Fingerprint] {
		warnings = append(warnings, fmt.Sprintf("the database's key registry records a master key newer than the ones given, %s (version %s): if a controller has run with it, the facts it sealed do not open here. Run this with the server's current MASTER_ENCRYPTION_KEY, and the key it replaced as MASTER_ENCRYPTION_KEY_PREVIOUS while any row still needs it",
			keyregistry.Short(newest.Fingerprint), termsafe.Quote(newest.Version)))
	}
	return warnings, nil
}

// versionWarning says that the key env names, which the registry records as
// r, was given as version given: a fact it sealed names r's version, which
// no key given as given opens.
func versionWarning(env string, r keyregistry.Record, given string) string {
	return fmt.Sprintf("%s (%s) is recorded in the database's key registry as version %s and was given as version %s: a fact it sealed names version %s, so it does not open unless a key is given as that version",
		env, keyregistry.Short(r.Fingerprint), termsafe.Quote(r.Version), termsafe.Quote(given), termsafe.Quote(r.Version))
}
