// Package pcapsum: when each packet was captured. A pcap record gives
// seconds and a fraction in microseconds, or nanoseconds when the file's
// magic says so; a pcapng enhanced packet block gives one 64-bit count in
// the units its interface's if_tsresol option names, microseconds when it
// names none (the pcapng specification, section 4.2). A simple packet block
// has no time at all, so its packet has none.
package pcapsum

import (
	"encoding/binary"
	"math/bits"
	"time"
)

// tsUnits is how many of a pcapng interface's time units make a second;
// 0 when the interface's resolution cannot be read as a count that fits.
type tsUnits uint64

// defaultUnits is pcapng's resolution when an interface gives none:
// microseconds.
const defaultUnits tsUnits = 1_000_000

// ifTSResol is the interface description option that gives its time
// resolution.
const ifTSResol = 9

// interfaceUnits reads an interface description block's options, body past
// its first 8 bytes, for if_tsresol: a byte whose high bit clear says the
// units are 10 to the minus its value of a second, and set says 2 to the
// minus its low seven bits. A resolution too fine to count in 64 bits gives
// 0, so its packets have no time rather than a wrong one.
func interfaceUnits(order binary.ByteOrder, opts []byte) tsUnits {
	for len(opts) >= 4 {
		code, length := order.Uint16(opts), int(order.Uint16(opts[2:]))
		if code == 0 {
			// opt_endofopt.
			break
		}
		padded := (length + 3) &^ 3
		if 4+padded > len(opts) {
			break
		}
		if code == ifTSResol && length >= 1 {
			v := opts[4]
			if v&0x80 != 0 {
				if e := v & 0x7f; e < 64 {
					return tsUnits(1) << e
				}
				return 0
			}
			if v > 19 {
				return 0
			}
			u := tsUnits(1)
			for range v {
				u *= 10
			}
			return u
		}
		opts = opts[4+padded:]
	}
	return defaultUnits
}

// at is the time count, in units a second, as a time; the zero time when
// units is 0.
func (units tsUnits) at(count uint64) time.Time {
	if units == 0 {
		return time.Time{}
	}
	sec, frac := count/uint64(units), count%uint64(units)
	// frac/units of a second in nanoseconds, computed in 128 bits: frac is
	// below units, so the quotient is below a billion and fits.
	hi, lo := bits.Mul64(frac, uint64(time.Second))
	nanos, _ := bits.Div64(hi, lo, uint64(units))
	if sec > 1<<62 {
		// Past any time a capture was made.
		return time.Time{}
	}
	return time.Unix(int64(sec), int64(nanos)).UTC()
}

// timeSpan is the first and last time of packets seen, either zero until one
// with a time is.
type timeSpan struct {
	first, last time.Time
}

// see widens s to hold t; a packet with no time changes nothing.
func (s *timeSpan) see(t time.Time) {
	if t.IsZero() {
		return
	}
	if s.first.IsZero() || t.Before(s.first) {
		s.first = t
	}
	if s.last.IsZero() || t.After(s.last) {
		s.last = t
	}
}

// seconds is the span's length in seconds, 0 for a span of one instant or
// none.
func (s timeSpan) seconds() float64 {
	return s.last.Sub(s.first).Seconds()
}
