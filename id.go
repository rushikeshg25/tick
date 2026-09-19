package tick

import "time"

// Epoch is the origin of every timestamp embedded in a Snowflake-64 ID:
// 2026-01-01T00:00:00Z, in milliseconds since the Unix epoch.
//
// This constant can never change. Changing it rewrites the meaning of every
// ID already issued, which silently reorders them against new ones. With 41
// bits of milliseconds it is good until roughly 2095.
const Epoch int64 = 1767225600000

// Bit widths of the Snowflake-64 layout:
//
//	 63  62                                    22 21        12 11         0
//	┌───┬────────────────────────────────────────┬───────────┬────────────┐
//	│ 0 │        timestamp ms (41 bits)          │ node (10) │  seq (12)  │
//	└───┴────────────────────────────────────────┴───────────┴────────────┘
//
// Bit 63 is always zero, so every ID is a positive int64 and fits a Postgres
// BIGINT column.
const (
	TimestampBits = 41
	NodeBits      = 10
	SequenceBits  = 12
)

const (
	// MaxNodeID is the largest addressable node, so the fleet tops out at
	// MaxNodeID+1 concurrent generators.
	MaxNodeID = (1 << NodeBits) - 1

	// MaxSequence is the largest sequence number within one millisecond, so a
	// single node tops out at MaxSequence+1 IDs per millisecond.
	MaxSequence = (1 << SequenceBits) - 1

	// MaxTimestamp is the largest representable offset from Epoch, in ms.
	MaxTimestamp = (1 << TimestampBits) - 1
)

const (
	nodeShift = SequenceBits
	timeShift = SequenceBits + NodeBits
)

// ID is a Snowflake-64 identifier. It is always positive.
//
// The zero value is not a valid ID.
type ID int64

// Time returns the instant the ID was generated, truncated to a millisecond.
func (id ID) Time() time.Time {
	// TODO(M1): reverse the layout above; remember to add Epoch back.
	panic("tick: ID.Time not implemented")
}

// Node returns the node ID that generated this ID.
func (id ID) Node() uint16 {
	// TODO(M1)
	panic("tick: ID.Node not implemented")
}

// Seq returns the within-millisecond sequence number of this ID.
func (id ID) Seq() uint16 {
	// TODO(M1)
	panic("tick: ID.Seq not implemented")
}

// String returns the ID in base-10.
func (id ID) String() string {
	// TODO(M1)
	panic("tick: ID.String not implemented")
}

// MarshalJSON encodes the ID as a JSON string rather than a number.
//
// This is deliberate and must not be "simplified" later. Values above 2^53
// lose precision when parsed by JavaScript, so an ID serialized as a bare
// number can come back from a browser as a different ID, silently.
func (id ID) MarshalJSON() ([]byte, error) {
	// TODO(M1): quoted base-10. Pair with the round-trip test.
	panic("tick: ID.MarshalJSON not implemented")
}

// UnmarshalJSON accepts the quoted form written by MarshalJSON, and also a
// bare JSON number so that documents written by other tools still load.
func (id *ID) UnmarshalJSON(data []byte) error {
	// TODO(M1)
	panic("tick: ID.UnmarshalJSON not implemented")
}
