//go:build integration

// Phase 84's compose upgrade gate: the previous release's stack, brought up
// with the previous release's own `make up`, upgraded by this checkout's
// `make up`, and then rolled back by restoring the backup that upgrade took.
//
// The previous release runs from its own tree (previous_build_test.go) on
// images tagged :previous, through a compose override file. They are never
// built as :dev: ensurePleiadesImages decides whether this checkout's images
// need rebuilding by comparing :dev's age with the sources, and an old tree's
// image built later than the sources would pass that check and quietly stand
// in for this checkout in every other gate.
package e2e

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// previousComposeOverride writes the compose override that runs every built
// service on the previous release's images, and returns its path.
func previousComposeOverride(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "previous.override.yml")
	body := "services:\n" +
		"  controller:\n    image: pleiades/controller:previous\n" +
		"  setup:\n    image: pleiades/controller:previous\n" +
		"  runner:\n    image: pleiades/runner:previous\n" +
		"  backup:\n    image: pleiades/backup:previous\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// controllerCertificate is the serving certificate the stack's controller
// holds right now.
func controllerCertificate(t *testing.T, root string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cert.pem")
	mustRunPackagingTool(t, root, nil, "", "docker", "compose", "cp", "controller:/data/tls/cert.pem", path)
	pem, err := os.ReadFile(path) // #nosec G304 -- a path this test just created
	if err != nil {
		t.Fatal(err)
	}
	return pem
}

