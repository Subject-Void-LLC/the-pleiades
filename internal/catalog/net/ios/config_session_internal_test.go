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

// zeroizeQuestion is the question removing a key pair asks, in IOL XE
// 17.15's own words (measured through net.ios.config on the blind CVD lab's
// BR3, 2026-10-24; answered "no", the key pair stays and nothing more is
// printed).
const zeroizeQuestion = "Do you really want to remove these keys? [yes/no]: "

// configuringDevice starts a scripted IOS device with a configuration mode
// whose key pair removal asks to confirm and whose lines print IOS's
// notices and errors.
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
			"crypto key zeroize rsa PLTEST": {{Text: zeroizeQuestion, Want: "yes"}},
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

// removeKey is the line that removes the key pair PLTEST, without a
// checkpoint, so the scripted device needs to answer no file commands.
func removeKey(extra map[string]any) map[string]any {
	params := map[string]any{paramLines: []any{"crypto key zeroize rsa PLTEST"}, paramCheckpoint: false}
	for k, v := range extra {
		params[k] = v
	}
	return sessionParams(params)
}

var confirmRemoval = map[string]any{"match": `Do you really want to remove these keys\? \[yes/no\]`, "answer": "yes"}

func TestConfig_AnswersAQuestionALineAsks(t *testing.T) {
	srv, device := configuringDevice(t)
	rc := &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}

	if _, err := Config(context.Background(), rc, device, removeKey(map[string]any{"prompts": []any{confirmRemoval}})); err != nil {
		t.Fatal(err)
	}
	got := srv.Commands()
	if i := slices.Index(got, "crypto key zeroize rsa PLTEST"); i < 0 || i+1 >= len(got) || got[i+1] != "yes" {
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

	_, err := Config(context.Background(), rc, device, removeKey(nil))
	if err == nil || !strings.Contains(err.Error(), strings.TrimSpace(zeroizeQuestion)) || !strings.Contains(err.Error(), "prompts") {
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
			if _, err := Config(context.Background(), rc, device, removeKey(tc.params)); err == nil || !strings.Contains(err.Error(), tc.want) {
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
	host.Answer = `{"choice":"confirm","confidence":0.9,"reason":"the task removes the key pair"}`
	rc := modelContext(t, srv, host)

	_, err := Config(context.Background(), rc, device, removeKey(map[string]any{"ask": map[string]any{
		"provider": "workstation", "model": "m:latest", "residency": "local",
		"choices": []any{
			map[string]any{"id": "confirm", "answer": "yes", "means": "Remove the key pair this task names."},
			map[string]any{"id": "keep", "answer": "no", "means": "Keep the key pair."},
		},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	exchanges, _ := rc.Stats["exchanges"].([]any)
	if len(exchanges) != 1 || host.Chats() != 1 {
		t.Fatalf("exchanges %v after %d calls", exchanges, host.Chats())
	}
	if ex := exchanges[0].(map[string]any); ex["choice"] != "confirm" || ex["answer"] != "yes" || ex["provider"] != "workstation" {
		t.Errorf("the model's exchange %v", ex)
	}
}
