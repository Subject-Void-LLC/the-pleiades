// One setup run, and the compose target.
package setup

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// Target is the kind of deployment the output is for.
type Target string

// The two targets, each of which reads its secrets from a different place.
const (
	// TargetCompose writes the .env file docker compose reads.
	TargetCompose Target = "compose"

	// TargetHelm writes a Secret manifest and a values file for the chart.
	TargetHelm Target = "helm"
)

// ComposeFile is the file the compose target writes: the one docker compose
// reads from the project directory without being told to.
const ComposeFile = ".env"

// ErrIncomplete is what --check returns for a compose env file that is
// readable but lacks a key or a JWT secret, so a caller can tell "run setup"
// from "something is wrong".
var ErrIncomplete = errors.New("setup: the env file does not hold a MASTER_ENCRYPTION_KEY and a JWT_SECRET yet")

// Options is one run's request.
type Options struct {
	// Target is where the output goes.
	Target Target

	// Dir is the directory to write into, and DisplayDir how messages name
	// it when that differs (a container sees the operator's directory at a
	// mount point).
	Dir, DisplayDir string

	// Check only reads the compose env file and reports whether it is
	// complete. It writes nothing and reads no database.
	Check bool

	// MaxOutage is --max-outage, empty when not given.
	MaxOutage string

	// NewJWTSecret, Force and DestroyKey are the field flags.
	NewJWTSecret, Force, DestroyKey bool

	// SecretName and Namespace name the Helm Secret.
	SecretName, Namespace string

	// DatabaseDSN is the database the census counts, exactly as the
	// controller would open it. Empty means none is configured here.
	DatabaseDSN string
}

// Result is what a run wrote, for the caller's steps after it: recording
// the key and creating the first administrator.
type Result struct {
	// File names the file that holds the key, as messages name it.
	File string

	// Key is the master key when this run generated one, nil otherwise.
	Key []byte

	// KeyVersion is the version tag the controller will read that key
	// under: the file's MASTER_ENCRYPTION_KEY_VERSION, or v1.
	KeyVersion string

	// Possession is whether the operator re-entered that key.
	Possession keyregistry.Possession
}

// Run carries out one setup request. screen is nil when there is no
// person at a terminal; every question then takes its default or its flag,
// and nothing that needs a person, the typed confirmation and the
// possession check, runs. out receives everything else this command says.
func Run(ctx context.Context, opts Options, screen *Screen, out io.Writer) (Result, error) {
	dir, err := OpenDir(opts.Dir, opts.DisplayDir)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = dir.Close() }()

	switch opts.Target {
	case TargetCompose:
		return runCompose(ctx, opts, dir, screen, out)
	case TargetHelm:
		return runHelm(ctx, opts, dir, screen, out)
	default:
		return Result{}, fmt.Errorf("setup: unknown target %q; use compose or helm", opts.Target)
	}
}

// runCompose writes, or adds to, the compose env file.
func runCompose(ctx context.Context, opts Options, dir *Dir, screen *Screen, out io.Writer) (Result, error) {
	display := dir.Show(ComposeFile)
	data, exists, err := dir.ReadExisting(ComposeFile)
	if err != nil {
		return Result{}, err
	}
	file, err := ParseEnvFile(data)
	if err != nil {
		return Result{}, fmt.Errorf("%w (in %s)", err, display)
	}
	if opts.Check {
		_, hasKey := file.Get(VarMasterKey)
		_, hasJWT := file.Get(VarJWTSecret)
		if !hasKey || !hasJWT {
			return Result{}, ErrIncomplete
		}
		return Result{File: display}, nil
	}

	answer, err := chooseBudget(opts, file, screen)
	if err != nil {
		return Result{}, err
	}
	plan, err := NewPlanBuilder(file, display, opts).MasterKey().JWTSecret().Budget(answer).Build()
	if err != nil {
		return Result{}, err
	}
	if err := dir.CheckWritable(); err != nil {
		return Result{}, err
	}

	result := Result{File: display, KeyVersion: crypto.DefaultKeyVersion}
	if v, ok := file.Get(VarMasterKeyVersion); ok {
		result.KeyVersion = v
	}
	if plan.Key != Keep {
		key, err := decideKey(ctx, opts, display, plan, screen, out)
		if err != nil {
			return Result{}, err
		}
		result.Key = key
		file.Set(VarMasterKey, crypto.EncodeKey(key))
	}
	if plan.JWT != Keep {
		jwt, err := newSecretText()
		if err != nil {
			return Result{}, err
		}
		file.Set(VarJWTSecret, jwt)
	}
	if plan.Budget != Keep {
		file.Set(VarMaxOutage, shortDuration(plan.BudgetValue.Duration()))
	}

	result.Possession, err = takePossession(result.Key, display, screen)
	if err != nil {
		return Result{}, err
	}

	if !exists {
		file.AddComment(composeFileNote()...)
		err = dir.CreateNew(ComposeFile, file.Bytes())
	} else {
		err = dir.Replace(ComposeFile, data, file.Bytes())
	}
	if err != nil {
		return Result{}, err
	}

	if result.Key != nil && result.Possession == keyregistry.PossessionNotChecked {
		fmt.Fprintln(out, notCheckedNotice(display))
	}
	if plan.Lowering {
		fmt.Fprintln(out, LoweringNotice(plan.PreviousBudget, plan.BudgetValue))
	}
	fmt.Fprintln(out, composeSummary(display, plan, result.Key))
	return result, nil
}

