// Package pcapsum: when each packet was captured. A pcap record gives
// seconds and a fraction in microseconds, or nanoseconds when the file's
// magic says so; a pcapng enhanced packet block gives one 64-bit count in
// the units its interface's if_tsresol option names, microseconds when it
// names none, from the second its if_tsoffset option names, the epoch when
// it names none (the pcapng specification, section 4.2). A simple packet
// block has no time at all, so its packet has none, and neither has a time
// RFC 3339 cannot write (before year 1 or after year 9999).
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

// The interface description options that give its time resolution and the
// second its time counts from.
const (
	ifTSResol  = 9
	ifTSOffset = 14
)

// tsClock is a pcapng interface's clock: its units a second and the second,
// from the epoch, its counts start at.
type tsClock struct {
	units  tsUnits
	offset int64
}

// pcapngOption returns the value of option code among an interface
// description block's options, body past its first 8 bytes, and whether it
// is there. A list cut short ends the search.
func pcapngOption(order binary.ByteOrder, opts []byte, code uint16) ([]byte, bool) {
	for len(opts) >= 4 {
		c, length := order.Uint16(opts), int(order.Uint16(opts[2:]))
		if c == 0 {
			// opt_endofopt.
			break
		}
		padded := (length + 3) &^ 3
		if 4+padded > len(opts) {
			break
		}
		if c == code {
			return opts[4 : 4+length], true
		}
		opts = opts[4+padded:]
	}
	return nil, false
}

// interfaceClock reads an interface's clock from its options.
func interfaceClock(order binary.ByteOrder, opts []byte) tsClock {
	clock := tsClock{units: interfaceUnits(order, opts)}
	if v, ok := pcapngOption(order, opts, ifTSOffset); ok && len(v) == 8 {
		// A signed count of seconds, in the section's byte order.
		clock.offset = int64(order.Uint64(v)) // #nosec G115 -- the option is a signed 64-bit integer, read as one
	}
	return clock
}

// interfaceUnits reads if_tsresol from an interface description block's
// options, body past its first 8 bytes: a byte whose high bit clear says the
// units are 10 to the minus its value of a second, and set says 2 to the
// minus its low seven bits. A resolution too fine to count in 64 bits gives
// 0, so its packets have no time rather than a wrong one.
func interfaceUnits(order binary.ByteOrder, opts []byte) tsUnits {
	v, ok := pcapngOption(order, opts, ifTSResol)
	if !ok || len(v) < 1 {
		return defaultUnits
	}
	if v[0]&0x80 != 0 {
		if e := v[0] & 0x7f; e < 64 {
			return tsUnits(1) << e
		}
		return 0
	}
	if v[0] > 19 {
		return 0
	}
	u := tsUnits(1)
	for range v[0] {
		u *= 10
	}
	return u
}

// at is count, in the clock's units from its offset, as a time; the zero
// time when the clock has no units or the time is one RFC 3339 cannot
// write.
func (c tsClock) at(count uint64) time.Time {
	t := c.units.at(count)
	if t.IsZero() {
		return t
	}
	sec := t.Unix()
	if c.offset > 0 && sec > maxUnix-c.offset || c.offset < 0 && sec < minUnix-c.offset {
		return time.Time{}
	}
	return writable(time.Unix(sec+c.offset, int64(t.Nanosecond())).UTC())
}

// at is the time count, in units a second, as a time; the zero time when
// units is 0, or when the time is one RFC 3339 cannot write.
func (units tsUnits) at(count uint64) time.Time {
	if units == 0 {
		return time.Time{}
	}
	sec, frac := count/uint64(units), count%uint64(units)
	// frac/units of a second in nanoseconds, computed in 128 bits: frac is
	// below units, so the quotient is below a billion and fits.
	hi, lo := bits.Mul64(frac, uint64(time.Second))
	nanos, _ := bits.Div64(hi, lo, uint64(units))
	if sec > maxUnix {
		return time.Time{}
	}
	return writable(time.Unix(int64(sec), int64(nanos)).UTC()) // #nosec G115 -- sec is at most maxUnix, checked above
}

// minUnix is the first second RFC 3339 writes, the start of year 1.
var minUnix = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).Unix()

// maxUnix is the last second RFC 3339 writes, the start of year 10000 less
// one second. It is an untyped constant so a uint64 count and an int64
// second compare with it without a conversion; TestMaxUnix_IsTheLastWritableSecond
// holds it to that date.
const maxUnix = 253402300799

// writable is t, or the zero time when RFC 3339 cannot write t's year.
func writable(t time.Time) time.Time {
	if y := t.Year(); y < 1 || y > 9999 {
		return time.Time{}
	}
	return t
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
