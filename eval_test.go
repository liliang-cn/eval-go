package evalgo

import (
	"context"
	"testing"
	"time"
)

// Deterministic-metric tests need no LLM and always run (cheap CI gate).
func TestDeterministicMetrics(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		metric Metric
		sample Sample
		want   bool
	}{
		{"valid json ok", ValidJSON(), Sample{Output: `{"a":1}`}, true},
		{"valid json bad", ValidJSON(), Sample{Output: `not json`}, false},
		{"has fields ok", JSONHasFields("name", "age"), Sample{Output: `{"name":"Jane","age":30}`}, true},
		{"has fields missing", JSONHasFields("name", "age"), Sample{Output: `{"name":"Jane"}`}, false},
		{"forbids pii ok", ForbidsRegex(`\b\d{16,19}\b`), Sample{Output: "card on file"}, true},
		{"forbids pii leak", ForbidsRegex(`\b\d{16,19}\b`), Sample{Output: "card 4111111111111111"}, false},
		{"citation present", CitationPresent(), Sample{Output: "rate is 0.3% [KB-001]"}, true},
		{"citation absent", CitationPresent(), Sample{Output: "the rate is 0.3%"}, false},
		{"exact match", ExactMatch(), Sample{Output: "yes", Expected: "yes"}, true},
		{"contains", Contains("Jane"), Sample{Output: "Hello JANE"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := c.metric.Score(ctx, c.sample)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Passed != c.want {
				t.Errorf("%s: passed=%v want %v (reason=%s)", c.metric.Name(), res.Passed, c.want, res.Reason)
			}
		})
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`{"passed":true}`:                            `{"passed":true}`,
		"```json\n{\"passed\":false}\n```":           `{"passed":false}`,
		"Here is my verdict: {\"score\":0.5} thanks": `{"score":0.5}`,
		"prefix [\"a\",\"b\"] suffix":                `["a","b"]`,
		"nested {\"a\":{\"b\":1}} ok":                `{"a":{"b":1}}`,
		"string with brace {\"s\":\"}{\"} end":       `{"s":"}{"}`,
	}
	for in, want := range cases {
		got, err := extractJSON(in)
		if err != nil {
			t.Errorf("extractJSON(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeJudge lets us exercise judge metrics without a network call.
func fakeJudge(reply string) JudgeFunc {
	return func(_ context.Context, _ string) (string, error) { return reply, nil }
}

func TestRubricJudgeParsing(t *testing.T) {
	m := RubricJudge(fakeJudge(`{"passed":true,"score":0.9,"reason":"good"}`), 0.7)
	res, err := m.Score(context.Background(), Sample{Rubric: "is good", Output: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 0.9 {
		t.Errorf("got passed=%v score=%.2f", res.Passed, res.Score)
	}
	// below threshold
	m2 := RubricJudge(fakeJudge(`{"passed":true,"score":0.5}`), 0.7)
	res2, _ := m2.Score(context.Background(), Sample{Rubric: "is good", Output: "x"})
	if res2.Passed {
		t.Errorf("expected fail below threshold, got pass")
	}
}

func TestSuiteRunAndReport(t *testing.T) {
	suite := Suite{
		Samples: []Sample{
			{Name: "good", Output: `{"name":"Jane"}`},
			{Name: "bad", Output: `oops`},
		},
		Metrics:     []Metric{ValidJSON()},
		Concurrency: 2,
	}
	rep := suite.Run(context.Background())
	if !rep.Failed() {
		t.Error("expected report to have failures")
	}
	sum := rep.Summary()
	if len(sum) != 1 || sum[0].Total != 2 || sum[0].Passed != 1 {
		t.Errorf("unexpected summary: %+v", sum)
	}
}

func TestRateLimit(t *testing.T) {
	calls := 0
	j := RateLimit(func(_ context.Context, _ string) (string, error) { calls++; return "ok", nil }, 50, 1)
	start := time.Now()
	for range 4 {
		if _, err := j(context.Background(), "p"); err != nil {
			t.Fatal(err)
		}
	}
	// burst=1, rps=50 → ~20ms between calls → 3 gaps ≈ 60ms minimum.
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("rate limiter too fast: %v for 4 calls", elapsed)
	}
	if calls != 4 {
		t.Errorf("want 4 calls, got %d", calls)
	}
}
