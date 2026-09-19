package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/rushikeshg25/tick"
	"github.com/rushikeshg25/tick/worker"
	"github.com/rushikeshg25/tick/worker/httpstore"
)

type generatorState struct {
	mu       sync.Mutex
	pending  []tick.ID
	total    int
	errs     map[string]int
	nodeID   uint16
	leaseOK  bool
	overflow int
}

const maxPending = 200000

func runGenerator(args []string) error {
	fs := flag.NewFlagSet("generator", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	coordinator := fs.String("coordinator", "http://coordinator:8080", "coordinator base url")
	holder := fs.String("holder", os.Getenv("HOSTNAME"), "unique identity for this process")
	rate := fs.Int("rate", 2000, "IDs per second")
	ttl := fs.Duration("ttl", 15*time.Second, "lease ttl")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *holder == "" {
		return errors.New("generator: -holder is required (or set HOSTNAME)")
	}

	roundTrip := 2 * time.Second
	store, err := httpstore.NewClient(*coordinator, roundTrip)
	if err != nil {
		return err
	}

	alloc, err := worker.New(worker.Config{
		Store:        store,
		Holder:       *holder,
		TTL:          *ttl,
		MaxRoundTrip: roundTrip,
		Range:        16,
	})
	if err != nil {
		return err
	}

	lease, err := acquireWithRetry(alloc)
	if err != nil {
		return err
	}
	log.Printf("%s acquired node id %d", *holder, lease.NodeID())

	gen, err := tick.New(lease.NodeID(), tick.WithLease(lease.Safe()))
	if err != nil {
		return err
	}

	st := &generatorState{errs: map[string]int{}, nodeID: lease.NodeID(), leaseOK: true}
	go st.mint(gen, lease, *rate)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /drain", st.drain)
	mux.HandleFunc("GET /stats", st.stats)

	log.Printf("generator listening on %s", *addr)
	return http.ListenAndServe(*addr, mux)
}

func acquireWithRetry(alloc worker.Allocator) (worker.Lease, error) {
	var lastErr error
	for attempt := 0; attempt < 60; attempt++ {
		lease, err := alloc.Acquire(context.Background())
		if err == nil {
			return lease, nil
		}
		lastErr = err
		time.Sleep(time.Second)
	}
	return nil, lastErr
}

func (s *generatorState) mint(gen *tick.Generator, lease worker.Lease, rate int) {
	interval := time.Second / time.Duration(max(rate, 1))
	t := time.NewTicker(interval)
	defer t.Stop()

	for range t.C {
		id, err := gen.Next()

		s.mu.Lock()
		if err != nil {
			s.errs[classify(err)]++
			if errors.Is(err, tick.ErrLeaseLost) {
				// The whole point of the fence: stop, and say so.
				s.leaseOK = false
				s.mu.Unlock()
				log.Printf("lease on node %d lost; no longer generating", lease.NodeID())
				return
			}
			s.mu.Unlock()
			continue
		}
		s.total++
		if len(s.pending) < maxPending {
			s.pending = append(s.pending, id)
		} else {
			s.overflow++
		}
		s.mu.Unlock()
	}
}

func (s *generatorState) drain(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := s.pending
	s.pending = nil
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *generatorState) stats(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := map[string]any{
		"node_id":  s.nodeID,
		"total":    s.total,
		"errors":   s.errs,
		"lease_ok": s.leaseOK,
		"overflow": s.overflow,
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func classify(err error) string {
	switch {
	case errors.Is(err, tick.ErrClockRegression):
		return "clock-regression"
	case errors.Is(err, tick.ErrSequenceExhausted):
		return "sequence-exhausted"
	case errors.Is(err, tick.ErrTimestampOutOfRange):
		return "timestamp-out-of-range"
	case errors.Is(err, tick.ErrLeaseLost):
		return "lease-lost"
	default:
		return "other"
	}
}
