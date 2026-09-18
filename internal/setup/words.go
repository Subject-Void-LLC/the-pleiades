// Everything an operator reads from the setup command, apart from the key
// refusals in keyrules.go.
package setup

import (
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// Everything an operator reads from this command is built here.
//
// The wording is a functional requirement rather than presentation: a
// message read once under time pressure is the whole of the safety
// mechanism at that moment. So it names the objects and their number, says
// plainly what cannot be undone, and is written in the present tense.
// TestWordsNameWhatIsLost holds every message here to that, including the
// words it must never use.

// recoveryMatrix answers, per field, what a future reinstall or upgrade
// needs, and what is simply gone if the field is lost.
func recoveryMatrix() string {
	return strings.Join([]string{
		"What each of these means for you later:",
		"",
		"MASTER_ENCRYPTION_KEY",
		"  Reinstall: restore this exact key. A reinstall without it is a new, empty system, not a recovered one.",
		"  Upgrade: keep it as it is.",
		"  Existing data: nothing stored can be read without it, and nothing regenerates it.",
		"  If it is lost: every credential, every stored device property and every saved survey answer is unreadable for good.",
		"  To change it safely: rotate it, with MASTER_ENCRYPTION_KEY_PREVIOUS and ROTATE_ENCRYPTION_KEYS=true.",
		"",
		"JWT_SECRET",
		"  Reinstall: a new one works. API tokens signed with the old one stop working.",
		"  Upgrade: keep it as it is.",
		"  Existing data: not needed to read anything stored.",
		"  Replicas: every controller needs the same one, or a token works on one controller and fails on another.",
		"  If it is lost: generate a new one and sign API tokens again. Browser sign-ins do not use it and are unaffected.",
		"",
		"PLEIADES_MAX_OUTAGE",
		"  Raising it is free at any time. Lowering it discards broker messages older than the new window.",
		"",
		"The database",
		"  It holds the work. Back it up on its own schedule: this key cannot restore a database that is gone,",
		"  and a backup cannot be read without this key.",
	}, "\n")
}

// OutageQuestion is the one question that sets the mesh's disrupted-link
// budget, asked in the terms an operator knows.
func OutageQuestion() string {
	return fmt.Sprintf("What is the longest link outage this deployment must survive? Answer as a duration from %s to %s, such as 20m or 2h. Press Enter for %s: ",
		shortDuration(time.Duration(topology.MinOutageBudget)), shortDuration(time.Duration(topology.MaxOutageBudget)), shortDuration(time.Duration(topology.DefaultOutageBudget)))
}

// OutageExplanation says what an answer costs, with the numbers the
// controller derives from it rather than restating them by hand, so the
// explanation cannot drift from the behavior.
func OutageExplanation(b topology.OutageBudget) string {
	retention := topology.DerivedMaxAge(b)
	window := topology.DerivedDuplicateWindow(b)
	return strings.Join([]string{
		fmt.Sprintf("With %s, the broker keeps every message for %s, which is %d times your answer. A job dispatched just before an outage is still there when the link returns.",
			shortDuration(time.Duration(b)), days(retention), int(retention/time.Duration(b))),
		fmt.Sprintf("Dispatch messages carry the credentials their jobs use, so those credentials also stay on the broker for %s.", days(retention)),
		fmt.Sprintf("The broker's duplicate detection covers the last %s of sends. It stops at 5 minutes and does not grow with a larger answer.", shortDuration(window)),
		"Raising this later is free. Lowering it discards messages older than the new window, so the controller refuses to lower it while it holds such messages, until you also set PLEIADES_MAX_OUTAGE_ALLOW_DISCARD=true.",
	}, "\n")
}

// LoweringNotice is added when a re-run lowers the budget.
func LoweringNotice(from, to topology.OutageBudget) string {
	return fmt.Sprintf("This lowers PLEIADES_MAX_OUTAGE from %s to %s. At the controller's next start, broker messages older than %s are discarded if it is told to, and the start is refused if it is not: set PLEIADES_MAX_OUTAGE_ALLOW_DISCARD=true for that one start to accept the loss.",
		shortDuration(time.Duration(from)), shortDuration(time.Duration(to)), days(topology.DerivedMaxAge(to)))
}

// days renders a long duration in days and hours, the unit an operator
// thinks about retention in.
func days(d time.Duration) string {
	whole := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	switch {
	case whole == 0:
		return shortDuration(d)
	case hours == 0 && whole == 1:
		return "1 day"
	case hours == 0:
		return fmt.Sprintf("%d days", whole)
	default:
		return fmt.Sprintf("%d days %d hours", whole, hours)
	}
}

// shortDuration prints a duration without Go's trailing zero units, so 30
// minutes reads as 30m rather than 30m0s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// confirmationPhrase is what an operator types to replace a key: a word and
// the old key's short fingerprint. It differs for every key, so it cannot
// become a habit, and it names the thing being destroyed.
func confirmationPhrase(fingerprint string) string {
	return "destroy " + keyregistry.Short(fingerprint)
}

// confirmationPrompt is shown before the typed confirmation.
func confirmationPrompt(file, database, fingerprint string, unknown int) string {
	lines := []string{
		fmt.Sprintf("This replaces the master key in %s, fingerprint %s.", file, keyregistry.Short(fingerprint)),
		fmt.Sprintf("The database at %s holds nothing encrypted under it.", database),
	}
	if unknown > 0 {
		lines = append(lines, fmt.Sprintf("It holds %d encrypted items that none of the keys setup was given can open. They are unreadable now and stay unreadable.", unknown))
	}
	lines = append(lines,
		"What is destroyed: anything encrypted under this key anywhere else, including every backup of this database taken while the key was in use. It becomes permanently unreadable. This cannot be undone.",
		fmt.Sprintf("Type exactly %q to replace the key. Anything else stops here and writes nothing: ", confirmationPhrase(fingerprint)),
	)
	return strings.Join(lines, "\n")
}

// possessionScreen is shown on the terminal's alternate screen at the
// moment a key is generated, and removed from view before the key is typed
// back.
func possessionScreen(key, fingerprint, file string) string {
	return strings.Join([]string{
		"MASTER ENCRYPTION KEY, fingerprint " + keyregistry.Short(fingerprint),
		"",
		"    " + key,
		"",
		"Right now this key exists in one place: this screen. Once setup finishes it also exists in",
		file + ", and nowhere else. `git clean -x` deletes that file.",
		"Copy the key somewhere that is not this machine, such as a password manager. To copy it,",
		"select it and use your terminal's copy command (Ctrl+Shift+C in most terminals): Ctrl+C here",
		"means stop, and setup asks before it stops.",
		"",
		recoveryMatrix(),
		"",
		"Press Enter when you have stored the key. The screen is then cleared, and you type or paste the key back.",
	}, "\n")
}

// ctrlCNotice is shown the first time Ctrl+C is pressed while the key is on
// the screen. It is written with the terminal in raw mode, so its lines end
// in "\r\n".
const ctrlCNotice = "\r\n\r\nThat was Ctrl+C, which most terminals send as stop rather than copy. Nothing has stopped.\r\n" +
	"Select the key and use your terminal's copy command (often Ctrl+Shift+C), then press Enter.\r\n" +
	"Press Ctrl+C again to stop setup; the key shown is then discarded.\r\n"

// possessionPrompt asks for the key back.
const possessionPrompt = "Type or paste the key you stored (it is not shown): "

// possessionMatched says what the re-entry showed, and no more.
const possessionMatched = "The key you entered matches, byte for byte. That shows you held an exact copy a moment ago. It cannot show where that copy is, or that it will last."

// possessionMismatch is shown after a wrong re-entry.
func possessionMismatch(left int) string {
	return fmt.Sprintf("That is not the key. %d %s left.", left, pick(left != 1, "attempt", "attempts"))
}

// possessionFailed is the refusal after every attempt is used.
func possessionFailed() *Refusal {
	return refuse("The key entered three times does not match the one shown. The key that was shown is discarded, because nobody holds a copy of it, and nothing was written. Run setup again.")
}

// notCheckedNotice is printed when a key is generated without a terminal.
func notCheckedNotice(file string) string {
	return fmt.Sprintf("No one saw this key, and no possession check ran. The only copy is in %s. Back that file up now: nothing this command did shows that anyone holds another copy.", file)
}

// nothingCountedNotice is printed when a key is generated with no database
// configured.
func nothingCountedNotice() string {
	return "No database is configured here (DB_DSN and DB_PATH are both unset), so setup counted nothing. That is correct for a first install, where no data exists yet. If this deployment already stores data, do not use this key: change a key that protects data by rotating it."
}

// alreadySetUp is the refusal a re-run with nothing to change gets. It
// names the file it would have destroyed.
func alreadySetUp(file, fingerprint string) *Refusal {
	return refuse(
		fmt.Sprintf("%s already holds a MASTER_ENCRYPTION_KEY, fingerprint %s. Replacing it is irreversible, and nothing was asked to change.", file, keyregistry.Short(fingerprint)),
		strings.Join([]string{
			"To change one setting in that file, name it:",
			"  --max-outage <duration> --force      changes the outage budget",
			"  --new-jwt-secret --force             replaces the JWT secret; API tokens signed with the old one stop working",
			"  --destroy-existing-encryption-key    replaces the master key; refused while any stored data is encrypted under it",
		}, "\n"),
	)
}

// needsForce is the refusal for a reversible change made without --force.
func needsForce(file, what string) *Refusal {
	return refuse(fmt.Sprintf("%s already holds %s. Changing it is reversible, and still takes --force, so an existing setting is never changed by accident.", file, what))
}
