// The reflective completeness test of Credential's four print paths: every
// field is classified, printed by String and MarshalJSON, and a secret's
// value reaches none of String, GoString, MarshalJSON and LogValue.
package credential_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// fieldClass is how one Credential field may appear in output.
type fieldClass int

// The four classes.
const (
	// classIdentity is shown as its value: which account.
	classIdentity fieldClass = iota + 1
	// classSecret is never shown: a marker says whether it is set.
	classSecret
	// classPublic is not a secret, and still shown as a marker rather than
	// its body: a certificate.
	classPublic
	// classProtocol is shown as its value: which algorithm.
	classProtocol
)

// credentialFieldClasses classifies every Credential field. A field added
// to Credential and not to this table fails
// TestCredential_EveryFieldIsClassifiedAndKeptFromEveryPrintPath, which is
// the point: deciding how a new field prints is not optional.
var credentialFieldClasses = map[string]fieldClass{
	"Username":           classIdentity,
	"Password":           classSecret,
	"PrivateKeyPEM":      classSecret,
	"Passphrase":         classSecret,
	"CertificatePEM":     classPublic,
	"PFXBase64":          classSecret,
	"Token":              classSecret,
	"EnablePassword":     classSecret,
	"SNMPCommunity":      classSecret,
	"SNMPUser":           classIdentity,
	"SNMPAuthProtocol":   classProtocol,
	"SNMPAuthPassphrase": classSecret,
	"SNMPPrivProtocol":   classProtocol,
	"SNMPPrivPassphrase": classSecret,
}

// TestCredential_EveryFieldIsClassifiedAndKeptFromEveryPrintPath fills every
// field of a Credential by reflection with a value of its own, then holds
// each field to its class on all four print paths: String and MarshalJSON
// name it, an identifier or protocol is shown as its value, a secret is
// marked redacted and its value appears in none of String, GoString,
// MarshalJSON and LogValue (through slog's JSON handler, the path a
// structured log takes), and a certificate is marked set but not secret
// with its body in none of them either.
func TestCredential_EveryFieldIsClassifiedAndKeptFromEveryPrintPath(t *testing.T) {
	typ := reflect.TypeOf(credential.Credential{})
	for name := range credentialFieldClasses {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("the table classifies %s, which Credential no longer has", name)
		}
	}

	var cred credential.Credential
	fill := reflect.ValueOf(&cred).Elem()
	values := map[string]string{}
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		value := "value-of-" + strings.ToLower(name) + "-x9"
		switch fill.Field(i).Kind() {
		case reflect.String:
			fill.Field(i).SetString(value)
		case reflect.Slice:
			fill.Field(i).SetBytes([]byte(value))
		default:
			t.Fatalf("Credential.%s is a %s, which this test cannot fill: teach it, then classify the field", name, fill.Field(i).Kind())
		}
		values[name] = value
	}

	encoded, err := json.Marshal(cred)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("MarshalJSON wrote something that is not a JSON object: %v", err)
	}
	var logged bytes.Buffer
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("connecting", "credential", cred)
	outputs := map[string]string{
		"String":      cred.String(),
		"GoString":    fmt.Sprintf("%#v", cred),
		"MarshalJSON": string(encoded),
		"LogValue":    logged.String(),
	}

	for i := range typ.NumField() {
		name := typ.Field(i).Name
		class, classified := credentialFieldClasses[name]
		if !classified {
			t.Errorf("Credential.%s is not classified: add it to credentialFieldClasses as an identity, a secret, public or a protocol, and to String and MarshalJSON", name)
			continue
		}
		if _, ok := decoded[name]; !ok {
			t.Errorf("MarshalJSON leaves out %s", name)
		}
		if !strings.Contains(outputs["String"], name+":") {
			t.Errorf("String leaves out %s", name)
		}
		value := values[name]
		switch class {
		case classIdentity, classProtocol:
			if !strings.Contains(outputs["String"], value) || decoded[name] != value {
				t.Errorf("%s is not a secret and should print as its value, and String or MarshalJSON hides it", name)
			}
		case classSecret, classPublic:
			marker := "<redacted, set>"
			if class == classPublic {
				marker = "<set, not secret>"
			}
			if !strings.Contains(outputs["String"], name+":"+marker) || decoded[name] != marker {
				t.Errorf("%s should print as %s in String and MarshalJSON", name, marker)
			}
			for path, out := range outputs {
				if strings.Contains(out, value) {
					t.Errorf("%s reaches %s's output: %s", name, path, out)
				}
			}
		}
	}
}
