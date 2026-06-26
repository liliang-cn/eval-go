package evalgo

import (
	"context"
	"testing"
)

func TestHallucination(t *testing.T) {
	ctx := context.Background()
	// Two contexts, both consistent with the output → 2/2 → pass.
	m := Hallucination(fakeJudge(`{"verdicts":[{"verdict":"yes"},{"verdict":"yes"}]}`), 0.8)
	res, err := m.Score(ctx, Sample{Context: []string{"sky is blue", "grass is green"}, Output: "the sky is blue"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// no context → skip-pass
	res2, _ := m.Score(ctx, Sample{Output: "anything"})
	if !res2.Passed || res2.Score != 1 {
		t.Errorf("expected skip-pass, got passed=%v score=%.2f", res2.Passed, res2.Score)
	}
}

func TestBias(t *testing.T) {
	ctx := context.Background()
	// extractStatements yields 2 statements; both verdicts "no" (not biased) → 2/2 unbiased.
	m := Bias(fakeJudge(`{"statements":["x","y"],"verdicts":[{"verdict":"no"},{"verdict":"no"}]}`), 0.8)
	res, err := m.Score(ctx, Sample{Output: "the team shipped the feature on time"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// empty output → no statements → skip-pass
	m2 := Bias(fakeJudge(`{"statements":[]}`), 0.8)
	res2, _ := m2.Score(ctx, Sample{Output: ""})
	if !res2.Passed || res2.Score != 1 {
		t.Errorf("expected skip-pass, got passed=%v score=%.2f", res2.Passed, res2.Score)
	}
}

func TestToxicity(t *testing.T) {
	ctx := context.Background()
	// 2 statements, both "no" (non-toxic) → 2/2.
	m := Toxicity(fakeJudge(`{"statements":["x","y"],"verdicts":[{"verdict":"no"},{"verdict":"no"}]}`), 0.8)
	res, err := m.Score(ctx, Sample{Output: "thanks for the help, much appreciated"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// no statements → skip-pass
	m2 := Toxicity(fakeJudge(`{"statements":[]}`), 0.8)
	res2, _ := m2.Score(ctx, Sample{Output: ""})
	if !res2.Passed || res2.Score != 1 {
		t.Errorf("expected skip-pass, got passed=%v score=%.2f", res2.Passed, res2.Score)
	}
}

func TestPIILeakage(t *testing.T) {
	m := PIILeakage(fakeJudge(`{"passed":true,"score":1.0,"reason":"no PII present"}`))
	res, err := m.Score(context.Background(), Sample{Output: "the order has shipped"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// leak detected → fail
	m2 := PIILeakage(fakeJudge(`{"passed":false,"score":0.2,"reason":"leaked email"}`))
	res2, _ := m2.Score(context.Background(), Sample{Output: "contact jane@example.com"})
	if res2.Passed {
		t.Errorf("expected fail on PII leak, got pass (score=%.2f)", res2.Score)
	}
}

func TestSummarization(t *testing.T) {
	m := Summarization(fakeJudge(`{"passed":true,"score":0.9,"reason":"faithful and complete"}`), 0.7)
	res, err := m.Score(context.Background(), Sample{Input: "a long source document about widgets", Output: "summary about widgets"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 0.9 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// missing source → skip-pass (nil judge must never be called)
	m2 := Summarization(nil, 0.7)
	res2, err := m2.Score(context.Background(), Sample{Output: "a summary with no source"})
	if err != nil {
		t.Fatal(err)
	}
	if !res2.Passed || res2.Score != 1 {
		t.Errorf("expected skip-pass, got passed=%v score=%.2f", res2.Passed, res2.Score)
	}
}
