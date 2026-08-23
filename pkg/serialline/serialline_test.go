package serialline_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
)

func TestParity_Valid(t *testing.T) {
	tests := []struct {
		name string
		p    serialline.Parity
		want bool
	}{
		{"none", serialline.ParityNone, true},
		{"odd", serialline.ParityOdd, true},
		{"even", serialline.ParityEven, true},
		{"mark", serialline.ParityMark, true},
		{"space", serialline.ParitySpace, true},
		{"negative", serialline.Parity(-1), false},
		{"beyond space", serialline.Parity(5), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.Valid(); got != tt.want {
				t.Errorf("Parity(%d).Valid() = %v, want %v", tt.p, got, tt.want)
			}
		})
	}
}

func TestParity_String(t *testing.T) {
	tests := []struct {
		p    serialline.Parity
		want string
	}{
		{serialline.ParityNone, "none"},
		{serialline.ParityOdd, "odd"},
		{serialline.ParityEven, "even"},
		{serialline.ParityMark, "mark"},
		{serialline.ParitySpace, "space"},
		{serialline.Parity(99), "Parity(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.p.String(); got != tt.want {
				t.Errorf("Parity(%d).String() = %q, want %q", tt.p, got, tt.want)
			}
		})
	}
}

func TestStopBits_Valid(t *testing.T) {
	tests := []struct {
		name string
		s    serialline.StopBits
		want bool
	}{
		{"one", serialline.StopBitsOne, true},
		{"one point five", serialline.StopBitsOnePointFive, true},
		{"two", serialline.StopBitsTwo, true},
		{"negative", serialline.StopBits(-1), false},
		{"beyond two", serialline.StopBits(3), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Valid(); got != tt.want {
				t.Errorf("StopBits(%d).Valid() = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestStopBits_String(t *testing.T) {
	tests := []struct {
		s    serialline.StopBits
		want string
	}{
		{serialline.StopBitsOne, "1"},
		{serialline.StopBitsOnePointFive, "1.5"},
		{serialline.StopBitsTwo, "2"},
		{serialline.StopBits(99), "StopBits(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.s.String(); got != tt.want {
				t.Errorf("StopBits(%d).String() = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

// TestConfig_FieldsRoundTrip is a minimal construction proof: Config is
// a plain data holder with no behavior of its own beyond its two enum
// fields' own methods, already covered above.
func TestConfig_FieldsRoundTrip(t *testing.T) {
	cfg := serialline.Config{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serialline.ParityEven,
		StopBits: serialline.StopBitsTwo,
	}
	if cfg.BaudRate != 115200 {
		t.Errorf("BaudRate = %d, want 115200", cfg.BaudRate)
	}
	if cfg.DataBits != 8 {
		t.Errorf("DataBits = %d, want 8", cfg.DataBits)
	}
	if cfg.Parity != serialline.ParityEven {
		t.Errorf("Parity = %v, want %v", cfg.Parity, serialline.ParityEven)
	}
	if cfg.StopBits != serialline.StopBitsTwo {
		t.Errorf("StopBits = %v, want %v", cfg.StopBits, serialline.StopBitsTwo)
	}
}

// TestDevice_IsOpaqueString is a documentation-as-test proof: Device is
// exactly a string underneath, with no smuggled structure, so a value
// with no slashes (a Windows COM port name) is just as valid as one that
// looks like a POSIX path.
func TestDevice_IsOpaqueString(t *testing.T) {
	for _, raw := range []string{"/dev/ttyUSB0", "/dev/tty.usbserial-A5069RR4", "COM3"} {
		d := serialline.Device(raw)
		if string(d) != raw {
			t.Errorf("Device(%q) round-trip = %q", raw, string(d))
		}
	}
}

// TestParseParity_RoundTripsEveryValue proves ParseParity is the exact
// inverse of Parity.String for all five modes. Asserting the round trip
// rather than a hand-written table is what keeps the two from drifting:
// adding a sixth mode with a new String case and no Parse case fails
// here without anyone remembering to extend a fixture list.
func TestParseParity_RoundTripsEveryValue(t *testing.T) {
	for _, want := range []serialline.Parity{
		serialline.ParityNone,
		serialline.ParityOdd,
		serialline.ParityEven,
		serialline.ParityMark,
		serialline.ParitySpace,
	} {
		got, err := serialline.ParseParity(want.String())
		if err != nil {
			t.Errorf("ParseParity(%q) returned %v, want nil", want.String(), err)
			continue
		}
		if got != want {
			t.Errorf("ParseParity(%q) = %v, want %v", want.String(), got, want)
		}
	}
}

// TestParseParity_AcceptsOperatorSpelling proves the whitespace and
// letter-case tolerance the doc comment promises, since the real input
// is a hand-written device property rather than generated text.
func TestParseParity_AcceptsOperatorSpelling(t *testing.T) {
	for _, raw := range []string{"EVEN", "Even", " even ", "\teven\n"} {
		got, err := serialline.ParseParity(raw)
		if err != nil {
			t.Errorf("ParseParity(%q) returned %v, want nil", raw, err)
			continue
		}
		if got != serialline.ParityEven {
			t.Errorf("ParseParity(%q) = %v, want %v", raw, got, serialline.ParityEven)
		}
	}
}

// TestParseParity_RefusesUnknown proves a typo is an error rather than a
// silent fall back to ParityNone, which is the whole reason this
// function returns an error at all: a wrong parity corrupts a line
// instead of failing it.
func TestParseParity_RefusesUnknown(t *testing.T) {
	for _, raw := range []string{"", "nome", "7", "no ne", "parity"} {
		if _, err := serialline.ParseParity(raw); err == nil {
			t.Errorf("ParseParity(%q) returned nil error, want a refusal", raw)
		}
	}
}

// TestParseStopBits_RoundTripsEveryValue is ParseParity's counterpart
// proof for the three stop-bit counts.
func TestParseStopBits_RoundTripsEveryValue(t *testing.T) {
	for _, want := range []serialline.StopBits{
		serialline.StopBitsOne,
		serialline.StopBitsOnePointFive,
		serialline.StopBitsTwo,
	} {
		got, err := serialline.ParseStopBits(want.String())
		if err != nil {
			t.Errorf("ParseStopBits(%q) returned %v, want nil", want.String(), err)
			continue
		}
		if got != want {
			t.Errorf("ParseStopBits(%q) = %v, want %v", want.String(), got, want)
		}
	}
}

// TestParseStopBits_TrimsSurroundingSpace proves the same hand-written
// input tolerance, without letter case (there are no letters in a
// stop-bit count).
func TestParseStopBits_TrimsSurroundingSpace(t *testing.T) {
	got, err := serialline.ParseStopBits("  1.5 ")
	if err != nil {
		t.Fatalf("ParseStopBits(\"  1.5 \") returned %v, want nil", err)
	}
	if got != serialline.StopBitsOnePointFive {
		t.Errorf("ParseStopBits(\"  1.5 \") = %v, want %v", got, serialline.StopBitsOnePointFive)
	}
}

// TestParseStopBits_RefusesUnknown proves an unrecognized count is
// refused rather than defaulted to one stop bit.
//
// "1,5" and "1.50" are in this list on purpose. Both are ways a person
// writes one and a half, and neither is accepted, because the loose
// forms of the other two real values ("1", "2") are themselves real
// values: there is no spelling this function can guess at safely.
func TestParseStopBits_RefusesUnknown(t *testing.T) {
	for _, raw := range []string{"", "1,5", "1.50", "3", "one"} {
		if _, err := serialline.ParseStopBits(raw); err == nil {
			t.Errorf("ParseStopBits(%q) returned nil error, want a refusal", raw)
		}
	}
}
