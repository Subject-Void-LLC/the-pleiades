// container.registry.info: which registries the engine's login file holds
// a login for, and as whom; its check is its run.
package registry

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnInfo = "container.registry.info"

func init() {
	register(fqcnInfo, containerengine.MethodManifest(collection.Reversibility{
		ReadOnly: true,
		Notes:    "It reads the login file and changes nothing.",
	}, infoDoc()), info)
}

// info is container.registry.info's body: with registry, whether that one
// has a login and as whom; without, every registry the file holds. A
// password is never read out of the file.
func info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, _ collection.Mode) (collection.Result, error) {
	fail := failer(fqcnInfo)
	host := ""
	if sdk.StringParam(params, paramRegistry) != "" {
		h, err := registryHost(params)
		if err != nil {
			return fail(err)
		}
		host = h
	}
	l, err := openLoginFile(ctx, rc, device, params, fqcnInfo)
	if err != nil {
		return collection.Result{}, err
	}
	defer l.files.Close()
	registries := []map[string]any{}
	for _, key := range l.file.keys() {
		auth, _ := l.file.entry(key)
		registries = append(registries, map[string]any{statRegistry: key, statUsername: username(auth)})
	}
	stats := map[string]any{statAuthFile: l.path, statRegistries: registries}
	if host != "" {
		auth, had := l.file.entry(configKey(l.kind, host))
		stats[statRegistry] = host
		stats[statLoggedIn] = had
		stats[statUsername] = username(auth)
	}
	return collection.Result{}, recordStats(rc, stats)
}
