// What backup and restore say.
package backup

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// takenSummary says what a backup holds, what it does not, and what it
// needs.
func takenSummary(w taken, dir *store, keys keySet) string {
	lines := []string{
		fmt.Sprintf("Backed up the database to %s (%s, schema %s).", printablePath(dir.show(w.name.String())), size(w.size), strings.TrimSuffix(w.version, ".sql")),
		"",
		fmt.Sprintf("It holds %s, sealed under the master encryption key.", sealedSummary(w.census)),
	}
	switch {
	case keys.key == nil:
		lines = append(lines, ".env holds no key, so nothing records which key those values need, and a restore will ask for it.")
	case w.census.Unknown() > 0:
		lines = append(lines, fmt.Sprintf("%d of them %s under no key in .env. They are in the backup as they are in the database, and a restore needs the key that opens them.",
			w.census.Unknown(), pick(w.census.Unknown() == 1, "opens", "open")))
	default:
		lines = append(lines, fmt.Sprintf("They open under key %s, the key in .env.", keys.short()))
	}
	if keys.key != nil {
		lines = append(lines,
			"",
			fmt.Sprintf("THIS FILE DOES NOT HOLD THE KEY. Restoring it needs the file AND key %s. Keep a copy of", keys.short()),
			"the key somewhere this file is not: either one alone restores nothing, and the two together are",
			"everything this deployment stores.")
	}
	if len(w.newer) > 0 {
		// Every recorded migration this build does not know, which is a newer
		// build's work inside the window and may be another lineage's outside
		// it, so the words claim only what is true of both. The newest is
		// named whole rather than the list cut short, since a name cut
		// midway identifies nothing.
		newest := w.newer[0]
		for _, name := range w.newer[1:] {
			if name > newest {
				newest = name
			}
		}
		lines = append(lines,
			"",
			fmt.Sprintf("This database records %d migration(s) this build does not know, the newest %s.", len(w.newer), printable(newest)),
			"Restore this backup with the build that applied them, or a later one: this one would refuse it.")
	}
	return strings.Join(append(lines,
		"",
		"It does not hold messages waiting on the broker, the controller's self-signed certificate, or the",
		"runbook directory. It does hold password hashes, the activity trail and every job's history as they",
		"are, so it is readable only by you (mode 0600): store it the way you would store the database.",
	), "\n")
}

// restoredSummary says what a restore did.
func restoredSummary(database, file string, name Name, named bool, census crypto.Census, keys keySet, r Restored) string {
	from := printablePath(file)
	if named {
		from += fmt.Sprintf(" (taken %s)", name.Taken.UTC().Format("2006-01-02 15:04 UTC"))
	}
	lines := []string{
		"",
		fmt.Sprintf("Restored the database %s from %s.", database, from),
		fmt.Sprintf("It holds %s, %s under key %s with the tag %s.", sealedSummary(census), pick(census.Sealed() == 1, "which opens", "all of which open"), keys.short(), keys.version),
	}
	if r.SetAside != "" {
		lines = append(lines, fmt.Sprintf("The database it replaced was backed up first, to %s. Restoring that file the same way puts it back.", printablePath(r.SetAside)))
	} else {
		lines = append(lines, "The database it replaced held nothing, so nothing was set aside.")
	}
	if r.FailedJobs > 0 {
		lines = append(lines, fmt.Sprintf("%d %s running when the backup was taken %s marked failed: what %s did after the backup is not recorded, so check %s devices before running %s again.",
			r.FailedJobs, pick(r.FailedJobs == 1, "job", "jobs"), pick(r.FailedJobs == 1, "was", "were"),
			pick(r.FailedJobs == 1, "it", "they"), pick(r.FailedJobs == 1, "its", "their"), pick(r.FailedJobs == 1, "it", "them")))
	}
	if r.EndedSessions > 0 {
		lines = append(lines, fmt.Sprintf("%d %s from the backup %s ended, so everyone signs in again.",
			r.EndedSessions, pick(r.EndedSessions == 1, "session", "sessions"), pick(r.EndedSessions == 1, "was", "were")))
	}
	if r.KeyFile != "" {
		lines = append(lines, fmt.Sprintf("Wrote the key to %s. Setup adds a new JWT secret and the outage budget the next time the stack comes up.", printablePath(r.KeyFile)))
	}
	return strings.Join(lines, "\n")
}

// readableLine says that every sealed value in a backup opens under the key
// named, in words that fit the count.
func readableLine(n int, key string) string {
	switch n {
	case 0:
		return "It holds no sealed values, so no key is needed to read it."
	case 1:
		return "Its one sealed value opens under " + key + ", under the tag .env gives it."
	default:
		return fmt.Sprintf("All %d of its sealed values open under %s, under the tag .env gives it.", n, key)
	}
}

// keyAdvice is what to do about a backup the key cannot read.
func keyAdvice(importing bool) string {
	if importing {
		return "Enter the key the backup was taken under: its name gives the first eight characters of that key's fingerprint. If the backup was taken during a key rotation, it needs both keys: write them to .env as MASTER_ENCRYPTION_KEY and MASTER_ENCRYPTION_KEY_PREVIOUS, each with its version tag, and restore again."
	}
	return strings.Join([]string{
		"If .env's key replaced the backup's key by rotation, put the backup's key back as MASTER_ENCRYPTION_KEY_PREVIOUS (with its tag as MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION) and restore again; the controller then rotates what it restored.",
		"If this deployment holds nothing you need, `make decom` removes it, and `make restore` on the emptied machine asks for the backup's key.",
		"If the key is lost, the sealed values in this backup cannot be read by anyone, and restoring it would not change that.",
	}, "\n")
}

// size renders a byte count for a person.
func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
