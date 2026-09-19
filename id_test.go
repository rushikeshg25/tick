package tick

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

func TestIDRoundTripsAllFields(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))

	for i := 0; i < 100000; i++ {
		ts := rng.Uint64N(MaxTimestamp + 1)
		node := uint64(rng.UintN(MaxNodeID + 1))
		seq := rng.Uint64N(MaxSequence + 1)

		id := compose(ts, node<<nodeShift, seq)

		if id < 0 {
			t.Fatalf("compose(%d,%d,%d) = %d, want non-negative", ts, node, seq, id)
		}
		if got := uint64(id.Node()); got != node {
			t.Fatalf("Node() = %d, want %d", got, node)
		}
		if got := uint64(id.Seq()); got != seq {
			t.Fatalf("Seq() = %d, want %d", got, seq)
		}
		if got, want := id.Time().UnixMilli(), int64(ts)+Epoch; got != want {
			t.Fatalf("Time().UnixMilli() = %d, want %d", got, want)
		}
	}
}

func TestIDLayoutBoundaries(t *testing.T) {
	max := compose(MaxTimestamp, MaxNodeID<<nodeShift, MaxSequence)
	if max < 0 {
		t.Errorf("maximal ID is negative (%d); bit 63 must stay clear", max)
	}
	if got := uint64(max.Node()); got != MaxNodeID {
		t.Errorf("Node() at boundary = %d, want %d", got, MaxNodeID)
	}
	if got := uint64(max.Seq()); got != MaxSequence {
		t.Errorf("Seq() at boundary = %d, want %d", got, MaxSequence)
	}

	// 41 + 10 + 12 = 63 bits, so the maximal ID is exactly MaxInt64 and the
	// sign bit is never reached.
	if int64(max) != math.MaxInt64 {
		t.Errorf("maximal ID = %d, want %d; the layout should fill 63 bits exactly", max, int64(math.MaxInt64))
	}
}

func TestEpochIsTwentyTwentySix(t *testing.T) {
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if Epoch != want {
		t.Fatalf("Epoch = %d, want %d (2026-01-01T00:00:00Z)", Epoch, want)
	}
}

func TestIDMarshalsAsJSONString(t *testing.T) {
	id := compose(1234567, 42<<nodeShift, 7)

	b, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if b[0] != '"' || b[len(b)-1] != '"' {
		t.Fatalf("Marshal produced %s, want a quoted string; bare numbers above 2^53 lose precision in JavaScript", b)
	}

	var got ID
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got != id {
		t.Errorf("round trip changed the ID: %d -> %d", id, got)
	}
}

func TestIDJSONRoundTripPreservesLargeValues(t *testing.T) {
	// Above 2^53, where a float64 round trip would start losing bits.
	id := compose(MaxTimestamp, MaxNodeID<<nodeShift, MaxSequence)
	if int64(id) <= 1<<53 {
		t.Fatalf("test is not exercising the large-value case: %d", id)
	}

	type row struct {
		ID ID `json:"id"`
	}
	b, err := json.Marshal(row{ID: id})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got row
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != id {
		t.Errorf("round trip changed the ID: %d -> %d", id, got.ID)
	}
}

func TestIDUnmarshalAcceptsBareNumber(t *testing.T) {
	var id ID
	if err := json.Unmarshal([]byte("12345"), &id); err != nil {
		t.Fatalf("Unmarshal bare number: %v", err)
	}
	if id != 12345 {
		t.Errorf("id = %d, want 12345", id)
	}
}

func TestIDUnmarshalRejectsGarbage(t *testing.T) {
	for _, in := range []string{`"-1"`, `"abc"`, `""`, `"1.5"`} {
		var id ID
		if err := json.Unmarshal([]byte(in), &id); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want an error", in)
		}
	}
}

func TestIDStringIsBaseTen(t *testing.T) {
	id := ID(9007199254740993)
	if got, want := id.String(), "9007199254740993"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
