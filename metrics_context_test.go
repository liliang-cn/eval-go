package evalgo

import (
	"context"
	"testing"
)

// These metrics call the judge twice (extractStatements then verdicts) and fakeJudge
// returns the same canned reply each call, so the reply satisfies BOTH shapes at once:
// extractStatements reads .statements, the verdict step reads .verdicts.
const ctxAllGood = `{"statements":["a","b"],"verdicts":[{"verdict":"yes"},{"verdict":"yes"}]}`

func TestContextualRecall(t *testing.T) {
	ctx := context.Background()

	// fully supported → score 1, pass
	m := ContextualRecall(fakeJudge(ctxAllGood), 0.7)
	res, err := m.Score(ctx, Sample{Expected: "a b", Context: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// no expected → skip-pass
	res2, _ := m.Score(ctx, Sample{Context: []string{"a"}})
	if !res2.Passed || res2.Score != 1 {
		t.Errorf("no-expected: got passed=%v score=%.2f", res2.Passed, res2.Score)
	}

	// no context → fail
	res3, _ := m.Score(ctx, Sample{Expected: "a b"})
	if res3.Passed || res3.Score != 0 {
		t.Errorf("no-context: got passed=%v score=%.2f", res3.Passed, res3.Score)
	}
}

func TestContextualRelevancy(t *testing.T) {
	ctx := context.Background()

	// all relevant → score 1, pass
	m := ContextualRelevancy(fakeJudge(ctxAllGood), 0.7)
	res, err := m.Score(ctx, Sample{Input: "q", Context: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}

	// no context → fail
	res2, _ := m.Score(ctx, Sample{Input: "q"})
	if res2.Passed || res2.Score != 0 {
		t.Errorf("no-context: got passed=%v score=%.2f", res2.Passed, res2.Score)
	}
}
