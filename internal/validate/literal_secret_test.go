// Tests for the literal secret warning.
package validate_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/gnmi"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/ios"
)

// TestLiteralSecretWarnings_NameTheFixAndNeverTheValue: a pre-shared key
// written out in a configuration line is warned about, naming add-secret and
// the reference, never the key; a line naming its secret by reference, and
// a line with no secret, are not.
func TestLiteralSecretWarnings_NameTheFixAndNeverTheValue(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(`id: literal
hosts: r1
tasks:
  - name: written out
    net.ios.config:
      lines:
        - crypto ikev2 keyring K
        - "  pre-shared-key WrittenOut-9876"
  - name: by reference
    net.ios.config:
      lines:
        - "  pre-shared-key {{ secret.dmvpn.psk }}"
        - interface Loopback0
`))
	if err != nil {
		t.Fatal(err)
	}
	warnings := validate.LiteralSecretWarnings(dag)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want one, for the written-out key", warnings)
	}
	w := warnings[0]
	if !strings.Contains(w.Message, "pleiades add-secret") || !strings.Contains(w.Message, "{{ secret.<name>.<field> }}") || !strings.Contains(w.Message, "pre-shared-key") {
		t.Errorf("message %q does not name the fix", w.Message)
	}
	if strings.Contains(w.Message, "WrittenOut-9876") {
		t.Error("the warning quotes the secret")
	}
	if validate.LiteralSecretWarnings(nil) != nil {
		t.Error("a nil DAG has warnings")
	}
}

// TestLiteralSecretWarnings_InPromptAnswers: a prompts answer written out
// for a passphrase question is warned about too, from inside the list of
// maps, and one answered by reference is not.
func TestLiteralSecretWarnings_InPromptAnswers(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(`id: answers
hosts: r1
tasks:
  - name: written out
    net.ios.command:
      command: crypto pki server CA start
      prompts:
        - match: 'Password:'
          answer: "password Typed-Out-555"
  - name: by reference
    net.ios.command:
      command: crypto pki server CA start
      prompts:
        - match: 'Password:'
          answer: "{{ secret.ca.passphrase }}"
`))
	if err != nil {
		t.Fatal(err)
	}
	warnings := validate.LiteralSecretWarnings(dag)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "parameter prompts") || strings.Contains(warnings[0].Message, "Typed-Out-555") {
		t.Fatalf("warnings = %+v", warnings)
	}
}

// TestLiteralSecretWarnings_InAMappingsField: a secret written as the value
// of a secret-named field of a mapping (net.gnmi.config's val), where no
// word comes before it, is warned about, naming the field and never the
// value, at any depth; the same field naming its secret by reference, and a
// mapping holding none, are not.
func TestLiteralSecretWarnings_InAMappingsField(t *testing.T) {
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(`id: fields
hosts: r1
tasks:
  - name: written out
    net.gnmi.config:
      update:
        - path: /ipsec/peer[name=hq]
          val:
            address: 192.0.2.1
            pre-shared-key: Hunter2Secret
  - name: written out deeper
    net.gnmi.config:
      replace:
        - path: /system
          val:
            ntp:
              keys:
                - id: 1
                  password: 4242
  - name: by reference
    net.gnmi.config:
      update:
        - path: /ipsec/peer[name=hq]
          val:
            pre-shared-key: "{{ secret.ike.psk }}"
  - name: no secret
    net.gnmi.config:
      update:
        - path: /interface[name=lo0]
          val:
            description: managed loopback
`))
	if err != nil {
		t.Fatal(err)
	}
	warnings := validate.LiteralSecretWarnings(dag)
	if len(warnings) != 2 {
		t.Fatalf("warnings = %+v, want two, one for each written-out value", warnings)
	}
	fields := map[string]bool{}
	for _, w := range warnings {
		for _, field := range []string{"pre-shared-key", "password"} {
			if strings.Contains(w.Message, "in field \""+field+"\"") {
				fields[field] = true
			}
		}
		if strings.Contains(w.Message, "Hunter2Secret") || strings.Contains(w.Message, "4242") || !strings.Contains(w.Message, "pleiades add-secret") {
			t.Errorf("message %q quotes the secret or names no fix", w.Message)
		}
	}
	if !fields["pre-shared-key"] || !fields["password"] {
		t.Errorf("warnings %+v, want the pre-shared-key and password fields named", warnings)
	}
}

