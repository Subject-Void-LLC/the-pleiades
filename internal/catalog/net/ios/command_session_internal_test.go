// Package ios: net.ios.command over a real SSH session, against a
// scripted device that asks the questions IOS asks (pkg/netcli's own
// tests prove the dialog against bytes shaped like a real device's; these
// prove the method reads its parameters, reaches the device, answers, and
// records what happened), and a fake Ollama host for ask.
package ios

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	copyLine = "copy http://192.0.2.5/a.bin flash:a.bin"
	copied   = "Accessing http://192.0.2.5/a.bin...\r\nLoading a.bin !!!!\r\n[OK - 5 bytes]\r\n5 bytes copied in 0.105 secs (48 bytes/sec)"
)

// askingDevice starts a scripted IOS device whose copy asks for a
// destination filename and then whether to overwrite, and whose reload
// asks to confirm and then drops the session.
func askingDevice(t *testing.T) (*remoteexectest.Server, *sshDevice) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt: "sw1#",
		Replies: map[string]string{
			copyLine:                       copied,
			"copy flash:missing.bin null:": "%Error opening flash:missing.bin (No such file or directory)",
		},
		Asks: map[string][]remoteexectest.Question{
			copyLine: {
				{Text: "Destination filename [a.bin]? "},
				{Text: "%Warning:There is a file already existing with this name\r\nDo you want to over write? [confirm]", Declined: "%Error: copy aborted"},
			},
			"reload": {{Text: "Proceed with reload? [confirm]"}},
		},
		Hangup: map[string]bool{"reload": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv, &sshDevice{
		Stub: &inventorytest.Stub{StubName: "sw1", Caps: []capability.Name{capability.NameSSHTransport}},
		host: srv.Host, port: srv.Port,
	}
}

// destination and overwrite are the copy's two questions as a task lists
// them.
var (
	destination = map[string]any{"match": `Destination filename \[[^\]]*\]\?`, "answer": ""}
	overwrite   = map[string]any{"match": `over ?write\? \[confirm\]`, "answer": ""}
)

func TestCommand_AnswersTheQuestionsACommandAsks(t *testing.T) {
	srv, device := askingDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	result, err := Command(context.Background(), rc, device, sessionParams(map[string]any{
		"command": copyLine, "prompts": []any{destination, overwrite},
	}))
	if err != nil || !result.Changed {
		t.Fatalf("result %+v, err %v", result, err)
	}
	if got := srv.Commands(); strings.Join(got, "|") != "terminal length 0|"+copyLine+"||" {
		t.Errorf("the device received %q: the command, then Enter for each question", got)
	}
	if out, _ := rc.stats[statCommandStdout].(string); !strings.Contains(out, "bytes copied") || strings.Contains(out, "sw1#") {
		t.Errorf("stdout %q", out)
	}
	exchanges, _ := rc.stats["exchanges"].([]any)
	if len(exchanges) != 2 || exchanges[1].(map[string]any)["prompt"] != 1 {
		t.Errorf("exchanges %v", exchanges)
	}
}

