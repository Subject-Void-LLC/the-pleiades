// The failure-mode walk of net.frr.config against a real FRRouting router
// (Phase 127b's Adversarial Pattern Justification): an empty candidate, a
// candidate edited between the gate and the change, a backup removed or
// emptied before its rollback, and vtysh present but denied to the account.
// Each is judged by the router's own running configuration read as root,
// never by what the method reported.
package frr_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/frr"
)

// TestConfig_FailureModesLeaveTheRouterAsItWas walks each way a change or
// its undo can be handed something other than what was meant, on r1.
func TestConfig_FailureModesLeaveTheRouterAsItWas(t *testing.T) {
	l := startLab(t)
	r1 := l.Nodes["r1"]
	ctx := context.Background()
	project := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(project, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	rcWithProject := func() *runContext {
		rc := l.rc()
		rc.project = project
		return rc
	}
	unchanged := func(t *testing.T, want, what string) {
		t.Helper()
		if got := l.running(t, "r1"); got != want {
			t.Fatalf("%s changed the router:\n%s\nwant\n%s", what, got, want)
		}
	}
	baseline := l.running(t, "r1")

	t.Run("an empty candidate", func(t *testing.T) {
		write("empty.conf", "")
		if _, err := frr.Config(ctx, rcWithProject(), l.device("r1"), map[string]any{"src": "empty.conf"}); err == nil || !strings.Contains(err.Error(), "the candidate is empty") {
			t.Errorf("an empty src: err %v", err)
		}
		unchanged(t, baseline, "an empty src")
		if _, err := frr.Config(ctx, l.rc(), l.device("r1"), map[string]any{"config": " \n\t\n"}); err == nil || !strings.Contains(err.Error(), "the candidate is empty") {
			t.Errorf("a blank config: err %v", err)
		}
		unchanged(t, baseline, "a blank config")
	})

	t.Run("a candidate edited after the gate", func(t *testing.T) {
		gated := l.candidate(t, "r1", "!\ninterface "+l.link["r1"]+"\n description gated\nexit\n!\n")
		sum := sha256.Sum256([]byte(gated))
		write("r1.conf", strings.Replace(gated, "description gated", "description edited-after-the-gate", 1))
		if _, err := frr.Config(ctx, rcWithProject(), l.device("r1"), map[string]any{"src": "r1.conf", "expect_sha256": hex.EncodeToString(sum[:])}); err == nil || !strings.Contains(err.Error(), "these are not the bytes that were checked") {
			t.Errorf("an edited candidate: err %v", err)
		}
		unchanged(t, baseline, "an edited candidate")
	})

	// change applies a description and returns its undo and the backup.
	change := func(t *testing.T, description string) (map[string]any, string) {
		t.Helper()
		// An earlier change's description, cleared as root, so the
		// candidate below holds one description and FRR runs it as written.
		r1.Exec(t, "vtysh", "-c", "configure terminal", "-c", "interface "+l.link["r1"], "-c", "no description")
		rc := l.rc()
		if _, err := frr.Config(ctx, rc, l.device("r1"), map[string]any{"config": l.candidate(t, "r1", "!\ninterface "+l.link["r1"]+"\n description "+description+"\nexit\n!\n")}); err != nil {
			t.Fatal(err)
		}
		undo, _ := inverseParams(t, rc)
		backup, _ := undo["restore"].(string)
		if backup == "" {
			t.Fatalf("the undo names no backup: %v", undo)
		}
		return undo, backup
	}

	t.Run("a backup removed before its rollback", func(t *testing.T) {
		undo, backup := change(t, "removed-backup")
		after := l.running(t, "r1")
		r1.Exec(t, "rm", "-f", backup)
		if _, err := frr.Config(ctx, l.rc(), l.device("r1"), undo); err == nil || !strings.Contains(err.Error(), backup) {
			t.Errorf("an undo of a removed backup: err %v", err)
		}
		unchanged(t, after, "an undo of a removed backup")
		forced := map[string]any{"restore": backup, "force": true}
		if _, err := frr.Config(ctx, l.rc(), l.device("r1"), forced); err == nil {
			t.Error("a forced undo of a removed backup succeeded")
		}
		unchanged(t, after, "a forced undo of a removed backup")
	})

	t.Run("a backup emptied before its rollback", func(t *testing.T) {
		_, backup := change(t, "emptied-backup")
		after := l.running(t, "r1")
		// Truncated in place, so its owner and its 0600 stay the method's.
		r1.Exec(t, "sh", "-c", ": > '"+backup+"'")
		if _, err := frr.Config(ctx, l.rc(), l.device("r1"), map[string]any{"restore": backup, "force": true}); err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Errorf("a forced undo of an emptied backup: err %v", err)
		}
		unchanged(t, after, "a forced undo of an emptied backup")
		// Blank rather than empty, which frr-reload itself does not refuse:
		// before the check it reloaded the router to no configuration at all.
		r1.Exec(t, "sh", "-c", "printf ' \\n\\t\\n' > '"+backup+"'")
		if _, err := frr.Config(ctx, l.rc(), l.device("r1"), map[string]any{"restore": backup, "force": true}); err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Errorf("a forced undo of a blank backup: err %v", err)
		}
		unchanged(t, after, "a forced undo of a blank backup")
	})

	t.Run("vtysh denied to the account", func(t *testing.T) {
		before := l.running(t, "r1")
		vtysh := strings.TrimSpace(r1.Exec(t, "sh", "-c", "command -v vtysh"))
		backups := r1.Exec(t, "sh", "-c", "ls -1 /home/*/.pleiades-frr 2>/dev/null | wc -l")
		r1.Exec(t, "chmod", "0700", vtysh)
		t.Cleanup(func() { r1.Exec(t, "chmod", "0755", vtysh) })
		if _, err := frr.Facts(ctx, l.rc(), l.device("r1"), map[string]any{}); err == nil || !strings.Contains(err.Error(), "vtysh") {
			t.Errorf("facts with vtysh denied: err %v", err)
		}
		if _, err := frr.Config(ctx, l.rc(), l.device("r1"), map[string]any{"config": l.candidate(t, "r1", "!\ninterface "+l.link["r1"]+"\n description denied\nexit\n!\n")}); err == nil || !strings.Contains(err.Error(), "vtysh") {
			t.Errorf("a change with vtysh denied: err %v", err)
		}
		r1.Exec(t, "chmod", "0755", vtysh)
		unchanged(t, before, "a change with vtysh denied")
		if now := r1.Exec(t, "sh", "-c", "ls -1 /home/*/.pleiades-frr 2>/dev/null | wc -l"); now != backups {
			t.Errorf("a refused change left a backup: %s files, was %s", strings.TrimSpace(now), strings.TrimSpace(backups))
		}
	})
}
