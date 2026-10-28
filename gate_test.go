package evalgo

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func fixedJudge(passed bool) JudgeFunc {
	return func(context.Context, string) (string, error) {
		if passed {
			return `{"passed":true,"score":1,"reason":"ok"}`, nil
		}
		return `{"passed":false,"score":0,"reason":"no"}`, nil
	}
}

func TestResolveGateRejectsUnknownNames(t *testing.T) {
	metrics, err := BuildMetrics([]string{"tool_correctness", "rubric"}, MetricSpec{Judge: fixedJudge(true)})
	if err != nil {
		t.Fatal(err)
	}
	g, err := ResolveGate([]string{"rubric", "tool_correctness"}, metrics, nil)
	if err != nil || g[0] != "rubric_judge" {
		t.Fatalf("registry key should resolve to the reported name: %v %v", g, err)
	}
	_, err = ResolveGate([]string{"task_completion"}, metrics, nil)
	if err == nil || !strings.Contains(err.Error(), "rubric_judge") {
		t.Fatalf("a gate on an unscored metric must error and list the scored ones: %v", err)
	}
	if _, err := ResolveGate([]string{"rubrik"}, metrics, nil); err == nil {
		t.Fatal("a typo in the gate must be an error")
	}
	if _, err := BuildMetrics([]string{"rubrik"}, MetricSpec{}); err == nil {
		t.Fatal("a typo in -m must be an error")
	}
	if _, err := BuildMetrics([]string{"rubric", "rubric_judge"}, MetricSpec{Judge: fixedJudge(true)}); err == nil {
		t.Fatal("the same metric twice must be an error")
	}
}

func TestAutoGateNeedsTheMetricsItGatesOn(t *testing.T) {
	tasks := []Task{{Name: "a", Input: "x", Rubric: "must refuse"}}
	metrics, _ := BuildMetrics([]string{"task_completion"}, MetricSpec{Judge: fixedJudge(true)})
	if _, err := ResolveGate([]string{"auto"}, metrics, tasks); err == nil {
		t.Fatal("auto gate with rubric tasks but no rubric_judge must error")
	}
	tasks[0].Gate = []string{"nonsense"}
	metrics, _ = BuildMetrics([]string{"rubric_judge"}, MetricSpec{Judge: fixedJudge(true)})
	if _, err := ResolveGate([]string{"auto"}, metrics, tasks); err == nil {
		t.Fatal("a per-task gate naming an unscored metric must error")
	}
}

// A correct refusal fails task_completion ("the pizza was not ordered") but
// passes its rubric. Under the auto gate the rubric decides.
func TestAutoGateRefusalGatedByRubric(t *testing.T) {
	rubricPass := MetricFunc{"rubric_judge", func(context.Context, Sample) (Result, error) {
		return Result{Metric: "rubric_judge", Passed: true, Score: 1}, nil
	}}
	tcFail := MetricFunc{"task_completion", func(context.Context, Sample) (Result, error) {
		return Result{Metric: "task_completion", Passed: false}, nil
	}}
	refuser := TargetFunc(func(context.Context, Task) (RunOutput, error) {
		return RunOutput{Output: "I can't order food: there is no ordering tool."}, nil
	})
	tasks := []Task{
		{Name: "pizza", Input: "Order a pizza", Rubric: "must refuse, call no tool"},
		{Name: "plain", Input: "Do the thing"}, // no rubric: task_completion gates
		{Name: "optin", Input: "Do it", Rubric: "x", Gate: []string{"rubric", "task_completion"}},
	}
	metrics := []Metric{tcFail, rubricPass}
	gate, err := ResolveGate([]string{"auto"}, metrics, tasks)
	if err != nil {
		t.Fatal(err)
	}
	rep := Comparison{Targets: []NamedTarget{{"m", refuser}}, Tasks: tasks, Metrics: metrics, Gate: gate}.Run(context.Background())
	grid := rep.Grid()
	if !grid["pizza"]["m"] {
		t.Error("a correct refusal must pass under the auto gate")
	}
	if grid["plain"]["m"] {
		t.Error("a task without rubric is gated by task_completion")
	}
	if grid["optin"]["m"] {
		t.Error("a task that opts into task_completion is gated by it")
	}
}

func TestRunnerFailsMultiTurnTaskTheTargetIgnored(t *testing.T) {
	single := TargetFunc(func(context.Context, Task) (RunOutput, error) {
		return RunOutput{Output: "done"}, nil
	})
	task := Task{Name: "mt", Input: "remind me at 7", Turns: []string{"actually 8"}}
	s := Runner{Target: single}.Run(context.Background(), []Task{task})[0]
	if s.Meta["run_error"] == "" || !strings.Contains(s.Meta["run_error"], "EVAL_TURNS") {
		t.Fatalf("an adapter that ignored the follow-up turn must be a run error: %+v", s.Meta)
	}
	multi := TargetFunc(func(_ context.Context, tk Task) (RunOutput, error) {
		return RunOutput{Output: "moved to 8", Turns: []Turn{
			{"user", tk.Input}, {"assistant", "set for 7"}, {"user", tk.Turns[0]}, {"assistant", "moved to 8"},
		}}, nil
	})
	s = Runner{Target: multi}.Run(context.Background(), []Task{task})[0]
	if s.Meta["run_error"] != "" || len(s.Turns) != 4 || s.Usage == nil {
		t.Fatalf("multi-turn run should carry turns and usage: %+v", s)
	}
}

