// Package netcli_test: the questions a real IOS XE device asks partway
// through a command, probed through Session.Dialog when PLEIADES_E2E_IOS is set.
package netcli_test

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// TestLiveIOSDialogs pins the questions IOS XE asks partway through a
// command, and its "%Error" form, against a real device, through
// Session.Dialog itself: the shapes net.ios.command's prompts are written
// against, and the error form it fails on. Like the diagnostics beside it,
// it is gated on PLEIADES_E2E_IOS and its credentials, and safe on the
// shared DevNet sandbox: it copies the running configuration to one file
// named for this process, reads it back, refuses to overwrite it, checks
// a wrong checksum against it, and deletes it. It never reloads, and never
// touches a boot statement.
func TestLiveIOSDialogs(t *testing.T) {
	if os.Getenv("PLEIADES_E2E_IOS") == "" {
		t.Skip("set PLEIADES_E2E_IOS=1 to run this live diagnostic against a real Cisco IOS XE device")
	}
	user, pass := os.Getenv("PLEIADES_E2E_IOS_USER"), os.Getenv("PLEIADES_E2E_IOS_PASS")
	if user == "" || pass == "" {
		t.Skip("PLEIADES_E2E_IOS_USER and PLEIADES_E2E_IOS_PASS must both be set (no default credential: the sandbox issues a unique password per reservation)")
	}
	host := os.Getenv("PLEIADES_E2E_IOS_HOST")
	if host == "" {
		host = "devnetsandboxiosxec8k.cisco.com"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// A refused question closes its session, so each step opens its own.
	open := func() *netcli.Session {
		t.Helper()
		conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).Connect(ctx, nil, remoteexec.Target{Host: host, Port: 22}, remoteexec.PasswordAuth(user, pass))
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		shell, err := conn.Shell(ctx, remoteexec.ShellOptions{})
		if err != nil {
			t.Fatalf("Shell: %v", err)
		}
		session, err := netcli.Open(ctx, shell, netcli.IOS, netcli.Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	prompt := func(re string, answer string) netcli.Prompt {
		return netcli.Prompt{Match: regexp.MustCompile(re), Answer: answer}
	}
	dialog := func(s *netcli.Session, line string, opts netcli.DialogOptions) (netcli.DialogResult, error) {
		t.Helper()
		res, err := s.Dialog(ctx, line, opts)
		t.Logf("\n===== %s =====\n%q\nexchanges %+v\nerr %v", line, res.Output, res.Exchanges, err)
		return res, err
	}

	file := "bootflash:pleiades-probe-" + strconv.Itoa(os.Getpid()) + ".cfg"
	destination := prompt(`Destination filename \[[^\]]*\]\?`, "")

	s := open()
	res, err := dialog(s, "copy bootflash:pleiades-probe-missing.bin null:", netcli.DialogOptions{})
	if err != nil || !netcli.IOS.ErrorPattern.MatchString(res.Output) {
		t.Errorf("a failed copy does not print a line netcli.IOS reads as an error: %v", err)
	}

	res, err = dialog(s, "copy running-config "+file, netcli.DialogOptions{Prompts: []netcli.Prompt{destination}})
	if err != nil || len(res.Exchanges) != 1 || !strings.Contains(res.Output, "bytes copied") {
		t.Fatalf("the copy's filename question was not answered as listed: %v", err)
	}
	t.Cleanup(func() {
		// Removes the file whatever the steps below did.
		_, _ = open().Dialog(context.Background(), "delete /force "+file, netcli.DialogOptions{})
	})

	res, err = dialog(s, "verify /md5 "+file+" 00000000000000000000000000000000", netcli.DialogOptions{})
	if err != nil || !netcli.IOS.ErrorPattern.MatchString(res.Output) {
		t.Errorf("a checksum mismatch does not print a line netcli.IOS reads as an error: %v", err)
	}

	_, err = dialog(s, "copy running-config "+file, netcli.DialogOptions{Prompts: []netcli.Prompt{
		destination, {Match: regexp.MustCompile(`over ?write\? \[confirm\]`), Refuse: true},
	}})
	if err == nil || !strings.Contains(err.Error(), "which this task refuses") {
		t.Errorf("the overwrite question was not the one listed: %v", err)
	}

	res, err = dialog(open(), "delete "+file, netcli.DialogOptions{Prompts: []netcli.Prompt{
		prompt(`Delete filename \[[^\]]*\]\?`, ""), prompt(`Delete \S+\? \[confirm\]`, ""),
	}})
	if err != nil || len(res.Exchanges) != 2 {
		t.Errorf("the delete's two questions were not the ones listed: %v", err)
	}
	if res, _ := dialog(open(), "dir "+file, netcli.DialogOptions{}); !netcli.IOS.ErrorPattern.MatchString(res.Output) && !strings.Contains(res.Output, "No such file") {
		t.Errorf("%s is still on the device after its delete", file)
	}
}
