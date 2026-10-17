// container.registry.logout: a registry's login removed from the engine's
// login file, through one body its check shares.
package registry

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnLogout = "container.registry.logout"

func init() {
	register(fqcnLogout, manifest(collection.Reversibility{
		Reversible: false,
		Notes:      "The removed login's password is not kept, so it cannot be written back; container.registry.login writes the bound credential again.",
	}, logoutDoc()), logout)
}

// logout is container.registry.logout's one body: an absent login is no
// change; the file's other logins and settings are kept as they were.
func logout(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := failer(fqcnLogout)
	host, err := registryHost(params)
	if err != nil {
		return fail(err)
	}
	l, err := openLoginFile(ctx, rc, device, params, fqcnLogout)
	if err != nil {
		return collection.Result{}, err
	}
	defer l.files.Close()
	key := configKey(l.kind, host)
	if err := recordStats(rc, map[string]any{statRegistry: host, statAuthFile: l.path}); err != nil {
		return collection.Result{}, err
	}
	if _, had := l.file.entry(key); !had {
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}
	l.file.remove(key)
	if err := l.write(ctx); err != nil {
		return fail(err)
	}
	return collection.Result{Changed: true}, nil
}
