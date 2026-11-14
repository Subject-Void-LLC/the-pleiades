//go:build unix

// Tests that a refusal an external program marks on its response reaches
// the engine as one, as a refused login from an in-process method does, so
// a task's until or loop stops there rather than have the program send the
// login again.
package loader

import (
	"errors"
	"strings"
	"testing"
)

// TestRefused_AMarkedRefusalStaysOne runs a program answering a failure
// marked refused and one not marked: the first is a refusal, its text masked
// and escaped like any of the program's text, and the second is not.
func TestRefused_AMarkedRefusalStaysOne(t *testing.T) {
	type refusal interface{ Refused() bool }
	for _, tc := range []struct {
		name    string
		body    string
		refused bool
	}{
		{"marked", `printf '{"refused":true,"error":"ssh: unable to authenticate as hunter2\\nnext"}' >&3`, true},
		{"not marked", `printf '{"error":"ssh: unable to authenticate as hunter2"}' >&3`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := programDir(t)
			oneMethodProgram(t, dir, "loadertest.refused.run", false, tc.body)
			d := loadOne(t, dir, "loadertest.refused.run", testOptions())
			rc := newRecordingContext()
			rc.secrets["password"] = "hunter2"
			_, err := d.Invoke(t.Context(), rc, newSSHDevice(), nil)
			if err == nil {
				t.Fatal("a failure was read as a success")
			}
			var r refusal
			if got := errors.As(err, &r) && r.Refused(); got != tc.refused {
				t.Fatalf("Refused = %v for %v, want %v", got, err, tc.refused)
			}
			if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "\n") {
				t.Errorf("the program's text %q was not masked and kept to one line", err.Error())
			}
		})
	}
}
