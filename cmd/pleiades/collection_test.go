// Package main_test: tests of the `pleiades collection` commands.
package main_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// promptProgram is a shell program answering describe with one method,
// enough for `collection approve` to show what it would approve.
const promptProgram = `#!/bin/sh
if [ "$1" = describe ]; then
  printf '{"protocol":1,"methods":[{"name":"clitest.prompt.run","manifest":{"status":"implemented","supportsCheck":false}}]}'
fi
`

// runWithStdin runs the real binary in dir with env over this process's
// environment and stdin as its standard input.
func runWithStdin(t *testing.T, dir string, env map[string]string, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestCLI_CollectionApproveAsksAndRecords drives the approval commands
// through the real binary: approving shows the program and asks, "n"
// records nothing and "y" records the build; --digest records a build
// without running anything and refuses a malformed digest; revoke and
// list do what they say.
func TestCLI_CollectionApproveAsksAndRecords(t *testing.T) {
	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collections, "prompt"), []byte(promptProgram), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_COLLECTIONS_DIR": collections}
	dir := t.TempDir()

	out, err := runWithStdin(t, dir, env, "n\n", "collection", "approve", "prompt")
	if err == nil || !strings.Contains(out, "clitest.prompt.run") || !strings.Contains(out, "not approved") {
		t.Fatalf("answering n = %v, want the method shown and nothing approved:\n%s", err, out)
	}
	if listed, _ := runWithStdin(t, dir, env, "", "collection", "list"); !strings.Contains(listed, "no approved builds") {
		t.Fatalf("answering n recorded an approval:\n%s", listed)
	}

	out, err = runWithStdin(t, dir, env, "y\n", "collection", "approve", "prompt")
	if err != nil || !strings.Contains(out, "approved prompt (sha256:") {
		t.Fatalf("answering y = %v:\n%s", err, out)
	}

	next := "sha256:" + strings.Repeat("b", 64)
	if out, err := runWithStdin(t, dir, env, "", "collection", "approve", "prompt", "--digest", next); err != nil {
		t.Fatalf("approving by digest = %v:\n%s", err, out)
	}
	if out, err := runWithStdin(t, dir, env, "", "collection", "approve", "prompt", "--digest", "sha256:nope"); err == nil {
		t.Errorf("a malformed digest was approved:\n%s", out)
	}
	listed, err := runWithStdin(t, dir, env, "", "collection", "list")
	if err != nil || strings.Count(listed, "prompt  sha256:") != 2 {
		t.Fatalf("collection list = %v, want both builds:\n%s", err, listed)
	}

	if out, err := runWithStdin(t, dir, env, "", "collection", "revoke", "prompt", "--digest", next); err != nil || !strings.Contains(out, "revoked 1") {
		t.Errorf("revoking one build = %v:\n%s", err, out)
	}
	if out, err := runWithStdin(t, dir, env, "", "collection", "revoke", "prompt"); err != nil || !strings.Contains(out, "revoked 1") {
		t.Errorf("revoking the rest = %v:\n%s", err, out)
	}
	if out, err := runWithStdin(t, dir, env, "", "collection", "revoke", "prompt"); err == nil {
		t.Errorf("revoking what is not there succeeded:\n%s", out)
	}
	if out, err := runWithStdin(t, dir, map[string]string{"PLEIADES_COLLECTIONS_DIR": ""}, "", "collection", "list"); err == nil || !strings.Contains(out, "PLEIADES_COLLECTIONS_DIR is not set") {
		t.Errorf("list with no directory = %v:\n%s", err, out)
	}
}

