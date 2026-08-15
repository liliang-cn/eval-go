package evalgo

import (
	"strings"
	"testing"
)

// mkRuns builds a ComparisonReport from per-target, per-run task outcomes:
// outcomes[target][run] = map[task]passed.
func mkRuns(gate []string, outcomes map[string][]map[string]bool) ComparisonReport {
	cr := ComparisonReport{Gate: gate}
	runs := 0
	for _, rs := range outcomes {
		runs = max(runs, len(rs))
	}
	cr.Runs = runs
	for run := range runs {
		for name, rs := range outcomes {
			if run >= len(rs) {
				continue
			}
			var rep Report
			for task, ok := range rs[run] {
				rep.Samples = append(rep.Samples, SampleReport{
					Sample:  task,
					Passed:  ok,
					Results: []Result{{Metric: "task_completion", Passed: ok, Score: b2f(ok)}},
				})
			}
			cr.Targets = append(cr.Targets, TargetReport{Name: name, Run: run + 1, Report: rep})
		}
	}
	return cr
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func TestStatsPassKAndCaretK(t *testing.T) {
	// "solid" passes both tasks every run; "flaky" passes each task once out of
	// two runs — same pass@1, very different reliability.
	cr := mkRuns([]string{"task_completion"}, map[string][]map[string]bool{
		"solid": {
			{"a": true, "b": true},
			{"a": true, "b": true},
		},
		"flaky": {
			{"a": true, "b": false},
			{"a": false, "b": true},
		},
	})

	stats := map[string]TargetStats{}
	for _, s := range cr.Stats() {
		stats[s.Name] = s
	}

	solid, flaky := stats["solid"], stats["flaky"]
	if solid.Pass1 != 1 || solid.PassK != 1 || solid.PassCaretK != 1 {
		t.Errorf("solid = pass@1 %.2f pass@k %.2f pass^k %.2f, want all 1", solid.Pass1, solid.PassK, solid.PassCaretK)
	}
	// flaky: 2 of 4 attempts win (pass@1 .5), both tasks solved at least once
	// (pass@k 1), neither solved every run (pass^k 0).
	if flaky.Pass1 != 0.5 {
		t.Errorf("flaky pass@1 = %.2f, want 0.50", flaky.Pass1)
	}
	if flaky.PassK != 1 {
		t.Errorf("flaky pass@k = %.2f, want 1.00 — each task succeeded once", flaky.PassK)
	}
	if flaky.PassCaretK != 0 {
		t.Errorf("flaky pass^k = %.2f, want 0.00 — no task succeeded every run", flaky.PassCaretK)
	}
}

func TestStatsSpreadTracksRunToRunSwing(t *testing.T) {
	cr := mkRuns(nil, map[string][]map[string]bool{
		"swingy": {
			{"a": true, "b": true},   // 100%
			{"a": false, "b": false}, // 0%
		},
	})
	s := cr.Stats()[0]
	if s.Spread() != 1 {
		t.Errorf("spread = %.2f, want 1.00 for a target that swings 100%%→0%%", s.Spread())
	}
	if len(s.Each) != 2 || s.Each[0] != 1 || s.Each[1] != 0 {
		t.Errorf("per-run rates = %v, want [1 0]", s.Each)
	}
}

func TestCompareCountsOnlyConsistentWins(t *testing.T) {
	// A wins task "x" in both runs. B wins "y" in both. On "z" A is flaky —
	// that must NOT count as a win for A.
	cr := mkRuns(nil, map[string][]map[string]bool{
		"A": {
			{"x": true, "y": false, "z": true},
			{"x": true, "y": false, "z": false},
		},
		"B": {
			{"x": false, "y": true, "z": false},
			{"x": false, "y": true, "z": false},
		},
	})
	d := cr.Compare("A", "B")
	if d.AOnly != 1 || d.BOnly != 1 {
		t.Errorf("discordant = A %d / B %d, want 1/1 (z is flaky for A, so not a win)", d.AOnly, d.BOnly)
	}
	if d.PValue != 1 {
		t.Errorf("p = %.3f, want 1.000 for a 1-1 split", d.PValue)
	}
}

func TestSignTestP(t *testing.T) {
	cases := []struct {
		x, y int
		want float64
	}{
		{0, 0, 1},      // nothing discordant
		{1, 1, 1},      // even split
		{5, 0, 0.0625}, // 2 * (1/32)
		{0, 5, 0.0625}, // symmetric
	}
	for _, c := range cases {
		got := signTestP(c.x, c.y)
		if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("signTestP(%d,%d) = %.6f, want %.6f", c.x, c.y, got, c.want)
		}
	}
}

func TestWriteStatsSaysWhenNotSeparated(t *testing.T) {
	// A leads on mean but both swing wildly: the report must say so rather than
	// let the ordering imply a winner.
	cr := mkRuns(nil, map[string][]map[string]bool{
		"A": {{"x": true, "y": true}, {"x": false, "y": false}},
		"B": {{"x": true, "y": false}, {"x": false, "y": false}},
	})
	var b strings.Builder
	cr.WriteStats(&b)
	out := b.String()
	if !strings.Contains(out, "not separated") {
		t.Errorf("expected the report to say the two are not separated:\n%s", out)
	}
	if !strings.Contains(out, "pass^k") {
		t.Errorf("expected pass^k in the table:\n%s", out)
	}
}
