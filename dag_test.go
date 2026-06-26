package evalgo

import (
	"context"
	"testing"
)

func TestDAGRoutesByChoice(t *testing.T) {
	// fakeJudge always answers "yes" → routes down the yes branch to Leaf(1).
	root := YesNo("Is the OUTPUT valid JSON?",
		Leaf(1, "valid"),
		Leaf(0, "not json"))
	m := DAG(fakeJudge(`{"choice":"yes","reason":"looks valid"}`), "json_shape", 0.99, root)

	res, err := m.Score(context.Background(), Sample{Output: `{"a":1}`})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 1 {
		t.Errorf("want pass score=1, got passed=%v score=%.2f reason=%s", res.Passed, res.Score, res.Reason)
	}
}

func TestDAGNoBranchMatchScoresZero(t *testing.T) {
	root := YesNo("ok?", Leaf(1, "y"), Leaf(1, "n")) // neither matches "maybe"
	m := DAG(fakeJudge(`{"choice":"maybe"}`), "d", 0.5, root)
	res, err := m.Score(context.Background(), Sample{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || res.Score != 0 {
		t.Errorf("unmatched choice should score 0, got passed=%v score=%.2f", res.Passed, res.Score)
	}
}

func TestDAGNestedAndFallback(t *testing.T) {
	// Multi-label branch with a fallback; fakeJudge answers "b".
	root := Branch("pick", map[string]DAGNode{
		"a": Leaf(0.2, "a"),
		"b": Leaf(0.8, "b"),
	}, Leaf(0, "fallback"))
	m := DAG(fakeJudge(`{"choice":"b","reason":"chose b"}`), "pick", 0.5, root)
	res, err := m.Score(context.Background(), Sample{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score != 0.8 {
		t.Errorf("want score 0.8, got %.2f (reason=%s)", res.Score, res.Reason)
	}
}
