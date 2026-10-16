// db.pg.dump: the device's pg_dump into a new archive on the device.
package pg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// dump's own parameters and returns.
const (
	paramDest       = "dest"
	paramSchemaOnly = "schema_only"

	statDest          = "dest"
	statSize          = "size"
	statSHA256        = "sha256"
	statServerVersion = "server_version"
	statToolVersion   = "tool_version"
)

// A tool run's timeout.
const (
	defaultToolTimeout = time.Hour
	maxToolTimeout     = 24 * time.Hour
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnDump,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo removes the archive it created; it never replaces one, so nothing else is lost.",
				Inverses:   []sdk.InverseSpec{{FQCN: "file.remove", Record: []string{"path"}}},
			},
			Doc: dumpDoc(),
		},
		Invoke: Dump,
		Check:  CheckDump,
	})
}

// Dump implements db.pg.dump.
func Dump(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dump(ctx, rc, device, params, collection.ModeExecute)
}

// CheckDump is db.pg.dump's check: every refusal a run makes, and nothing
// written.
func CheckDump(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return dump(ctx, rc, device, params, collection.ModeCheck)
}

// dump is Dump's and CheckDump's one body.
func dump(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnDump, err)
	}
	database, err := databaseName(params, paramDatabase)
	if err != nil {
		return fail(err)
	}
	dest, err := sdk.RequiredStringParam(params, paramDest)
	if err != nil {
		return fail(err)
	}
	if !path.IsAbs(dest) || path.Clean(dest) != dest || strings.ContainsAny(dest, "\x00\r\n") {
		return fail(fmt.Errorf("%s: %q is not an absolute, clean path", paramDest, dest))
	}
	timeout, err := durationParam(params, paramTimeout, defaultToolTimeout, maxToolTimeout)
	if err != nil {
		return fail(err)
	}
	schemaOnly, err := sdk.BoolParamOr(params, paramSchemaOnly, false)
	if err != nil {
		return fail(err)
	}
	conn, err := connectionFor(rc, params, paramConnection)
	if err == nil {
		err = toolsCanReach(conn)
	}
	if err != nil {
		return fail(err)
	}
	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnDump)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()

	if err := destFree(ctx, ssh, dest); err != nil {
		return fail(err)
	}
	if err := requireTimeout(ctx, ssh); err != nil {
		return fail(err)
	}
	s, err := open(ctx, ssh, conn, database)
	if err != nil {
		return fail(err)
	}
	_, serverMajor, err := s.ServerVersion(ctx)
	_ = s.Close()
	if err != nil {
		return fail(err)
	}
	toolMaj, toolVersion, err := toolMajor(ctx, ssh, "pg_dump")
	if err != nil {
		return fail(err)
	}
	if toolMaj < serverMajor {
		return fail(fmt.Errorf("the device's pg_dump is %d and the server is %d; pg_dump must be at least the server's major version", toolMaj, serverMajor))
	}
	for key, value := range map[string]any{statDest: dest, statServerVersion: serverMajor, statToolVersion: toolVersion} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, err
		}
	}
	warnings := transportWarnings(conn)
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordWarnings(rc, warnings)
	}

	// The archive is written beside its name and linked to it, which fails
	// rather than replaces if something took the name meanwhile, so a
	// failed or racing dump never leaves a partial archive under the name
	// a restore would read.
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return fail(err)
	}
	partial := dest + ".pleiades-partial-" + hex.EncodeToString(suffix)
	command := []string{"pg_dump", "--format=custom", "--no-password", "--dbname=" + conninfoDatabase(database), "--file=" + partial}
	if schemaOnly {
		command = append(command, "--schema-only")
	}
	if _, err := (toolRun{
		conn: conn, database: database, command: command, timeout: timeout,
		after:   "ln -- " + remoteexec.QuoteArg(partial) + " " + remoteexec.QuoteArg(dest),
		cleanup: "rm -f -- " + remoteexec.QuoteArg(partial),
	}).run(ctx, ssh); err != nil {
		return fail(err)
	}
	// The archive is under its name now. The engine drops whatever a
	// failed task recorded, its undo included, so from here nothing fails
	// the task: the undo is recorded first, and a read back that fails is
	// a warning.
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:        "file.remove",
		Params:      map[string]any{"path": dest},
		Description: "Remove the archive this task wrote.",
	}); err != nil {
		return collection.Result{}, err
	}
	size, sum, err := readBack(ctx, ssh, dest)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("the archive was written to %s, but reading back its size and SHA-256 failed: %v", dest, err))
		return collection.Result{Changed: true}, recordWarnings(rc, warnings)
	}
	if err := rc.SetStat(statSize, size); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat(statSHA256, sum); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, recordWarnings(rc, warnings)
}

// readBack is the written archive's size and SHA-256; a variable so a test
// can make it fail after the archive is in place.
var readBack = func(ctx context.Context, ssh *remoteexec.Conn, dest string) (int64, string, error) {
	info, err := remotefile.Stat(ctx, ssh, dest)
	if err != nil {
		return 0, "", err
	}
	sum, _, err := remotefile.Checksum(ctx, ssh, dest)
	if err != nil {
		return 0, "", err
	}
	return info.Size, sum, nil
}

// destFree refuses a destination that exists, or whose directory does not.
func destFree(ctx context.Context, ssh *remoteexec.Conn, dest string) error {
	info, err := remotefile.Stat(ctx, ssh, dest)
	if err != nil {
		return err
	}
	if info.Exists() {
		return fmt.Errorf("%s exists; a dump never replaces an archive, so name a new one", dest)
	}
	dir, err := remotefile.Stat(ctx, ssh, path.Dir(dest))
	if err != nil {
		return err
	}
	if dir.Kind != remotefile.KindDirectory {
		return fmt.Errorf("%s is not a directory on the device", path.Dir(dest))
	}
	return nil
}
