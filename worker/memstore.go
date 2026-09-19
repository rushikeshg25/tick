package worker

import (
	"context"
	"sync"
	"time"

	"github.com/rushikeshg25/tick"
)

// MemStore is an in-process Store for tests and for the simulator.
//
// Expiry is driven by an injected clock rather than real time, so a test can
// step a lease past its TTL without waiting. Partition makes every call fail,
// which is how the interesting case is reproduced: a holder that cannot renew
// must stop generating before the store lets anyone else in.
type MemStore struct {
	clock tick.Clock

	mu          sync.Mutex
	held        map[uint16]claim
	partitioned bool
}

type claim struct {
	holder      string
	expiresAtMs int64
}

// NewMemStore returns an empty store using clock for expiry.
func NewMemStore(clock tick.Clock) *MemStore {
	return &MemStore{clock: clock, held: make(map[uint16]claim)}
}

// Partition makes every subsequent call fail with ErrPartitioned until it is
// turned off again.
func (s *MemStore) Partition(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partitioned = on
}

// ErrPartitioned is what a partitioned MemStore returns.
var ErrPartitioned = context.DeadlineExceeded

// HolderOf reports who currently holds nodeID, treating expired claims as
// free. It exists so tests can assert on handover without reaching into
// unexported state.
func (s *MemStore) HolderOf(nodeID uint16) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.held[nodeID]
	if !ok || s.expiredLocked(c) {
		return "", false
	}
	return c.holder, true
}

func (s *MemStore) expiredLocked(c claim) bool {
	return s.clock.NowMillis() >= c.expiresAtMs
}

func (s *MemStore) TryAcquire(_ context.Context, nodeID uint16, holder string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partitioned {
		return false, ErrPartitioned
	}

	if c, ok := s.held[nodeID]; ok && c.holder != holder && !s.expiredLocked(c) {
		return false, nil
	}
	s.held[nodeID] = claim{holder: holder, expiresAtMs: s.clock.NowMillis() + ttl.Milliseconds()}
	return true, nil
}

func (s *MemStore) Renew(_ context.Context, nodeID uint16, holder string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.partitioned {
		return false, ErrPartitioned
	}

	c, ok := s.held[nodeID]
	if !ok || c.holder != holder || s.expiredLocked(c) {
		return false, nil
	}
	s.held[nodeID] = claim{holder: holder, expiresAtMs: s.clock.NowMillis() + ttl.Milliseconds()}
	return true, nil
}

func (s *MemStore) Release(_ context.Context, nodeID uint16, holder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.held[nodeID]; ok && c.holder == holder {
		delete(s.held, nodeID)
	}
	return nil
}

var _ Store = (*MemStore)(nil)
