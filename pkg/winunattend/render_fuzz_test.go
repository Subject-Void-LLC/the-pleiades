// Fuzz tests for the answer files winunattend renders. Hostile values in
// every field a caller sets must either leave well-formed XML that holds
// each value, escaped, in its own element and nowhere else, or be refused
// by a message naming the field.
package winunattend

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

// baseSeed is a seed Validate accepts. A fuzzed seed's answer file is
// compared with this one's, element by element.
var baseSeed = Seed{
	ComputerName:          "PLEIADES-W1",
	AdministratorPassword: "Baseline-Pass-42",
	Interfaces:            []Interface{{MAC: "080027AABBCC", Address: netip.MustParsePrefix("192.168.56.30/24")}},
}

// baseInstall is an installation Validate accepts. A fuzzed
// installation's answer file is compared with this one's.
var baseInstall = Install{
	Image:         "Windows Server 2025 Standard Evaluation (Desktop Experience)",
	AuditPassword: "Baseline-Pass-42",
}

// growth bounds how many bytes an answer file may grow per byte of
// input: a value appears at most five times, and escaping writes at most
// six bytes for one.
const growth = 5 * 6

// answerElement is one element of a decoded answer file: its path from
// the root, as names joined by slashes, and the text directly inside it.
type answerElement struct {
	path string
	text string
}

// field is a caller's value and the element, named by the end of its
// path, that must hold it.
type field struct {
	suffix string
	value  string
}

// seedInput is one fuzz input for a Seed: every value a caller sets, the
// adapter's address as text.
type seedInput struct {
	name     string
	password string
	mac      string
	address  string
	lang     string
	zone     string
}

// installInput is one fuzz input for an Install: every value a caller
// sets.
type installInput struct {
	image    string
	password string
	lang     string
}

// parseAnswer decodes an answer file with encoding/xml, failing the test
// unless it is well-formed with one root element. It returns the file's
// shape (every token except text, in order, with its attributes) and its
// elements in document order.
func parseAnswer(t *testing.T, doc []byte) ([]string, []answerElement) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(doc))
	var shape, names []string
	var elements []answerElement
	var open []int
	roots := 0
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("the answer file is not well-formed XML: %v\n%s", err, doc)
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if len(open) == 0 {
				roots++
			}
			names = append(names, tok.Name.Local)
			s := "<" + tok.Name.Space + " " + tok.Name.Local
			for _, a := range tok.Attr {
				s += " " + a.Name.Space + ":" + a.Name.Local + "=" + strconv.Quote(a.Value)
			}
			shape = append(shape, s)
			elements = append(elements, answerElement{path: strings.Join(names, "/")})
			open = append(open, len(elements)-1)
		case xml.EndElement:
			shape = append(shape, "</"+tok.Name.Space+" "+tok.Name.Local)
			names = names[:len(names)-1]
			open = open[:len(open)-1]
		case xml.CharData:
			if len(open) == 0 {
				if strings.TrimSpace(string(tok)) != "" {
					t.Fatalf("the answer file holds text outside its root element: %q", tok)
				}
				continue
			}
			elements[open[len(open)-1]].text += string(tok)
		case xml.Comment:
			shape = append(shape, "<!--"+string(tok))
		case xml.ProcInst:
			shape = append(shape, "<?"+tok.Target+" "+string(tok.Inst))
		case xml.Directive:
			shape = append(shape, "<!"+string(tok))
		}
	}
	if len(open) != 0 {
		t.Fatalf("the answer file leaves %d elements open", len(open))
	}
	if roots != 1 {
		t.Fatalf("the answer file has %d root elements, want 1", roots)
	}
	return shape, elements
}

// fieldFor returns the index of the field whose element ends path, or -1
// when path is not a field's element.
func fieldFor(path string, fields []field) int {
	for j, f := range fields {
		if strings.HasSuffix(path, "/"+f.suffix) {
			return j
		}
	}
	return -1
}

