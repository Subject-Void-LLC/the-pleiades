// Package registry implements the container.registry.* methods (Phase
// 119e): a registry login kept on the machine a device's engine runs on,
// in the file that engine's own tools and later pulls read, Docker's
// config.json or Podman's auth.json.
//
// The password comes only from the registry credential bound to the run
// (sdk.RegistryCredentials), never from a task parameter, and reaches no
// output, stat, error or journal entry: the methods record the registry,
// the account name and the file, and register the password and its
// encodings with the run's masking set before anything can echo them.
package registry

import (
	"context"
	"fmt"
	"regexp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// Parameters and returns shared by the namespace's methods.
const (
	paramRegistry = "registry"
	paramAuthFile = "auth_file"
	paramEngine   = containerengine.ParamEngine

	statRegistry   = "registry"
	statUsername   = "username"
	statAuthFile   = "auth_file"
	statLoggedIn   = "logged_in"
	statRegistries = "registries"

	// maxAuthFile bounds the login file this reads.
	maxAuthFile = 1 << 20
)

// hostPattern is a registry host as an image reference names one: a
// lower-case DNS name or address, with an optional port, and no path.
var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

// registryHost reads the registry parameter, normalized as the engines
// normalize it ("index.docker.io" is "docker.io").
func registryHost(params map[string]any) (string, error) {
	raw, err := sdk.RequiredStringParam(params, paramRegistry)
	if err != nil {
		return "", err
	}
	host := wire.RegistryHost(raw)
	if !hostPattern.MatchString(host) {
		return "", fmt.Errorf("%s: %q is not a registry host (a name or address with an optional port, and no path)", paramRegistry, raw)
	}
	return host, nil
}

// body is a method's one body, run with the mode it is called in.
type body func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any, mode collection.Mode) (collection.Result, error)

// register registers fqcn with one body serving its run and its check.
func register(fqcn string, m collection.Manifest, b body) {
	collection.MustRegister(collection.Descriptor{
		Name:     fqcn,
		Manifest: m,
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return b(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return b(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// loginFile is a login file opened for one task.
type loginFile struct {
	files *containerengine.HostFiles
	kind  capability.EngineKind
	// path is the file's absolute path on the engine's machine.
	path string
	file *authFile
}

// openLoginFile reaches the machine device's engine runs on and reads the
// login file the engine uses: the auth_file parameter, or the engine's own
// default under the login's home (Docker's ~/.docker/config.json, Podman's
// ~/.config/containers/auth.json, which Podman reads after its runtime
// file). A file that is absent reads as empty. The caller closes files.
func openLoginFile(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*loginFile, error) {
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: a container method addresses a specific container host, set the task's target", fqcn)
	}
	kind, err := containerengine.ChooseEngine(device, params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	p, err := containerengine.TextParam(params, paramAuthFile)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	if p != "" {
		if err := containerengine.ValidHostPath(paramAuthFile, p); err != nil {
			return nil, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	files, err := containerengine.OpenHostFiles(ctx, rc, device, params, fqcn)
	if err != nil {
		return nil, err
	}
	if p == "" {
		home, err := files.Home(ctx)
		if err != nil {
			files.Close()
			return nil, fmt.Errorf("%s: %w", fqcn, err)
		}
		p = home + "/.docker/config.json"
		if kind == capability.EnginePodman {
			p = home + "/.config/containers/auth.json"
		}
	}
	data, _, err := files.ReadSmall(ctx, p, maxAuthFile)
	if err != nil {
		files.Close()
		return nil, fmt.Errorf("%s: reading %s: %w", fqcn, p, err)
	}
	file, err := parseAuthFile(data)
	if err != nil {
		files.Close()
		return nil, fmt.Errorf("%s: reading %s: %w", fqcn, p, err)
	}
	return &loginFile{files: files, kind: kind, path: p, file: file}, nil
}

// write writes the login file back, mode 0600.
func (l *loginFile) write(ctx context.Context) error {
	data, err := l.file.bytes()
	if err != nil {
		return fmt.Errorf("encoding %s: %w", l.path, err)
	}
	return l.files.WritePrivate(ctx, l.path, data)
}

// recordStats records each stat, stopping at the first failure.
func recordStats(rc sdk.RunbookContext, stats map[string]any) error {
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return err
		}
	}
	return nil
}

// failer prefixes a method's errors with its name.
func failer(fqcn string) func(error) (collection.Result, error) {
	return func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
}