func TestExecTargetPassesTurnsAndScript(t *testing.T) {
	task := Task{Name: "t", Input: "hi", Turns: []string{"more"}, ToolScript: json.RawMessage(`{"profile":"life"}`)}
	script := `[ "$EVAL_TURNS" = '["more"]' ] && [ "$EVAL_TOOL_SCRIPT" = '{"profile":"life"}' ] && ` +
		`echo '{"output":"ok","usage":{"model_calls":3,"tool_calls":1}}'`
	out, err := ExecTarget{Command: []string{"sh", "-c", script}}.Run(context.Background(), task)
	if err != nil || out.Output != "ok" {
		t.Fatalf("EVAL_TURNS / EVAL_TOOL_SCRIPT not passed: %v %q", err, out.Output)
	}
	if out.Usage == nil || out.Usage.ModelCalls != 3 {
		t.Fatalf("usage not parsed: %+v", out.Usage)
	}
}

func TestStatsCIAndPaired(t *testing.T) {
	// 20 tasks; "a" solves all, "b" solves half: a clear, significant gap.
	outA, outB := map[string]bool{}, map[string]bool{}
	for i := range 20 {
		name := string(rune('a'+i)) + "task"
		outA[name] = true
		outB[name] = i%2 == 0
	}
	cr := mkRuns([]string{"task_completion"}, map[string][]map[string]bool{
		"a": {outA, outA, outA},
		"b": {outB, outB, outB},
	})
	st := map[string]TargetStats{}
	for _, s := range cr.Stats() {
		st[s.Name] = s
	}
	if b := st["b"]; math.Abs(b.Pass1-0.5) > 1e-9 || b.CILow >= 0.5 || b.CIHigh <= 0.5 || b.CILow < 0.2 {
		t.Fatalf("b's CI should bracket 0.5: %+v", b)
	}
	if a := st["a"]; a.CIHigh != 1 || a.CILow >= 1 || a.CILow < 0.7 {
		t.Fatalf("a perfect score still gets a Wilson lower bound below 1: %+v", a)
	}
	p, err := cr.Paired("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Significant || p.AWins != 10 || p.BWins != 0 || math.Abs(p.MeanDiff-0.5) > 1e-9 {
		t.Fatalf("10 one-sided wins of 20 should be significant: %+v", p)
	}
	if _, err := cr.Paired("a", "nobody"); err == nil {
		t.Fatal("an unknown target name must be an error")
	}

	// Flaky: c solves task x in one run of three.
	cr = mkRuns([]string{"task_completion"}, map[string][]map[string]bool{
		"c": {{"x": true, "y": true}, {"x": false, "y": true}, {"x": false, "y": true}},
		"d": {{"x": true, "y": true}, {"x": true, "y": true}, {"x": true, "y": true}},
	})
	for _, s := range cr.Stats() {
		if s.Name == "c" && (len(s.Flaky) != 1 || s.Flaky[0] != "x") {
			t.Fatalf("x is flaky for c: %+v", s)
		}
	}
	p, _ = cr.Paired("d", "c")
	if p.Significant {
		t.Fatalf("one partially-differing task cannot be significant: %+v", p)
	}
	var sb strings.Builder
	cr.WriteStats(&sb)
	if !strings.Contains(sb.String(), "flaky c:") || !strings.Contains(sb.String(), "not separated") {
		t.Fatalf("stats output missing flaky/paired lines:\n%s", sb.String())
	}
}

func TestLoadTasksStrict(t *testing.T) {
	if _, err := LoadTasks(strings.NewReader(`[{"name":"a","input":"x","expected_tool":["y"]}]`)); err == nil {
		t.Fatal("a misspelt field must be an error, not silently ignored")
	}
	if _, err := LoadTasks(strings.NewReader(`[{"name":"a","input":"x"},{"name":"a","input":"y"}]`)); err == nil {
		t.Fatal("duplicate task names must be an error")
	}
	if _, err := LoadTasks(strings.NewReader(`[{"name":"a","input":"x","check":{"calls":[{"tool":"t","after":["nope"]}]}}]`)); err == nil {
		t.Fatal("a check with a dangling reference must be an error")
	}
	if _, err := LoadTargets(strings.NewReader(`[{"name":"a","command":["x"],"enviroment":{}}]`)); err == nil {
		t.Fatal("a misspelt target field must be an error")
	}
}
