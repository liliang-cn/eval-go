package evalgo

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Meter wraps a JudgeFunc to record usage — call count, (estimated) tokens,
// cumulative judge time, and optional cost — so a CI run can report what an
// evaluation spent. Concurrency-safe.
//
// Wrap the Meter INSIDE the cache so cache hits aren't billed:
//
//	judge = RateLimit(judge, rps, burst)
//	judge = meter.Wrap(judge)        // counts only real provider calls
//	judge = Cache(judge, dir)        // a hit returns before reaching the meter
//
// Token counts are heuristic (see EstimateTokens), not a provider tokenizer —
// good for relative comparison and rough cost, not billing-exact.
type Meter struct {
	mu               sync.Mutex
	calls            int
	promptTokens     int
	completionTokens int
	dur              time.Duration
	costInPerM       float64 // USD per 1M prompt tokens
	costOutPerM      float64 // USD per 1M completion tokens
	now              func() time.Time
}

// NewMeter creates a Meter. costInPerM / costOutPerM are USD per 1,000,000
// prompt / completion tokens; pass 0 to omit cost from the report.
func NewMeter(costInPerM, costOutPerM float64) *Meter {
	return &Meter{costInPerM: costInPerM, costOutPerM: costOutPerM, now: time.Now}
}

// Wrap returns a JudgeFunc that records each call into the Meter.
func (m *Meter) Wrap(j JudgeFunc) JudgeFunc {
	if j == nil {
		return nil
	}
	return func(ctx context.Context, prompt string) (string, error) {
		start := m.now()
		resp, err := j(ctx, prompt)
		elapsed := m.now().Sub(start)

		m.mu.Lock()
		m.calls++
		m.dur += elapsed
		m.promptTokens += EstimateTokens(prompt)
		m.completionTokens += EstimateTokens(resp) // 0 on error (empty resp)
		m.mu.Unlock()
		return resp, err
	}
}

// Usage snapshots the metered totals.
func (m *Meter) Usage() Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := Usage{
		Calls:            m.calls,
		PromptTokens:     m.promptTokens,
		CompletionTokens: m.completionTokens,
		TotalTokens:      m.promptTokens + m.completionTokens,
		JudgeSeconds:     m.dur.Seconds(),
	}
	u.Cost = float64(m.promptTokens)/1e6*m.costInPerM + float64(m.completionTokens)/1e6*m.costOutPerM
	return u
}

// Usage is a snapshot of judge usage, suitable for reports.
type Usage struct {
	Calls            int     `json:"calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	JudgeSeconds     float64 `json:"judge_seconds"`
	Cost             float64 `json:"cost,omitempty"`
}

// EstimateTokens approximates BPE token count as ~4 characters per token — a
// rough, model-agnostic heuristic (no tokenizer dependency, keeps the core
// stdlib-only). Use it for relative comparison and ballpark cost, not billing.
func EstimateTokens(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return (len([]rune(s)) + 3) / 4
}
