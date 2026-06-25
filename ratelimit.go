package evalgo

import (
	"context"
	"sync"
	"time"
)

// RateLimit wraps a JudgeFunc with a lazy token-bucket limiter so concurrent
// samples don't trigger 429 Too Many Requests from the judge provider.
//
// rps is tokens refilled per second; burst is the bucket capacity. Production
// code may prefer golang.org/x/time/rate; this is an equivalent, dependency-free
// implementation kept in-tree to honor the framework's stdlib-only core.
func RateLimit(j JudgeFunc, rps float64, burst int) JudgeFunc {
	if rps <= 0 || burst <= 0 {
		return j
	}
	b := &tokenBucket{rps: rps, burst: float64(burst), tokens: float64(burst), now: time.Now}
	b.last = b.now()
	return func(ctx context.Context, prompt string) (string, error) {
		if err := b.wait(ctx); err != nil {
			return "", err
		}
		return j(ctx, prompt)
	}
}

type tokenBucket struct {
	mu     sync.Mutex
	rps    float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func (b *tokenBucket) wait(ctx context.Context) error {
	for {
		b.mu.Lock()
		now := b.now()
		b.tokens += now.Sub(b.last).Seconds() * b.rps
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}
		wait := time.Duration((1 - b.tokens) / b.rps * float64(time.Second))
		b.mu.Unlock()

		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}