// checkAnswer proves got is well-formed and has want's exact tags and
// attributes, that each field's element holds that field's value exactly
// once decoded, and that every other element holds what it holds in
// want. Together these show no value added an element or an attribute,
// none escaped its own element, and each was escaped rather than read as
// markup.
func checkAnswer(t *testing.T, got, want []byte, fields []field) {
	t.Helper()
	gotShape, gotElements := parseAnswer(t, got)
	wantShape, wantElements := parseAnswer(t, want)
	if len(gotShape) != len(wantShape) {
		t.Fatalf("the answer file has %d tags, want %d: a value added or removed one\n%s", len(gotShape), len(wantShape), got)
	}
	for i := range gotShape {
		if gotShape[i] != wantShape[i] {
			t.Fatalf("tag %d is %q, want %q: a value changed the file's structure\n%s", i, gotShape[i], wantShape[i], got)
		}
	}
	hits := make([]int, len(fields))
	for i, e := range gotElements {
		j := fieldFor(e.path, fields)
		if j < 0 {
			if e.text != wantElements[i].text {
				t.Errorf("%s holds %q, not %q: a value reached an element not its own", e.path, e.text, wantElements[i].text)
			}
			continue
		}
		hits[j]++
		if e.text != fields[j].value {
			t.Errorf("%s holds %q, want %q", e.path, e.text, fields[j].value)
		}
	}
	for j, f := range fields {
		if hits[j] == 0 {
			t.Errorf("no element %s holds its value %q", f.suffix, f.value)
		}
	}
}

// checkRefusal proves a renderer refused with an error that names field,
// returned no bytes with it, and holds no raw line break a value put
// there.
func checkRefusal(t *testing.T, out []byte, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatalf("a value its %s check refuses was accepted", field)
	}
	if out != nil {
		t.Errorf("the refusal came with %d bytes of answer file", len(out))
	}
	if !strings.Contains(err.Error(), field) {
		t.Errorf("refusal %q does not name the %s", err, field)
	}
	if strings.ContainsAny(err.Error(), "\r\n") {
		t.Errorf("refusal %q holds a raw line break", err)
	}
}

// checkPasswordUnsaid proves a password refusal is one of checkPassword's
// two fixed messages, so it never repeats the password it refused.
func checkPasswordUnsaid(t *testing.T, err error) {
	t.Helper()
	for _, p := range []string{"x", "\x00"} {
		if err.Error() == checkPassword(p).Error() {
			return
		}
	}
	t.Errorf("password refusal %q is not one of the fixed messages, so it may repeat the password", err)
}

// seedRefusal returns the field Seed.Validate should name when it
// refuses s, or "" when every field is acceptable. It reads the same
// patterns and helpers Validate does, in the same order, so the fuzz
// target checks each refusal names its field rather than re-deciding
// what is valid.
func seedRefusal(s Seed) string {
	if !computerNamePattern.MatchString(s.ComputerName) || isDigits(s.ComputerName) {
		return "computer name"
	}
	if checkPassword(s.AdministratorPassword) != nil {
		return "password"
	}
	if len(s.Interfaces) == 0 {
		return "adapter"
	}
	for _, i := range s.Interfaces {
		if _, err := dashedMAC(i.MAC); err != nil {
			return "MAC address"
		}
		a := i.Address
		if !a.IsValid() || !a.Addr().Is4() || a.Bits() < 8 || a.Bits() > 30 {
			return "address"
		}
	}
	if _, err := language(s.Language); err != nil {
		return "language"
	}
	if s.TimeZone != "" && !timeZonePattern.MatchString(s.TimeZone) {
		return "time zone"
	}
	return ""
}

// installRefusal returns the field Install.Validate should name when it
// refuses i, or "" when every field is acceptable, reading the same
// patterns and helpers Validate does, in the same order.
func installRefusal(i Install) string {
	if !imageNamePattern.MatchString(i.Image) {
		return "image"
	}
	if n, err := strconv.Atoi(i.Image); err == nil && n < 1 {
		return "image index"
	}
	if _, err := language(i.Language); err != nil {
		return "language"
	}
	if checkPassword(i.AuditPassword) != nil {
		return "password"
	}
	return ""
}

