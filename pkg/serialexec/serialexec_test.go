package serialexec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialline"
	realserial "go.bug.st/serial"
)

func TestModeFrom_TranslatesEveryField(t *testing.T) {
	mode, err := modeFrom(serialline.Config{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serialline.ParityEven,
		StopBits: serialline.StopBitsTwo,
	})
	if err != nil {
		t.Fatalf("modeFrom: %v", err)
	}
	if mode.BaudRate != 115200 {
		t.Errorf("BaudRate = %d, want 115200", mode.BaudRate)
	}
	if mode.DataBits != 8 {
		t.Errorf("DataBits = %d, want 8", mode.DataBits)
	}
	if mode.Parity != realserial.EvenParity {
		t.Errorf("Parity = %v, want EvenParity", mode.Parity)
	}
	if mode.StopBits != realserial.TwoStopBits {
		t.Errorf("StopBits = %v, want TwoStopBits", mode.StopBits)
	}
}

func TestModeFrom_InvalidStopBitsFailsEvenWithValidParity(t *testing.T) {
	_, err := modeFrom(serialline.Config{Parity: serialline.ParityNone, StopBits: serialline.StopBits(99)})
	if err == nil {
		t.Fatal("expected modeFrom to reject an invalid StopBits value even when Parity is valid")
	}
}

func TestParityFrom_AllFiveModes(t *testing.T) {
	tests := []struct {
		in   serialline.Parity
		want realserial.Parity
	}{
		{serialline.ParityNone, realserial.NoParity},
		{serialline.ParityOdd, realserial.OddParity},
		{serialline.ParityEven, realserial.EvenParity},
		{serialline.ParityMark, realserial.MarkParity},
		{serialline.ParitySpace, realserial.SpaceParity},
	}
	for _, tt := range tests {
		t.Run(tt.in.String(), func(t *testing.T) {
			got, err := parityFrom(tt.in)
			if err != nil {
				t.Fatalf("parityFrom(%v): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parityFrom(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParityFrom_UnknownValueIsAnError(t *testing.T) {
	if _, err := parityFrom(serialline.Parity(99)); err == nil {
		t.Error("expected an error for an unknown Parity value")
	}
}

func TestStopBitsFrom_AllThreeModes(t *testing.T) {
	tests := []struct {
		in   serialline.StopBits
		want realserial.StopBits
	}{
		{serialline.StopBitsOne, realserial.OneStopBit},
		{serialline.StopBitsOnePointFive, realserial.OnePointFiveStopBits},
		{serialline.StopBitsTwo, realserial.TwoStopBits},
	}
	for _, tt := range tests {
		t.Run(tt.in.String(), func(t *testing.T) {
			got, err := stopBitsFrom(tt.in)
			if err != nil {
				t.Fatalf("stopBitsFrom(%v): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("stopBitsFrom(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestStopBitsFrom_UnknownValueIsAnError(t *testing.T) {
	if _, err := stopBitsFrom(serialline.StopBits(99)); err == nil {
		t.Error("expected an error for an unknown StopBits value")
	}
}

func TestExec_InvalidLineConfigFailsBeforeAnyOpen(t *testing.T) {
	_, err := Exec(context.Background(), "/dev/irrelevant", serialline.Config{Parity: serialline.Parity(99)}, Options{}, "cmd")
	if err == nil {
		t.Fatal("expected an error for an invalid Parity, before any device open was attempted")
	}
}

// fakePort is a minimal realserial.Port double for exercising
// readUntilQuiet's branches that a real PTY cannot conveniently drive:
// unbounded output and context cancellation mid-stream. The main
// data-path, timeout, and lifecycle claims are proven against a real
// socat PTY pair in serialexec_pty_test.go, per RULE 0; this fake earns
// its place only for the two branches that need deterministic,
// instant behavior a real timing-based device would make flaky.
type fakePort struct {
	realserial.Port
	reads    [][]byte
	readErrs []error
	readIdx  int
}

func (f *fakePort) Read(p []byte) (int, error) {
	if f.readIdx >= len(f.reads) {
		return 0, nil
	}
	data := f.reads[f.readIdx]
	err := f.readErrs[f.readIdx]
	f.readIdx++
	n := copy(p, data)
	return n, err
}

func TestReadUntilQuiet_StopsOnZeroByteRead(t *testing.T) {
	port := &fakePort{
		reads:    [][]byte{[]byte("hello "), []byte("world"), nil},
		readErrs: []error{nil, nil, nil},
	}
	out, err := readUntilQuiet(context.Background(), port, DefaultMaxOutputBytes)
	if err != nil {
		t.Fatalf("readUntilQuiet: %v", err)
	}
	if got := string(out); got != "hello world" {
		t.Errorf("readUntilQuiet accumulated %q, want %q", got, "hello world")
	}
}

func TestReadUntilQuiet_PropagatesAReadError(t *testing.T) {
	wantErr := errors.New("boom")
	port := &fakePort{
		reads:    [][]byte{nil},
		readErrs: []error{wantErr},
	}
	_, err := readUntilQuiet(context.Background(), port, DefaultMaxOutputBytes)
	if !errors.Is(err, wantErr) {
		t.Errorf("readUntilQuiet error = %v, want %v", err, wantErr)
	}
}

// infinitePort never goes quiet, so readUntilQuiet can only stop via
// context cancellation or the max-output cap -- exactly the two
// conditions this fake exists to drive deterministically.
type infinitePort struct {
	realserial.Port
	chunk []byte
}

func (p *infinitePort) Read(buf []byte) (int, error) {
	return copy(buf, p.chunk), nil
}

func TestReadUntilQuiet_StopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readUntilQuiet(ctx, &infinitePort{chunk: []byte("x")}, DefaultMaxOutputBytes)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("readUntilQuiet error = %v, want context.Canceled", err)
	}
}

func TestReadUntilQuiet_RefusesOutputExceedingTheCap(t *testing.T) {
	_, err := readUntilQuiet(context.Background(), &infinitePort{chunk: []byte(strings.Repeat("x", 4096))}, 10)
	if err == nil {
		t.Fatal("expected an error once accumulated output exceeded the cap")
	}
}
