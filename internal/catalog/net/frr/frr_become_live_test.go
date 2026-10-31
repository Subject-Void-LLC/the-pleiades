// Become against a real router and a real sudo: the account that connects may
// not write /etc/frr, so net.frr.config's save is refused as it is; the same
// task run as root, through sudo -n on the router, saves; its undo records
// root and runs only as root; and an account sudo does not allow is refused
// before anything runs.
package frr_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/frr"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestConfig_BecomeRootSavesWhereTheLoginCannot is the blind lab's failure and
// its fix on one router.
func TestConfig_BecomeRootSavesWhereTheLoginCannot(t *testing.T) {
	l := startLab(t)
	r2 := l.Nodes["r2"]
	root := sdk.WithBecome(context.Background(), "root")
	plain := context.Background()

	// The login may write frr.conf but not its directory.
	r2.Exec(t, "chmod", "g-w,o-w", "/etc/frr")
	t.Cleanup(func() { r2.Exec(t, "chmod", "g+w", "/etc/frr") })

	baseline := l.running(t, "r2")
	good := l.candidate(t, "r2", "!\ninterface "+l.link["r2"]+"\n description became-root\nexit\n!\n")
	params := map[string]any{"config": good, "save": true}

	// As the login, a check and a run both refuse the save.
	if _, err := frr.CheckConfig(plain, l.rc(), l.device("r2"), params); err == nil {
		t.Fatal("as the login, a save into an unwritable /etc/frr was accepted")
	}

	// As root the same task, check and run, is accepted and saves.
	if _, err := frr.CheckConfig(root, l.rc(), l.device("r2"), params); err != nil {
		t.Fatalf("a check as root: %v", err)
	}
	if l.running(t, "r2") != baseline {
		t.Fatal("a check changed the router")
	}
	rc := l.rc()
	if _, err := frr.Config(root, rc, l.device("r2"), params); err != nil {
		t.Fatalf("as root: %v", err)
	}
	now := l.running(t, "r2")
	if !strings.Contains(now, "description became-root") {
		t.Fatalf("the router does not run the change:\n%s", now)
	}
	if conf := r2.Exec(t, "cat", "/etc/frr/frr.conf"); conf != now {
		t.Errorf("frr.conf is not the running configuration:\n%s", conf)
	}

	// The undo records who made the backup: root, not the login.
	undo, _ := inverseParams(t, rc)
	if undo["login"] != "root" {
		t.Fatalf("the undo records login %v, want root: %v", undo["login"], undo)
	}
	// Run as the login, it is refused before anything is read, naming root.
	_, err := frr.Config(plain, l.rc(), l.device("r2"), undo)
	if err == nil || !strings.Contains(err.Error(), "was made by the account root") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Errorf("the undo as the login: %v", err)
	}
	if l.running(t, "r2") != now {
		t.Error("a refused undo changed the router")
	}
	// Run as root, it puts the router back.
	if _, err := frr.Config(root, l.rc(), l.device("r2"), undo); err != nil {
		t.Fatalf("the undo as root: %v", err)
	}
	if got := l.running(t, "r2"); got != baseline {
		t.Errorf("the undo did not restore the router:\n%s", got)
	}
}

// TestBecome_AnAccountSudoDoesNotAllowIsRefusedUpFront: the lab's rule lets the
// login become root and frr and no one else, so becoming nobody is refused
// when the connection is made, naming sudo's own words, and nothing runs.
func TestBecome_AnAccountSudoDoesNotAllowIsRefusedUpFront(t *testing.T) {
	l := startLab(t)
	before := l.running(t, "r2")
	good := l.candidate(t, "r2", "!\ninterface "+l.link["r2"]+"\n description never\nexit\n!\n")

	_, err := frr.Config(sdk.WithBecome(context.Background(), "nobody"), l.rc(), l.device("r2"), map[string]any{"config": good})
	if err == nil || !strings.Contains(err.Error(), "cannot run commands as nobody through sudo without a password") || !strings.Contains(err.Error(), "NOPASSWD") {
		t.Fatalf("becoming an account sudo does not allow: %v", err)
	}
	if l.running(t, "r2") != before {
		t.Error("a refused become changed the router")
	}

	// An account the rule does allow works, and is the one commands run as.
	rc := l.rc()
	if _, err := frr.Facts(sdk.WithBecome(context.Background(), "frr"), rc, l.device("r2"), map[string]any{"include_config": false}); err != nil {
		t.Errorf("become frr: %v", err)
	}
}
