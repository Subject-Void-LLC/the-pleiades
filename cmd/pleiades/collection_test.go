// Package main_test: tests of the `pleiades collection` commands.
package main_test

import (
	"encoding/json"
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
