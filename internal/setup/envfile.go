// Reading and rewriting a compose env file: strict on the lines setup owns, and
// byte for byte on every other line.
package setup

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// ErrEnvFile is wrapped by every refusal ParseEnvFile returns.
var ErrEnvFile = errors.New("setup: the env file cannot be read safely")

// EnvFile is a compose env file, as this command reads and rewrites it.
//
// Two rules make rewriting one safe. Lines this command owns (the variables
// ComposeVariables names) are read strictly: a form compose might read
// differently from this parser is refused rather than guessed at, since a
// key this command misread is a key it would count the wrong data against.
// Every other line is kept byte for byte, because an operator's own
// settings in the same file are theirs.
type EnvFile struct {
	lines    []envLine
	trailing bool
}

// envLine is one line of the file.
type envLine struct {
	// raw is the line exactly as read, without its line feed.
	raw string

	// name is the owned variable this line sets, or empty.
	name string

	// value is the owned variable's value.
	value string
}

// validators checks each owned variable's value. Each returns an error that
// never contains the value: a rejected value is often a real key one
// character off.
var validators = map[string]func(string) error{
	VarMasterKey:          validateKey,
	VarPreviousKey:        validateKey,
	VarMasterKeyVersion:   validateVersion,
	VarPreviousKeyVersion: validateVersion,
	VarRotate:             validateRotate,
	VarJWTSecret:          validateJWTSecret,
	VarMaxOutage:          validateMaxOutage,
}

// ParseEnvFile reads data as a compose env file.
//
// A refusal names the line and the variable, and never the value.
func ParseEnvFile(data []byte) (*EnvFile, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: it is not valid UTF-8", ErrEnvFile)
	}
	if strings.ContainsRune(string(data), 0) {
		return nil, fmt.Errorf("%w: it contains a NUL byte", ErrEnvFile)
	}

	text := string(data)
	f := &EnvFile{trailing: strings.HasSuffix(text, "\n")}
	if f.trailing {
		text = strings.TrimSuffix(text, "\n")
	}
	if text == "" && !f.trailing {
		return f, nil
	}

	seen := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		number := i + 1
		line := envLine{raw: raw}
		content := strings.TrimSuffix(raw, "\r")

		name, owned := ownedName(content)
		if !owned {
			if err := checkUnownedLine(content, number); err != nil {
				return nil, err
			}
			f.lines = append(f.lines, line)
			continue
		}

		value, err := strictValue(content, name, number)
		if err != nil {
			return nil, err
		}
		if first, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: %s is set on line %d and again on line %d; compose uses the last one, and setup will not guess which you meant", ErrEnvFile, name, first, number)
		}
		seen[name] = number
		if value != "" {
			if err := validators[name](value); err != nil {
				return nil, fmt.Errorf("%w: line %d: %s %v", ErrEnvFile, number, name, err)
			}
		}
		line.name, line.value = name, value
		f.lines = append(f.lines, line)
	}
	return f, nil
}

// ownedName reports whether content assigns one of this command's
// variables, in any spelling compose would accept (leading space, export,
// spaces around the equals sign), so that a non-strict spelling is caught
// and refused rather than treated as someone else's line.
func ownedName(content string) (string, bool) {
	trimmed := strings.TrimLeft(content, " \t")
	if strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	trimmed = strings.TrimPrefix(trimmed, "export ")
	trimmed = strings.TrimLeft(trimmed, " \t")
	before, _, found := splitAssignment(trimmed)
	if !found {
		return "", false
	}
	candidate := strings.TrimRight(before, " \t")
	if _, ok := validators[candidate]; ok {
		return candidate, true
	}
	return "", false
}

