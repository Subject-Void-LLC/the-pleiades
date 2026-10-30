// An undo records the account that made its backup, and an undo run as any
// other account is refused before it reads a thing, naming the account to
// connect as. Run 1 of the blind lab changed the router's login between
// builds and met "restore must name a backup this method made" with no word of
// whose backup it was.
package frr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/frr"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestConfig_AnUndoNamesTheAccountThatMadeItsBackup applies a change, reads
// the undo it recorded, and runs that undo as the right account and as a
// wrong one.
func TestConfig_AnUndoNamesTheAccountThatMadeItsBackup(t *testing.T) {
	l := startLab(t)
	ctx := context.Background()
	baseline := l.running(t, "r2")

	good := l.candidate(t, "r2", "!\ninterface "+l.link["r2"]+"\n description login-probe\nexit\n!\n")
	rc := l.rc()
	if _, err := frr.Config(ctx, rc, l.device("r2"), map[string]any{"config": good}); err != nil {
		t.Fatal(err)
	}
	undo, _ := inverseParams(t, rc)
	if undo["login"] != testsupport.NetLabUser {
		t.Fatalf("the undo records login %v, want %s: %v", undo["login"], testsupport.NetLabUser, undo)
	}
	changed := l.running(t, "r2")

	// Another account's undo is refused with the account named, and the
	// router keeps the change.
	wrong := map[string]any{}
	for k, v := range undo {
		wrong[k] = v
	}
	wrong["login"] = "someone-else"
	_, err := frr.Config(ctx, l.rc(), l.device("r2"), wrong)
	if err == nil {
		t.Fatal("an undo recorded for another account was run")
	}
	for _, want := range []string{"someone-else", testsupport.NetLabUser, "nothing was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	if l.running(t, "r2") != changed {
		t.Error("a refused undo changed the router")
	}

	// A login with no restore is a mistake, not a quiet no-op.
	if _, err := frr.Config(ctx, l.rc(), l.device("r2"), map[string]any{"config": good, "login": "x"}); err == nil {
		t.Error("login without restore was accepted")
	}

	// The right account's undo puts the router back.
	if _, err := frr.Config(ctx, l.rc(), l.device("r2"), undo); err != nil {
		t.Fatal(err)
	}
	if got := l.running(t, "r2"); got != baseline {
		t.Errorf("the undo did not restore the router:\n%s", got)
	}
}
