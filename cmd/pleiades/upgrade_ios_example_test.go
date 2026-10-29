// Gate for examples/upgrade_ios through the real binary: both runbooks run
// against a scripted Catalyst switch over real SSH (an in-process server
// that asks the questions IOS asks, drops the session when it reloads, and
// answers the new version afterwards) and, for the AI-assisted one, a real
// HTTP server speaking Ollama's API.
//
// The switch is a script and the model a stand-in that answers what it is
// told. So this proves each runbook answers, refuses, routes a model's pick
// and recovers as written, never that IOS or a real model behaves this way;
// pkg/netcli's live probe records IOS's own questions from a real device.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/ai/aitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// What the runbooks send the switch.
const (
	upgradeImage  = "cat9k_iosxe.17.03.04.SPA.bin"
	upgradeCopy   = "copy http://10.10.10.5/images/" + upgradeImage + " flash:" + upgradeImage
	upgradeVerify = "verify /md5 flash:" + upgradeImage + " 5f4dcc3b5aa765d61d8327deb882cf99"
	upgradeDelete = "delete /force flash:" + upgradeImage
	upgradeSecret = "$9$UPGRADEGATESECRET"
)

// scriptedSwitch starts a scripted Catalyst switch running 16.12.04. With
// imageThere, the copy also asks whether to overwrite, as IOS does when
// the file is already on flash; answering anything but Enter aborts it.
func scriptedSwitch(t *testing.T, imageThere bool) *remoteexectest.Server {
	t.Helper()
	questions := []remoteexectest.Question{{Text: "Destination filename [" + upgradeImage + "]? "}}
	if imageThere {
		questions = append(questions, remoteexectest.Question{
			Text:     "%Warning:There is a file already existing with this name\r\nDo you want to over write? [confirm]",
			Declined: "%Error: copy aborted",
		})
	}
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt: "sw1#",
		Config: &remoteexectest.ConfigMode{Enter: "configure terminal", Exit: "end", Prompt: "sw1(config)#"},
		Replies: map[string]string{
			"show running-config":            "hostname sw1\r\nenable secret 9 " + upgradeSecret + "\r\nend",
			"show version | include Version": "Cisco IOS XE Software, Version 16.12.04",
			"dir flash:":                     "Directory of flash:/\r\n  1  -rw-  1048576  cat9k_iosxe.16.12.04.SPA.bin",
			upgradeCopy:                      "Accessing http://10.10.10.5/images/" + upgradeImage + "...\r\n[OK - 1048576 bytes]\r\n1048576 bytes copied in 2.105 secs",
			upgradeVerify:                    "Verified (flash:" + upgradeImage + ") = 5f4dcc3b5aa765d61d8327deb882cf99",
			"write memory":                   "Building configuration...\r\n[OK]",
			"configure terminal":             "Enter configuration commands, one per line.  End with CNTL/Z.",
		},
		Asks:        map[string][]remoteexectest.Question{upgradeCopy: questions, "reload": {{Text: "Proceed with reload? [confirm]"}}},
		Hangup:      map[string]bool{"reload": true},
		AfterHangup: map[string]string{"show version | include Version": "Cisco IOS XE Software, Version 17.03.04"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// upgradeProject copies examples/upgrade_ios into a project whose sw1 is
// srv and whose model host is host, with the post-reload wait's delay cut
// to nothing, a home whose known_hosts names srv, sw1's credential stored
// and workstation onboarded. It returns the project and the home.
func upgradeProject(t *testing.T, srv *remoteexectest.Server, host *aitest.Ollama) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "upgrade_ios", "pleiades"))); err != nil {
		t.Fatal(err)
	}
	edit := func(rel string, r *strings.Replacer, mustHold ...string) {
		path := filepath.Join(dir, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range mustHold {
			if !strings.Contains(string(data), s) {
				t.Fatalf("%s no longer holds %q; change this gate with it", rel, s)
			}
		}
		if err := os.WriteFile(path, []byte(r.Replace(string(data))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const sw1 = "host: 10.10.20.11\n      port: 22"
	edit("inventory.yaml", strings.NewReplacer(sw1, "host: "+srv.Host+"\n      port: "+strconv.Itoa(srv.Port), "http://127.0.0.1:11434", host.URL), sw1, "http://127.0.0.1:11434")
	for _, rb := range []string{"upgrade_ios_xe.yaml", "upgrade_ios_xe_ai.yaml"} {
		const wait = "wait.connection:\n      delay: 60"
		edit(filepath.Join("runbooks", rb), strings.NewReplacer(wait, "wait.connection:\n      delay: 0"), wait)
	}

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey) + "\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	env := personEnv(map[string]string{"HOME": home})
	for _, args := range [][]string{
		{"add-credential", "sw1", "--username", srv.Username, "--password", srv.Password},
		{"onboard", "workstation"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return dir, home
}

// TestCLI_UpgradeIOSExamplesAnswerRefuseAndRecover is the gate.
func TestCLI_UpgradeIOSExamplesAnswerRefuseAndRecover(t *testing.T) {
	tasks := func(rep jsonReport) map[string]string {
		status := map[string]string{}
		for _, task := range rep.Tasks {
			status[task.Name] = task.Status
		}
		return status
	}
	// recorded is what the run's set_metadata tasks recorded, merged in
	// order.
	recorded := func(rep jsonReport) map[string]any {
		out := map[string]any{}
		for _, task := range rep.Tasks {
			if task.Method == "pleiades.builtin.set_metadata" && task.Status != "skipped" {
				for k, v := range task.Stats {
					out[k] = v
				}
			}
		}
		return out
	}
	stats := func(rep jsonReport, name string) map[string]any {
		for _, task := range rep.Tasks {
			if task.Name == name {
				return task.Stats
			}
		}
		return nil
	}

	t.Run("both runbooks validate once workstation is onboarded", func(t *testing.T) {
		dir, home := upgradeProject(t, scriptedSwitch(t, false), aitest.NewOllama(t, "gemma4:e4b-it-qat"))
		for _, rb := range []string{"upgrade_ios_xe.yaml", "upgrade_ios_xe_ai.yaml"} {
			out, err := runPleiadesWithEnv(t, dir, personEnv(map[string]string{"HOME": home}), "validate", "runbooks/"+rb)
			// The post-reload re-check retries net.ios.command with until, and
			// is not warned about: its show version answers no question and
			// writes no file, so net.ios.command declares that call read-only
			// (its ReadOnlyCall). No other warning is earned either.
			if err != nil || !strings.Contains(out, "no issues found") || strings.Contains(out, "[warning]") {
				t.Errorf("validate %s: %v\n%s", rb, err, out)
			}
		}
	})

	t.Run("the listed answers upgrade the switch", func(t *testing.T) {
		srv := scriptedSwitch(t, false)
		dir, home := upgradeProject(t, srv, aitest.NewOllama(t, "gemma4:e4b-it-qat"))
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/upgrade_ios_xe.yaml")
		if code != 0 || rep.Outcome.Status != "complete" || recorded(rep)["upgrade_succeeded"] != true {
			t.Fatalf("exit %d, outcome %+v, metadata %v\n%s", code, rep.Outcome, recorded(rep), raw)
		}
		if reload := stats(rep, "Reload the switch"); reload["disconnected"] != true {
			t.Errorf("the reload's stats %v, want it ended by the device dropping the session", reload)
		}
		sent := strings.Join(srv.Commands(), "|")
		for _, want := range []string{upgradeCopy + "||", "no boot system|boot system flash:" + upgradeImage, "write memory", "reload||"} {
			if !strings.Contains(sent+"|", want) {
				t.Errorf("the switch did not receive %q:\n%s", want, sent)
			}
		}
		if strings.Contains(raw, upgradeSecret) {
			t.Error("the running configuration's secret is in the report")
		}
	})

	t.Run("an image already on flash is refused, and nothing reloads", func(t *testing.T) {
		srv := scriptedSwitch(t, true)
		dir, home := upgradeProject(t, srv, aitest.NewOllama(t, "gemma4:e4b-it-qat"))
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/upgrade_ios_xe.yaml")
		if code != 0 || rep.Outcome.Status != "rescued" || recorded(rep)["upgrade_failed"] != true || recorded(rep)["failed_task"] != "Pull the new image onto flash" {
			t.Fatalf("exit %d, outcome %+v, metadata %v\n%s", code, rep.Outcome, recorded(rep), raw)
		}
		if slices.Contains(srv.Commands(), "reload") || recorded(rep)["upgrade_succeeded"] != false {
			t.Errorf("the switch received %q and the metadata says %v", srv.Commands(), recorded(rep))
		}
	})

	t.Run("the model answers the question nobody listed", func(t *testing.T) {
		srv := scriptedSwitch(t, true)
		host := aitest.NewOllama(t, "gemma4:e4b-it-qat")
		host.SetAnswer(`{"choice":"overwrite","confidence":0.9,"reason":"the question asks to overwrite the image this run downloads"}`)
		dir, home := upgradeProject(t, srv, host)
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/upgrade_ios_xe_ai.yaml")
		if code != 0 || rep.Outcome.Status != "complete" || recorded(rep)["upgrade_succeeded"] != true || host.Chats() != 1 {
			t.Fatalf("exit %d, outcome %+v, %d model call(s)\n%s", code, rep.Outcome, host.Chats(), raw)
		}
		exchanges, _ := stats(rep, "Pull the new image onto flash")["exchanges"].([]any)
		if len(exchanges) != 2 || exchanges[1].(map[string]any)["choice"] != "overwrite" {
			t.Errorf("the copy's exchanges %v", exchanges)
		}
	})

	t.Run("the model keeps the file, and the rescue's model removes it", func(t *testing.T) {
		srv := scriptedSwitch(t, true)
		host := aitest.NewOllama(t, "gemma4:e4b-it-qat")
		host.SetAnswers(
			`{"choice":"keep","confidence":0.8,"reason":"a file of that name may be another run's"}`,
			`{"recovery":"remove_new_image","confidence":0.9,"reason":"the copy stopped at a file left from an earlier attempt"}`,
		)
		dir, home := upgradeProject(t, srv, host)
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/upgrade_ios_xe_ai.yaml")
		if code != 0 || rep.Outcome.Status != "rescued" || host.Chats() != 2 || recorded(rep)["recovery"] != "remove_new_image" {
			t.Fatalf("exit %d, outcome %+v, %d model call(s), metadata %v\n%s", code, rep.Outcome, host.Chats(), recorded(rep), raw)
		}
		if status := tasks(rep); status["Recovery, remove the image this run copied"] != "changed" || status["Reload the switch"] == "changed" {
			t.Errorf("task statuses %v", status)
		}
		if !slices.Contains(srv.Commands(), upgradeDelete) || slices.Contains(srv.Commands(), "reload") {
			t.Errorf("the switch received %q", srv.Commands())
		}
	})
}
