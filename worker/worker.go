// Package worker allocates the node IDs that tick.Generator embeds in every
// ID it issues.
//
// This is a fencing problem, not a naming problem. Two processes holding the
// same node ID at the same time will mint duplicate IDs, and no amount of
// care inside the generator can prevent it. So a Lease does not merely say
// which node ID to use; it carries a context that is cancelled while the
// claim is still valid, leaving the holder time to stop before anyone else
// can take over.
package worker

import (
	"context"
	"errors"
)

// ErrNoFreeNodeID is returned when every node ID in the configured range is
// already leased.
var ErrNoFreeNodeID = errors.New("worker: no free node id")

// A Lease is an exclusive claim on one node ID.
type Lease interface {
	// NodeID is the claimed ID.
	NodeID() uint16

	// Safe is cancelled once the claim can no longer be guaranteed. It is
	// cancelled strictly before the ID could be granted to anyone else, so a
	// holder that stops generating on cancellation never overlaps with its
	// successor.
	//
	// Pass it to tick.WithLease so the generator enforces this itself.
	Safe() context.Context

	// Release gives the ID up immediately. It is safe to call more than once.
	Release(ctx context.Context) error
}

// An Allocator hands out leases.
type Allocator interface {
	Acquire(ctx context.Context) (Lease, error)
}