// splitAssignment splits a line at its first equals sign or colon. The
// colon is included because a dotenv reader may accept the YAML-like
// "NAME: value" spelling, and a line setting one of this command's own
// variables that way has to be recognised as such, and refused, rather than
// passed over as somebody else's line while a second definition is added
// below it.
func splitAssignment(s string) (before, after string, found bool) {
	i := strings.IndexAny(s, "=:")
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// strictValue returns an owned line's value, refusing any form but the one
// this command writes itself: NAME=value at the start of the line, with a
// value made only of characters compose cannot read two ways.
func strictValue(content, name string, number int) (string, error) {
	prefix := name + "="
	if !strings.HasPrefix(content, prefix) {
		return "", fmt.Errorf("%w: line %d: write %s as %s<value>, with nothing before the name and no spaces around the equals sign", ErrEnvFile, number, name, prefix)
	}
	value := content[len(prefix):]
	for _, r := range value {
		switch {
		case r == '"' || r == '\'' || r == '`':
			return "", fmt.Errorf("%w: line %d: the value of %s is quoted; setup reads only plain values, because compose reads quoted ones by rules it would have to copy", ErrEnvFile, number, name)
		case r == '$':
			return "", fmt.Errorf("%w: line %d: the value of %s contains a dollar sign, which compose replaces with another variable's value", ErrEnvFile, number, name)
		case r == '#':
			return "", fmt.Errorf("%w: line %d: the value of %s contains a number sign, which compose may read as the start of a comment", ErrEnvFile, number, name)
		case r == ' ' || r == '\t':
			return "", fmt.Errorf("%w: line %d: the value of %s contains a space, which compose may trim or end the value at", ErrEnvFile, number, name)
		case r == '\\':
			return "", fmt.Errorf("%w: line %d: the value of %s contains a backslash, which compose may read as an escape", ErrEnvFile, number, name)
		case r < 0x20 || r == 0x7f:
			return "", fmt.Errorf("%w: line %d: the value of %s contains a control character", ErrEnvFile, number, name)
		}
	}
	return value, nil
}

// checkUnownedLine refuses a line that opens a quoted value it does not
// close. Compose reads such a value across the following lines, and a
// line-by-line reader would mistake a line inside it for a real setting,
// including one of this command's own.
func checkUnownedLine(content string, number int) error {
	trimmed := strings.TrimLeft(content, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}
	_, after, found := splitAssignment(trimmed)
	if !found {
		return nil
	}
	value := strings.TrimLeft(after, " \t")
	if value == "" {
		return nil
	}
	quote := value[0]
	if quote != '"' && quote != '\'' {
		return nil
	}
	for i := 1; i < len(value); i++ {
		if value[i] == '\\' && quote == '"' {
			i++
			continue
		}
		if value[i] == quote {
			return nil
		}
	}
	return fmt.Errorf("%w: line %d starts a quoted value that continues onto later lines; setup does not rewrite a file whose values span lines, because it cannot find where each one ends the way compose does", ErrEnvFile, number)
}

// Get returns an owned variable's value. An empty value reads as absent,
// matching the controller, which treats an empty variable as unset.
func (f *EnvFile) Get(name string) (string, bool) {
	for _, l := range f.lines {
		if l.name == name && l.value != "" {
			return l.value, true
		}
	}
	return "", false
}

// Set writes an owned variable, replacing its line where it has one and
// adding a line at the end where it does not.
func (f *EnvFile) Set(name, value string) {
	for i, l := range f.lines {
		if l.name == name {
			f.lines[i] = envLine{raw: name + "=" + value, name: name, value: value}
			return
		}
	}
	f.lines = append(f.lines, envLine{raw: name + "=" + value, name: name, value: value})
	f.trailing = true
}

// AddComment adds comment lines at the end, each prefixed with "# ".
func (f *EnvFile) AddComment(lines ...string) {
	for _, l := range lines {
		f.lines = append(f.lines, envLine{raw: strings.TrimRight("# "+l, " ")})
	}
	f.trailing = true
}

// Bytes renders the file. A file read and not changed renders to exactly the
// bytes it was read from.
func (f *EnvFile) Bytes() []byte {
	var b strings.Builder
	for i, l := range f.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l.raw)
	}
	if f.trailing {
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// validateKey accepts exactly what the controller's own key loader accepts.
func validateKey(value string) error {
	if _, err := crypto.DecodeKey(value, "this line"); err != nil {
		return errors.New("is not base64 of exactly 32 bytes, which is what the controller requires")
	}
	return nil
}

// versionPattern is what a key version tag may look like: a short label,
// never containing the "$" the envelope format separates its fields with.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// validateVersion accepts a plain version tag.
func validateVersion(value string) error {
	if !versionPattern.MatchString(value) {
		return errors.New("is not a plain version tag of letters, digits, dots, dashes and underscores")
	}
	return nil
}

// validateRotate accepts only the two words the setting means.
func validateRotate(value string) error {
	if value != "true" && value != "false" {
		return errors.New("is neither true nor false")
	}
	return nil
}

// minJWTSecretBytes matches internal/auth's minimum for a static signing key.
const minJWTSecretBytes = 32

// jwtSecretPattern is the characters this command accepts in a JWT secret it
// reads back. Anything it generates is base64, which fits.
var jwtSecretPattern = regexp.MustCompile(`^[A-Za-z0-9+/=._~-]+$`)

// validateJWTSecret accepts a secret the controller would start with.
func validateJWTSecret(value string) error {
	if len(value) < minJWTSecretBytes {
		return fmt.Errorf("is shorter than the %d bytes the controller requires", minJWTSecretBytes)
	}
	if !jwtSecretPattern.MatchString(value) {
		return errors.New("contains a character setup does not read back; use letters, digits and + / = . _ ~ -")
	}
	return nil
}

// validateMaxOutage accepts what topology.ParseOutageBudget accepts. The
// budget is not a secret, so its own error, which quotes the value, stands.
func validateMaxOutage(value string) error {
	_, err := topology.ParseOutageBudget(value)
	return err
}
