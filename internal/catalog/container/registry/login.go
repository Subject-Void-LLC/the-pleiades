// container.registry.login: the bound registry credential, proved against
// the registry through the engine, then kept in the engine's login file for
// the engine's own later pulls.
package registry

import (
	"context"
	"fmt"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnLogin = "container.registry.login"

func init() {
	register(fqcnLogin, containerengine.MethodManifest(collection.Reversibility{
		Reversible: true,
		Notes:      "A run that added the registry's login records removing it; one that replaced or found a login records nothing, since the password it replaced is not kept.",
		Inverses:   []sdk.InverseSpec{{FQCN: fqcnLogout, Record: []string{paramRegistry, paramAuthFile, paramEngine}}},
	}, loginDoc()), login)
}

// login is container.registry.login's one body.
func login(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnLogin)
	host, err := registryHost(params)
	if err != nil {
		return fail(err)
	}
	creds, ok := rc.(sdk.RegistryCredentials)
	if !ok {
		return fail(fmt.Errorf("this run holds no registry credentials; bind a registry credential for %s", host))
	}
	user, password, ok := creds.RegistryCredential(host)
	if !ok || user == "" || password == "" {
		return fail(fmt.Errorf("no registry credential is bound for %s", host))
	}
	auth := encodeAuth(user, password)
	header, err := containerengine.EncodeRegistryAuth(user, password, host)
	if err != nil {
		return fail(err)
	}
	// Every form the password could be echoed in is masked before any
	// request is made with it.
	sdk.RegisterSecret(rc, password, auth, header)

	s, err := containerengine.Open(ctx, rc, device, params, fqcnLogin, containerengine.OpRegistryAuth)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	// The registry is asked whether the account works before anything is
	// written, in a check as in a run: an account it refuses is never
	// kept where every later pull would send it.
	if _, err := s.API.RegistryLogin(ctx, client.RegistryLoginOptions{Username: user, Password: password, ServerAddress: host}); err != nil {
		return fail(fmt.Errorf("the registry %s refused the account %s: %w", host, user, err))
	}

	l, err := openLoginFile(ctx, rc, device, params, fqcnLogin)
	if err != nil {
		return collection.Result{}, err
	}
	defer l.files.Close()
	key := configKey(l.kind, host)
	if helper := l.file.helperFor(key); helper != "" {
		return fail(fmt.Errorf("%s sends %s's login to the credential helper %q, which the engine reads instead of the file, and this method does not write to a helper", l.path, host, helper))
	}
	if err := recordStats(rc, map[string]any{statRegistry: host, statUsername: user, statAuthFile: l.path}); err != nil {
		return collection.Result{}, err
	}
	// Every run says so, changed or not: the password stays on the device.
	if err := sdk.RecordWarnings(rc, []string{fmt.Sprintf(
		"the password for %s is stored in %s on %s, readable by its owner and by root; container.registry.logout removes it",
		host, l.path, device.Name())}); err != nil {
		return collection.Result{}, err
	}
	prior, had := l.file.entry(key)
	if had && prior == auth {
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	l.file.set(key, auth)
	if err := l.write(ctx); err != nil {
		return fail(err)
	}
	if had {
		return collection.Result{Changed: true}, nil
	}
	undo := map[string]any{paramRegistry: host, paramAuthFile: l.path}
	return collection.Result{Changed: true}, containerengine.RecordUndo(rc, s, sdk.Inverse{
		FQCN: fqcnLogout, Params: undo, Description: "Remove the registry login this task added.",
	})
}
