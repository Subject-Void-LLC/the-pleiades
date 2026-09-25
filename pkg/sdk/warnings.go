// The stat a method records a warning under.
package sdk

// StatWarnings is the stat a method sets to a []string of warnings: what a
// weakening the device's record allows means for this run (a deprecated
// TLS version, legacy ciphers, a credential sent over plain HTTP). A run
// always shows them, verbose or not, since the person running the work is
// the one who has to act on them.
const StatWarnings = "warnings"

// RecordWarnings sets warnings on rc when there are any.
func RecordWarnings(rc RunbookContext, warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	return rc.SetStat(StatWarnings, warnings)
}
