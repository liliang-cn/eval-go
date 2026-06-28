package evalgo

import (
	"context"
	"strings"
	"testing"
)

func TestLoadTasksAndTargets(t *testing.T) {
	tasks, err := LoadTasks(strings.NewReader(`[
		{"name":"t1","input":"do x","expected_tools":["write_file"],"rubric":"must x","files":{"a.txt":"1"}}
	]`))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("LoadTasks: %v %d", err, len(tasks))
	}
	if tasks[0].Files["a.txt"] != "1" || tasks[0].ExpectedTools[0] != "write_file" {
		t.Fatalf("task not parsed: %+v", tasks[0])
	}

	targets, err := LoadTargets(strings.NewReader(`[
		{"name":"agent-a","command":["sh","-c","echo"],"env":{"K":"V"}},
		{"name":"agent-b","command":["./b"],"dir":"/tmp"}
	]`))
	if err != nil || len(targets) != 2 {
		t.Fatalf("LoadTargets: %v %d", err, len(targets))
	}
	if targets[0].Name != "agent-a" {
		t.Fatalf("target name: %+v", targets[0])
	}
	if _, ok := targets[1].Target.(ExecTarget); !ok {
		t.Fatalf("target not an ExecTarget: %T", targets[1].Target)
	}
}

func TestLoadTargetsValidates(t *testing.T) {
	if _, err := LoadTargets(strings.NewReader(`[{"command":["x"]}]`)); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := LoadTargets(strings.NewReader(`[{"name":"x"}]`)); err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestComparisonGrid(t *testing.T) {
	// Two agents: "good" passes both tasks; "weak" fails the second.
	good := TargetFunc(func(_ context.Context, task Task) (RunOutput, error) {
		return RunOutput{Output: "answer", ToolCalls: []ToolCall{{Name: "write_file"}}}, nil
	})
	weak := TargetFunc(func(_ context.Context, task Task) (RunOutput, error) {
		if task.Name == "t2" {
			return RunOutput{Output: ""}, nil // empty -> fails nonempty gate
		}
		return RunOutput{Output: "answer", ToolCalls: []ToolCall{{Name: "write_file"}}}, nil
	})

	cmp := Comparison{
		Targets: []NamedTarget{{Name: "good", Target: good}, {Name: "weak", Target: weak}},
		Tasks:   []Task{{Name: "t1", ExpectedTools: []string{"write_file"}}, {Name: "t2", ExpectedTools: []string{"write_file"}}},
		Metrics: []Metric{NonEmpty(), ToolCorrectness()},
		Gate:    []string{"nonempty"},
	}
	rep := cmp.Run(context.Background())
	grid := rep.Grid()

	if !grid["t1"]["good"] || !grid["t2"]["good"] {
		t.Fatalf("good should pass both: %+v", grid)
	}
	if !grid["t1"]["weak"] || grid["t2"]["weak"] {
		t.Fatalf("weak should pass t1, fail t2: %+v", grid)
	}

	var sb strings.Builder
	rep.RenderGrid(&sb)
	out := sb.String()
	if !strings.Contains(out, "good") || !strings.Contains(out, "weak") || !strings.Contains(out, "PASS") {
		t.Fatalf("grid render missing content:\n%s", out)
	}
}

func TestSamplePassedGateLenientRubricName(t *testing.T) {
	// gate "rubric" must match a result metric named "rubric_judge".
	sr := SampleReport{
		Sample:  "t",
		Results: []Result{{Metric: "rubric_judge", Passed: true}, {Metric: "task_completion", Passed: true}},
	}
	if !samplePassed(sr, []string{"rubric", "task_completion"}) {
		t.Fatal("gate 'rubric' should match 'rubric_judge'")
	}
	sr.Results[0].Passed = false
	if samplePassed(sr, []string{"rubric"}) {
		t.Fatal("failing rubric_judge should fail the rubric gate")
	}
}
