package evalgo

import (
	"context"
	"testing"
)

func TestToolCorrectness(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		sample     Sample
		wantPassed bool
		wantScore  float64
	}{
		{
			name:       "exact set match",
			sample:     Sample{ExpectedTools: []string{"search", "calc"}, ToolCalls: []ToolCall{{Name: "calc"}, {Name: "search"}}},
			wantPassed: true, wantScore: 1,
		},
		{
			name:       "missing a tool",
			sample:     Sample{ExpectedTools: []string{"search", "calc"}, ToolCalls: []ToolCall{{Name: "search"}}},
			wantPassed: false, wantScore: 0.5, // matched 1, union 2
		},
		{
			name:       "extra unexpected tool",
			sample:     Sample{ExpectedTools: []string{"search"}, ToolCalls: []ToolCall{{Name: "search"}, {Name: "delete"}}},
			wantPassed: false, wantScore: 0.5, // matched 1, union 2
		},
		{
			name:       "no expected tools is skipped",
			sample:     Sample{ToolCalls: []ToolCall{{Name: "search"}}},
			wantPassed: true, wantScore: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := ToolCorrectness().Score(ctx, c.sample)
			if err != nil {
				t.Fatal(err)
			}
			if res.Passed != c.wantPassed || res.Score != c.wantScore {
				t.Errorf("passed=%v score=%.2f, want passed=%v score=%.2f (%s)",
					res.Passed, res.Score, c.wantPassed, c.wantScore, res.Reason)
			}
		})
	}
}

func TestTaskCompletionParsing(t *testing.T) {
	m := TaskCompletion(fakeJudge(`{"passed":true,"score":0.95,"reason":"done"}`), 0.7)
	res, err := m.Score(context.Background(), Sample{Input: "book a flight", Output: "booked", Trajectory: []string{"searched", "booked"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 0.95 {
		t.Errorf("got passed=%v score=%.2f", res.Passed, res.Score)
	}
}

func TestArgumentCorrectness(t *testing.T) {
	m := ArgumentCorrectness(fakeJudge(`{"verdicts":[{"verdict":"yes"},{"verdict":"no","reason":"wrong city"}]}`), 0.5)
	s := Sample{
		Input:     "weather in Tokyo",
		ToolCalls: []ToolCall{{Name: "geocode", Args: map[string]any{"q": "Tokyo"}}, {Name: "weather", Args: map[string]any{"city": "Osaka"}}},
	}
	res, err := m.Score(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if res.Score != 0.5 || res.Passed != true { // 1/2 correct, threshold 0.5 → pass
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// no tool calls → skipped pass
	res2, _ := m.Score(context.Background(), Sample{Input: "x"})
	if !res2.Passed || res2.Score != 1 {
		t.Errorf("expected skip-pass, got passed=%v score=%.2f", res2.Passed, res2.Score)
	}
}

func TestPlanMetricsSkipWithoutPlan(t *testing.T) {
	// A nil judge must never be called when there is no plan to evaluate.
	for _, m := range []Metric{PlanQuality(nil, 0.7), PlanAdherence(nil, 0.7)} {
		res, err := m.Score(context.Background(), Sample{Input: "x", Trajectory: []string{"step"}})
		if err != nil {
			t.Fatalf("%s: %v", m.Name(), err)
		}
		if !res.Passed {
			t.Errorf("%s: expected skip-pass without a plan", m.Name())
		}
	}
}
