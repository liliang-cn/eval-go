package evalgo

import (
	"context"
	"testing"
)

func TestCacheHitsSkipJudge(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	base := func(_ context.Context, _ string) (string, error) { calls++; return "verdict", nil }
	j := Cache(base, dir)

	for range 3 {
		v, err := j(context.Background(), "same prompt")
		if err != nil || v != "verdict" {
			t.Fatalf("got %q err=%v", v, err)
		}
	}
	if calls != 1 {
		t.Errorf("want 1 underlying call (2 cache hits), got %d", calls)
	}

	// A different prompt is a miss.
	if _, err := j(context.Background(), "other prompt"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("want 2 calls after new prompt, got %d", calls)
	}

	// A fresh Cache over the same dir reads the persisted file — no call.
	j2 := Cache(base, dir)
	if _, err := j2(context.Background(), "same prompt"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("persisted hit should not call judge; calls=%d", calls)
	}
}

func TestCacheDoesNotCacheErrors(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	base := func(_ context.Context, _ string) (string, error) { calls++; return "", context.Canceled }
	j := Cache(base, dir)
	for range 2 {
		if _, err := j(context.Background(), "p"); err == nil {
			t.Fatal("expected error")
		}
	}
	if calls != 2 {
		t.Errorf("errors must not be cached; want 2 calls, got %d", calls)
	}
}

func TestCacheEmptyDirDisabled(t *testing.T) {
	calls := 0
	base := func(_ context.Context, _ string) (string, error) { calls++; return "x", nil }
	j := Cache(base, "")
	for range 2 {
		_, _ = j(context.Background(), "p")
	}
	if calls != 2 {
		t.Errorf("empty dir should disable caching; want 2 calls, got %d", calls)
	}
}
