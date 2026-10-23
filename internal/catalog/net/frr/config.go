// Package frr: net.frr.config, a router's whole configuration, applied
// through FRR's own reload and undone from a backup kept on the router.
package frr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// configFQCN is net.frr.config's name.
const configFQCN = "net.frr.config"

// frrConf is the file FRR starts from.
const frrConf = "/etc/frr/frr.conf"

// maxConfig bounds a configuration this method reads: a candidate from the
// project, a backup, frr.conf.
const maxConfig = 4 << 20

// backupDir is where backups go, under the connecting account's home.
const backupDir = ".pleiades-frr"

// The files beside a backup: the running configuration the change left,
// which the undo compares with what runs then, and frr.conf as it was when
// the change saved.
const (
	afterSuffix = ".after"
	savedSuffix = ".saved"
)

// backupName is a backup's file name, as this method makes it: the time
// and a random suffix, nothing a restore parameter could stretch into
// another path.
var backupName = regexp.MustCompile(`^pleiades-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}\.conf$`)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: configFQCN,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameFRR, capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				Reversible: true,
				Notes:      "Its undo restores the running configuration saved on the router before the change (and frr.conf, when the change saved), named by the backup's path alone, and refuses when the router has been changed since unless forced.",
				Inverses: []sdk.InverseSpec{
					{FQCN: configFQCN, Record: []string{"restore", "save"}, MayKeepOnFailure: true},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Makes an FRRouting router run exactly the configuration given, and saves what it ran before so the change can be undone.",
				Description: "Takes the whole intended configuration (config, or src: a file in the project) and applies only what differs, through FRR's own frr-reload.py. The candidate is first checked with vtysh -m -f, and a line vtysh refuses fails the task before anything reaches the router: frr-reload reads a file vtysh cannot parse as an empty configuration and would delete everything. The difference comes from frr-reload.py --test; an empty one is no change, and a check returns it as the prediction. A real run saves the running configuration to a file on the router (0600, under the connecting account's ~/.pleiades-frr), reloads, confirms the running configuration now matches the candidate, and saves the result beside the backup. Its undo names only that backup, so no configuration text, and no secret in one, reaches the journal; the undo restores exactly the configuration that was running, and refuses when the router no longer runs what this change left, unless its step names force. The undo is recorded before the reload and kept if the reload then fails, since restoring a backup when nothing changed is itself no change. With save, the running configuration is also written to frr.conf, keeping that file's owner and mode, so it survives a restart; the backup then keeps frr.conf as it was too, and the undo puts it back (where there was none, the undo writes the configuration it restores). expect_sha256 refuses a candidate whose bytes are not the ones a model check analysed.",
				Params: []collection.Param{
					{Name: "config", Type: "string", Description: "The whole intended configuration, as frr.conf text. Set config or src, not both. Lines not in it are removed from the running configuration."},
					{Name: "src", Type: "string", Description: "A file in the project directory holding the whole intended configuration, read by the command line; refused on a Walk-tier Runner, where config is the way. Set config or src, not both."},
					{Name: "save", Type: "bool", Default: "false", Description: "Also write the configuration to frr.conf (keeping that file's owner and mode), so it survives a restart. The undo puts frr.conf back as it was."},
					{Name: "expect_sha256", Type: "string", Description: "Refuse unless the candidate's SHA-256 is this, in hex: the digest net.batfish.check returned for the bytes it analysed, so the change applied is the change checked."},
					{Name: "restore", Type: "string", Description: "Restore the configuration this method saved before an earlier change, by the path its undo recorded. Set by an undo; with restore, config and src are not given."},
					{Name: "force", Type: "bool", Default: "false", Description: "With restore, restore even though the router no longer runs what the earlier change left (someone changed it since)."},
					{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
				},
				Returns: []collection.ReturnField{
					{Name: "lines_added", Type: "list of string", Returned: "always", Description: "The configuration lines frr-reload added, as it reported them."},
					{Name: "lines_deleted", Type: "list of string", Returned: "always", Description: "The configuration lines frr-reload removed."},
					{Name: "sha256", Type: "string", Returned: "always", Description: "The SHA-256 of the configuration applied, in hex."},
					{Name: "backup", Type: "string", Returned: "when something changed", Description: "The path of the running configuration saved before the change, on the router."},
					{Name: "inverse", Type: "dict", Returned: "when something changed", Description: "The undo: this method with restore set to backup (and save when the change saved), held whole in the journal."},
				},
				Examples: []collection.Example{
					{Name: "Apply a router's intended configuration", RunbookYAML: "- name: r1 runs its intended configuration\n  net.frr.config:\n    target: r1\n    src: configs/r1.frr.conf\n    save: true\n"},
				},
				SeeAlso: []string{"net.frr.facts", "net.frr.interface", "net.batfish.check", "wait.routes"},
			},
		},
		Invoke: Config,
		Check:  CheckConfig,
	})
}

