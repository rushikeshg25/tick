package tick

import "errors"

var (
	// ErrClockRegression is returned when the wall clock has moved backward by
	// more than the configured tolerance, or when it has moved backward within
	// tolerance but the borrowed sequence space for that millisecond is also
	// exhausted. The generator does not guess and does not emit; the caller
	// decides whether to retry, fail the request, or shed load.
	ErrClockRegression = errors.New("tick: clock moved backward beyond tolerance")

	// ErrSequenceExhausted is returned by TryNext when all sequence numbers for
	// the current millisecond have been used. Next waits for the next
	// millisecond instead of returning this.
	ErrSequenceExhausted = errors.New("tick: sequence exhausted for this millisecond")

	// ErrNodeIDOutOfRange is returned by New when the node ID does not fit in
	// NodeBits.
	ErrNodeIDOutOfRange = errors.New("tick: node id out of range")

	// ErrLeaseLost is returned once the allocator that issued this generator's
	// node ID can no longer guarantee exclusive ownership of it. A generator
	// that returns this error never generates again.
	ErrLeaseLost = errors.New("tick: worker id lease lost")
)
