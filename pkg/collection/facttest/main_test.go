// Tests of Main as a test binary runs it: testdata/auditee's test binary is
// built once and run for each case, and its exit code and standard error are
// read, since what Main decides (the package it audits, whether the run was
// partial, the code it returns) is only real inside a whole test binary.
package facttest

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// auditeePackage is the import path of the package the auditee's audit
// holds.
const auditeePackage = thisPackage + "/testdata/auditee"

// The auditee's test binary, built once for every case.
var (
	auditeeOnce sync.Once
	auditeeBin  string
	auditeeErr  error
)

// auditeeBinary builds testdata/auditee's test binary into a directory of
// its own, once, and returns its path.
func auditeeBinary(t *testing.T) string {
	t.Helper()
	auditeeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "facttest-auditee-")
		if err != nil {
			auditeeErr = err
			return
		}
		auditeeBin = filepath.Join(dir, "auditee.test")
		if runtime.GOOS == "windows" {
			auditeeBin += ".exe"
		}
		cmd := exec.Command("go", "test", "-c", "-o", auditeeBin, "./testdata/auditee")
		if out, err := cmd.CombinedOutput(); err != nil {
			auditeeErr = errors.New("building testdata/auditee: " + err.Error() + "\n" + string(out))
		}
	})
	if auditeeErr != nil {
		t.Fatal(auditeeErr)
	}
	return auditeeBin
}

// TestMain removes the auditee's binary once every test has run.
func TestMain(m *testing.M) {
	code := m.Run()
	if auditeeBin != "" {
		_ = os.RemoveAll(filepath.Dir(auditeeBin))
	}
	os.Exit(code)
}

// runAuditee runs the auditee's test binary with env added to this one's and
// args as its flags, and returns its exit code and its whole output.
func runAuditee(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(auditeeBinary(t), args...) // #nosec G204 -- a binary this test built
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), out.String()
	}
	t.Fatalf("running the auditee: %v", err)
	return 0, ""
}

func TestMain_AuditsAWholeTestBinary(t *testing.T) {
	fail := "facttest: FAIL: " + auditeePackage + ": "
	note := "facttest: note: " + auditeePackage + ": "
	for _, tc := range []struct {
		name string
		env  []string
		args []string
		// exit is the code wanted, 0 or not.
		pass bool
		// want and refuse are lines the output must and must not hold.
		want, refuse []string
	}{
		{
			name: "a clean package passes, audited as the package an external test package tests",
			pass: true,
			want: []string{note + "held the facts these methods emitted to their declarations: facts.auditee_alpha (2 keys), facts.auditee_beta (1 keys)"},
		},
		{
			name: "Main called as an expression audits the same package",
			env:  []string{"AUDITEE_MAIN=expression"},
			pass: true,
			want: []string{note + "held the facts these methods emitted to their declarations: facts.auditee_alpha (2 keys), facts.auditee_beta (1 keys)"},
		},
		{
			name: "an undeclared fact fails tests that pass",
			env:  []string{"AUDITEE_CASE=refused"},
			want: []string{"PASS", fail + `facts.auditee_alpha emitted fact "auditee.unknown", which it does not declare (Manifest.Facts) (in TestAlpha)`},
		},
		{
			name: "a value of another type fails tests that pass",
			env:  []string{"AUDITEE_CASE=wrongtype"},
			want: []string{"PASS", fail + `facts.auditee_alpha emitted fact "auditee.count", and its value is a string, and the method declares it integer (in TestAlpha)`},
		},
		{
			name: "a declaring method no test drove fails tests that pass",
			env:  []string{"AUDITEE_CASE=undriven"},
			want: []string{"PASS", fail + "facts.auditee_beta declares facts, and no test of " + auditeePackage + " made it emit one through facttest.Observe"},
		},
		{
			name:   "-run reports the coverage it cannot judge",
			env:    []string{"AUDITEE_CASE=undriven"},
			args:   []string{"-test.run=TestAlpha"},
			pass:   true,
			want:   []string{note + "facts.auditee_beta was not driven, and this run cannot say whether a test would: -run chose only some tests"},
			refuse: []string{"facttest: FAIL"},
		},
		{
			name:   "-skip reports the coverage it cannot judge",
			args:   []string{"-test.skip=TestBeta"},
			pass:   true,
			want:   []string{note + "facts.auditee_beta was not driven, and this run cannot say whether a test would: -skip chose only some tests"},
			refuse: []string{"facttest: FAIL"},
		},
		{
			name:   "-short reports the coverage it cannot judge",
			args:   []string{"-test.short"},
			pass:   true,
			want:   []string{note + "facts.auditee_beta was not driven, and this run cannot say whether a test would: -short skips the tests that start a device"},
			refuse: []string{"facttest: FAIL"},
		},
		{
			name: "a partial run still fails a refused fact",
			env:  []string{"AUDITEE_CASE=refused"},
			args: []string{"-test.run=TestAlpha"},
			want: []string{fail + `facts.auditee_alpha emitted fact "auditee.unknown"`},
		},
		{
			name:   "a failing test's own failure is kept when the audit passes",
			env:    []string{"AUDITEE_CASE=testfails"},
			want:   []string{"--- FAIL: TestFails", "this test fails on purpose"},
			refuse: []string{"facttest: FAIL"},
		},
		{
			name: "an Of naming a package with no declaring method fails",
			env:  []string{"AUDITEE_OF=example.com/not/the/package"},
			want: []string{"PASS", `facttest: FAIL: example.com/not/the/package: the audit found no fact-declaring built-in whose Invoke or Check "example.com/not/the/package" declares`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runAuditee(t, append([]string{"AUDITEE_CASE=", "AUDITEE_MAIN=", "AUDITEE_OF="}, tc.env...), tc.args...)
			if (code == 0) != tc.pass {
				t.Errorf("exit code %d, want passing %v; output:\n%s", code, tc.pass, out)
			}
			lines := strings.Split(out, "\n")
			for _, w := range tc.want {
				if !hasLine(lines, w) {
					t.Errorf("no line holds %q; output:\n%s", w, out)
				}
			}
			for _, r := range tc.refuse {
				if hasLine(lines, r) {
					t.Errorf("a line holds %q; output:\n%s", r, out)
				}
			}
		})
	}
}