// Config makes the router run the configuration given.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return config(ctx, rc, device, params, collection.ModeExecute)
}

// CheckConfig predicts Config: the same refusals and the same difference,
// from frr-reload's --test, with nothing applied. The candidate is written
// to a temporary file on the router for vtysh and frr-reload to read, and
// removed; the configuration is untouched.
func CheckConfig(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return config(ctx, rc, device, params, collection.ModeCheck)
}

// config is both modes' one body.
func config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	text, err := sdk.OptionalStringParam(params, "config")
	if err != nil {
		return collection.Result{}, err
	}
	src, err := sdk.OptionalStringParam(params, "src")
	if err != nil {
		return collection.Result{}, err
	}
	restore, err := sdk.OptionalStringParam(params, "restore")
	if err != nil {
		return collection.Result{}, err
	}
	expect, err := sdk.OptionalStringParam(params, "expect_sha256")
	if err != nil {
		return collection.Result{}, err
	}
	given := 0
	for _, v := range []string{text, src, restore} {
		if v != "" {
			given++
		}
	}
	if given != 1 {
		return collection.Result{}, fmt.Errorf("%s: set exactly one of config, src and restore", configFQCN)
	}
	if restore != "" && expect != "" {
		return collection.Result{}, fmt.Errorf("%s: expect_sha256 checks a candidate, and a restore has none", configFQCN)
	}
	if restore == "" && sdk.BoolParam(params, "force") {
		return collection.Result{}, fmt.Errorf("%s: force applies only to a restore", configFQCN)
	}
	if src != "" {
		files, ok := rc.(sdk.ProjectFiles)
		if !ok {
			return collection.Result{}, fmt.Errorf("%s: src: %w", configFQCN, sdk.ErrNoProjectFiles)
		}
		b, err := files.ReadProjectFile(src, maxConfig)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: src: %w", configFQCN, err)
		}
		text = string(b)
	}
	if restore == "" {
		sum := sha256.Sum256([]byte(text))
		digest := hex.EncodeToString(sum[:])
		if expect != "" && !strings.EqualFold(expect, digest) {
			return collection.Result{}, fmt.Errorf("%s: the candidate's SHA-256 is %s, not the %s expect_sha256 names: these are not the bytes that were checked, so nothing was sent", configFQCN, digest, expect)
		}
	}

	s, err := open(ctx, rc, device, params, configFQCN)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.close()
	dir, err := s.backupDir(ctx, mode)
	if err != nil {
		return collection.Result{}, err
	}
	var saved string
	var hasSaved bool
	if restore != "" {
		text, saved, hasSaved, err = s.readBackup(ctx, dir, restore, sdk.BoolParam(params, "force"))
		if err != nil {
			return collection.Result{}, err
		}
	}
	return s.apply(ctx, rc, dir, text, sdk.BoolParam(params, "save"), saved, hasSaved, mode)
}

