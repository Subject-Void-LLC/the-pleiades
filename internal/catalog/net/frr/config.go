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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
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
					{FQCN: configFQCN, Record: []string{"restore", "save", "login"}, MayKeepOnFailure: true},
				},
			},
			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Makes an FRRouting router run exactly the configuration given, and saves what it ran before so the change can be undone.",
				Description: "Takes the whole intended configuration (config, or src: a file in the project) and applies only what differs, through FRR's own frr-reload.py. The candidate is first checked with vtysh -m -f, and a line vtysh refuses fails the task before anything reaches the router: frr-reload reads a file vtysh cannot parse as an empty configuration and would delete everything. The difference comes from frr-reload.py --test; an empty one is no change, and a check returns it as the prediction. A real run saves the running configuration to a file on the router (0600, under the connecting account's ~/.pleiades-frr), reloads, confirms the running configuration now matches the candidate, and saves the result beside the backup. Its undo names only that backup, so no configuration text, and no secret in one, reaches the journal; the undo restores exactly the configuration that was running, and refuses when the router no longer runs what this change left, unless its step names force. The undo is recorded before the reload and kept if the reload then fails, since restoring a backup when nothing changed is itself no change. With save, the running configuration is also written to frr.conf, keeping that file's owner and mode, so it survives a restart; the backup then keeps frr.conf as it was too, and the undo puts it back (where there was none, the undo writes the configuration it restores). expect_sha256 refuses a candidate whose bytes are not the ones a model check analysed. Three facts about FRR decide whether this works on the first try. FRR rewrites a configuration in its own form (it adds lines such as frr version and frr defaults, drops comments and reorders), so a hand-written candidate that differs from that form fails after the reload with the difference shown as - and + lines; start from net.frr.facts's running_config, which is FRR's own form, and edit that. The account that saves must be able to replace frr.conf: it needs write access to the file and to its directory (/etc/frr), since the save writes a new file beside it, and a check refuses a save the account cannot make before anything is reloaded, naming the account that owns the file. Rollback restores from a backup under the connecting account's home, so undo with the same account that made the change.",
				Params: []collection.Param{
					{Name: "config", Type: "string", Description: "The whole intended configuration, as frr.conf text. Set config or src, not both. Lines not in it are removed from the running configuration."},
					{Name: "src", Type: "string", Description: "A file in the project directory holding the whole intended configuration, read by the command line; refused on a Walk-tier Runner, where config is the way. Set config or src, not both."},
					{Name: "save", Type: "bool", Default: "false", Description: "Also write the configuration to frr.conf (keeping that file's owner and mode), so it survives a restart. The undo puts frr.conf back as it was, or, where there was none, writes the configuration it restores."},
					{Name: "expect_sha256", Type: "string", Description: "Refuse unless the candidate's SHA-256 is this, in hex: the digest net.batfish.check returned for the bytes it analysed, so the change applied is the change checked."},
					{Name: "restore", Type: "string", Description: "Restore the configuration this method saved before an earlier change, by the path its undo recorded. Set by an undo; with restore, config and src are not given."},
					{Name: "login", Type: "string", Description: "With restore, the account that made the backup, as its undo recorded it. A backup lives under that account's home, so an undo run as another account is refused before it reads anything, naming the account to connect as. Set by an undo."},
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
	login, err := sdk.OptionalStringParam(params, "login")
	if err != nil {
		return collection.Result{}, err
	}
	if login != "" && restore == "" {
		return collection.Result{}, fmt.Errorf("%s: login names the account that made a backup, and only a restore reads one", configFQCN)
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
		// A configuration read through net.frr.facts has its secrets masked;
		// applied, it would set every key to its placeholder and drop every
		// authenticated session (the branch review's F5).
		if err := netcfg.RefuseMasked("the candidate", text); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
		}
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
		if err := s.sameLogin(ctx, login, restore); err != nil {
			return collection.Result{}, err
		}
		text, saved, hasSaved, err = s.readBackup(ctx, dir, restore, sdk.BoolParam(params, "force"))
		if err != nil {
			return collection.Result{}, err
		}
	}
	return s.apply(ctx, rc, dir, text, sdk.BoolParam(params, "save"), saved, hasSaved, mode)
}

