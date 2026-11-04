// A malformed PLEIADES_FACTS_DIGEST_KEY stops the real Controller binary
// before it opens its database or binds its listener, as a malformed master
// key does (FAILURE_PATTERNS 297): an operator who set a key means it to be
// used, and a probe must never see a healthy controller that is already
// exiting.
package main_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFactDigestKeyReleaseGate_AMalformedKeyStopsTheControllerFirst(t *testing.T) {
	dbDir := filepath.Join(t.TempDir(), "never-created")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath)
	cmd.Dir = t.TempDir()
	cmd.Env = append(setupEnv(filepath.Join(dbDir, "controller.db")),
		"JWT_SECRET="+strings.Repeat("j", 40),
		"MASTER_ENCRYPTION_KEY="+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))),
		"PLEIADES_FACTS_DIGEST_KEY=abc")
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("the controller did not refuse a malformed digest key:\n%s", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("the controller ended %v, want a refusal (exit 1):\n%s", err, out)
	}
	if !strings.Contains(string(out), "PLEIADES_FACTS_DIGEST_KEY") {
		t.Errorf("the refusal does not name PLEIADES_FACTS_DIGEST_KEY:\n%s", out)
	}
	if _, statErr := os.Stat(dbDir); !os.IsNotExist(statErr) {
		t.Errorf("the controller created %s, so it migrated its database before refusing the key", dbDir)
	}
	if strings.Contains(string(out), "controller listening") {
		t.Errorf("the controller bound its listener before refusing the key:\n%s", out)
	}
}
