package evalgo

import (
	"context"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("empty = %d, want 0", got)
	}
	if got := EstimateTokens("abcd"); got != 1 { // 4 runes / 4
		t.Errorf("abcd = %d, want 1", got)
	}
	if got := EstimateTokens("abcde"); got != 2 { // 5 runes → ceil(5/4)
		t.Errorf("abcde = %d, want 2", got)
	}
}

func TestMeterCountsAndCost(t *testing.T) {
	// $3 per 1M prompt tokens, $15 per 1M completion tokens.
	m := NewMeter(3, 15)
	base := func(_ context.Context, _ string) (string, error) { return "abcd", nil } // 1 completion token
	j := m.Wrap(base)

	// prompt "abcdefgh" = 8 runes → 2 prompt tokens; call twice.
	for range 2 {
		if _, err := j(context.Background(), "abcdefgh"); err != nil {
			t.Fatal(err)
		}
	}
	u := m.Usage()
	if u.Calls != 2 || u.PromptTokens != 4 || u.CompletionTokens != 2 || u.TotalTokens != 6 {
		t.Fatalf("unexpected usage: %+v", u)
	}
	wantCost := 4.0/1e6*3 + 2.0/1e6*15
	if u.Cost != wantCost {
		t.Errorf("cost = %v, want %v", u.Cost, wantCost)
	}
}

func TestMeterInsideCacheNotBilledOnHit(t *testing.T) {
	dir := t.TempDir()
	m := NewMeter(0, 0)
	calls := 0
	base := func(_ context.Context, _ string) (string, error) { calls++; return "resp", nil }
	// Meter inside cache: a hit returns before reaching the meter.
	j := Cache(m.Wrap(base), dir)

	for range 3 {
		if _, err := j(context.Background(), "same"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("want 1 real call, got %d", calls)
	}
	if u := m.Usage(); u.Calls != 1 {
		t.Errorf("meter should count only the 1 real call, got %d", u.Calls)
	}
}