// login returns the account this session runs commands as.
func (s *session) login(ctx context.Context) (string, error) {
	out, err := s.run(ctx, "id", "-un")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// sameLogin refuses a restore run as an account other than the one that made
// the backup, before anything is read. A backup is under its maker's home, so
// another account would only find "restore must name a backup this method
// made" and no word of whose it is; this names the account to connect as.
// recorded is the login the undo recorded, empty for an undo recorded before
// logins were (which still meets the directory check in readBackup).
func (s *session) sameLogin(ctx context.Context, recorded, backup string) error {
	if recorded == "" {
		return nil
	}
	now, err := s.login(ctx)
	if err != nil {
		return err
	}
	if now != recorded {
		return fmt.Errorf("%s: the backup %s was made by the account %s and lives in its home, but this task connects as %s: run the undo as %s (give the device that account's credential), or restore by hand; nothing was changed", configFQCN, backup, recorded, now, recorded)
	}
	return nil
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
	if runChange {
		// Checked in a check too: frr-reload --test needs no write access,
		// so without this a check passed a change the account could not
		// make (the clean-slate lab's G3).
		if err := s.canReload(ctx); err != nil {
			return collection.Result{}, err
		}
	}

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
	// Whether frr.conf can be written is asked before the reload, in a
	// check as in a real run: a save refused after the reload would leave the
	// router changed and the task failed (the branch review's F8).
	if saveChange {
		if err := s.canSave(ctx, confBefore); err != nil {
			return collection.Result{}, err
		}
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, nil
	}

	backup, err := s.writeBackup(ctx, dir, running, confText, save && confBefore.Exists())
	if err != nil {
		return collection.Result{}, err
	}
	login, err := s.login(ctx)
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
		Params:        map[string]any{"restore": backup, "save": save, "login": login},
		Description:   fmt.Sprintf("Restores the configuration the router ran before this change, saved at %s by the account %s (the undo must connect as it).", backup, login),
		KeepOnFailure: true,
	}); err != nil {
		return collection.Result{}, err
	}
	result, err := s.reloadAndSave(ctx, candidate, backup, saveChange, savedConf, hasSaved, confBefore)
	if err != nil {
		// What the change left, so its undo can tell a router changed since
		// from one this run left part way.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if now, rerr := s.runningConfig(cleanup); rerr == nil {
			_ = s.writePrivate(cleanup, backup+afterSuffix, now)
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
		return collection.Result{}, fmt.Errorf("%s: after the reload the router does not run the candidate as written (FRR changed or refused part of it). A - line is one the router runs that the candidate lacks, a + line one the candidate has that the router lacks:\n%s\nWrite the candidate in FRR's own form: start from net.frr.facts's running_config. The undo is kept", configFQCN, diffText(deleted, added))
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

// diffText renders frr-reload's difference as - and + lines, each secret
// masked as net.frr.facts masks one: the router's lines the candidate lacks,
// then the candidate's lines the router lacks.
func diffText(deleted, added []string) string {
	var b strings.Builder
	for _, l := range maskLines(deleted) {
		b.WriteString("- " + l + "\n")
	}
	for _, l := range maskLines(added) {
		b.WriteString("+ " + l + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// canSave refuses a save this login cannot make: an existing frr.conf it
// may not write, or a save that remotefile's Put or Replace would refuse.
//
// Replace writes a new file beside frr.conf before it renames or copies it
// over, so the login needs the directory as well as the file: a file the login
// may write in a directory it may not was passed by a check and refused by the
// real save, after the reload had already changed the router.
func (s *session) canSave(ctx context.Context, before remotefile.Info) error {
	if before.Exists() {
		res, err := s.conn.Run(ctx, remoteexec.QuoteCommand([]string{"test", "-w", frrConf}))
		if err != nil {
			return fmt.Errorf("%s: checking %s: %w", configFQCN, frrConf, err)
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("%s: the connecting account cannot write %s (it belongs to %s:%s), so save cannot keep the change: connect as %s, or give this account write access; nothing was changed", configFQCN, frrConf, before.Owner, before.Group, before.Owner)
		}
	}
	// The same prediction Replace's own refusals come from (Put's, for a
	// file that is not there yet).
	if err := remotefile.PredictPut(ctx, s.conn, frrConf, before, remotefile.Attributes{}); err != nil {
		return fmt.Errorf("%s: the save would be refused (%w), so save cannot keep the change: connect as the account that owns %s and its directory, or give this account write access to both; nothing was changed", configFQCN, err, frrConf)
	}
	return nil
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
// net.frr.facts masks one (pkg/netcfg), and empty lists rather than nothing when there
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
		out = append(out, netcfg.Mask(l))
	}
	return out
}
