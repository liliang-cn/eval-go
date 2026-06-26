// Package evalgo is a small, native-Go LLM evaluation framework.
//
// Philosophy (vs. Python's DeepEval/RAGAS): extend Go's strengths — concurrency,
// determinism, zero-cost heuristics, and `go test` — instead of importing a heavy
// framework. Metrics split into two families:
//
//   - Deterministic (code-based): JSON/regex/contains/exact — fast, free, no LLM.
//   - Semantic (LLM-as-a-judge): rubric, faithfulness, answer relevancy, context
//     precision — capture intent via a Judge.
//
// The core package has zero third-party dependencies (stdlib only). The agent-go
// judge adapter lives in the sibling package ./llmjudge so library users who only
// want deterministic metrics pay no dependency cost.
package evalgo

import (
	"context"
	"sort"
	"sync"
)

// Sample is one row of a golden dataset plus the system's actual output.
//
// The first block of fields describes a single-turn output (RAG / generation).
// The agent block records what an agent actually did during a run — Eval-Go
// evaluates that recorded trajectory rather than instrumenting a live runtime,
// so any framework (or none) can emit these fields and be scored the same way.
type Sample struct {
	Name     string            `json:"name"`               // unique-ish label for reports
	Input    string            `json:"input"`              // the question / prompt / task given to the system
	Output   string            `json:"output"`             // the actual answer produced by the system under test
	Expected string            `json:"expected,omitempty"` // optional reference answer (for ExactMatch etc.)
	Context  []string          `json:"context,omitempty"`  // retrieved evidence chunks (for RAG metrics)
	Rubric   string            `json:"rubric,omitempty"`   // pass/fail criterion for a rubric judge
	Meta     map[string]string `json:"meta,omitempty"`     // free-form labels carried into reports

	// --- agent execution (recorded from an agent run; for agentic metrics) ---
	Plan          string     `json:"plan,omitempty"`           // the agent's stated plan (for plan_quality / plan_adherence)
	Trajectory    []string   `json:"trajectory,omitempty"`     // ordered reasoning/action steps the agent took
	ToolCalls     []ToolCall `json:"tool_calls,omitempty"`     // tools the agent actually invoked, in order
	ExpectedTools []string   `json:"expected_tools,omitempty"` // ground-truth tool names (for tool_correctness)

	// --- multi-turn conversation (for conversational metrics) ---
	Turns   []Turn `json:"turns,omitempty"`   // the full chat history, in order
	Persona string `json:"persona,omitempty"` // the role the assistant should hold (for role_adherence)
}

// Turn is one message in a multi-turn conversation.
type Turn struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content string `json:"content"` // the message text
}

// ToolCall is one tool invocation recorded from an agent run.
type ToolCall struct {
	Name   string         `json:"name"`             // tool / function name invoked
	Args   map[string]any `json:"args,omitempty"`   // arguments the agent passed
	Output string         `json:"output,omitempty"` // tool result, if captured (helps the judge)
}

// Result is the outcome of one metric on one sample. Score is normalized 0..1.
type Result struct {
	Metric string  `json:"metric"`
	Score  float64 `json:"score"`
	Passed bool    `json:"passed"`
	Reason string  `json:"reason,omitempty"`
	Err    string  `json:"error,omitempty"`
}

// Metric scores a single sample. Deterministic metrics ignore ctx; semantic
// metrics use it for the judge call (cancellation / deadlines).
type Metric interface {
	Name() string
	Score(ctx context.Context, s Sample) (Result, error)
}

// MetricFunc adapts a function into a Metric.
type MetricFunc struct {
	MetricName string
	Fn         func(ctx context.Context, s Sample) (Result, error)
}

func (m MetricFunc) Name() string { return m.MetricName }
func (m MetricFunc) Score(ctx context.Context, s Sample) (Result, error) {
	return m.Fn(ctx, s)
}

// pass is a helper for building deterministic Results.
func pass(metric string, ok bool, reason string) Result {
	score := 0.0
	if ok {
		score = 1.0
	}
	return Result{Metric: metric, Score: score, Passed: ok, Reason: reason}
}

// Suite is a dataset + the metrics to apply, run concurrently across samples.
type Suite struct {
	Samples     []Sample
	Metrics     []Metric
	Concurrency int // samples evaluated in parallel; default 4
}

// Run evaluates every metric against every sample. Samples run concurrently
// (bounded by Concurrency); metrics within a sample run sequentially so a shared
// rate-limited judge paces cleanly. A metric error becomes a failed Result
// (Err set) rather than aborting the whole run.
func (s Suite) Run(ctx context.Context) Report {
	conc := s.Concurrency
	if conc <= 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	reports := make([]SampleReport, len(s.Samples))

	for i, sample := range s.Samples {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, sample Sample) {
			defer wg.Done()
			defer func() { <-sem }()

			sr := SampleReport{Sample: sample.Name, Meta: sample.Meta, Passed: true}
			for _, m := range s.Metrics {
				res, err := m.Score(ctx, sample)
				if err != nil {
					res = Result{Metric: m.Name(), Passed: false, Err: err.Error()}
				}
				sr.Results = append(sr.Results, res)
				if !res.Passed {
					sr.Passed = false
				}
			}
			reports[i] = sr
		}(i, sample)
	}
	wg.Wait()

	return Report{Samples: reports}
}

// MetricSummary aggregates one metric across all samples.
type MetricSummary struct {
	Metric    string  `json:"metric"`
	PassRate  float64 `json:"pass_rate"` // 0..1
	MeanScore float64 `json:"mean_score"`
	Passed    int     `json:"passed"`
	Total     int     `json:"total"`
}

// Summary computes per-metric aggregates, ordered by metric name.
func (r Report) Summary() []MetricSummary {
	type acc struct {
		passed, total int
		scoreSum      float64
	}
	byMetric := map[string]*acc{}
	var order []string
	for _, sr := range r.Samples {
		for _, res := range sr.Results {
			a := byMetric[res.Metric]
			if a == nil {
				a = &acc{}
				byMetric[res.Metric] = a
				order = append(order, res.Metric)
			}
			a.total++
			a.scoreSum += res.Score
			if res.Passed {
				a.passed++
			}
		}
	}
	sort.Strings(order)
	out := make([]MetricSummary, 0, len(order))
	for _, name := range order {
		a := byMetric[name]
		ms := MetricSummary{Metric: name, Passed: a.passed, Total: a.total}
		if a.total > 0 {
			ms.PassRate = float64(a.passed) / float64(a.total)
			ms.MeanScore = a.scoreSum / float64(a.total)
		}
		out = append(out, ms)
	}
	return out
}
