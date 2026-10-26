// Tests for the literal secret warning.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"

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
