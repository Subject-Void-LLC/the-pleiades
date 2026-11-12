// Release gate for the file and identity half of FAILURE_PATTERNS 1174: an
// owner or group chown would read as an option, and an account or group
// name getent would, never reach a real Debian device, whether written in
// the task or rendered from a variable, while a real name still does.
//
// Before the fix, file.copy with owner --reference=/etc/shadow sent chown
// --reference=/etc/shadow, which GNU chown read as "give the file
// /etc/shadow's owner and group" and did. identity.user.create with name -i
// asked getent passwd -i, which lists every account and exits 0, so the
// method read root's record as the account asked for.
//
// "Nothing reached the device" is read from the device's own sshd log, as
// the apt gate beside this one reads it (acceptedLogins): the count of
// logins it accepted is unchanged by every refused call, and moved by each
// control.
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileOperandGateRunbook renders a file's owner from a run variable, the
// way a register or a loop item would hand one over.
const fileOperandGateRunbook = `# Gives the release gate's file the owner a run variable names.
# Blast radius: one file in the release gate's own Debian container.
# Changes: that file's owner, when the name is one.
# Undo: file.permissions restores the owner it found; the container is discarded.
id: owner-operand-gate
hosts: debian
tasks:
  - name: give the file the owner the variable names
    file.copy:
      dest: /srv/gate/target
      content: x
      owner: "{{ vars.owner }}"
`

// accountOperandGateRunbook renders an account name from a run variable.
const accountOperandGateRunbook = `# Makes sure the account a run variable names exists.
# Blast radius: the release gate's own Debian container.
# Changes: one account, when the name is one.
# Undo: identity.user.remove of the account; the container is discarded.
id: account-operand-gate
hosts: debian
tasks:
  - name: make the account the variable names
    identity.user.create:
      name: "{{ vars.account }}"
      create_home: false
`

