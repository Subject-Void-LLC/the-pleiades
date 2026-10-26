// Named secrets in the project's credential file: values a runbook refers to
// as {{ secret.<name>.<field> }} in a parameter a method resolves itself,
// such as an IKE pre-shared key shared by several routers. They live in a
// namespace of their own beside the devices', the registries' and the
// database connections', so no device name reaches one.
package credential

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// secretNamePattern is what a secret's name and each of its fields must
// match: a lowercase identifier, so `secret.<name>.<field>` reads as one
// template path with no quoting, and no name can carry a dot, a space or a
// brace into a reference.
var secretNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// MinSecretLength is the shortest value a named secret may hold: the length a
// value must have to be masked by value safely (redact.MinLiteralLength's
// reasoning), and a floor for a shared key besides.
const MinSecretLength = 8

// ValidSecretName reports whether name may name a secret or one of its
// fields.
func ValidSecretName(name string) bool {
	return secretNamePattern.MatchString(name)
}

// checkSecretNames refuses a name or field a reference could not name.
func checkSecretNames(name, field string) error {
	if !ValidSecretName(name) {
		return fmt.Errorf("secret name %q must be a lowercase identifier (a letter, then letters, digits or _; at most 64)", name)
	}
	if !ValidSecretName(field) {
		return fmt.Errorf("secret field %q must be a lowercase identifier (a letter, then letters, digits or _; at most 64)", field)
	}
	return nil
}

// SecretStore resolves one field of a named secret. The Crawl tier's run
// hands it to a method through sdk.NamedSecrets, which asks for the
// references its task wrote.
type SecretStore interface {
	// LookupSecret returns the value of field in the secret named name, and
	// false when there is no such secret or field.
	LookupSecret(ctx context.Context, name, field string) (string, bool, error)
}

// SaveSecretField stores value as field of the secret named name, encrypted
// under the project's master key like every device credential, replacing the
// field's old value and keeping the secret's other fields.
func SaveSecretField(dir string, key []byte, name, field, value string) error {
	if err := checkSecretNames(name, field); err != nil {
		return err
	}
	if len(value) < MinSecretLength {
		return fmt.Errorf("secret %s.%s: a value is at least %d characters, so masking it by value cannot also scrub ordinary words from a run's output", name, field, MinSecretLength)
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return fmt.Errorf("failed to initialize credential encryption: %w", err)
	}
	encrypted, err := encryptField(svc, []byte(value))
	if err != nil {
		return fmt.Errorf("failed to encrypt secret %s.%s: %w", name, field, err)
	}
	return updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		if doc.Secrets == nil {
			doc.Secrets = make(map[string]map[string]string)
		}
		if doc.Secrets[name] == nil {
			doc.Secrets[name] = make(map[string]string)
		}
		doc.Secrets[name][field] = encrypted
		return nil
	})
}

// RemoveSecret removes field of the secret named name, or the whole secret
// when field is empty, reporting whether there was anything to remove. A
// secret left with no fields is removed too.
func RemoveSecret(dir, name, field string) (bool, error) {
	removed := false
	err := updateCredentialsDocument(dir, func(doc *credentialsDocument) error {
		fields, ok := doc.Secrets[name]
		if !ok {
			return nil
		}
		if field == "" {
			delete(doc.Secrets, name)
			removed = true
			return nil
		}
		if _, ok := fields[field]; ok {
			delete(fields, field)
			removed = true
		}
		if len(fields) == 0 {
			delete(doc.Secrets, name)
		}
		return nil
	})
	return removed, err
}

// SecretFields is one stored secret as a listing shows it: its name and its
// fields' names, never a value.
type SecretFields struct {
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

// ListSecrets returns every stored secret's name and field names, sorted.
// Listing needs no master key and decrypts nothing.
func ListSecrets(dir string) ([]SecretFields, error) {
	doc, err := readCredentialsDocument(credentialsPath(dir))
	if err != nil {
		return nil, err
	}
	out := make([]SecretFields, 0, len(doc.Secrets))
	for name, fields := range doc.Secrets {
		s := SecretFields{Name: name}
		for f := range fields {
			s.Fields = append(s.Fields, f)
		}
		sort.Strings(s.Fields)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SecretValues returns the value of every field of every named secret in
// dir's credential file, for a run to mask from the start: a run that only
// reads a device (show running-config) never resolves the pre-shared key it
// prints, and must mask it all the same. A project with no named secrets
// needs no master key, and none is resolved.
func SecretValues(dir string) ([]string, error) {
	doc, err := readCredentialsDocument(credentialsPath(dir))
	if err != nil || len(doc.Secrets) == 0 {
		return nil, err
	}
	key, err := ResolveMasterKey(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve credential master key: %w", err)
	}
	svc, err := crypto.NewAESService(key)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize credential encryption: %w", err)
	}
	s := &fileStore{path: credentialsPath(dir), svc: svc}
	var out []string
	for name, fields := range doc.Secrets {
		for field, encrypted := range fields {
			plain, err := s.decryptField(encrypted)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt secret %s.%s: %w", name, field, err)
			}
			out = append(out, string(plain))
		}
	}
	return out, nil
}

// LookupSecret implements SecretStore.
func (s *fileStore) LookupSecret(_ context.Context, name, field string) (string, bool, error) {
	if err := checkSecretNames(name, field); err != nil {
		return "", false, err
	}
	doc, err := readCredentialsDocument(s.path)
	if err != nil {
		return "", false, err
	}
	encrypted, ok := doc.Secrets[name][field]
	if !ok {
		return "", false, nil
	}
	plain, err := s.decryptField(encrypted)
	if err != nil {
		return "", false, fmt.Errorf("failed to decrypt secret %s.%s: %w", name, field, err)
	}
	return string(plain), true, nil
}

// LookupSecret implements SecretStore, resolving the master key on first use
// as Lookup does.
func (l *lazyFileStore) LookupSecret(ctx context.Context, name, field string) (string, bool, error) {
	if err := l.open(); err != nil {
		return "", false, err
	}
	ss, ok := l.store.(SecretStore)
	if !ok {
		return "", false, nil
	}
	return ss.LookupSecret(ctx, name, field)
}
