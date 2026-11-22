// White-box tests of add-credential's SNMP flags: their help text against
// the CLI spec the reference is generated from, the families a set of flags
// names, and the reader every piped secret shares.
package main

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestAddSNMPFlags_MatchTheCLISpec: each SNMP flag add-credential parses is
// in internal/clispec with its type's default and the same help text, so the
// generated reference and --help cannot drift apart, and a protocol added to
// pkg/wire's vocabulary fails here until the reference names it too.
func TestAddSNMPFlags_MatchTheCLISpec(t *testing.T) {
	fs := flag.NewFlagSet("add-credential", flag.ContinueOnError)
	addSNMPFlags(fs)
	spec, ok := clispec.Find(clispec.Root, "add-credential")
	if !ok {
		t.Fatal("clispec has no add-credential")
	}
	documented := map[string]clispec.Flag{}
	for _, f := range spec.Flags {
		documented[f.Name] = f
	}
	count := 0
	fs.VisitAll(func(f *flag.Flag) {
		count++
		doc, ok := documented[f.Name]
		if !ok {
			t.Errorf("--%s is not in clispec's add-credential", f.Name)
			return
		}
		if doc.Doc != f.Usage || doc.Default != f.DefValue {
			t.Errorf("--%s: clispec says %q (default %q), the flag %q (default %q)", f.Name, doc.Doc, doc.Default, f.Usage, f.DefValue)
		}
	})
	if count != 7 {
		t.Errorf("addSNMPFlags registered %d flags, want 7", count)
	}
}

// TestAddCredentialFlags_MatchTheCLISpec: every flag runAddCredential
// registers is in internal/clispec with the same help text and default, and
// every flag clispec lists for add-credential is registered, so the generated
// reference and --help cannot drift apart in either direction. The control is
// that the flag set was seen and holds the flags this change added.
func TestAddCredentialFlags_MatchTheCLISpec(t *testing.T) {
	var registered *flag.FlagSet
	inspectAddCredentialFlags = func(fs *flag.FlagSet) { registered = fs }
	t.Cleanup(func() { inspectAddCredentialFlags = nil })
	// No device name: the command refuses after registering its flags.
	if err := runAddCredential(nil); err == nil {
		t.Fatal("add-credential with no device ran")
	}
	if registered == nil {
		t.Fatal("runAddCredential registered no flags through the hook")
	}
	spec, ok := clispec.Find(clispec.Root, "add-credential")
	if !ok {
		t.Fatal("clispec has no add-credential")
	}
	documented := map[string]clispec.Flag{}
	for _, f := range spec.Flags {
		documented[f.Name] = f
	}
	seen := map[string]bool{}
	registered.VisitAll(func(f *flag.Flag) {
		seen[f.Name] = true
		doc, ok := documented[f.Name]
		switch {
		case !ok:
			t.Errorf("--%s is registered and not in clispec's add-credential", f.Name)
		case doc.Doc != f.Usage || doc.Default != f.DefValue:
			t.Errorf("--%s: clispec says %q (default %q), the flag %q (default %q)", f.Name, doc.Doc, doc.Default, f.Usage, f.DefValue)
		}
	})
	for name := range documented {
		if !seen[name] {
			t.Errorf("clispec lists --%s, which add-credential does not register", name)
		}
	}
	for _, name := range []string{"key-type", "no-password", "json", "generate", "username"} {
		if !seen[name] {
			t.Errorf("the control: --%s was not seen", name)
		}
	}
}

// TestFamiliesGiven_NamesEachFamilyItsFlagsSet: SNMP flags alone name the
// SNMP family, login flags the login, both both, and neither the login,
// which is add-credential's password prompt.
func TestFamiliesGiven_NamesEachFamilyItsFlagsSet(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want credential.Families
	}{
		{nil, credential.LoginFamily},
		{[]string{"--dir", "x"}, credential.LoginFamily},
		{[]string{"--username", "u"}, credential.LoginFamily},
		{[]string{"--snmp-community-stdin"}, credential.SNMPFamily},
		{[]string{"--allow-short-community"}, credential.SNMPFamily},
		{[]string{"--token-stdin", "--snmp-user", "u"}, credential.LoginFamily | credential.SNMPFamily},
	} {
		fs := flag.NewFlagSet("add-credential", flag.ContinueOnError)
		fs.String("dir", ".", "")
		fs.String("username", "", "")
		fs.Bool("token-stdin", false, "")
		addSNMPFlags(fs)
		if err := fs.Parse(tc.args); err != nil {
			t.Fatal(err)
		}
		if got := familiesGiven(fs); got != tc.want {
			t.Errorf("familiesGiven(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// TestStdinQueue_RefusesALineItDidNotCount: a reader asked for more lines
// than it counted says so rather than reading again, which would lose a
// line to the first read's buffer.
func TestStdinQueue_RefusesALineItDidNotCount(t *testing.T) {
	q := &stdinQueue{read: true}
	if _, err := q.next(); err == nil || !strings.Contains(err.Error(), "bug") {
		t.Errorf("next() past the count = %v, want it refused", err)
	}
}

// TestSNMPFlagError_SpeaksInFlags: a problem about each field is said in the
// flag's or the secret's own words, a short community names the flag that
// allows one, and an error that is not an SNMP problem passes as it is.
func TestSNMPFlagError_SpeaksInFlags(t *testing.T) {
	for key, want := range map[string]string{
		wire.SecretSNMPUser:           "--snmp-user ",
		wire.SecretSNMPAuthProtocol:   "--snmp-auth ",
		wire.SecretSNMPPrivProtocol:   "--snmp-priv ",
		wire.SecretSNMPAuthPassphrase: "the SNMP authentication passphrase ",
		wire.SecretSNMPPrivPassphrase: "the SNMP privacy passphrase ",
	} {
		if got := snmpFlagError(&wire.SNMPProblem{Key: key, Problem: "is wrong"}).Error(); !strings.HasPrefix(got, want) {
			t.Errorf("%s reads %q, want it to begin %q", key, got, want)
		}
	}
	short := wire.SNMPCredential{Community: "public"}.Check(false)
	if got := snmpFlagError(short).Error(); !strings.HasPrefix(got, "the SNMP community is shorter") || !strings.HasSuffix(got, "(--allow-short-community)") {
		t.Errorf("a short community reads %q", got)
	}
	other := errors.New("not an SNMP problem")
	if snmpFlagError(other) != other || snmpFlagError(nil) != nil {
		t.Error("snmpFlagError changed an error that is not an SNMP problem")
	}
}
