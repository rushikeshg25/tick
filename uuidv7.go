package tick

import "math/rand/v2"

// UUIDv7Generator issues RFC 9562 version 7 UUIDs.
//
// Layout:
//
//	48 bits  unix_ts_ms
//	 4 bits  version (0111)
//	12 bits  rand_a   - used here as a within-millisecond counter
//	 2 bits  variant (10)
//	62 bits  rand_b   - fresh randomness on every call
//
// Plain v7 leaves ordering within a millisecond undefined. This generator
// follows RFC 9562's "monotonic random" guidance by driving rand_a from the
// shared watermark, which makes IDs from one generator strictly increasing
// even inside a single millisecond, and caps throughput at 4096 per
// millisecond in exchange.
//
// Randomness comes from math/rand/v2, whose global source is ChaCha8 seeded
// from the operating system. That is ample for collision avoidance but is not
// a promise of unpredictability to an adversary; use tick/opaque for
// externally visible identifiers.
//
// A UUIDv7Generator is safe for concurrent use.
type UUIDv7Generator struct {
	wm *watermark
}

// NewUUIDv7Generator returns a generator for version 7 UUIDs.
//
// No node ID is needed: collisions between hosts are prevented by the 62 bits
// of rand_b rather than by coordination.
func NewUUIDv7Generator(opts ...Option) (*UUIDv7Generator, error) {
	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}
	wm := newWatermark(cfg.clock, cfg.tol, SequenceBits)
	if _, err := wm.timestamp(); err != nil {
		return nil, err
	}
	return &UUIDv7Generator{wm: wm}, nil
}

// Next returns the next UUID, waiting for the next millisecond if this one's
// counter space is exhausted.
func (g *UUIDv7Generator) Next() (UUID, error) { return g.next(true) }

// TryNext returns the next UUID without waiting, reporting
// ErrSequenceExhausted rather than blocking.
func (g *UUIDv7Generator) TryNext() (UUID, error) { return g.next(false) }

func (g *UUIDv7Generator) next(block bool) (UUID, error) {
	ts, seq, err := g.wm.advance(block)
	if err != nil {
		return UUID{}, err
	}

	// The watermark counts from Epoch; v7 embeds Unix milliseconds.
	unixMs := int64(ts) + Epoch
	rb := rand.Uint64()

	var u UUID
	u[0] = byte(unixMs >> 40)
	u[1] = byte(unixMs >> 32)
	u[2] = byte(unixMs >> 24)
	u[3] = byte(unixMs >> 16)
	u[4] = byte(unixMs >> 8)
	u[5] = byte(unixMs)
	u[6] = 0x70 | byte(seq>>8)&0x0f // version 7 in the high nibble
	u[7] = byte(seq)
	u[8] = 0x80 | byte(rb>>56)&0x3f // RFC 9562 variant in the top two bits
	u[9] = byte(rb >> 48)
	u[10] = byte(rb >> 40)
	u[11] = byte(rb >> 32)
	u[12] = byte(rb >> 24)
	u[13] = byte(rb >> 16)
	u[14] = byte(rb >> 8)
	u[15] = byte(rb)
	return u, nil
}

// NewUUIDv4 returns a random version 4 UUID.
//
// It exists mainly as the baseline the locality benchmark measures the
// time-ordered formats against. Prefer a version 7 UUID for anything that
// becomes a database key.
func NewUUIDv4() UUID {
	hi, lo := rand.Uint64(), rand.Uint64()

	var u UUID
	for i := 0; i < 8; i++ {
		u[i] = byte(hi >> (56 - 8*i))
		u[8+i] = byte(lo >> (56 - 8*i))
	}
	u[6] = 0x40 | u[6]&0x0f
	u[8] = 0x80 | u[8]&0x3f
	return u
}
