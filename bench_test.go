package tick

import (
	"testing"
	"time"
)

// BenchmarkSystemClockNowMillis is the floor every other benchmark here sits
// on: a generator cannot be faster than one clock read.
func BenchmarkSystemClockNowMillis(b *testing.B) {
	c := NewSystemClock()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = c.NowMillis()
	}
}

// BenchmarkNext measures real throughput against the host clock, so it
// includes the layout's hard ceiling of MaxSequence+1 IDs per millisecond per
// node. That ceiling, not the atomic, is what this number is mostly
// reporting: 4096 per millisecond is 4.096M per second, or about 244ns each.
func BenchmarkNext(b *testing.B) {
	g, err := New(1)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Next(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNextParallel shows what contention costs. One atomic word is one
// cache line, and every core issuing a CompareAndSwap against it makes that
// line ping-pong. The degradation is inherent to a single globally ordered
// sequence; the fix is more node IDs per process, not a different loop.
func BenchmarkNextParallel(b *testing.B) {
	g, err := New(1)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := g.Next(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkNextFakeClock isolates the CAS loop from the millisecond ceiling
// by advancing a fake clock fast enough that the sequence never fills.
func BenchmarkNextFakeClock(b *testing.B) {
	clock := NewFakeClock(time.UnixMilli(Epoch).Add(time.Hour))
	g, err := New(1, WithClock(clock))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Next(); err != nil {
			b.Fatal(err)
		}
		if i%1024 == 0 {
			clock.Advance(time.Millisecond)
		}
	}
}

func BenchmarkUUIDv7Next(b *testing.B) {
	g, err := NewUUIDv7Generator()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Next(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkULIDNext(b *testing.B) {
	g, err := NewULIDGenerator()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Next(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkULIDString(b *testing.B) {
	g, _ := NewULIDGenerator(WithClock(NewFakeClock(time.UnixMilli(Epoch).Add(time.Hour))))
	u, _ := g.Next()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = u.String()
	}
}

func BenchmarkUUIDString(b *testing.B) {
	g, _ := NewUUIDv7Generator(WithClock(NewFakeClock(time.UnixMilli(Epoch).Add(time.Hour))))
	u, _ := g.Next()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = u.String()
	}
}