// stillSignedIn reports whether client's session cookie still reaches an
// authenticated page.
func stillSignedIn(t *testing.T, client *http.Client) bool {
	t.Helper()
	resp, err := client.Get("https://localhost:8080/ui/dashboard")
	if err != nil {
		t.Fatalf("GET /ui/dashboard: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK && !strings.Contains(string(body), "Sign in")
}

// TestUpgradeReleaseGate_ComposeUpgradesAndRollsBack is the gate.
func TestUpgradeReleaseGate_ComposeUpgradesAndRollsBack(t *testing.T) {
	requireDockerDaemon(t)
	requirePackagingTool(t, "make", "run the make targets an operator types")
	requirePackagingTool(t, "git", "extract the previous release")
	root := ensurePleiadesImages(t)
	prev := requirePreviousBuild(t)
	t.Logf("upgrading from %s across %v", prev.ref, prev.crossed)
	freshComposeStack(t, root)

	g := backupGate{root: root, setupDir: t.TempDir(), backupDir: t.TempDir()}
	adminSetup := "SETUP_FLAGS=--non-interactive --admin-email " + composeGateEmail + " --password-stdin"
	previousEnv := append(g.env(), "COMPOSE_FILE="+filepath.Join(prev.tree, "docker-compose.yml")+":"+previousComposeOverride(t))
	previousMake := func(stdin string, args ...string) string {
		t.Helper()
		out, err := runPackagingTool(t, prev.tree, previousEnv, stdin, "make",
			append([]string{"--no-print-directory", "BACKUP_DIR=" + g.backupDir}, args...)...)
		if err != nil {
			// The controller's own log says why a container went unhealthy,
			// and the cleanup that follows takes the stack, and it, away.
			logs, _ := runPackagingTool(t, prev.tree, previousEnv, "", "docker", "compose", "logs", "--no-color", "--tail", "40", "controller")
			t.Fatalf("the previous release's make %s: %v\n%s\ncontroller log:\n%s", strings.Join(args, " "), err, out, logs)
		}
		return out
	}

	// 1. The previous release, installed the way it documents, holding data
	// and a signed-in browser session.
	g.emptyEnvFile(t)

	// The previous release's images are built here, from its own tree, rather
	// than left to its `make up`: that Makefile builds the controller only as
	// a side effect of setup and never the runner (FAILURE_PATTERNS.md #285),
	// so a :previous runner left by an earlier run, from another previous
	// release, would stand in for this one unnoticed.
	//
	// One at a time: each is a full Go build inside a container, sized to
	// every CPU the machine has and outside any limit a test can set, and
	// three of them at once is more memory than a small developer machine has.
	for _, service := range []string{"controller", "runner", "backup"} {
		if out, err := runPackagingTool(t, prev.tree, previousEnv, "", "docker", "compose", "build", service); err != nil {
			t.Fatalf("building the previous release's %s image: %v\n%s", service, err, out)
		}
	}
	previousMake(composeGatePassword+"\n", "up", adminSetup)
	browser := composeTLSClient(t, root)
	signInOverTLS(t, browser)
	certBefore := controllerCertificate(t, root)
	keyBefore, jwtBefore := readSetupEnvFile(t, filepath.Join(g.setupDir, ".env"))
	typeID := g.addCredential(t, "made before the upgrade", 0)

	// 2a. A backup that cannot be written stops the upgrade before anything
	// is migrated. The next make up still finding an upgrade to back up
	// (step 2's own check) is what proves nothing was.
	if len(prev.crossed) > 0 {
		if os.Geteuid() == 0 {
			t.Log("running as root, which can write to any directory, so the failed-backup refusal is not exercised")
		} else {
			failing := g
			failing.backupDir = filepath.Join(t.TempDir(), "read-only-backups")
			if err := os.Mkdir(failing.backupDir, 0o500); err != nil {
				t.Fatal(err)
			}
			out, err := failing.make(t, "", "up")
			if err == nil || !strings.Contains(out, "the backup failed, so the database was NOT upgraded") {
				t.Fatalf("make up with a backup it cannot write = %v; want it to refuse to upgrade:\n%s", err, out)
			}
		}
	}

	// 2. This checkout's make up: it asks, backs up, and upgrades.
	out := g.mustMake(t, "", "up")
	if len(prev.crossed) > 0 {
		if !strings.Contains(out, "this build upgrades the database, so it is backed up first") {
			t.Fatalf("make up did not say it was backing up before the upgrade:\n%s", out)
		}
		if n := len(g.backups(t)); n != 1 {
			t.Fatalf("make up left %d backups; want the one taken before the upgrade", n)
		}
	}
	// The rollback half below restores the backup the upgrade took, and
	// `make up` takes one only when there is a migration to cross. When the
	// previous build and this one share a schema there is no such backup,
	// which is correct behavior and not something to roll back from.
	//
	// This used to be an unguarded index and it panicked. The two
	// assertions above are already conditioned on prev.crossed; this line
	// was not, so it was reachable exactly when they were skipped. It needs
	// a DIRTY tree with no migration in it to happen, which is why it
	// survived Phase 84: that phase's own branch added a migration, so its
	// author could not reach this path. Every later session working
	// uncommitted, which this repository's own rule makes the normal state,
	// reaches it.
	taken := g.backups(t)
	if len(taken) == 0 {
		// Distinguish the legitimate case from the defect it would
		// otherwise hide: no backup WITH a crossed migration means the
		// upgrade skipped a backup it owed, which is the whole thing this
		// gate exists to catch.
		if len(prev.crossed) > 0 {
			t.Fatalf("make up crossed %v and left no backup; the upgrade must back up before it migrates", prev.crossed)
		}
		t.Logf("the previous build %s shares this build's schema (no migrations crossed), so make up correctly took no backup and there is no pre-upgrade state to restore; the upgrade half above still ran in full", prev.ref)
		return
	}
	preUpgrade := filepath.Join(g.backupDir, taken[0])

	// What an upgrade keeps: the data, the key (every sealed value still
	// opens under it, counted by a backup in this build's image), the JWT
	// secret, the browser session and the serving certificate.
	if names := g.credentialNames(t); !strings.Contains(names, "made before the upgrade") {
		t.Fatalf("after the upgrade the credentials are %s", names)
	}
	keyAfter, jwtAfter := readSetupEnvFile(t, filepath.Join(g.setupDir, ".env"))
	if !bytes.Equal(keyBefore, keyAfter) || jwtBefore != jwtAfter {
		t.Fatal("the upgrade changed a secret in .env")
	}
	if !stillSignedIn(t, browser) {
		t.Error("the browser session from before the upgrade no longer signs in")
	}
	if !bytes.Equal(certBefore, controllerCertificate(t, root)) {
		t.Error("the upgrade replaced the controller's serving certificate")
	}
	if out := g.mustMake(t, "", "backup"); !strings.Contains(out, "They open under key") {
		t.Fatalf("after the upgrade not every sealed value opens under the key:\n%s", out)
	}

	// 3. A second make up is an ordinary start, not an upgrade: no backup.
	before := len(g.backups(t))
	if out := g.mustMake(t, "", "up"); strings.Contains(out, "backed up first") || len(g.backups(t)) != before {
		t.Fatalf("make up on an upgraded stack took another backup:\n%s", out)
	}

	// 4. Rolling back past the window, the way docs/10 says: the previous
	// release's own `make restore`, run against the live upgraded stack,
	// which brings the previous release's stack back up. That restore sets
	// the upgraded database aside before replacing it, which only a build
	// with Phase 84's rollback window can do.
	g.addCredential(t, "made after the upgrade", typeID)
	if !prev.phase84 {
		// TRANSITIONAL, removed at 1.0 gold with the other allowances for
		// builds before Phase 84 (FAILURE_PATTERNS.md #286). Such a build
		// refuses to set aside a database holding a newer build's tables, so
		// the documented path cannot run against it. First prove that is the
		// only reason, then roll back from a dropped stack, and say so, so a
		// run like this is never read as proof of the documented path.
		out, err := runPackagingTool(t, prev.tree, previousEnv, "", "make",
			"--no-print-directory", "BACKUP_DIR="+g.backupDir, "restore", "BACKUP="+preUpgrade)
		if err == nil {
			t.Fatalf("the pre-Phase 84 release %s restored over the live upgraded stack, so it can take the documented path: remove this transitional branch", prev.ref)
		}
		if !strings.Contains(out, "could not be backed up first") {
			t.Fatalf("the pre-Phase 84 release's restore failed for a reason other than setting the newer database aside:\n%s", out)
		}
		t.Logf("the previous release %s predates Phase 84 and cannot set aside a newer database, so this run rolls back from a dropped stack, not by the documented path", prev.ref)
		composeDown(t, root)
	}
	previousMake("", "restore", "BACKUP="+preUpgrade)
	names := g.credentialNames(t)
	if !strings.Contains(names, "made before the upgrade") || strings.Contains(names, "made after the upgrade") {
		t.Fatalf("after rolling back the credentials are %s", names)
	}
	signInOverTLS(t, composeTLSClient(t, root))
}
