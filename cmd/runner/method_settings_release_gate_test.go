// Package main_test: the Walk-tier Release Gate for method settings, a run's
// values for the parameters a method declares a run may set
// (collection.Param.RunSetting), carried on the dispatch's launch fields
// (launch.MethodSettingsField) to the native Adapter, its engine and the
// per-task child that runs the method.
package main_test

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runner"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// settingsGatePath is a file the gate's login may not remove: /etc is not
// writable for it. Only ever checked, never removed.
const settingsGatePath = "/etc/passwd"

// TestMethodSettingsReleaseGate_ARunSettingReachesTheMethodOnARunner proves
// method settings on the Walk tier against real NATS, the real Agent and
// native Adapter, and a real sshd: a check of file.remove on a path the login
// may not remove is refused by default, completes with the job's
// method_settings saying warn, is still refused when the task itself says
// refuse, and a setting no method declares refuses the dispatch.
func TestMethodSettingsReleaseGate_ARunSettingReachesTheMethodOnARunner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Walk-tier method settings Release Gate container test in short mode")
	}
	header := "# Checks removing a file the login may not remove, never really.\n# Changes: nothing; the gate only checks.\n# Undo: none needed.\n"
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{
		"remove.yaml": header + "id: remove\ntasks:\n  - name: remove-a-file\n    file.remove:\n      path: " + settingsGatePath + "\n",
		"refuse.yaml": header + "id: refuse\ntasks:\n  - name: remove-a-file\n    file.remove:\n      path: " + settingsGatePath + "\n      check_forbidden: refuse\n",
	})
	h.startCheckAgent(t)
	addr := net.JoinHostPort(h.sshHost, strconv.Itoa(h.sshPort))

	logs := map[string]*jobWatch{}
	check := func(t *testing.T, runbookID string, settings map[string]any) runner.ResultEntry {
		t.Helper()
		payload := wire.DispatchPayload{
			JobID: uuid.New().String(), RunbookID: runbookID, Mode: "check",
			DeviceID: "release-gate-device", DeviceName: "release-gate-device",
			DeviceHost: h.sshHost, SSHPort: h.sshPort,
			Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
			Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		}
		if settings != nil {
			payload.Fields = map[string]any{launch.MethodSettingsField: settings}
		}
		results := h.results(t, payload.JobID)
		logs[runbookID+fmtSettings(settings)] = h.watchJob(t, payload.JobID)
		h.publish(t, topology.CheckSubject(payload.DeviceID), payload)
		select {
		case entry := <-results:
			return entry
		case <-time.After(45 * time.Second):
			t.Fatalf("the check of %s reported no result", runbookID)
			return runner.ResultEntry{}
		}
	}
	warn := map[string]any{"file.remove.check_forbidden": "warn"}

	if got := check(t, "remove", nil); got.Outcome != "failed" || !strings.Contains(got.Reason, "file.remove.check_forbidden=warn") {
		t.Errorf("the check with no setting = %+v, want it refused naming the run setting", got)
	}
	if got := check(t, "remove", warn); got.Outcome != "completed" {
		t.Errorf("the check with method_settings saying warn = %+v, want it completed", got)
	}
	if !logs["remove"+fmtSettings(warn)].logHolds(t, "may not remove "+settingsGatePath, 10*time.Second) {
		t.Error("the check with method_settings saying warn logged no warning naming the path")
	}
	if got := check(t, "refuse", warn); got.Outcome != "failed" || !strings.Contains(got.Reason, "refusing") {
		t.Errorf("a task saying refuse under method_settings saying warn = %+v, want the task's refusal", got)
	}
	if got := check(t, "remove", map[string]any{"file.remove.recurse": "true"}); got.Outcome != "failed" || !strings.Contains(got.Reason, "declares no run setting") {
		t.Errorf("a setting file.remove does not declare = %+v, want the dispatch refused naming it", got)
	}
	if got := readOverSSH(t, addr, "if [ -e "+settingsGatePath+" ]; then echo present; else echo absent; fi"); got != "present" {
		t.Fatal("a check removed the file")
	}
}

// fmtSettings is settings as a stable key for the job it ran under.
func fmtSettings(settings map[string]any) string {
	if settings == nil {
		return ""
	}
	raw, _ := json.Marshal(settings)
	return string(raw)
}

// logHolds reports whether any of the job's log events, read off its real log
// subject, holds want within the bound.
func (w *jobWatch) logHolds(t *testing.T, want string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		msgs, err := w.consumer.Fetch(8, jetstream.FetchMaxWait(time.Second))
		if err != nil {
			t.Fatalf("fetch log events: %v", err)
		}
		for msg := range msgs.Messages() {
			if strings.Contains(string(msg.Data()), want) {
				return true
			}
		}
	}
	return false
}
