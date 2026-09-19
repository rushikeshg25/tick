package tick

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUUIDv7LayoutFields(t *testing.T) {
	clock := NewFakeClock(testStart)
	g, err := NewUUIDv7Generator(WithClock(clock))
	if err != nil {
		t.Fatalf("NewUUIDv7Generator: %v", err)
	}

	u, err := g.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	if got := u.Version(); got != 7 {
		t.Errorf("Version() = %d, want 7", got)
	}
	if !u.IsRFC9562Variant() {
		t.Errorf("variant bits = %#02x, want the top two bits to be 10", u[8])
	}
	if got, want := u.Time().UnixMilli(), clock.NowMillis(); got != want {
		t.Errorf("Time() = %d, want %d", got, want)
	}
}

func TestUUIDv7IsMonotonicWithinAMillisecond(t *testing.T) {
	clock := NewFakeClock(testStart)
	g, err := NewUUIDv7Generator(WithClock(clock))
	if err != nil {
		t.Fatalf("NewUUIDv7Generator: %v", err)
	}

	// The clock never moves, so ordering can only come from the counter.
	var prev UUID
	for i := 0; i <= MaxSequence; i++ {
		u, err := g.Next()
		if err != nil {
			t.Fatalf("Next at %d: %v", i, err)
		}
		if i > 0 && bytes.Compare(u[:], prev[:]) <= 0 {
			t.Fatalf("UUIDv7 not increasing within a millisecond at %d: %s then %s", i, prev, u)
		}
		if got, want := u.Counter(), uint16(i); got != want {
			t.Fatalf("counter = %d, want %d", got, want)
		}
		prev = u
	}
}

func TestUUIDStringRoundTrip(t *testing.T) {
	clock := NewFakeClock(testStart)
	g, _ := NewUUIDv7Generator(WithClock(clock))

	for i := 0; i < 1000; i++ {
		u, err := g.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		s := u.String()
		if len(s) != 36 {
			t.Fatalf("String() = %q, want 36 characters", s)
		}
		back, err := ParseUUID(s)
		if err != nil {
			t.Fatalf("ParseUUID(%q): %v", s, err)
		}
		if back != u {
			t.Fatalf("round trip changed the UUID: %s -> %s", u, back)
		}
		clock.Advance(time.Millisecond)
	}
}

func TestUUIDJSONUsesCanonicalString(t *testing.T) {
	g, _ := NewUUIDv7Generator(WithClock(NewFakeClock(testStart)))
	u, _ := g.Next()

	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `"` + u.String() + `"`; string(b) != want {
		t.Errorf("Marshal = %s, want %s", b, want)
	}

	var back UUID
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != u {
		t.Errorf("round trip changed the UUID: %s -> %s", u, back)
	}
}

func TestParseUUIDRejectsGarbage(t *testing.T) {
	for _, in := range []string{
		"",
		"not-a-uuid",
		"0192f8a0a1b27c3d8e4f5a6b7c8d9e0f",
		"0192f8a0-a1b2-7c3d-8e4f-5a6b7c8d9e0",
		"0192f8a0xa1b2-7c3d-8e4f-5a6b7c8d9e0f",
		"zzzzzzzz-a1b2-7c3d-8e4f-5a6b7c8d9e0f",
	} {
		if _, err := ParseUUID(in); err == nil {
			t.Errorf("ParseUUID(%q) succeeded, want an error", in)
		}
	}
}

func TestUUIDv4HasCorrectVersionAndVariant(t *testing.T) {
	for i := 0; i < 1000; i++ {
		u := NewUUIDv4()
		if got := u.Version(); got != 4 {
			t.Fatalf("Version() = %d, want 4", got)
		}
		if !u.IsRFC9562Variant() {
			t.Fatalf("variant bits = %#02x, want the top two bits to be 10", u[8])
		}
	}
}

func TestULIDLayoutAndMonotonicity(t *testing.T) {
	clock := NewFakeClock(testStart)
	g, err := NewULIDGenerator(WithClock(clock))
	if err != nil {
		t.Fatalf("NewULIDGenerator: %v", err)
	}

	var prev ULID
	for i := 0; i <= MaxSequence; i++ {
		u, err := g.Next()
		if err != nil {
			t.Fatalf("Next at %d: %v", i, err)
		}
		if got, want := u.Time().UnixMilli(), clock.NowMillis(); got != want {
			t.Fatalf("Time() = %d, want %d", got, want)
		}
		if got, want := u.Counter(), uint16(i); got != want {
			t.Fatalf("counter = %d, want %d", got, want)
		}
		if i > 0 && bytes.Compare(u[:], prev[:]) <= 0 {
			t.Fatalf("ULID not increasing within a millisecond at %d", i)
		}
		prev = u
	}
}

