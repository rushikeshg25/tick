// Package bench measures how each ID format behaves as a B-tree key.
//
// The claim that random UUIDs are bad for index inserts is repeated
// everywhere and measured almost nowhere. These tests measure it, against the
// instrumented B+tree in internal/btree.
package bench

import (
	"fmt"
	"testing"
	"time"

	"github.com/rushikeshg25/tick"
	"github.com/rushikeshg25/tick/internal/btree"
)

const (
	inserts   = 200000
	perMs     = 64 // IDs generated per virtual millisecond
	treeOrder = 64
	poolPages = 256
)

var origin = time.UnixMilli(tick.Epoch).Add(200 * 24 * time.Hour)

func measure(t testing.TB, f tick.Format, clock *tick.FakeClock) btree.Stats {
	t.Helper()
	tree := btree.New(treeOrder, poolPages)

	for i := 0; i < inserts; i++ {
		key, err := f.NextBytes()
		if err != nil {
			t.Fatalf("%s: NextBytes at %d: %v", f.Name(), i, err)
		}
		tree.Insert(key)
		if (i+1)%perMs == 0 {
			clock.Advance(time.Millisecond)
		}
	}
	return tree.Stats()
}

func TestInsertLocality(t *testing.T) {
	results := make(map[string]btree.Stats)

	for _, name := range []string{"snowflake64", "uuidv7", "ulid", "uuidv4"} {
		// A fresh clock per format so each starts from the same instant.
		clock := tick.NewFakeClock(origin)
		f := formatNamed(t, name, clock)
		results[name] = measure(t, f, clock)
	}

	t.Log("\n" + table(results))

	seq := results["snowflake64"]
	rnd := results["uuidv4"]

	if seq.PageWrites >= rnd.PageWrites {
		t.Errorf("snowflake caused %d page writes and uuidv4 %d; time-ordered keys should write far less",
			seq.PageWrites, rnd.PageWrites)
	}
	if results["uuidv7"].PageWrites >= rnd.PageWrites {
		t.Errorf("uuidv7 caused %d page writes and uuidv4 %d", results["uuidv7"].PageWrites, rnd.PageWrites)
	}
	if results["ulid"].PageWrites >= rnd.PageWrites {
		t.Errorf("ulid caused %d page writes and uuidv4 %d", results["ulid"].PageWrites, rnd.PageWrites)
	}

	// The time-ordered formats should be in the same league as each other.
	// A large gap between them means one of them is not actually ordered.
	v7, ul := results["uuidv7"].WriteAmplification(), results["ulid"].WriteAmplification()
	if ratio := v7 / ul; ratio > 3 || ratio < 1.0/3 {
		t.Errorf("uuidv7 amplification %.3f and ulid %.3f differ by more than 3x; "+
			"one of them is not ordering as intended", v7, ul)
	}
}

func formatNamed(t testing.TB, name string, clock *tick.FakeClock) tick.Format {
	t.Helper()
	switch name {
	case "snowflake64":
		g, err := tick.New(1, tick.WithClock(clock))
		if err != nil {
			t.Fatal(err)
		}
		return g
	case "uuidv7":
		g, err := tick.NewUUIDv7Generator(tick.WithClock(clock))
		if err != nil {
			t.Fatal(err)
		}
		return g
	case "ulid":
		g, err := tick.NewULIDGenerator(tick.WithClock(clock))
		if err != nil {
			t.Fatal(err)
		}
		return g
	case "uuidv4":
		return tick.UUIDv4Source{}
	}
	t.Fatalf("unknown format %q", name)
	return nil
}

func table(results map[string]btree.Stats) string {
	s := fmt.Sprintf("%-14s %10s %8s %7s %8s %12s %9s\n",
		"format", "writes", "splits", "height", "pages", "write amp", "hit rate")
	s += fmt.Sprintf("%-14s %10s %8s %7s %8s %12s %9s\n",
		"------", "------", "------", "------", "-----", "---------", "--------")
	for _, name := range []string{"snowflake64", "uuidv7", "ulid", "uuidv4"} {
		r := results[name]
		s += fmt.Sprintf("%-14s %10d %8d %7d %8d %12.3f %8.1f%%\n",
			name, r.PageWrites, r.Splits, r.Height, r.Pages,
			r.WriteAmplification(), 100*r.HitRate())
	}
	return s
}

// BenchmarkInsert reports the cost of generating and inserting one key,
// including the page accounting.
func BenchmarkInsert(b *testing.B) {
	for _, name := range []string{"snowflake64", "uuidv7", "ulid", "uuidv4"} {
		b.Run(name, func(b *testing.B) {
			clock := tick.NewFakeClock(origin)
			f := formatNamed(b, name, clock)
			tree := btree.New(treeOrder, poolPages)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key, err := f.NextBytes()
				if err != nil {
					b.Fatal(err)
				}
				tree.Insert(key)
				if (i+1)%perMs == 0 {
					clock.Advance(time.Millisecond)
				}
			}
			b.StopTimer()
			b.ReportMetric(tree.Stats().WriteAmplification(), "writes/insert")
		})
	}
}