// chooseBudget returns the outage budget a run writes when the file holds
// none, or the one --max-outage names: the flag, then the operator's answer
// at a terminal, then the default.
func chooseBudget(opts Options, file *EnvFile, screen *Screen) (topology.OutageBudget, error) {
	if opts.MaxOutage != "" {
		b, err := topology.ParseOutageBudget(opts.MaxOutage)
		if err != nil {
			return 0, refuse(err.Error())
		}
		return b, nil
	}
	if _, held := file.Get(VarMaxOutage); held {
		return 0, nil
	}
	if screen != nil {
		return screen.AskOutage()
	}
	return topology.DefaultOutageBudget, nil
}

// decideKey counts what the database holds, applies the key rules, takes the
// typed confirmation a replacement needs, and only then generates the key.
func decideKey(ctx context.Context, opts Options, display string, plan *Plan, screen *Screen, out io.Writer) ([]byte, error) {
	census, database, err := takeCensus(ctx, opts, display, plan)
	if err != nil {
		return nil, err
	}
	held := []string{heldName(display)}
	if plan.HeldPrevious != nil {
		held = append(held, previousName(display))
	}
	heldFingerprint := ""
	if plan.HeldKey != nil {
		heldFingerprint = crypto.Fingerprint(plan.HeldKey)
	}
	if err := JudgeKey(KeyContext{
		File: display, Database: database, Census: census,
		HeldKey: heldFingerprint, Held: held, Replacing: plan.Key == Replace,
	}); err != nil {
		return nil, err
	}
	if census == nil {
		fmt.Fprintln(out, nothingCountedNotice())
	}
	if plan.Key == Replace && screen != nil {
		if err := screen.ConfirmReplace(display, database, heldFingerprint, census.Unknown()); err != nil {
			return nil, err
		}
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	redact.Shared().Literals().Add(crypto.EncodeKey(key))
	return key, nil
}

// takeCensus reads the configured database, if any, and counts what every
// held key opens. It returns a nil census when no database is configured,
// and an empty one when a SQLite file does not exist yet.
func takeCensus(ctx context.Context, opts Options, display string, plan *Plan) (*crypto.Census, string, error) {
	if opts.DatabaseDSN == "" {
		return nil, "", nil
	}
	db, err := ent.OpenExisting(ctx, opts.DatabaseDSN)
	if errors.Is(err, ent.ErrNoDatabase) {
		return &crypto.Census{}, "a database file that does not exist yet", nil
	}
	if err != nil {
		return nil, "", refuse(fmt.Sprintf("setup cannot read the configured database, so it cannot count what a key protects: %v", err))
	}
	defer func() { _ = db.Close() }()

	var candidates []crypto.CandidateKey
	if plan.HeldKey != nil {
		candidates = append(candidates, crypto.CandidateKey{Name: heldName(display), Key: plan.HeldKey})
	}
	if plan.HeldPrevious != nil {
		candidates = append(candidates, crypto.CandidateKey{Name: previousName(display), Key: plan.HeldPrevious})
	}
	if opts.Target == TargetCompose {
		candidates = append(candidates, crypto.CandidateKey{Name: PublishedComposeKeyName, Key: PublishedComposeKey()})
	}
	census, err := crypto.TakeCensus(ctx, db, candidates)
	if err != nil {
		return nil, "", refuse(fmt.Sprintf("setup could not finish counting what %s holds, so it cannot tell whether a new key would make stored data unreadable: %v", db.Describe(), err))
	}
	return &census, db.Describe(), nil
}

// takePossession runs the possession check for a newly generated key when a
// person is at the terminal, and reports honestly when none is.
func takePossession(key []byte, display string, screen *Screen) (keyregistry.Possession, error) {
	if key == nil {
		return "", nil
	}
	if screen == nil {
		return keyregistry.PossessionNotChecked, nil
	}
	if err := screen.CheckPossession(key, display); err != nil {
		return "", err
	}
	return keyregistry.PossessionChecked, nil
}

// newSecretText generates a secret as text: base64 of 32 bytes from the one
// key generator, which is 44 characters and passes the controller's own
// 32-byte minimum for a JWT secret with room to spare.
func newSecretText() (string, error) {
	raw, err := crypto.GenerateKey()
	if err != nil {
		return "", err
	}
	text := crypto.EncodeKey(raw)
	redact.Shared().Literals().Add(text)
	return text, nil
}

// heldName and previousName are how the census names the file's own keys.
func heldName(display string) string     { return "the key in " + display }
func previousName(display string) string { return "the previous key in " + display }
