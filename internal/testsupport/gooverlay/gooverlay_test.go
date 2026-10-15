// Tests that a tree's generated source builds and tests as a member of the
// module while the module's own directory is never written.
package gooverlay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot is this module's directory, three levels above this package.
func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// stopped is what fatalRecorder panics with, so a test can tell a Fatalf
// it provoked from a real failure.
type stopped struct{ msg string }

// fatalRecorder is a testing.TB whose Fatalf stops the call it was made
// in, as testing.T's does, and keeps the message.
type fatalRecorder struct{ testing.TB }

func (fatalRecorder) Helper() {}
func (fatalRecorder) Fatalf(format string, args ...any) {
	panic(stopped{fmt.Sprintf(format, args...)})
}

// fatalOf runs call against a fatalRecorder over t and returns the message
// it stopped with, or "" when it returned.
func fatalOf(t *testing.T, call func(testing.TB)) (msg string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			s, ok := r.(stopped)
			if !ok {
				panic(r)
			}
			msg = s.msg
		}
	}()
	call(fatalRecorder{t})
	return ""
}

// TestTree_BuildsAndTestsAPackageTheModuleNeverHolds generates a package
// and a test-only package importing it, builds and tests both as members of
// the module, and finds neither directory in the module afterwards: the go
// command read them through the overlay alone.
func TestTree_BuildsAndTestsAPackageTheModuleNeverHolds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	root := moduleRoot(t)
	tr := New(t, root)
	name := fmt.Sprintf("gooverlaytest%d", os.Getpid())
	lib := "internal/testsupport/gooverlay/" + name
	user := lib + "user"
	tr.Write(t, lib+"/lib.go", []byte("// Package "+name+" is generated.\npackage "+name+"\n\n// Answer is generated.\nfunc Answer() int { return 42 }\n"))
	tr.Write(t, lib+"/lib_test.go", []byte("package "+name+"\n\nimport \"testing\"\n\nfunc TestAnswer(t *testing.T) {\n\tif Answer() != 42 {\n\t\tt.Fatal(Answer())\n\t}\n}\n"))
	tr.Write(t, user+"/user_test.go", []byte("package user\n\nimport (\n\t\"testing\"\n\n\tlib \"github.com/Subject-Void-LLC/the-pleiades/"+lib+"\"\n)\n\nfunc TestImportsTheGeneratedPackage(t *testing.T) {\n\tt.Log(lib.Answer())\n}\n"))

	tr.Build(t, tr.module+"/"+lib)
	if out := tr.Test(t, tr.module+"/"+lib); !strings.Contains(out, "--- PASS: TestAnswer") {
		t.Errorf("the generated test did not run:\n%s", out)
	}
	if out := tr.Test(t, tr.module+"/"+user, "-test.run", "TestImportsTheGeneratedPackage"); !strings.Contains(out, "--- PASS: TestImportsTheGeneratedPackage") {
		t.Errorf("the importing test did not run:\n%s", out)
	}
	for _, rel := range []string{lib, user} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s exists in the module (err %v): the tree wrote into it", rel, err)
		}
	}
}

// TestTree_AFailingGeneratedTestFailsTheCaller: a generated test that fails
// is the caller's failure, never a pass.
func TestTree_AFailingGeneratedTestFailsTheCaller(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command")
	}
	tr := New(t, moduleRoot(t))
	name := fmt.Sprintf("gooverlayfail%d", os.Getpid())
	rel := "internal/testsupport/gooverlay/" + name
	tr.Write(t, rel+"/fail_test.go", []byte("package "+name+"\n\nimport \"testing\"\n\nfunc TestFails(t *testing.T) { t.Fatal(\"generated failure\") }\n"))
	msg := fatalOf(t, func(tb testing.TB) { tr.Test(tb, tr.module+"/"+rel) })
	if !strings.Contains(msg, "generated failure") {
		t.Fatalf("a failing generated test stopped with %q", msg)
	}
}

// TestTree_RefusesWhatItCannotStandIn: a package outside the module, a
// build with nothing generated, and a root with no go.mod each stop the
// caller, naming why.
func TestTree_RefusesWhatItCannotStandIn(t *testing.T) {
	tr := New(t, moduleRoot(t))
	for name, call := range map[string]func(testing.TB){
		"a package outside the module": func(tb testing.TB) { tr.Test(tb, "example.com/elsewhere") },
		"nothing generated":            func(tb testing.TB) { tr.Build(tb, tr.module+"/pkg/nothing") },
		"a root with no go.mod":        func(tb testing.TB) { New(tb, t.TempDir()) },
	} {
		if msg := fatalOf(t, call); msg == "" {
			t.Errorf("%s: the call returned", name)
		}
	}
}

// TestModulePath reads the module directive, quoted or not, and refuses a
// go.mod without one.
func TestModulePath(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]string{
		"module example.com/m\n\ngo 1.26\n":      "example.com/m",
		"// comment\nmodule \"example.com/q\"\n": "example.com/q",
		"go 1.26\n":                              "",
	} {
		path := filepath.Join(dir, "go.mod")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := modulePath(path)
		if got != want || (want == "") != (err != nil) {
			t.Errorf("modulePath(%q) = %q, %v; want %q", body, got, err, want)
		}
	}
}
