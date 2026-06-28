package evalgo

import (
	"context"
	"errors"
	"testing"
)

func TestTaskSampleMerge(t *testing.T) {
	task := Task{
		Name:          "t1",
		Input:         "do it",
		ExpectedTools: []string{"write_file"},
		Rubric:        "must write",
		Expected:      "ref",
		Context:       []string{"seed"},
		Meta:          map[string]string{"framework": "x"},
	}
	s := task.Sample(RunOutput{
		Output:     "done",
		ToolCalls:  []ToolCall{{Name: "write_file"}},
		Trajectory: []string{"call write_file"},
		Plan:       "p",
	})
	if s.Name != "t1" || s.Input != "do it" || s.Output != "done" {
		t.Fatalf("core fields not merged: %+v", s)
	}
	if s.Rubric != "must write" || s.Expected != "ref" || len(s.ExpectedTools) != 1 {
		t.Fatalf("ground-truth fields not carried: %+v", s)
	}
	if len(s.ToolCalls) != 1 || s.Plan != "p" || s.Meta["framework"] != "x" {
		t.Fatalf("run fields not carried: %+v", s)
	}
	// RunOutput.Context absent -> falls back to Task.Context.
	if len(s.Context) != 1 || s.Context[0] != "seed" {
		t.Fatalf("context fallback failed: %+v", s.Context)
	}
}

func TestRunnerCapturesErrorAsFailedSample(t *testing.T) {
	target := TargetFunc(func(_ context.Context, task Task) (RunOutput, error) {
		if task.Name == "boom" {
			return RunOutput{}, errors.New("kaboom")
		}
		return RunOutput{Output: "ok:" + task.Input}, nil
	})
	tasks := []Task{{Name: "good", Input: "hi"}, {Name: "boom", Input: "x"}}
	samples := Runner{Target: target}.Run(context.Background(), tasks)

	if len(samples) != 2 {
		t.Fatalf("want 2 samples, got %d", len(samples))
	}
	if samples[0].Output != "ok:hi" { // input order preserved
		t.Fatalf("sample 0 wrong: %+v", samples[0])
	}
	if samples[1].Meta["run_error"] != "kaboom" {
		t.Fatalf("error not captured into meta: %+v", samples[1].Meta)
	}
	if samples[1].Output == "" || samples[1].Output[:9] != "RUN ERROR" {
		t.Fatalf("error not recorded in output: %q", samples[1].Output)
	}
}

func TestBenchEndToEnd(t *testing.T) {
	// A deterministic metric: pass iff the output is non-empty (no LLM needed).
	nonEmpty := MetricFunc{
		MetricName: "nonempty",
		Fn: func(_ context.Context, s Sample) (Result, error) {
			return pass("nonempty", s.Output != "", "checked output"), nil
		},
	}
	target := TargetFunc(func(_ context.Context, task Task) (RunOutput, error) {
		if task.Name == "empty" {
			return RunOutput{Output: ""}, nil
		}
		return RunOutput{Output: "answer"}, nil
	})
	bench := Bench{
		Target:  target,
		Tasks:   []Task{{Name: "ok"}, {Name: "empty"}},
		Metrics: []Metric{nonEmpty},
	}
	report, samples := bench.Run(context.Background())
	if len(samples) != 2 || len(report.Samples) != 2 {
		t.Fatalf("want 2 samples/results, got %d/%d", len(samples), len(report.Samples))
	}
	passed := map[string]bool{}
	for _, sr := range report.Samples {
		passed[sr.Sample] = sr.Passed
	}
	if !passed["ok"] || passed["empty"] {
		t.Fatalf("end-to-end pass/fail wrong: %+v", passed)
	}
}

func TestExecTargetDrivesSubprocess(t *testing.T) {
	// A subprocess that echoes a RunOutput JSON built from the EVAL_* env.
	script := `printf '{"output":"ran %s","tool_calls":[{"name":"write_file"}],"trajectory":["call write_file"]}' "$EVAL_INPUT"`
	target := ExecTarget{Command: []string{"sh", "-c", script}}
	out, err := target.Run(context.Background(), Task{Name: "t", Input: "hello"})
	if err != nil {
		t.Fatalf("exec target: %v", err)
	}
	if out.Output != "ran hello" {
		t.Fatalf("output not parsed from subprocess: %q", out.Output)
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Name != "write_file" {
		t.Fatalf("tool calls not parsed: %+v", out.ToolCalls)
	}
}

func TestExecTargetAcceptsSampleArray(t *testing.T) {
	// Back-compat: a runner that emits a one-element array of Samples works too.
	script := `printf '[{"name":"t","output":"hi","tool_calls":[],"context":["files"]}]'`
	target := ExecTarget{Command: []string{"sh", "-c", script}}
	out, err := target.Run(context.Background(), Task{Name: "t"})
	if err != nil {
		t.Fatalf("exec target: %v", err)
	}
	if out.Output != "hi" || len(out.Context) != 1 || out.Context[0] != "files" {
		t.Fatalf("sample-array form not parsed: %+v", out)
	}
}

func TestExecTargetReportsFailure(t *testing.T) {
	target := ExecTarget{Command: []string{"sh", "-c", "echo oops >&2; exit 3"}}
	_, err := target.Run(context.Background(), Task{Name: "t"})
	if err == nil {
		t.Fatal("expected error from failing subprocess")
	}
}
