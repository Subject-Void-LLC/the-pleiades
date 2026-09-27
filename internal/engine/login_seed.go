// Seeding a machine a method creates with an inventory device's stored
// login (collection.Manifest.SeedsLogin), without the method ever holding
// that device's private key or password.
package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/shacrypt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// LoginSeeder resolves the device named device into the secrets a seeding
// method receives: wire.SecretSeedUsername, SecretSeedAuthorizedKey and
// SecretSeedPasswordHash.
type LoginSeeder func(ctx context.Context, device string) (map[string]string, error)

// WithLoginSeeder installs seed as how a collectionActionExecutor
// resolves a seeding method's login. Without it such a method is refused.
func WithLoginSeeder(seed LoginSeeder) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.seed = seed
	}
}

// NewCredentialLoginSeeder returns a LoginSeeder reading store, the same
// vault a run's device credentials come from.
//
// The credential must hold a key, since the machine is reached by it; a
// password is optional, and becomes a SHA-512 crypt hash under a fresh
// salt, so no two seeds of it are alike and the password itself never
// leaves the vault. The key's public half is labelled user@device.
func NewCredentialLoginSeeder(store credential.Store) LoginSeeder {
	return func(ctx context.Context, device string) (map[string]string, error) {
		if store == nil {
			return nil, fmt.Errorf("no credential store to seed %q's login from", device)
		}
		cred, err := store.Lookup(ctx, device)
		if errors.Is(err, credential.ErrNotFound) {
			return nil, fmt.Errorf("device %q has no stored credential to seed; run pleiades add-credential %s --username <user> --generate first", device, device)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve credential for device %q: %w", device, err)
		}
		if cred.Username == "" || len(cred.PrivateKeyPEM) == 0 {
			return nil, fmt.Errorf("device %q's credential needs a username and a key to seed a machine with, since the machine is reached by the key", device)
		}
		public, err := credential.PublicKey(cred, cred.Username+"@"+device)
		if err != nil {
			return nil, fmt.Errorf("device %q: %w", device, err)
		}
		seed := map[string]string{
			wire.SecretSeedUsername:      cred.Username,
			wire.SecretSeedAuthorizedKey: public,
		}
		if cred.Password != "" {
			hash, err := shacrypt.Hash(cred.Password)
			if err != nil {
				return nil, fmt.Errorf("device %q: %w", device, err)
			}
			seed[wire.SecretSeedPasswordHash] = hash
		}
		return seed, nil
	}
}

// seedLogin adds the login a seeding method's parameters name to rc. It
// refuses when this executor has no seeder, when the method would run
// in another process (which the seed cannot yet cross to), and when the
// parameter names no device.
func (e *collectionActionExecutor) seedLogin(ctx context.Context, fqcn, param string, params map[string]interface{}) (map[string]string, error) {
	if e.seed == nil || e.invoke != nil {
		return nil, fmt.Errorf("collection method %q seeds a login into the machine it creates, which only the pleiades CLI resolves so far", fqcn)
	}
	device, _ := params[param].(string)
	if device == "" {
		return nil, fmt.Errorf("collection method %q: %s must name the inventory device whose login the machine gets", fqcn, param)
	}
	seed, err := e.seed(ctx, device)
	if err != nil {
		return nil, fmt.Errorf("collection method %q: %w", fqcn, err)
	}
	return seed, nil
}
