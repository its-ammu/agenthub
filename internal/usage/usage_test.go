package usage

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupPicksLongestPrefix(t *testing.T) {
	p, ok := lookup("claude-opus-5-5-20260401")
	if !ok || p.Input != 4 {
		t.Fatalf("opus 5.5 should match its own price, got %+v ok=%v", p, ok)
	}
	if p, ok := lookup("claude-opus-5"); !ok || p.Input != 5 {
		t.Fatalf("opus 5 should price at 5, got %+v ok=%v", p, ok)
	}
	if _, ok := lookup("some-other-model"); ok {
		t.Fatal("unknown models must be unpriced")
	}
}

func TestCostDefaultsAndFastMultiplier(t *testing.T) {
	p := Price{Input: 4, Output: 20, CacheRead: 0.2, FastMultiplier: 2}
	// 1M each: input 4 + output 20 + cache read 0.2 + write5m 5 (1.25x) + write1h 8 (2x)
	got := p.cost(1e6, 1e6, 1e6, 1e6, 1e6, false)
	if math.Abs(got-37.2) > 1e-9 {
		t.Fatalf("standard cost = %v, want 37.2", got)
	}
	if fast := p.cost(1e6, 1e6, 1e6, 1e6, 1e6, true); math.Abs(fast-74.4) > 1e-9 {
		t.Fatalf("fast cost = %v, want 74.4", fast)
	}
	// A model with no fast multiplier is not scaled.
	if c := (Price{Input: 1, Output: 1}).cost(1e6, 0, 0, 0, 0, true); c != 1 {
		t.Fatalf("fast without multiplier should be unscaled, got %v", c)
	}
}

func TestLoadPricesOverridesAndAdds(t *testing.T) {
	orig := prices
	defer func() { prices = orig }()

	path := filepath.Join(t.TempDir(), "prices.json")
	os.WriteFile(path, []byte(`{"models":{"claude-opus-5-5":{"input":1,"output":2,"cache_read":0.1},"my-model":{"input":9,"output":9,"cache_read":1}}}`), 0o600)
	if err := LoadPrices(path); err != nil {
		t.Fatal(err)
	}
	if p, _ := lookup("claude-opus-5-5"); p.Input != 1 {
		t.Fatalf("override not applied: %+v", p)
	}
	if _, ok := lookup("my-model-x"); !ok {
		t.Fatal("added model not found")
	}
	if p, _ := lookup("claude-haiku-4-5"); p.Input != 1 || p.Output != 5 {
		t.Fatalf("untouched model changed: %+v", p)
	}
}

func TestBuildTimelineBucketsAndTotals(t *testing.T) {
	turns := []turn{
		{at: "2026-10-05T10:00:10Z"}, {at: "2026-10-05T10:00:50Z"}, {at: "2026-10-05T10:30:00Z"},
	}
	costs := []float64{1, 2, 4}
	b, size := buildTimeline(turns, costs)
	if size != 1 {
		t.Fatalf("a 30 minute span should use 1 minute buckets, got %d", size)
	}
	var total float64
	var n int
	for _, x := range b {
		total += x.Cost
		n += x.Turns
	}
	if total != 7 || n != 3 {
		t.Fatalf("buckets lost data: total=%v turns=%d", total, n)
	}
	if b[0].Cost != 3 {
		t.Fatalf("first bucket should hold the first two turns, got %v", b[0].Cost)
	}
}

func TestSafeID(t *testing.T) {
	if safeID("../etc/passwd") || safeID("") {
		t.Fatal("path-like ids must be rejected")
	}
	if !safeID("dc8701d0-14aa-41ff-8b13-e3bea6aa634a") {
		t.Fatal("uuid should be accepted")
	}
}