// apply makes the running configuration text, and with save writes it to
// frr.conf: savedConf, when hasSaved, is what frr.conf gets instead (a
// restore putting the file back as it was).
func (s *session) apply(ctx context.Context, rc sdk.RunbookContext, dir, text string, save bool, savedConf string, hasSaved bool, mode collection.Mode) (collection.Result, error) {
	sum := sha256.Sum256([]byte(text))
	if err := rc.SetStat("sha256", hex.EncodeToString(sum[:])); err != nil {
		return collection.Result{}, err
	}
	candidate, cleanup, err := s.writeTemp(ctx, text)
	if err != nil {
		return collection.Result{}, err
	}
	defer cleanup()
	if err := s.check(ctx, candidate); err != nil {
		return collection.Result{}, err
	}
	deleted, added, err := s.diff(ctx, candidate)
	if err != nil {
		return collection.Result{}, err
	}
	if err := setLines(rc, deleted, added); err != nil {
		return collection.Result{}, err
	}
	running, err := s.runningConfig(ctx)
	if err != nil {
		return collection.Result{}, err
	}
	runChange := len(deleted)+len(added) > 0

	confBefore, err := remotefile.Stat(ctx, s.conn, frrConf)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: reading %s: %w", configFQCN, frrConf, err)
	}
	if save && confBefore.Exists() && confBefore.Kind != remotefile.KindFile {
		return collection.Result{}, fmt.Errorf("%s: %s is a %s, not a file, so it is not saved over", configFQCN, frrConf, confBefore.Kind)
	}
	var confText string
	if save && confBefore.Exists() {
		b, _, err := remotefile.ReadText(ctx, s.conn, frrConf, maxConfig)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: reading %s: %w", configFQCN, frrConf, err)
		}
		confText = string(b)
	}
	// What frr.conf would hold: the file a restore puts back, or the
	// running configuration, which before a change is only known when
	// there is none to make.
	saveChange := save && (runChange || !confBefore.Exists() || confText != wantSaved(running, savedConf, hasSaved))
	if !runChange && !saveChange {
		return collection.Result{}, nil
	}
	if mode == collection.ModeCheck {
		if saveChange && !confBefore.Exists() {
			if err := remotefile.PredictPut(ctx, s.conn, frrConf, confBefore, remotefile.Attributes{}); err != nil {
				return collection.Result{}, err
			}
		}
		return collection.Result{Changed: true}, nil
	}

	backup, err := s.writeBackup(ctx, dir, running, confText, save && confBefore.Exists())
	if err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat("backup", backup); err != nil {
		return collection.Result{}, err
	}
	// Recorded before the reload and kept through a failure: restoring the
	// backup when nothing changed is itself no change.
	if err := sdk.RecordInverse(rc, sdk.Inverse{
		FQCN:          configFQCN,
		Params:        map[string]any{"restore": backup, "save": save},
		Description:   fmt.Sprintf("Restores the configuration the router ran before this change, saved at %s.", backup),
		KeepOnFailure: true,
	}); err != nil {
		return collection.Result{}, err
	}
	result, err := s.reloadAndSave(ctx, candidate, backup, saveChange, savedConf, hasSaved, confBefore)
	if err != nil {
		// What the change left, so its undo can tell a router changed since
		// from one this run left part way.
		if now, rerr := s.runningConfig(context.WithoutCancel(ctx)); rerr == nil {
			_ = s.writePrivate(context.WithoutCancel(ctx), backup+afterSuffix, now)
		}
		return collection.Result{}, err
	}
	return result, nil
}

// reloadAndSave reloads candidate, confirms the router runs it, records
// what it left beside backup, and saves.
func (s *session) reloadAndSave(ctx context.Context, candidate, backup string, saveChange bool, savedConf string, hasSaved bool, confBefore remotefile.Info) (collection.Result, error) {
	if err := s.reload(ctx, candidate); err != nil {
		return collection.Result{}, err
	}
	deleted, added, err := s.diff(ctx, candidate)
	if err != nil {
		return collection.Result{}, err
	}
	if len(deleted)+len(added) > 0 {
		return collection.Result{}, fmt.Errorf("%s: after the reload the router does not run the candidate as written (FRR changed or refused part of it): to delete %q, to add %q; its undo is kept", configFQCN, maskLines(deleted), maskLines(added))
	}
	now, err := s.runningConfig(ctx)
	if err != nil {
		return collection.Result{}, err
	}
	if err := s.writePrivate(ctx, backup+afterSuffix, now); err != nil {
		return collection.Result{}, err
	}
	if saveChange {
		content := []byte(wantSaved(now, savedConf, hasSaved))
		if confBefore.Exists() {
			_, err = remotefile.Replace(ctx, s.conn, frrConf, content, confBefore)
		} else {
			_, err = remotefile.Put(ctx, s.conn, frrConf, content, confBefore, remotefile.Attributes{})
		}
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: saving %s: %w", configFQCN, frrConf, err)
		}
	}
	return collection.Result{Changed: true}, nil
}

// wantSaved is what frr.conf should hold: the file a restore puts back, or
// else the running configuration.
func wantSaved(running, savedConf string, hasSaved bool) string {
	if hasSaved {
		return savedConf
	}
	return running
}

// setLines records frr-reload's difference, each line's secret masked as
// net.frr.facts masks one, and empty lists rather than nothing when there
// is none.
func setLines(rc sdk.RunbookContext, deleted, added []string) error {
	if err := rc.SetStat("lines_deleted", maskLines(deleted)); err != nil {
		return err
	}
	return rc.SetStat("lines_added", maskLines(added))
}

// maskLines returns lines with their secrets masked, never nil.
func maskLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, maskSecrets(l))
	}
	return out
}
