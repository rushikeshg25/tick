package worker

import "context"

// Static returns an Allocator that always hands out the same node ID.
//
// Its lease is never cancelled, because nothing is watching: the caller is
// asserting that this ID is held exclusively. That is a reasonable assertion
// for a fixed fleet with IDs in configuration, and a bad one for anything
// that autoscales, where Lease over a shared store belongs instead.
func Static(nodeID uint16) Allocator { return staticAllocator(nodeID) }

type staticAllocator uint16

func (a staticAllocator) Acquire(context.Context) (Lease, error) {
	return staticLease(a), nil
}

type staticLease uint16

func (l staticLease) NodeID() uint16                { return uint16(l) }
func (l staticLease) Safe() context.Context         { return context.Background() }
func (l staticLease) Release(context.Context) error { return nil }
