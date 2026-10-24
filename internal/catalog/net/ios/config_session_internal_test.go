// Package ios: net.ios.config over a real SSH session, against a scripted
// device in configuration mode whose lines ask questions and print IOS's
// notices (pkg/netcli's own tests prove the reading against bytes shaped
// like IOL XE's; these prove the method reads its parameters, reaches the
// device, answers, and records what happened), and a fake Ollama host for
// ask.
package ios

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// caQuestion is the question crypto pki server's "no shutdown" asks.
const caQuestion = "Are you sure you want to do this? [yes/no]: "

// configuringDevice starts a scripted IOS device with a configuration mode
// whose "no shutdown" asks to confirm and whose lines print IOS's notices
// and errors.
func configuringDevice(t *testing.T) (*remoteexectest.Server, *sshDevice) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt: "r1#",
		Config: &remoteexectest.ConfigMode{Enter: "configure terminal", Exit: "end", Prompt: "r1(config)#"},
		Replies: map[string]string{
			"show running-config":  "Building configuration...\n\nhostname r1\n!\nend",
			"permit ip any any":    "% Duplicate permit statement ignored",
			"frob":                 "% Frob is deprecated and was ignored",
			"10 permit ip any any": "% Duplicate sequence number\r\n% Failed to add ace to access-list",
		},
		Asks: map[string][]remoteexectest.Question{
			"no shutdown": {{Text: caQuestion, Want: "yes", Declined: "% The CA was not started"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv, &sshDevice{
		Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameSSHTransport}},
		host: srv.Host, port: srv.Port,
	}
}

// startCA are the lines that start a CA server, without a checkpoint, so
// the scripted device needs to answer no file commands.
func startCA(extra map[string]any) map[string]any {
	params := map[string]any{paramLines: []any{"crypto pki server CA", "no shutdown"}, paramCheckpoint: false}
	for k, v := range extra {
		params[k] = v
	}
	return sessionParams(params)
}

var confirmCA = map[string]any{"match": `Are you sure you want to do this\? \[yes/no\]`, "answer": "yes"}

func TestConfig_AnswersAQuestionALineAsks(t *testing.T) {
	srv, device := configuringDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	if _, err := Config(context.Background(), rc, device, startCA(map[string]any{"prompts": []any{confirmCA}})); err != nil {
		t.Fatal(err)
	}
	got := srv.Commands()
	if i := slices.Index(got, "no shutdown"); i < 0 || i+1 >= len(got) || got[i+1] != "yes" {
		t.Errorf("the device received %q: the answer follows the line that asked", got)
	}
	exchanges, _ := rc.stats["exchanges"].([]any)
	if len(exchanges) != 1 || exchanges[0].(map[string]any)["answer"] != "yes" {
		t.Errorf("exchanges %v", exchanges)
	}
}

// TestConfig_AnUnansweredQuestionFailsTheLineAtOnce: with no deadline on
// the task, a question nothing answers still ends it, naming the question
// and how to answer it, and nothing is typed at it.
func TestConfig_AnUnansweredQuestionFailsTheLineAtOnce(t *testing.T) {
	srv, device := configuringDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	_, err := Config(context.Background(), rc, device, startCA(nil))
	if err == nil || !strings.Contains(err.Error(), strings.TrimSpace(caQuestion)) || !strings.Contains(err.Error(), "prompts") {
		t.Fatalf("err %v", err)
	}
	if slices.Contains(srv.Commands(), "yes") {
		t.Errorf("the device received %q: nothing may answer an unlisted question", srv.Commands())
	}
}

// TestConfig_NoticesAreReportedNotFailed: IOS's own notice and one the task
// accepts let the batch finish and are reported; any other "% " line fails
// it.
func TestConfig_NoticesAreReportedNotFailed(t *testing.T) {
	srv, device := configuringDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	_, err := Config(context.Background(), rc, device, sessionParams(map[string]any{
		paramLines: []any{"permit ip any any", "frob"}, paramCheckpoint: false, paramAccept: []any{"% Frob is deprecated"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	notices, _ := rc.stats[statNotices].([]string)
	if strings.Join(notices, "|") != "% Duplicate permit statement ignored|% Frob is deprecated and was ignored" {
		t.Errorf("notices %q", notices)
	}
	if _, set := rc.stats["exchanges"]; set {
		t.Error("a task with no prompts recorded exchanges")
	}

	rc = &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
	_, err = Config(context.Background(), rc, device, sessionParams(map[string]any{paramLines: []any{"10 permit ip any any"}, paramCheckpoint: false}))
	if err == nil || !strings.Contains(err.Error(), "Duplicate sequence number") {
		t.Errorf("a real rejection: %v", err)
	}
}

func TestConfig_DialogParametersThatDoNotReadNeverReachTheDevice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"an accept not beginning with %", map[string]any{paramAccept: []any{"Frob"}}, "does not begin with %"},
		{"an accept that does not compile", map[string]any{paramAccept: []any{"%("}}, "accept[0]"},
		{"an accept that is not a list", map[string]any{paramAccept: 5}, paramAccept},
		{"a disconnect", map[string]any{"expect_disconnect": true}, "configuration lines"},
		{"a prompt that does not compile", map[string]any{"prompts": []any{map[string]any{"match": "(", "answer": ""}}}, "match"},
		{"ask on a run with no project", map[string]any{"ask": map[string]any{"provider": "w", "model": "m", "choices": []any{map[string]any{"id": "a", "answer": "", "means": "m"}}}}, "Walk tier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, device := configuringDevice(t)
			rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}
			if _, err := Config(context.Background(), rc, device, startCA(tc.params)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want %q", err, tc.want)
			}
			if srv.Logins() != 0 {
				t.Error("the device was reached")
			}
		})
	}
}

func TestConfig_AModelAnswersAQuestionALineAsks(t *testing.T) {
	srv, device := configuringDevice(t)
	host := aitest.NewOllama(t, "m:latest")
	host.Answer = `{"choice":"start","confidence":0.9,"reason":"the task starts the CA"}`
	rc := modelContext(t, srv, host)

	_, err := Config(context.Background(), rc, device, startCA(map[string]any{"ask": map[string]any{
		"provider": "workstation", "model": "m:latest", "residency": "local",
		"choices": []any{
			map[string]any{"id": "start", "answer": "yes", "means": "Confirm starting the CA server."},
			map[string]any{"id": "keep", "answer": "no", "means": "Leave the CA server stopped."},
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	exchanges, _ := rc.Stats["exchanges"].([]any)
	if len(exchanges) != 1 || host.Chats() != 1 {
		t.Fatalf("exchanges %v after %d calls", exchanges, host.Chats())
	}
	if ex := exchanges[0].(map[string]any); ex["choice"] != "start" || ex["answer"] != "yes" || ex["provider"] != "workstation" {
		t.Errorf("the model's exchange %v", ex)
	}
}
