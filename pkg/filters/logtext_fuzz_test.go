package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzSyslogParse fuzzes SyslogParse against malformed and adversarial
// input, per this phase's own Fuzz/Stress Test checklist item ("a
// syslog line with a malformed priority field"), covering both the PRI
// hard-gate and the RFC 5424 STRUCTURED-DATA scanner's own bracket/
// quote/escape state machine.
func FuzzSyslogParse(f *testing.F) {
	seeds := []string{
		"",
		"<34>1 2003-10-11T22:14:15Z host su - ID47 - login ok",
		"<34>Oct 11 22:14:15 host su[123]: login ok",
		"<abc>malformed priority field",
		"<999>out of range priority",
		"<>empty priority",
		"<->negative-looking priority",
		"no angle brackets at all",
		"<34unterminated priority bracket",
		`<13>1 2003-10-11T22:14:15Z host app - - [a@1 x="va\]lue"] msg`,
		`<13>1 2003-10-11T22:14:15Z host app - - [a@1 x="1"][b@1 y="2"] msg`,
		"<13>1 2003-10-11T22:14:15Z host app - - [unterminated sd",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		filters.SyslogParse(line)
	})
}