// TestLiteralSecretWarnings_RealIOSAndNXOSLines holds real IOS and NX-OS
// configuration lines both ways, each in a net.ios.config task of its own:
// a secret written out is warned about, after the word it names when that
// word is given; a secret named by reference is not, whatever words follow
// the reference; and a line holding no secret (an aaa method list) is not.
// The CML window of 2026-11-17 warned of "role" after a referenced NX-OS
// password, and of "login" in an aaa authentication line.
func TestLiteralSecretWarnings_RealIOSAndNXOSLines(t *testing.T) {
	cases := []struct {
		line  string
		after string // the word the warning names; "" for no warning
	}{
		// Named by reference: no warning, whatever follows.
		{"username netops password {{ secret.nx1login.password }} role network-admin", ""},
		{"username netops password {{ secret.nx1login.password }}", ""},
		{"username netops password 5 {{ secret.nx1login.hash }} role network-operator", ""},
		{"username admin privilege 15 secret 9 {{ secret.r1.enable }}", ""},
		{"enable secret {{secret.r1.enable }}", ""},
		{"tacacs-server host 10.1.1.5 key 7 {{ secret.tac.key }} port 49", ""},
		{"radius-server host 10.1.1.9 key {{ secret.rad.key }} authentication accounting", ""},
		{"crypto isakmp key {{ secret.ike.psk }} address 0.0.0.0", ""},
		{"snmp-server community {{ secret.snmp.ro }} RO", ""},
		{"snmp-server user ops g v3 auth sha {{ secret.snmp.auth }} priv aes 128 {{ secret.snmp.priv }}", ""},
		{"  authentication text {{ secret.hsrp.key }}", ""},
		// No secret at all.
		{"aaa authentication login default group tacacs+ local", ""},
		{"aaa authentication login default local", ""},
		{"aaa authentication enable default group tacacs+ enable", ""},
		{"aaa authorization exec default group tacacs+ local", ""},
		{"aaa authentication login console local", ""},
		{"hostname nx1", ""},
		{"interface Tunnel100", ""},
		{" tunnel key 100", ""},
		// Written out: warned, naming the word before it.
		{"username netops password Literal-99 role network-admin", "password"},
		{"tacacs-server key Literal-99", "key"},
		{"radius-server host 10.1.1.9 key Literal-99", "key"},
		{"crypto isakmp key Literal-99 address 0.0.0.0", "key"},
		{"snmp-server community Literal-99 RO", "community"},
		{"snmp-server user ops g v3 auth sha {{ secret.snmp.auth }} priv aes 128 Literal-99", "128"},
		{"  authentication text Literal-99", "text"},
		{" standby 1 authentication Literal-99", "authentication"},
		{"ntp authentication-key 1 md5 Literal-99", "md5"},
		{" neighbor 10.0.0.2 password Literal-99", "password"},
	}
	var yaml strings.Builder
	yaml.WriteString("id: lines\nhosts: r1\ntasks:\n")
	for i, c := range cases {
		fmt.Fprintf(&yaml, "  - name: line %d\n    net.ios.config:\n      lines:\n        - %q\n", i, c.line)
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		t.Fatal(err)
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML([]byte(yaml.String()))
	if err != nil {
		t.Fatal(err)
	}
	byTask := map[string]validate.Finding{}
	for _, w := range validate.LiteralSecretWarnings(dag) {
		byTask[w.Node] = w
	}
	for i, c := range cases {
		w, warned := byTask[fmt.Sprintf("tasks[%d]", i)]
		switch {
		case c.after == "" && warned:
			t.Errorf("%q: warned, want none: %s", c.line, w.Message)
		case c.after != "" && !warned:
			t.Errorf("%q: not warned, want a warning after %q", c.line, c.after)
		case warned && !strings.Contains(w.Message, fmt.Sprintf("(after %q)", c.after)):
			t.Errorf("%q: %s, want it to name %q", c.line, w.Message, c.after)
		case warned && strings.Contains(w.Message, "Literal-99"):
			t.Errorf("%q: the warning quotes the secret: %s", c.line, w.Message)
		}
	}
}