// FuzzSeedXML proves a Seed's answer file is well-formed XML holding each
// value exactly, escaped, in its own element only, whatever a caller
// passes; that it grows at most linearly with its input; and that a seed
// it cannot carry is refused by a message naming the field, never
// repeating a refused password.
func FuzzSeedXML(f *testing.F) {
	good := seedInput{"PLEIADES-W1", "Correct-Horse-9", "08:00:27:aa:bb:cc", "192.168.56.30/24", "", ""}
	for _, change := range []func(*seedInput){
		func(*seedInput) {},
		func(in *seedInput) {
			*in = seedInput{"WEB01", "Pass word 2026!", "080027AABBCC", "10.0.0.5/8", "pt-BR", "Pacific Standard Time"}
		},
		func(in *seedInput) { in.password = "</Value><PlainText>false</PlainText><Value>Aa1" },
		func(in *seedInput) { in.password = "Aa1&amp;<!--" },
		func(in *seedInput) { in.password = `Aa1"]]><x/>'"` },
		func(in *seedInput) { in.password = "Aa1&#x0;bcd" },
		func(in *seedInput) { in.password = strings.Repeat("<a>", 30) + "Aa1" },
		func(in *seedInput) { in.password = "-Aa1bcdefg" },
		func(in *seedInput) { in.password = "Aa1\u2028bcde" },
		func(in *seedInput) { in.password = "Aa1!bcd\nefg" },
		func(in *seedInput) { in.password = "Aa1!bcd\x00" },
		func(in *seedInput) { in.password = "Aa1!bcd\xff" },
		func(in *seedInput) { in.password = strings.Repeat("Aa1!", 1000) },
		func(in *seedInput) { in.password = "MyAdministrator1" },
		// U+FFFE is no control character, and XML 1.0 cannot carry it:
		// checkPassword let it through until 2026-11-13, and the escaper
		// wrote U+FFFD in its place, a password nobody chose.
		func(in *seedInput) { in.password = "Aa1!bcde\uFFFE" },
		func(in *seedInput) { in.name = "PLEIADES\nW1" },
		func(in *seedInput) { in.name = "-PLEIADES" },
		func(in *seedInput) { in.name = "12345" },
		func(in *seedInput) { in.name = strings.Repeat("A", 1<<16) },
		func(in *seedInput) { *in = seedInput{} },
		func(in *seedInput) { in.mac = "08:00:27:aa:bb:cc</Identifier>" },
		func(in *seedInput) { in.mac = "-080027AABBCC" },
		func(in *seedInput) { in.address = "192.168.56.30/31" },
		func(in *seedInput) { in.address = "fe80::1/64" },
		func(in *seedInput) { in.lang = "en-US\n" },
		func(in *seedInput) { in.lang = "en-US</UILanguage><x>" },
		func(in *seedInput) { in.zone = "UTC</TimeZone><RunSynchronous>" },
		func(in *seedInput) { in.zone = "UTC\n" },
		func(in *seedInput) { in.zone = "\"UTC" },
		func(in *seedInput) { in.zone = "W. Europe Standard Time" },
	} {
		in := good
		change(&in)
		f.Add(in.name, in.password, in.mac, in.address, in.lang, in.zone)
	}
	base, err := baseSeed.XML()
	if err != nil {
		f.Fatalf("the base seed is refused: %v", err)
	}
	f.Fuzz(func(t *testing.T, name, password, mac, address, lang, zone string) {
		checkSeedInput(t, base, seedInput{name, password, mac, address, lang, zone})
	})
}

