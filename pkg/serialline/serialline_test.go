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
