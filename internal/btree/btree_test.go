package btree

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// The model is only worth measuring with if it is a working tree, so check it
// against a plain map.
func TestTreeHoldsEverythingInserted(t *testing.T) {
	tree := New(16, 64)
	rng := rand.New(rand.NewPCG(1, 2))

	want := make(map[string]bool)
	for i := 0; i < 20000; i++ {
		key := make([]byte, 8)
		for j := range key {
			key[j] = byte(rng.UintN(256))
		}
		tree.Insert(key)
		want[string(key)] = true
	}

	for k := range want {
		if !tree.Contains([]byte(k)) {
			t.Fatalf("key %x was inserted but cannot be found", k)
		}
	}
	if got := tree.Stats().Inserts; got != 20000 {
		t.Errorf("Inserts = %d, want 20000", got)
	}
}

func TestTreeKeepsLeavesOrdered(t *testing.T) {
	tree := New(8, 32)
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 5000; i++ {
		key := []byte{byte(rng.UintN(256)), byte(rng.UintN(256))}
		tree.Insert(key)
	}

	var walk func(n *node)
	walk = func(n *node) {
		for i := 1; i < len(n.keys); i++ {
			if bytes.Compare(n.keys[i-1], n.keys[i]) > 0 {
				t.Fatalf("node %d has keys out of order at %d", n.id, i)
			}
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(tree.root)
}

func TestSequentialKeysBeatRandomKeys(t *testing.T) {
	const n = 50000

	seq := New(32, 128)
	for i := 0; i < n; i++ {
		seq.Insert([]byte{byte(i >> 24), byte(i >> 16), byte(i >> 8), byte(i)})
	}

	rnd := New(32, 128)
	rng := rand.New(rand.NewPCG(5, 6))
	for i := 0; i < n; i++ {
		v := rng.Uint32()
		rnd.Insert([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}

	s, r := seq.Stats(), rnd.Stats()
	t.Logf("sequential: %+v amp=%.3f hit=%.3f", s, s.WriteAmplification(), s.HitRate())
	t.Logf("random:     %+v amp=%.3f hit=%.3f", r, r.WriteAmplification(), r.HitRate())

	if s.PageWrites >= r.PageWrites {
		t.Errorf("sequential inserts caused %d page writes and random ones %d; "+
			"the model is not capturing locality at all", s.PageWrites, r.PageWrites)
	}
	if s.HitRate() <= r.HitRate() {
		t.Errorf("sequential hit rate %.3f is not above random %.3f", s.HitRate(), r.HitRate())
	}
}
