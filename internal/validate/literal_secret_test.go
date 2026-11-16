// Tests for the literal secret warning.
package validate_test

import (
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
