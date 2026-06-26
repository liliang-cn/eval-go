package evalgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
)

// Cache wraps a JudgeFunc so identical prompts are served from an on-disk cache
// instead of re-calling (and re-billing) the provider. Responses are keyed by a
// SHA-256 of the prompt and persisted as files under dir, so they survive across
// runs — a re-run of an unchanged dataset spends no tokens. Concurrency-safe.
//
// Only successful responses are cached; errors always re-call. An empty dir
// disables caching (returns the judge unchanged), mirroring RateLimit.
//
// Cache and RateLimit compose; wrap with Cache outermost so cache hits skip the
// rate limiter entirely: RateLimit(Cache(judge, dir), rps, burst) caches, then
// paces only the misses — or Cache(RateLimit(judge,...), dir) to pace nothing on
// a hit. The CLI uses the latter.
func Cache(j JudgeFunc, dir string) JudgeFunc {
	if dir == "" {
		return j
	}
	c := &judgeCache{dir: dir, mem: map[string]string{}}
	return func(ctx context.Context, prompt string) (string, error) {
		key := promptKey(prompt)
		if v, ok := c.load(key); ok {
			return v, nil
		}
		v, err := j(ctx, prompt)
		if err != nil {
			return "", err
		}
		c.store(key, v)
		return v, nil
	}
}

type judgeCache struct {
	dir  string
	mu   sync.RWMutex
	mem  map[string]string
	once sync.Once
}

func promptKey(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:])
}

func (c *judgeCache) path(key string) string { return filepath.Join(c.dir, key+".txt") }

// load checks the in-memory map first, then falls back to a cache file on disk.
func (c *judgeCache) load(key string) (string, bool) {
	c.mu.RLock()
	v, ok := c.mem[key]
	c.mu.RUnlock()
	if ok {
		return v, true
	}
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		return "", false
	}
	c.mu.Lock()
	c.mem[key] = string(data)
	c.mu.Unlock()
	return string(data), true
}

// store writes the response to memory and best-effort to disk.
func (c *judgeCache) store(key, val string) {
	c.mu.Lock()
	c.mem[key] = val
	c.mu.Unlock()
	c.once.Do(func() { _ = os.MkdirAll(c.dir, 0o755) })
	_ = os.WriteFile(c.path(key), []byte(val), 0o644)
}
