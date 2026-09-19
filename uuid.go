package tick

import (
	"encoding/hex"
	"fmt"
	"time"
)

// UUID is a 128-bit identifier in RFC 9562 binary layout: big-endian, so
// bytewise ordering matches the ordering of the values the fields encode.
type UUID [16]byte

// Version returns the 4-bit version field, 7 for the IDs this package
// generates with NewUUIDv7Generator and 4 for UUIDv4.
func (u UUID) Version() int { return int(u[6] >> 4) }

// Variant reports whether the two-bit variant field marks this as an RFC 9562
// UUID rather than one of the legacy layouts.
func (u UUID) IsRFC9562Variant() bool { return u[8]&0xc0 == 0x80 }

// Time returns the embedded timestamp. It is only meaningful for version 7.
func (u UUID) Time() time.Time {
	ms := int64(u[0])<<40 | int64(u[1])<<32 | int64(u[2])<<24 |
		int64(u[3])<<16 | int64(u[4])<<8 | int64(u[5])
	return time.UnixMilli(ms).UTC()
}

// Counter returns the 12-bit rand_a field, which this package uses as a
// within-millisecond counter. It is only meaningful for version 7.
func (u UUID) Counter() uint16 {
	return uint16(u[6]&0x0f)<<8 | uint16(u[7])
}

// String returns the canonical 8-4-4-4-12 hyphenated form.
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// MarshalText implements encoding.TextMarshaler, which also gives JSON
// encoding the canonical string form.
func (u UUID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (u *UUID) UnmarshalText(text []byte) error {
	parsed, err := ParseUUID(string(text))
	if err != nil {
		return err
	}
	*u = parsed
	return nil
}

// ParseUUID reads the canonical hyphenated form.
func ParseUUID(s string) (UUID, error) {
	var u UUID
	if len(s) != 36 {
		return u, fmt.Errorf("tick: invalid UUID %q: want 36 characters, got %d", s, len(s))
	}
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return u, fmt.Errorf("tick: invalid UUID %q: misplaced hyphens", s)
	}
	stripped := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	if _, err := hex.Decode(u[:], []byte(stripped)); err != nil {
		return u, fmt.Errorf("tick: invalid UUID %q: %w", s, err)
	}
	return u, nil
}
