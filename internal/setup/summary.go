// What a run says it wrote, and the note a new env file starts with.
package setup

import (
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
)

// composeFileNote is the comment a new compose env file starts its setup
// section with, for whoever opens the file later.
func composeFileNote() []string {
	return []string{
		"",
		"Written by pleiades-controller setup on " + time.Now().UTC().Format("2006-01-02") + ".",
		"MASTER_ENCRYPTION_KEY is the only copy of the key that reads every credential",
		"this deployment stores. Back this file up somewhere that is not this machine.",
		"Change that key by rotating it, never by editing it: see the production guide.",
	}
}

// composeSummary says what a compose run wrote.
func composeSummary(file string, plan *Plan, key []byte) string {
	lines := []string{"Wrote " + file + ":"}
	switch plan.Key {
	case Generate:
		lines = append(lines, "  MASTER_ENCRYPTION_KEY  new, fingerprint "+keyregistry.Short(crypto.Fingerprint(key)))
	case Replace:
		lines = append(lines, "  MASTER_ENCRYPTION_KEY  replaced, fingerprint "+keyregistry.Short(crypto.Fingerprint(key)))
	default:
		lines = append(lines, "  MASTER_ENCRYPTION_KEY  unchanged")
	}
	switch plan.JWT {
	case Generate:
		lines = append(lines, "  JWT_SECRET             new")
	case Replace:
		lines = append(lines, "  JWT_SECRET             replaced; API tokens signed with the old one no longer work")
	default:
		lines = append(lines, "  JWT_SECRET             unchanged")
	}
	if plan.Budget != Keep {
		lines = append(lines, "  PLEIADES_MAX_OUTAGE    "+shortDuration(plan.BudgetValue.Duration()))
	} else {
		lines = append(lines, "  PLEIADES_MAX_OUTAGE    unchanged")
	}
	return strings.Join(lines, "\n")
}

// helmSummary says what a Helm run wrote and the commands that install it.
func helmSummary(secretFile, valuesFile string, opts Options, key []byte) string {
	ns := opts.Namespace
	nsFlag := ""
	if ns != "" {
		nsFlag = " --namespace " + ns
	}
	return strings.Join([]string{
		fmt.Sprintf("Wrote %s (the master key, fingerprint %s, the JWT secret and the database password)", secretFile, keyregistry.Short(crypto.Fingerprint(key))),
		fmt.Sprintf("and %s (no secrets).", valuesFile),
		"",
		"Install with:",
		"  kubectl create" + nsFlag + " -f " + secretFile,
		"  helm install <release> helm/the-pleiades" + nsFlag + " -f " + valuesFile,
		"",
		"Then create the first administrator with the bootstrap-admin command `helm install` prints.",
		"",
		"The controller records this key in the activity trail the first time it starts with it.",
	}, "\n")
}
