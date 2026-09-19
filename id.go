package tick

import (
	"fmt"
	"strconv"
	"time"
)

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

// compose folds the three fields into an ID. node must already be shifted
// into its final bit position, which is how Generator stores it.
func compose(ts, shiftedNode, seq uint64) ID {
	return ID(ts<<timeShift | shiftedNode | seq)
}

// Time returns the instant the ID was generated, truncated to a millisecond.
func (id ID) Time() time.Time {
	return time.UnixMilli(int64(id)>>timeShift + Epoch).UTC()
}

// Node returns the node ID that generated this ID.
func (id ID) Node() uint16 {
	return uint16(int64(id) >> nodeShift & MaxNodeID)
}

// Seq returns the within-millisecond sequence number of this ID.
func (id ID) Seq() uint16 {
	return uint16(int64(id) & MaxSequence)
}

// String returns the ID in base-10.
func (id ID) String() string {
	return strconv.FormatInt(int64(id), 10)
}

// MarshalJSON encodes the ID as a JSON string rather than a number.
//
// This is deliberate and must not be "simplified" later. Values above 2^53
// lose precision when parsed by JavaScript, so an ID serialized as a bare
// number can come back from a browser as a different ID, silently.
func (id ID) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 24)
	b = append(b, '"')
	b = strconv.AppendInt(b, int64(id), 10)
	b = append(b, '"')
	return b, nil
}

// UnmarshalJSON accepts the quoted form written by MarshalJSON, and also a
// bare JSON number so that documents written by other tools still load.
func (id *ID) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("tick: invalid ID %s: %w", data, err)
	}
	if v < 0 {
		return fmt.Errorf("tick: invalid ID %s: negative", data)
	}
	*id = ID(v)
	return nil
}
