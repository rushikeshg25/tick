// Package httpstore implements worker.Store over plain HTTP.
//
// It exists so that tick can be run as a real multi-process cluster — and
// have faults injected into it — without the module taking a dependency on
// etcd or anything else. The server is a single coordinator process holding
// the claims in memory; that is a single point of failure and deliberately
// so, because the interesting question is what the *generators* do when they
// cannot reach it.
//
// For production, implement worker.Store against something replicated. The
// interface is three methods; see docs/WORKER-IDS.md.
package httpstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/rushikeshg25/tick"
	"github.com/rushikeshg25/tick/worker"
)

type request struct {
	NodeID uint16 `json:"node_id"`
	Holder string `json:"holder"`
	TTLMs  int64  `json:"ttl_ms"`
}

type response struct {
	OK bool `json:"ok"`
}

// Server holds the claims. Expiry is driven by the injected clock.
type Server struct {
	clock tick.Clock

	mu   sync.Mutex
	held map[uint16]entry
}

type entry struct {
	holder      string
	expiresAtMs int64
}

// NewServer returns a coordinator. Pass a tick.SystemClock in production.
func NewServer(clock tick.Clock) *Server {
	return &Server{clock: clock, held: make(map[uint16]entry)}
}

// Handler routes the three store operations plus a /claims view for
// debugging and for tests to assert handover against.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /acquire", s.handle(s.tryAcquire))
	mux.HandleFunc("POST /renew", s.handle(s.renew))
	mux.HandleFunc("POST /release", s.handle(s.release))
	mux.HandleFunc("GET /claims", s.claims)
	return mux
}

func (s *Server) handle(op func(request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response{OK: op(req)})
	}
}

func (s *Server) claims(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make(map[string]string, len(s.held))
	for id, e := range s.held {
		if !s.expiredLocked(e) {
			out[strconv.Itoa(int(id))] = e.holder
		}
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) expiredLocked(e entry) bool { return s.clock.NowMillis() >= e.expiresAtMs }

func (s *Server) tryAcquire(req request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.held[req.NodeID]; ok && e.holder != req.Holder && !s.expiredLocked(e) {
		return false
	}
	s.held[req.NodeID] = entry{holder: req.Holder, expiresAtMs: s.clock.NowMillis() + req.TTLMs}
	return true
}

func (s *Server) renew(req request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.held[req.NodeID]
	if !ok || e.holder != req.Holder || s.expiredLocked(e) {
		return false
	}
	s.held[req.NodeID] = entry{holder: req.Holder, expiresAtMs: s.clock.NowMillis() + req.TTLMs}
	return true
}

func (s *Server) release(req request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.held[req.NodeID]; ok && e.holder == req.Holder {
		delete(s.held, req.NodeID)
	}
	return true
}

// Client is a worker.Store backed by a Server over HTTP.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a Store talking to the coordinator at base.
//
// The timeout should be no longer than the lease Config's MaxRoundTrip;
// otherwise a hung request outlives the window it was budgeted for.
func NewClient(base string, timeout time.Duration) (*Client, error) {
	if _, err := url.Parse(base); err != nil {
		return nil, fmt.Errorf("httpstore: invalid base url %q: %w", base, err)
	}
	return &Client{base: base, http: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) call(ctx context.Context, path string, nodeID uint16, holder string, ttl time.Duration) (bool, error) {
	body, err := json.Marshal(request{NodeID: nodeID, Holder: holder, TTLMs: ttl.Milliseconds()})
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("httpstore: %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("httpstore: %s: status %d", path, resp.StatusCode)
	}
	var out response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("httpstore: %s: %w", path, err)
	}
	return out.OK, nil
}

func (c *Client) TryAcquire(ctx context.Context, nodeID uint16, holder string, ttl time.Duration) (bool, error) {
	return c.call(ctx, "/acquire", nodeID, holder, ttl)
}

func (c *Client) Renew(ctx context.Context, nodeID uint16, holder string, ttl time.Duration) (bool, error) {
	return c.call(ctx, "/renew", nodeID, holder, ttl)
}

func (c *Client) Release(ctx context.Context, nodeID uint16, holder string) error {
	_, err := c.call(ctx, "/release", nodeID, holder, 0)
	return err
}

var _ worker.Store = (*Client)(nil)