// TestCLI_FileAndIdentityRefuseANameTheyWouldReadAsAnOption is the gate.
func TestCLI_FileAndIdentityRefuseANameTheyWouldReadAsAnOption(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the file and identity operand gate, which builds a Debian device, in short mode")
	}
	device := startTestdataDevice(t, "debian-sshd", debianGatePassword, nil)
	dir, home := device.manage(t, "debian", "linux_server,debian_family")

	// The file the attack aims at, owned by neither root nor /etc/shadow's
	// group, so a --reference that reached chown would show.
	if out, code := device.exec(t, "sh", "-c", "mkdir -p /srv/gate && printf x > /srv/gate/target && chown nobody:nogroup /srv/gate/target"); code != 0 {
		t.Fatalf("making the gate's file: %s", out)
	}
	ownership := func() string {
		out, _ := device.exec(t, "stat", "-c", "%U:%G", "/srv/gate/target")
		return out
	}
	// The control for the control: chown without "--" really does what the
	// fix stops, on this device, so the file keeping its owner below means
	// something. A copy of the file takes the attack, not the file itself.
	if out, code := device.exec(t, "sh", "-c", "cp -p /srv/gate/target /srv/gate/probe && chown --reference=/etc/shadow /srv/gate/probe && stat -c %U:%G /srv/gate/probe"); code != 0 || out != "root:shadow" {
		t.Fatalf("control: chown --reference=/etc/shadow on this device gave %q (exit %d), so its absence below proves nothing", out, code)
	}
	accounts := func() string {
		out, _ := device.exec(t, "sh", "-c", "getent passwd | sort")
		return out
	}
	accountsBefore := accounts()
	untouched := func(t *testing.T) {
		t.Helper()
		if got := ownership(); got != "nobody:nogroup" {
			t.Fatalf("the gate's file is now %s: a refused owner or group reached chown", got)
		}
		if got := accounts(); got != accountsBefore {
			t.Fatalf("the account database changed during a refused call:\nbefore:\n%s\nafter:\n%s", accountsBefore, got)
		}
	}

	// A literal is refused by validation, so nothing runs and nothing logs in.
	for _, tc := range []struct {
		name   string
		method string
		params []string
		key    string
	}{
		{"file.copy owner", "file.copy", []string{"dest=/srv/gate/target", "content=x", "owner=--reference=/etc/shadow"}, "owner"},
		{"file.copy group", "file.copy", []string{"dest=/srv/gate/target", "content=x", "group=--reference=/etc/shadow"}, "group"},
		{"file.permissions owner", "file.permissions", []string{"path=/srv/gate/target", "owner=-R"}, "owner"},
		{"identity.user.create name", "identity.user.create", []string{"name=-i"}, "name"},
		{"identity.user.modify group", "identity.user.modify", []string{"name=deploy", "group=-i"}, "group"},
		{"identity.group.remove name", "identity.group.remove", []string{"name=-i"}, "name"},
		{"identity.group.create pattern", "identity.group.create", []string{"name=adm*"}, "name"},
	} {
		t.Run("literal "+tc.name, func(t *testing.T) {
			before := device.acceptedLogins(t)
			rep, raw, code := runPleiadesJSONWithHome(t, dir, home, append([]string{"adhoc", "debian", tc.method}, tc.params...)...)
			if code == 0 || rep.Outcome.Status != "invalid" || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "not_run" {
				t.Fatalf("exit %d, want validation to refuse the call before anything ran:\n%s", code, raw)
			}
			var refused bool
			for _, v := range rep.Validation {
				refused = refused || (v.Rule == "param-operand" && strings.Contains(v.Message, `parameter "`+tc.key+`"`))
			}
			if !refused {
				t.Fatalf("no param-operand finding for %s:\n%s", tc.key, raw)
			}
			if after := device.acceptedLogins(t); after != before {
				t.Errorf("sshd accepted %d login(s) during a refused call", after-before)
			}
			untouched(t)
		})
	}

	// A rendered value is refused by the engine before the method runs.
	for name, body := range map[string]string{"owner.yaml": fileOperandGateRunbook, "account.yaml": accountOperandGateRunbook} {
		if err := os.WriteFile(filepath.Join(dir, "runbooks", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ runbook, variable string }{
		{"runbooks/owner.yaml", "owner=--reference=/etc/shadow"},
		{"runbooks/owner.yaml", "owner=adm*"},
		{"runbooks/account.yaml", "account=-i"},
		{"runbooks/account.yaml", "account=--badname"},
	} {
		t.Run("rendered "+tc.variable, func(t *testing.T) {
			value := tc.variable[strings.Index(tc.variable, "=")+1:]
			before := device.acceptedLogins(t)
			rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", tc.runbook, "-e", tc.variable)
			if code == 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "failed" ||
				!strings.Contains(rep.Tasks[0].Error, "cannot pass on as one name") {
				t.Fatalf("exit %d, want the task failed by the operand rule:\n%s", code, raw)
			}
			if strings.Contains(rep.Tasks[0].Error, value) {
				t.Errorf("the refusal carries the value: %s", rep.Tasks[0].Error)
			}
			if after := device.acceptedLogins(t); after != before {
				t.Errorf("sshd accepted %d login(s) during a refused call", after-before)
			}
			untouched(t)
		})
	}

	// The controls: a real owner and a real account through the same
	// runbooks and variables do log in and change the device, so the
	// unchanged counts above are refusals, not counts that never move. The
	// owner's change also proves chown takes the "--" it is now sent.
	t.Run("control owner", func(t *testing.T) {
		before := device.acceptedLogins(t)
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/owner.yaml", "-e", "owner=root")
		if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "changed" {
			t.Fatalf("exit %d, want the owner changed:\n%s", code, raw)
		}
		if got := ownership(); got != "root:nogroup" {
			t.Fatalf("the control reported a change and the file is %s, want root:nogroup", got)
		}
		if after := device.acceptedLogins(t); after <= before {
			t.Error("the control changed the owner with no login sshd accepted, so the counts above prove nothing")
		}
	})
	t.Run("control account", func(t *testing.T) {
		before := device.acceptedLogins(t)
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/account.yaml", "-e", "account=pleiadesop")
		if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "changed" {
			t.Fatalf("exit %d, want the account made:\n%s", code, raw)
		}
		if out, code := device.exec(t, "getent", "passwd", "pleiadesop"); code != 0 || !strings.HasPrefix(out, "pleiadesop:") {
			t.Fatalf("the control reported a change and getent has no pleiadesop (exit %d): %q", code, out)
		}
		if after := device.acceptedLogins(t); after <= before {
			t.Error("the control made the account with no login sshd accepted, so the counts above prove nothing")
		}
	})
}
