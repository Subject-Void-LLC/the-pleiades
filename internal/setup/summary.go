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
		"Back up PLEIADES_FACTS_DIGEST_KEY with it, and keep it the same on a restore:",
		"a new one makes every private file's stored digest read as changed once.",
	}
}

// composeSummary says what a compose run wrote.
func composeSummary(file string, plan *Plan, key []byte) string {
	lines := []string{"Wrote " + file + ":"}
	switch plan.Key {
	case Generate:
		lines = append(lines, "  MASTER_ENCRYPTION_KEY      new, fingerprint "+keyregistry.Short(crypto.Fingerprint(key)))
	case Replace:
		lines = append(lines, "  MASTER_ENCRYPTION_KEY      replaced, fingerprint "+keyregistry.Short(crypto.Fingerprint(key)))
	default:
		lines = append(lines, "  MASTER_ENCRYPTION_KEY      unchanged")
	}
	lines = append(lines, tokenCheckLines(plan)...)
	if plan.DigestKey != Keep {
		lines = append(lines, "  PLEIADES_FACTS_DIGEST_KEY  new")
	} else {
		lines = append(lines, "  PLEIADES_FACTS_DIGEST_KEY  unchanged")
	}
	if plan.Budget != Keep {
		lines = append(lines, "  PLEIADES_MAX_OUTAGE        "+shortDuration(plan.BudgetValue.Duration()))
	} else {
		lines = append(lines, "  PLEIADES_MAX_OUTAGE        unchanged")
	}
	return strings.Join(lines, "\n")
}

// tokenCheckLines are the summary's lines for how API tokens are checked:
// the JWT secret, or the JWKS URL and, when a move to it removed one, the
// JWT secret it replaced.
func tokenCheckLines(plan *Plan) []string {
	if !plan.UsesJWKS {
		switch plan.JWT {
		case Generate:
			return []string{"  JWT_SECRET                 new"}
		case Replace:
			return []string{"  JWT_SECRET                 replaced; API tokens signed with the old one no longer work"}
		default:
			return []string{"  JWT_SECRET                 unchanged"}
		}
	}
	var lines []string
	switch plan.JWKS {
	case Generate:
		lines = append(lines, "  JWKS_URL                   "+plan.JWKSValue)
	case Replace:
		lines = append(lines, "  JWKS_URL                   "+plan.JWKSValue+"; API tokens the old identity provider signed no longer work")
	default:
		lines = append(lines, "  JWKS_URL                   unchanged")
	}
	if plan.JWT == Remove {
		lines = append(lines, "  JWT_SECRET                 removed; API tokens signed with it no longer work")
	}
	return lines
}

// helmSummary says what a Helm run wrote and the commands that install it.
func helmSummary(secretFile, valuesFile string, opts Options, key []byte) string {
	ns := opts.Namespace
	nsFlag := ""
	if ns != "" {
		nsFlag = " --namespace " + ns
	}
	tokens := "the JWT secret, "
	values := fmt.Sprintf("and %s (no secrets).", valuesFile)
	if opts.JWKSURL != "" {
		tokens = ""
		values = fmt.Sprintf("and %s (no secrets; it sets controller.jwksUrl, so the controller checks API tokens against %s and needs no JWT secret).", valuesFile, opts.JWKSURL)
	}
	return strings.Join([]string{
		fmt.Sprintf("Wrote %s (the master key, fingerprint %s, %sthe facts digest key and the database password)", secretFile, keyregistry.Short(crypto.Fingerprint(key)), tokens),
		values,
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
