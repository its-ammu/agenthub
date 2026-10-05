package usage

import "testing"

func TestLookupPicksLongestPrefix(t *testing.T) {
	p, ok := lookup("claude-opus-5-5-20260401")
	if !ok || p.in != 4 {
		t.Fatalf("opus 5.5 should match its own price, got %+v ok=%v", p, ok)
	}
	if p, ok := lookup("claude-opus-5"); !ok || p.in != 5 {
		t.Fatalf("opus 5 should price at 5, got %+v ok=%v", p, ok)
	}
	if _, ok := lookup("some-other-model"); ok {
		t.Fatal("unknown models must be unpriced")
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
