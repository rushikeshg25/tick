package tick

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// crockford is Crockford base32: the digits and uppercase letters with I, L,
// O and U removed. The alphabet is in ascending ASCII order, so the
// lexicographic order of encoded ULIDs matches the order of their bytes.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ULID is a 128-bit identifier: a 48-bit millisecond timestamp followed by 80
// bits of entropy, stored big-endian.
type ULID [16]byte

// Time returns the embedded timestamp.
func (u ULID) Time() time.Time {
	ms := int64(u[0])<<40 | int64(u[1])<<32 | int64(u[2])<<24 |
		int64(u[3])<<16 | int64(u[4])<<8 | int64(u[5])
	return time.UnixMilli(ms).UTC()
}

// Counter returns the 12-bit within-millisecond counter this package writes
// into the top of the entropy field.
func (u ULID) Counter() uint16 {
	return uint16(u[6])<<4 | uint16(u[7]>>4)
}

// String returns the canonical 26-character Crockford base32 form.
//
// 26 characters carry 130 bits, so the value is encoded as if left-padded
// with two zero bits; the first character therefore never exceeds '7'.
func (u ULID) String() string {
	hi := uint64(u[0])<<56 | uint64(u[1])<<48 | uint64(u[2])<<40 | uint64(u[3])<<32 |
		uint64(u[4])<<24 | uint64(u[5])<<16 | uint64(u[6])<<8 | uint64(u[7])
	lo := uint64(u[8])<<56 | uint64(u[9])<<48 | uint64(u[10])<<40 | uint64(u[11])<<32 |
		uint64(u[12])<<24 | uint64(u[13])<<16 | uint64(u[14])<<8 | uint64(u[15])

	var s [26]byte
	for i := 0; i < 26; i++ {
		shift := uint(125 - 5*i)
		s[i] = crockford[shiftRight128(hi, lo, shift)&0x1f]
	}
	return string(s[:])
}

// shiftRight128 returns the low 64 bits of the 128-bit value hi:lo shifted
// right by n, for n < 128.
func shiftRight128(hi, lo uint64, n uint) uint64 {
	switch {
	case n == 0:
		return lo
	case n >= 64:
		return hi >> (n - 64)
	default:
		return lo>>n | hi<<(64-n)
	}
}

// MarshalText implements encoding.TextMarshaler.
func (u ULID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (u *ULID) UnmarshalText(text []byte) error {
	parsed, err := ParseULID(string(text))
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

var crockfordDecode = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(crockford); i++ {
		t[crockford[i]] = int8(i)
		// Accept lowercase on input; canonical output stays uppercase.
		if c := crockford[i]; c >= 'A' && c <= 'Z' {
			t[c+('a'-'A')] = int8(i)
		}
	}
	return t
}()

// ParseULID reads the canonical 26-character form. Input may be lowercase.
func ParseULID(s string) (ULID, error) {
	var u ULID
	if len(s) != 26 {
		return u, fmt.Errorf("tick: invalid ULID %q: want 26 characters, got %d", s, len(s))
	}

	var hi, lo uint64
	for i := 0; i < 26; i++ {
		v := crockfordDecode[s[i]]
		if v < 0 {
			return u, fmt.Errorf("tick: invalid ULID %q: %q is not in the Crockford alphabet", s, s[i])
		}
		if i == 0 && v > 7 {
			return u, fmt.Errorf("tick: invalid ULID %q: first character %q overflows 128 bits", s, s[i])
		}
		hi = hi<<5 | lo>>59
		lo = lo<<5 | uint64(v)
	}

	for i := 0; i < 8; i++ {
		u[i] = byte(hi >> (56 - 8*i))
		u[8+i] = byte(lo >> (56 - 8*i))
	}
	return u, nil
}

// ULIDGenerator issues ULIDs that are strictly increasing, including within a
// single millisecond.
//
// The specification leaves same-millisecond ordering to the implementation.
// This one writes the shared watermark's 12-bit counter into the top of the
// entropy field and fills the remaining 68 bits freshly on every call, so
// ordering comes from the counter and collision resistance from the rest.
// Throughput is therefore capped at 4096 per millisecond, matching the other
// formats in this package.
//
// A ULIDGenerator is safe for concurrent use.
type ULIDGenerator struct {
	wm *watermark
}

// NewULIDGenerator returns a generator for monotonic ULIDs.
func NewULIDGenerator(opts ...Option) (*ULIDGenerator, error) {
	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}
	if cfg.safe != nil {
		return nil, errors.New("tick: WithLease does not apply to a format without a node id")
	}
	wm := newWatermark(cfg.clock, cfg.tol, SequenceBits)
	if _, err := wm.timestamp(); err != nil {
		return nil, err
	}
	return &ULIDGenerator{wm: wm}, nil
}

// Next returns the next ULID, waiting for the next millisecond if this one's
// counter space is exhausted.
func (g *ULIDGenerator) Next() (ULID, error) { return g.next(true) }

// TryNext returns the next ULID without waiting.
func (g *ULIDGenerator) TryNext() (ULID, error) { return g.next(false) }

func (g *ULIDGenerator) next(block bool) (ULID, error) {
	ts, seq, err := g.wm.advance(block)
	if err != nil {
		return ULID{}, err
	}

	unixMs := int64(ts) + Epoch
	r := rand.Uint64()

	var u ULID
	u[0] = byte(unixMs >> 40)
	u[1] = byte(unixMs >> 32)
	u[2] = byte(unixMs >> 24)
	u[3] = byte(unixMs >> 16)
	u[4] = byte(unixMs >> 8)
	u[5] = byte(unixMs)
	// Entropy: 12 bits of counter, then 68 bits of randomness.
	u[6] = byte(seq >> 4)
	u[7] = byte(seq<<4) | byte(r>>60)&0x0f
	u[8] = byte(r >> 56)
	u[9] = byte(r >> 48)
	u[10] = byte(r >> 40)
	u[11] = byte(r >> 32)
	u[12] = byte(r >> 24)
	u[13] = byte(r >> 16)
	u[14] = byte(r >> 8)
	u[15] = byte(r)
	return u, nil
}