// checkSeedInput holds one Seed to what FuzzSeedXML proves: refused by a
// message naming the field (never repeating a refused password), or an
// answer file of bounded size holding each value exactly, escaped, in its
// own element only, compared with base's.
func checkSeedInput(t *testing.T, base []byte, in seedInput) {
	t.Helper()
	prefix, _ := netip.ParsePrefix(in.address)
	s := Seed{
		ComputerName:          in.name,
		AdministratorPassword: in.password,
		Interfaces:            []Interface{{MAC: in.mac, Address: prefix}},
		Language:              in.lang,
		TimeZone:              in.zone,
	}
	out, err := s.XML()
	if field := seedRefusal(s); field != "" {
		checkRefusal(t, out, err, field)
		if field == "password" {
			checkPasswordUnsaid(t, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("XML refuses a seed whose every field is acceptable: %v", err)
	}
	input := len(in.name) + len(in.password) + len(in.mac) + len(in.address) + len(in.lang) + len(in.zone)
	if limit := len(base) + growth*input; len(out) > limit {
		t.Errorf("the answer file is %d bytes for %d bytes of input, more than %d", len(out), input, limit)
	}
	l := in.lang
	if l == "" {
		l = DefaultLanguage
	}
	z := in.zone
	if z == "" {
		z = DefaultTimeZone
	}
	dashed, _ := dashedMAC(in.mac)
	checkAnswer(t, out, base, []field{
		{"ComputerName", in.name},
		{"TimeZone", z},
		{"AdministratorPassword/Value", in.password},
		{"Identifier", dashed},
		{"IpAddress", prefix.String()},
		{"InputLocale", l},
		{"SystemLocale", l},
		{"UILanguage", l},
		{"UserLocale", l},
	})
}

// FuzzInstallXML proves an Install's answer file is well-formed XML
// holding the image, the language and the audit password exactly,
// escaped, in their own elements only, whatever a caller passes; that it
// grows at most linearly with its input; and that an installation it
// cannot carry is refused by a message naming the field, never repeating
// a refused password.
func FuzzInstallXML(f *testing.F) {
	good := installInput{"Windows Server 2025 Standard Evaluation (Desktop Experience)", "Correct-Horse-9", ""}
	for _, change := range []func(*installInput){
		func(*installInput) {},
		func(in *installInput) { in.image, in.lang = "1", "de-DE" },
		func(in *installInput) { in.image = "0001" },
		func(in *installInput) { in.image = "0" },
		func(in *installInput) { in.image = "-1" },
		func(in *installInput) { in.image = "Windows</Value><x>" },
		func(in *installInput) { in.image = "Windows\n" },
		func(in *installInput) { in.image = strings.Repeat("9", 200) },
		func(in *installInput) { in.image = "Windows (Core" },
		func(in *installInput) { in.password = "</Value></Password><Username>evil</Username>Aa1" },
		func(in *installInput) { in.password = `Aa1"'"&lt;` },
		func(in *installInput) { in.password = strings.Repeat("<a>", 30) + "Aa1" },
		func(in *installInput) { in.password = "Aa1!bcd\xff" },
		func(in *installInput) { in.password = "Aa1!bcd\r\n" },
		// U+FFFF, the other noncharacter XML 1.0 cannot carry.
		func(in *installInput) { in.password = "Aa1!bcde\uFFFF" },
		func(in *installInput) { in.lang = "en-us" },
		func(in *installInput) { in.lang = "en-US\x00" },
		func(in *installInput) { *in = installInput{} },
	} {
		in := good
		change(&in)
		f.Add(in.image, in.password, in.lang)
	}
	base, err := baseInstall.XML()
	if err != nil {
		f.Fatalf("the base installation is refused: %v", err)
	}
	f.Fuzz(func(t *testing.T, image, password, lang string) {
		checkInstallInput(t, base, installInput{image, password, lang})
	})
}

// checkInstallInput holds one Install to what FuzzInstallXML proves:
// refused by a message naming the field (never repeating a refused
// password), or an answer file of bounded size holding the image, the
// language and the audit password exactly, escaped, in their own elements
// only, compared with base's.
func checkInstallInput(t *testing.T, base []byte, in installInput) {
	t.Helper()
	i := Install{Image: in.image, Language: in.lang, AuditPassword: in.password}
	out, err := i.XML()
	if field := installRefusal(i); field != "" {
		checkRefusal(t, out, err, field)
		if field == "password" {
			checkPasswordUnsaid(t, err)
		}
		return
	}
	if err != nil {
		t.Fatalf("XML refuses an installation whose every field is acceptable: %v", err)
	}
	input := len(in.image) + len(in.password) + len(in.lang)
	if limit := len(base) + growth*input; len(out) > limit {
		t.Errorf("the answer file is %d bytes for %d bytes of input, more than %d", len(out), input, limit)
	}
	l := in.lang
	if l == "" {
		l = DefaultLanguage
	}
	key := "/IMAGE/NAME"
	if isDigits(in.image) {
		key = "/IMAGE/INDEX"
	}
	checkAnswer(t, out, base, []field{
		{"MetaData/Key", key},
		{"MetaData/Value", in.image},
		{"AdministratorPassword/Value", in.password},
		{"AutoLogon/Password/Value", in.password},
		{"InputLocale", l},
		{"SystemLocale", l},
		{"UILanguage", l},
		{"UserLocale", l},
	})
}
