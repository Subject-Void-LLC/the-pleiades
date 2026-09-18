// The Helm target, which only ever writes new files.
package setup

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// runHelm writes the Secret manifest and the values file for a first Helm
// install.
//
// It only ever writes new files. A Helm release's key is changed by rotating
// it, and its other settings by editing the values file, so a second run
// that found either file already there would have nothing safe to do: it
// refuses and names the file instead.
func runHelm(ctx context.Context, opts Options, dir *Dir, screen *Screen, out io.Writer) (Result, error) {
	if opts.Check || opts.NewJWTSecret || opts.DestroyKey {
		return Result{}, refuse("--check, --new-jwt-secret and --destroy-existing-encryption-key apply to the compose target. A Helm release's master key is changed by rotating it, and its JWT secret by editing the Secret.")
	}
	for _, name := range []string{HelmSecretFile, HelmValuesFile} {
		_, exists, err := dir.ReadExisting(name)
		if err != nil {
			return Result{}, err
		}
		if exists {
			return Result{}, refuse(fmt.Sprintf("%s already exists. It holds the output of an earlier run, and replacing it would discard the key it holds. Write to a different --dir to make a separate release's Secret.", dir.Show(name)))
		}
	}

	budget, err := chooseBudget(opts, &EnvFile{}, screen)
	if err != nil {
		return Result{}, err
	}
	if err := dir.CheckWritable(); err != nil {
		return Result{}, err
	}

	display := dir.Show(HelmSecretFile)
	key, err := decideKey(ctx, opts, display, &Plan{Key: Generate}, screen, out)
	if err != nil {
		return Result{}, err
	}
	jwt, err := newSecretText()
	if err != nil {
		return Result{}, err
	}
	pgRaw, err := crypto.GenerateKey()
	if err != nil {
		return Result{}, err
	}
	// Hex rather than base64: the chart places this password inside a
	// connection string, where a "/" or "+" would change what it means.
	pgPassword := hex.EncodeToString(pgRaw)
	redact.Shared().Literals().Add(pgPassword)

	secret, values, err := RenderHelm(HelmInput{
		SecretName: opts.SecretName, Namespace: opts.Namespace,
		MasterKey: key, JWTSecret: jwt, PostgresPassword: pgPassword, Budget: budget,
	})
	if err != nil {
		return Result{}, err
	}

	possession, err := takePossession(key, display, screen)
	if err != nil {
		return Result{}, err
	}
	if err := dir.CreateNew(HelmSecretFile, secret); err != nil {
		return Result{}, err
	}
	if err := dir.CreateNew(HelmValuesFile, values); err != nil {
		// Nothing has used the Secret file yet, so taking it back out
		// leaves the directory as it was rather than half written, which
		// a re-run would otherwise refuse over.
		if rmErr := dir.remove(HelmSecretFile); rmErr != nil {
			return Result{}, fmt.Errorf("%w; %s was written and could not be removed again (%v), so delete it by hand before running setup again", err, display, rmErr)
		}
		return Result{}, fmt.Errorf("%w; setup removed %s again, since nothing had used it, and the key shown is discarded. Run setup again", err, display)
	}

	if possession != keyregistry.PossessionChecked {
		fmt.Fprintln(out, notCheckedNotice(display))
	}
	fmt.Fprintln(out, helmSummary(dir.Show(HelmSecretFile), dir.Show(HelmValuesFile), opts, key))
	return Result{File: display, Key: key, KeyVersion: crypto.DefaultKeyVersion, Possession: possession}, nil
}