func TestCommand_ARefusedQuestionFailsAndIsNotAnswered(t *testing.T) {
	srv, device := askingDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	_, err := Command(context.Background(), rc, device, sessionParams(map[string]any{
		"command": copyLine, "prompts": []any{destination, map[string]any{"match": `over ?write\?`, "refuse": true}},
	}))
	if err == nil || !strings.Contains(err.Error(), "net.ios.command") || !strings.Contains(err.Error(), "refuses") {
		t.Fatalf("err %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := srv.Commands(); len(got) != 3 {
		t.Errorf("the device received %q: nothing may answer the refused question", got)
	}
	if _, set := rc.stats[statCommandStdout]; set {
		t.Error("a refused command recorded output")
	}
}

func TestCommand_AnIOSErrorFailsTheTask(t *testing.T) {
	srv, device := askingDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	_, err := Command(context.Background(), rc, device, sessionParams(map[string]any{"command": "copy flash:missing.bin null:"}))
	if err == nil || !strings.Contains(err.Error(), "%Error opening flash:missing.bin") {
		t.Fatalf("err %v", err)
	}
}

func TestCommand_AReloadEndsWhenTheDeviceDropsTheSession(t *testing.T) {
	srv, device := askingDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	result, err := Command(context.Background(), rc, device, sessionParams(map[string]any{
		"command": "reload", "expect_disconnect": true,
		"prompts": []any{map[string]any{"match": `Proceed with reload\? \[confirm\]`, "answer": ""}},
	}))
	if err != nil || !result.Changed || rc.stats["disconnected"] != true {
		t.Fatalf("result %+v, err %v, stats %v", result, err, rc.stats)
	}
}

func TestCommand_AnUnansweredQuestionIsNamedWhenTheTaskTimesOut(t *testing.T) {
	srv, device := askingDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := Command(ctx, rc, device, sessionParams(map[string]any{"command": copyLine, "prompts": []any{destination}}))
	if err == nil || !strings.Contains(err.Error(), `waiting on "Do you want to over write? [confirm]"`) {
		t.Fatalf("err %v", err)
	}
}

func TestCommand_ParametersThatDoNotReadNeverReachTheDevice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"no command", map[string]any{}, "command"},
		{"a pattern that does not compile", map[string]any{"command": "x", "prompts": []any{map[string]any{"match": "(", "answer": ""}}}, "match"},
		{"neither answer nor refuse", map[string]any{"command": "x", "prompts": []any{map[string]any{"match": "x"}}}, "not both or neither"},
		{"a misspelled key", map[string]any{"command": "x", "prompts": []any{map[string]any{"match": "x", "anwser": ""}}}, "anwser"},
		{"ask on a run with no project", map[string]any{"command": "x", "ask": map[string]any{"provider": "w", "model": "m", "choices": []any{map[string]any{"id": "a", "answer": "", "means": "m"}}}}, "Walk tier"},
		{"a choice with no meaning", map[string]any{"command": "x", "ask": map[string]any{"choices": []any{map[string]any{"id": "a", "answer": ""}}}}, "means"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, device := askingDevice(t)
			rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
			if _, err := Command(context.Background(), rc, device, sessionParams(tc.params)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if srv.Logins() != 0 {
				t.Error("the device was reached")
			}
		})
	}
}

// askParams are a copy that leaves its overwrite question to a model on
// the workstation provider.
func askParams(review []any) map[string]any {
	ask := map[string]any{
		"provider": "workstation", "model": "m:latest", "residency": "local",
		"choices": []any{
			map[string]any{"id": "confirm", "answer": "", "means": "Press Enter to overwrite the file."},
			map[string]any{"id": "decline", "answer": "n", "means": "Answer no and keep the file."},
		},
	}
	if review != nil {
		ask["review"] = review
	}
	return sessionParams(map[string]any{"command": copyLine, "prompts": []any{destination}, "ask": ask})
}

// modelContext is a run context with a project holding the workstation
// provider on host, and the device's credential.
func modelContext(t *testing.T, srv *remoteexectest.Server, host *aitest.Ollama) *aitest.Context {
	rc := aitest.NewContext()
	rc.Secrets = srv.Secrets()
	rc.Providers = map[string]inventory.InventoryItem{"workstation": aitest.Local(t, "workstation", host, false)}
	return rc
}

func TestCommand_AModelAnswersAQuestionNobodyListed(t *testing.T) {
	srv, device := askingDevice(t)
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = `{"choice":"confirm","confidence":0.9,"reason":"the run is replacing its own download"}`
	rc := modelContext(t, srv, host)

	if _, err := Command(context.Background(), rc, device, askParams(nil)); err != nil {
		t.Fatal(err)
	}
	exchanges, _ := rc.Stats["exchanges"].([]any)
	if len(exchanges) != 2 || host.Chats() != 1 {
		t.Fatalf("exchanges %v after %d calls", exchanges, host.Chats())
	}
	ex := exchanges[1].(map[string]any)
	if ex["choice"] != "confirm" || ex["provider"] != "workstation" || ex["question"] != "Do you want to over write? [confirm]" {
		t.Errorf("the model's exchange %v", ex)
	}
}

func TestCommand_AHeldModelAnswerAbandonsTheCommand(t *testing.T) {
	srv, device := askingDevice(t)
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = `{"choice":"confirm","confidence":0.3,"reason":"unsure"}`
	rc := modelContext(t, srv, host)

	_, err := Command(context.Background(), rc, device, askParams([]any{map[string]any{"field": "confidence", "below": 0.8}}))
	if !errors.Is(err, sdk.ErrHeld) {
		t.Fatalf("err %v, want held", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := srv.Commands(); len(got) != 3 {
		t.Errorf("the device received %q: a held answer is never typed", got)
	}
}