// TestCLI_AProgramCannotClaimACatalogNamespace proves, through the real
// binary with the whole built-in catalog registered, that an approved
// external program still cannot provide a method in a namespace the
// catalog uses: a name like file.* can only mean code that ships with
// The Pleiades.
func TestCLI_AProgramCannotClaimACatalogNamespace(t *testing.T) {
	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	impostor := strings.ReplaceAll(promptProgram, "clitest.prompt.run", "file.impostor.write")
	if err := os.WriteFile(filepath.Join(collections, "impostor"), []byte(impostor), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}
	env := map[string]string{"PLEIADES_COLLECTIONS_DIR": collections}
	dir := t.TempDir()
	if out, err := runWithStdin(t, dir, env, "", "collection", "approve", "impostor", "--yes"); err != nil {
		t.Fatalf("approving: %v\n%s", err, out)
	}
	out, err := runWithStdin(t, dir, env, "", "doc", "--list")
	if err == nil || !strings.Contains(out, `"file" namespace, which belongs to The Pleiades itself`) {
		t.Fatalf("doc --list = %v, want the impostor refused for its namespace:\n%s", err, out)
	}
}

// TestCLI_AProgramCannotDeclareABuiltInFact proves, through the real binary
// with the whole built-in catalog registered, that an approved external
// program cannot declare a fact a built-in holds, a security rule: a spoofed
// fact would steer targeting and baselines. A built-in's exact key with the
// built-in's own kind and type (which used to load) and a reserved family are
// both refused, each naming what holds it; one under the program's own
// namespace loads.
func TestCLI_AProgramCannotDeclareABuiltInFact(t *testing.T) {
	for _, tc := range []struct {
		name, fact, want string
	}{
		{name: "spoof", fact: `{"key":"devices","kind":"volatile","type":"list","description":"x"}`, want: `declares fact "devices", which built-in method "net.catalyst.`},
		{name: "family", fact: `{"key":"package.","prefix":true,"item_names":true,"kind":"stable","type":"object","description":"x"}`, want: `declares fact "package.", which the reserved family "package."* holds`},
		{name: "own", fact: `{"key":"clitest.widgets","kind":"stable","type":"integer","description":"widgets"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			collections := t.TempDir()
			if err := os.Chmod(collections, 0o700); err != nil {
				t.Fatal(err)
			}
			program := strings.Replace(promptProgram, `"supportsCheck":false}`, `"supportsCheck":false,"reversibility":{"readOnly":true,"notes":"a test"},"facts":[`+tc.fact+`]}`, 1)
			if err := os.WriteFile(filepath.Join(collections, "facts"), []byte(program), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
				t.Fatal(err)
			}
			env := map[string]string{"PLEIADES_COLLECTIONS_DIR": collections}
			dir := t.TempDir()
			if out, err := runWithStdin(t, dir, env, "", "collection", "approve", "facts", "--yes"); err != nil {
				t.Fatalf("approving: %v\n%s", err, out)
			}
			out, err := runWithStdin(t, dir, env, "", "doc", "--list")
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("a fact under the program's own namespace was refused: %v\n%s", err, out)
			case tc.want != "" && (err == nil || !strings.Contains(out, tc.want)):
				t.Fatalf("doc --list = %v, want a refusal containing %q:\n%s", err, tc.want, out)
			}
		})
	}
}

// TestCLI_CollectionApproveEscapesTheProgramsText proves the approve
// prompt shows a program's own text escaped. The program describes itself
// with a summary that clears the screen and draws a fake "approved" line;
// the real binary must show the escape as text and keep the fake line
// inside the summary's own line, where it cannot pass for the command's
// answer.
func TestCLI_CollectionApproveEscapesTheProgramsText(t *testing.T) {
	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	escape := string(rune(0x1b))
	desc, err := json.Marshal(external.Description{Protocol: external.ProtocolVersion, Methods: []external.DescribedMethod{{
		Name: "clitest.evil.run",
		Manifest: collection.Manifest{
			Status: collection.StatusImplemented,
			Doc:    collection.Doc{Summary: "harmless" + escape + "[2J\napproved evil (sha256:forged)"},
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	descPath := filepath.Join(collections, ".describe.json")
	if err := os.WriteFile(descPath, desc, 0o600); err != nil {
		t.Fatal(err)
	}
	program := "#!/bin/sh\n[ \"$1\" = describe ] && cat '" + descPath + "'\n"
	if err := os.WriteFile(filepath.Join(collections, "evil"), []byte(program), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
		t.Fatal(err)
	}

	out, _ := runWithStdin(t, t.TempDir(), map[string]string{"PLEIADES_COLLECTIONS_DIR": collections}, "n\n", "collection", "approve", "evil")
	if strings.Contains(out, escape) {
		t.Errorf("the prompt printed a raw escape character:\n%q", out)
	}
	if !strings.Contains(out, `harmless\x1b[2J\napproved evil`) {
		t.Errorf("the prompt did not show the program's text escaped:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "approved ") {
			t.Errorf("the program's text produced a line of its own: %q", line)
		}
	}
}

// TestCLI_CollectionListNamesEachBuildsProtocol proves, through the real
// binary, that `collection list` names the protocols this build speaks and
// that --describe names, for each approved build present, the protocol the
// two agree on: supported for a program speaking 1, unsupported naming both
// sides for one speaking only 2 to 3, and "not this build" for an approval
// whose digest the program in the directory no longer has.
func TestCLI_CollectionListNamesEachBuildsProtocol(t *testing.T) {
	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	newer := strings.Replace(promptProgram, `"protocol":1`, `"protocol":3,"protocol_min":2,"protocol_max":3`, 1)
	// replaced stands for bytes swapped in after approval: its describe
	// fails, so running it would print "unknown"; it must not be run at all.
	replaced := "#!/bin/sh\nexit 1\n"
	for name, body := range map[string]string{"prompt": promptProgram, "newer": newer, "replaced": replaced} {
		if err := os.WriteFile(filepath.Join(collections, name), []byte(body), 0o700); err != nil { // #nosec G306 -- a test program that must be executable
			t.Fatal(err)
		}
	}
	env := map[string]string{"PLEIADES_COLLECTIONS_DIR": collections}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"collection", "approve", "prompt", "--yes"},
		{"collection", "approve", "prompt", "--digest", "sha256:" + strings.Repeat("c", 64)},
		{"collection", "approve", "newer", "--digest", fileDigest(t, filepath.Join(collections, "newer"))},
		{"collection", "approve", "replaced", "--digest", "sha256:" + strings.Repeat("e", 64)},
	} {
		if out, err := runWithStdin(t, dir, env, "", args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	listed, err := runWithStdin(t, dir, env, "", "collection", "list")
	if err != nil || !strings.Contains(listed, "this build speaks external collection protocols: 1") || strings.Contains(listed, "protocol: ") {
		t.Fatalf("list without --describe = %v, want the window and no program run:\n%s", err, listed)
	}
	described, err := runWithStdin(t, dir, env, "", "collection", "list", "--describe")
	if err != nil {
		t.Fatalf("list --describe: %v\n%s", err, described)
	}
	for _, want := range []string{"protocol: 1, supported", "protocol: not this build", "protocol: unsupported ("} {
		if !strings.Contains(described, want) {
			t.Errorf("list --describe lacks %q:\n%s", want, described)
		}
	}
	if strings.Contains(described, "protocol: unknown") || strings.Count(described, "protocol: not this build") != 2 {
		t.Errorf("a build whose bytes are not the approved ones was run, or not named:\n%s", described)
	}
	// The newer program is refused at describe for its range: say which
	// versions each side speaks.
	if !strings.Contains(described, "describe speaks protocol 2 to 3, and this build speaks protocol 1") {
		t.Errorf("list --describe does not name both sides' protocols for the newer program:\n%s", described)
	}
}

// fileDigest is the sha256:<hex> digest of the file at path, as the loader
// computes it.
func fileDigest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- a file this test wrote
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}
