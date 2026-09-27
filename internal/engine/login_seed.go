// Seeding a machine a method creates with an inventory device's stored
// login (collection.Manifest.SeedsLogin), handing the method no more of
// that device's secret than the machine needs to admit it.
package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/shacrypt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// LoginSeeder resolves the device named device into the secrets a seeding
// method receives. password says the method sets SeedsLoginPassword, so
// a device reached over WinRM may be seeded, with its password itself.
type LoginSeeder func(ctx context.Context, device string, password bool) (map[string]string, error)

// DeviceFinder finds an inventory device by its name.
type DeviceFinder interface {
	GetByName(ctx context.Context, name string) (inventory.InventoryItem, error)
}

// WithLoginSeeder installs seed as how a collectionActionExecutor
// resolves a seeding method's login. Without it such a method is refused.
func WithLoginSeeder(seed LoginSeeder) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.seed = seed
	}
}

// NewCredentialLoginSeeder returns a LoginSeeder reading store, the same
// vault a run's device credentials come from, for devices found in
// devices.
//
// What a machine needs depends on how the device standing for it is
// reached. One reached over SSH is admitted by its key: the credential
// must hold one, the method gets its public half labelled user@device,
// and a password, if there is one, becomes a SHA-512 crypt hash under a
// fresh salt, so the password itself never leaves the vault. One reached
// over WinRM (a Windows machine) is admitted by its password, and a
// Windows answer file can hold nothing else, so the method gets the
// password itself; only a method that says it can seed one gets that far.
func NewCredentialLoginSeeder(store credential.Store, devices DeviceFinder) LoginSeeder {
	return func(ctx context.Context, device string, password bool) (map[string]string, error) {
		if store == nil || devices == nil {
			return nil, fmt.Errorf("no credential store or inventory to seed %q's login from", device)
		}
		item, err := devices.GetByName(ctx, device)
		if err != nil {
			return nil, fmt.Errorf("the device to seed a login from: %w", err)
		}
		cred, err := store.Lookup(ctx, device)
		if errors.Is(err, credential.ErrNotFound) {
			return nil, fmt.Errorf("device %q has no stored credential to seed; run pleiades add-credential %s --username <user> --generate first", device, device)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve credential for device %q: %w", device, err)
		}
		if item.HasCapability(capability.NameWinRM) {
			return passwordSeed(device, cred, password)
		}
		return keySeed(device, cred)
	}
}

// passwordSeed is what a method seeding a machine reached over WinRM
// receives: the username and the password.
func passwordSeed(device string, cred credential.Credential, allowed bool) (map[string]string, error) {
	if !allowed {
		return nil, fmt.Errorf("device %q is reached over WinRM, by a password, and this method seeds only machines reached by a key", device)
	}
	if cred.Username == "" || cred.Password == "" {
		return nil, fmt.Errorf("device %q is reached over WinRM, so its credential needs a username and a password to seed a machine with", device)
	}
	return map[string]string{
		wire.SecretSeedUsername: cred.Username,
		wire.SecretSeedPassword: cred.Password,
	}, nil
}

// keySeed is what a method seeding a machine reached over SSH receives:
// the username, the key's public half, and a hash of the password when
// there is one.
func keySeed(device string, cred credential.Credential) (map[string]string, error) {
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

// seedLogin adds the login a seeding method's parameters name to rc. It
// refuses when this executor has no seeder, when the method would run
// in another process (which the seed cannot yet cross to), and when the
// parameter names no device.
func (e *collectionActionExecutor) seedLogin(ctx context.Context, fqcn, param string, password bool, params map[string]interface{}) (map[string]string, error) {
	if e.seed == nil || e.invoke != nil {
		return nil, fmt.Errorf("collection method %q seeds a login into the machine it creates, which only the pleiades CLI resolves so far", fqcn)
	}
	device, _ := params[param].(string)
	if device == "" {
		return nil, fmt.Errorf("collection method %q: %s must name the inventory device whose login the machine gets", fqcn, param)
	}
	seed, err := e.seed(ctx, device, password)
	if err != nil {
		return nil, fmt.Errorf("collection method %q: %w", fqcn, err)
	}
	return seed, nil
}