func TestULIDStringRoundTrip(t *testing.T) {
	clock := NewFakeClock(testStart)
	g, _ := NewULIDGenerator(WithClock(clock))

	for i := 0; i < 1000; i++ {
		u, err := g.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		s := u.String()
		if len(s) != 26 {
			t.Fatalf("String() = %q, want 26 characters", s)
		}
		if strings.ContainsAny(s, "ILOU") {
			t.Fatalf("String() = %q, contains a letter excluded from Crockford base32", s)
		}
		back, err := ParseULID(s)
		if err != nil {
			t.Fatalf("ParseULID(%q): %v", s, err)
		}
		if back != u {
			t.Fatalf("round trip changed the ULID: %x -> %x", u, back)
		}
		clock.Advance(time.Millisecond)
	}
}

// The Crockford alphabet ascends in ASCII order, so sorting the strings must
// give the same answer as sorting the bytes. This is the property that makes
// ULIDs usable as sortable text keys.
func TestULIDStringOrderMatchesByteOrder(t *testing.T) {
	clock := NewFakeClock(testStart)
	g, _ := NewULIDGenerator(WithClock(clock))

	const n = 2000
	values := make([]ULID, 0, n)
	for i := 0; i < n; i++ {
		u, err := g.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		values = append(values, u)
		if i%7 == 0 {
			clock.Advance(time.Millisecond)
		}
	}

	byBytes := slices.Clone(values)
	slices.SortFunc(byBytes, func(a, b ULID) int { return bytes.Compare(a[:], b[:]) })

	strs := make([]string, len(values))
	for i, v := range values {
		strs[i] = v.String()
	}
	slices.Sort(strs)

	for i := range byBytes {
		if byBytes[i].String() != strs[i] {
			t.Fatalf("string order diverges from byte order at %d: %s vs %s", i, byBytes[i], strs[i])
		}
	}
}

func TestParseULIDRejectsGarbage(t *testing.T) {
	for _, in := range []string{
		"",
		"TOOSHORT",
		"01ARZ3NDEKTSV4RRFFQ69G5FAVX", // 27 chars
		"01ARZ3NDEKTSV4RRFFQ69G5FAI",  // I is excluded
		"81ARZ3NDEKTSV4RRFFQ69G5FAV",  // first character overflows 128 bits
	} {
		if _, err := ParseULID(in); err == nil {
			t.Errorf("ParseULID(%q) succeeded, want an error", in)
		}
	}
}

func TestULIDAcceptsLowercaseInput(t *testing.T) {
	g, _ := NewULIDGenerator(WithClock(NewFakeClock(testStart)))
	u, _ := g.Next()

	back, err := ParseULID(strings.ToLower(u.String()))
	if err != nil {
		t.Fatalf("ParseULID on lowercase: %v", err)
	}
	if back != u {
		t.Errorf("lowercase round trip changed the ULID: %x -> %x", u, back)
	}
}

// Every format shares the watermark, so every format inherits its clock
// safety. This checks the wiring rather than the logic, which is covered by
// the watermark's own tests.
func TestAllFormatsRejectRegressionBeyondTolerance(t *testing.T) {
	clock := NewFakeClock(testStart)
	opts := []Option{WithClock(clock), WithRegressionTolerance(10 * time.Millisecond)}

	snow, err := New(1, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v7, err := NewUUIDv7Generator(opts...)
	if err != nil {
		t.Fatalf("NewUUIDv7Generator: %v", err)
	}
	ulid, err := NewULIDGenerator(opts...)
	if err != nil {
		t.Fatalf("NewULIDGenerator: %v", err)
	}

	if _, err := snow.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := v7.Next(); err != nil {
		t.Fatal(err)
	}
	if _, err := ulid.Next(); err != nil {
		t.Fatal(err)
	}

	clock.Step(-time.Second)

	if _, err := snow.Next(); err == nil {
		t.Error("Snowflake generated through a 1s backward step")
	}
	if _, err := v7.Next(); err == nil {
		t.Error("UUIDv7 generated through a 1s backward step")
	}
	if _, err := ulid.Next(); err == nil {
		t.Error("ULID generated through a 1s backward step")
	}
}
