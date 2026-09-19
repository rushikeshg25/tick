// Package btree implements a small B+tree instrumented for measuring insert
// locality.
//
// It is a model, not a storage engine: nodes live in memory and nothing is
// serialized. What it reproduces faithfully is the part that matters for
// comparing key formats — the shape of the tree, where splits happen, and
// which pages a bounded buffer pool has to write back.
//
// Page writes are the headline number. A buffer pool of fixed size holds
// recently touched pages; when a dirty page is evicted, that is a write.
// Sequential keys dirty the same rightmost leaf over and over and it is
// written once per eviction, while random keys scatter across every leaf in
// the tree, so nearly every insert eventually costs a write.
package btree

import "bytes"

// Stats is what a run measured.
type Stats struct {
	Inserts    int
	Splits     int
	Height     int
	Pages      int
	PageWrites int
	CacheHits  int
	CacheMiss  int
}

// WriteAmplification is page writes per insert. One is the floor for a
// perfectly sequential workload with a large enough pool.
func (s Stats) WriteAmplification() float64 {
	if s.Inserts == 0 {
		return 0
	}
	return float64(s.PageWrites) / float64(s.Inserts)
}

// HitRate is the fraction of page accesses served from the buffer pool.
func (s Stats) HitRate() float64 {
	total := s.CacheHits + s.CacheMiss
	if total == 0 {
		return 0
	}
	return float64(s.CacheHits) / float64(total)
}

// Tree is a B+tree over byte-slice keys.
type Tree struct {
	order int
	root  *node
	pool  *pool
	next  int
	stats Stats
}

type node struct {
	id       int
	leaf     bool
	keys     [][]byte
	children []*node
}

// New returns an empty tree whose nodes hold at most order keys, backed by a
// buffer pool of poolPages pages.
func New(order, poolPages int) *Tree {
	t := &Tree{order: order, pool: newPool(poolPages)}
	t.root = t.newNode(true)
	return t
}

func (t *Tree) newNode(leaf bool) *node {
	t.next++
	t.stats.Pages++
	return &node{id: t.next, leaf: leaf}
}

func (t *Tree) touch(n *node, dirty bool) {
	hit, evictedDirty := t.pool.touch(n.id, dirty)
	if hit {
		t.stats.CacheHits++
	} else {
		t.stats.CacheMiss++
	}
	if evictedDirty {
		t.stats.PageWrites++
	}
}

// Insert adds a key. Duplicate keys are inserted; the tree is a measurement
// model, not a set.
func (t *Tree) Insert(key []byte) {
	t.stats.Inserts++

	if len(t.root.keys) >= t.order {
		old := t.root
		root := t.newNode(false)
		root.children = []*node{old}
		t.root = root
		t.splitChild(root, 0)
	}
	t.insertNonFull(t.root, key)
}

func (t *Tree) insertNonFull(n *node, key []byte) {
	if n.leaf {
		t.touch(n, true)
		i := lowerBound(n.keys, key)
		n.keys = append(n.keys, nil)
		copy(n.keys[i+1:], n.keys[i:])
		n.keys[i] = append([]byte(nil), key...)
		return
	}

	t.touch(n, false)
	i := lowerBound(n.keys, key)
	if i < len(n.keys) && bytes.Equal(n.keys[i], key) {
		// A separator lives in the subtree to its right.
		i++
	}
	child := n.children[i]
	if len(child.keys) >= t.order {
		t.splitChild(n, i)
		if bytes.Compare(key, n.keys[i]) >= 0 {
			i++
		}
	}
	t.insertNonFull(n.children[i], key)
}

func (t *Tree) splitChild(parent *node, i int) {
	t.stats.Splits++

	full := parent.children[i]
	mid := len(full.keys) / 2
	sep := append([]byte(nil), full.keys[mid]...)

	right := t.newNode(full.leaf)
	if full.leaf {
		// A B+tree keeps every key in the leaves, so the separator is copied
		// up and the right leaf keeps its own copy.
		right.keys = append(right.keys, full.keys[mid:]...)
		full.keys = full.keys[:mid]
	} else {
		// In an internal node the separator moves up instead, leaving
		// len(keys)+1 children on each side.
		right.keys = append(right.keys, full.keys[mid+1:]...)
		right.children = append(right.children, full.children[mid+1:]...)
		full.keys = full.keys[:mid]
		full.children = full.children[:mid+1]
	}

	parent.children = append(parent.children, nil)
	copy(parent.children[i+2:], parent.children[i+1:])
	parent.children[i+1] = right

	parent.keys = append(parent.keys, nil)
	copy(parent.keys[i+1:], parent.keys[i:])
	parent.keys[i] = sep

	// A split rewrites both halves and the parent.
	t.touch(full, true)
	t.touch(right, true)
	t.touch(parent, true)
}

// Contains reports whether key is present. It exists so the model can be
// checked against a plain map rather than trusted.
func (t *Tree) Contains(key []byte) bool {
	n := t.root
	for {
		i := lowerBound(n.keys, key)
		if n.leaf {
			return i < len(n.keys) && bytes.Equal(n.keys[i], key)
		}
		if i < len(n.keys) && bytes.Equal(n.keys[i], key) {
			i++
		}
		n = n.children[i]
	}
}

// Stats returns the measurements, flushing the buffer pool first so pages
// still dirty at the end are counted.
func (t *Tree) Stats() Stats {
	s := t.stats
	s.PageWrites += t.pool.dirtyCount()
	s.Height = t.height()
	return s
}

func (t *Tree) height() int {
	h := 1
	for n := t.root; !n.leaf; n = n.children[0] {
		h++
	}
	return h
}

func lowerBound(keys [][]byte, key []byte) int {
	lo, hi := 0, len(keys)
	for lo < hi {
		mid := (lo + hi) / 2
		if bytes.Compare(keys[mid], key) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}
