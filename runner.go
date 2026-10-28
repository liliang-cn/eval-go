package evalgo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// This file makes eval-go end-to-end: instead of only scoring Samples you bring,
// it can RUN the system under test to produce them. Define Tasks, plug in your
// agent as a Target, and a Runner/Bench drives every Task through it, captures
// the run (output, tool calls, trajectory) into a Sample, and scores it.
//
//	bench := evalgo.Bench{Target: myAgent, Tasks: tasks, Metrics: metrics}
//	report, samples := bench.Run(ctx)

// Task is one unit of agentic work plus the ground truth needed to grade the
// resulting run. It is the input side of an agent eval; the Target turns it into
// a RunOutput, and Task.Sample merges the two into a gradeable Sample.
type Task struct {
	Name          string            `json:"name"`
	Input         string            `json:"input"`                    // the prompt / goal handed to the agent
	ExpectedTools []string          `json:"expected_tools,omitempty"` // ground-truth tools (for tool_correctness)
	Rubric        string            `json:"rubric,omitempty"`         // pass/fail criterion (for the rubric judge)
	Expected      string            `json:"expected,omitempty"`       // optional reference answer
	Context       []string          `json:"context,omitempty"`        // evidence carried into the Sample if the run adds none
	Files         map[string]string `json:"files,omitempty"`          // optional seed fixtures (path -> content) for the target
	Meta          map[string]string `json:"meta,omitempty"`           // labels carried into the report

	// Turns are the user's follow-up messages, sent in order after Input in the
	// same conversation ("actually make it 8pm"). A target that ignores them
	// would be graded on a conversation that never happened, so the Runner
	// fails a multi-turn task whose RunOutput.Turns does not show every one.
	Turns []string `json:"turns,omitempty"`

	// ToolScript is opaque to eval-go: it is handed to the target verbatim (as
	// EVAL_TOOL_SCRIPT) so the tool environment can seed state and script
	// errors, timeouts or pagination for this one task.
	ToolScript json.RawMessage `json:"tool_script,omitempty"`

	// Check holds deterministic assertions on the run — which calls, with which
	// arguments, in which order, what must never be called, what the final
	// answer and tool state must show. Scored by the tool_trajectory metric.
	Check *ToolCheck `json:"check,omitempty"`

	// Gate overrides the auto gate for this task: the metric names that must
	// pass for it to count as solved. See AutoGate.
	Gate []string `json:"gate,omitempty"`
}

// RunOutput is what a Target reports after executing one Task: everything the
// metrics need to grade the run.
type RunOutput struct {
	Output     string     `json:"output"`               // the final answer the agent produced
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"` // tools invoked, in order
	Trajectory []string   `json:"trajectory,omitempty"` // ordered action/reasoning steps
	Plan       string     `json:"plan,omitempty"`       // the agent's stated plan, if any
	Context    []string   `json:"context,omitempty"`    // evidence the judge should see (e.g. final files)

	// Turns is the whole conversation of a multi-turn task, user and assistant
	// messages in order. Output stays the last assistant message.
	Turns []Turn `json:"turns,omitempty"`
	// State is the tool environment's final state (reminders, notes, ...), for
	// assertions on what the run actually left behind.
	State json.RawMessage `json:"state,omitempty"`
	// Usage is what the run cost: model calls, tool calls, tokens, time.
	Usage *RunUsage `json:"usage,omitempty"`
}

// RunUsage is the cost of one run as the target reports it, plus the wall time
// the harness measured around it. A benchmark that only says "passed" cannot
// tell a fast agent from one that took nine model calls to get there.
type RunUsage struct {
	ModelCalls       int   `json:"model_calls"`               // every model request, auxiliary ones included
	AuxModelCalls    int   `json:"aux_model_calls,omitempty"` // of which outside the main tool loop (compaction, extraction, routing...)
	ToolCalls        int   `json:"tool_calls"`
	PromptTokens     int   `json:"prompt_tokens,omitempty"`
	CompletionTokens int   `json:"completion_tokens,omitempty"`
	CachedTokens     int   `json:"cached_tokens,omitempty"`
	WallMs           int64 `json:"wall_ms"`            // measured by the harness around the whole target process
	ModelMs          int64 `json:"model_ms,omitempty"` // time spent inside model calls, as the target measured it
	// Calls is per-call detail when the target has it. An agent runtime that
	// emits a run summary (agent-go's run_end) can be copied here verbatim, so
	// a hidden auxiliary call shows up instead of being folded into a total.
	Calls []ModelCall `json:"calls,omitempty"`
	// Source names where the numbers came from ("adapter", "run_end", ...).
	Source string `json:"source,omitempty"`
}

// ModelCall is one model request inside a run.
type ModelCall struct {
	Kind             string `json:"kind,omitempty"` // "loop" for the tool loop, anything else is auxiliary
	Model            string `json:"model,omitempty"`
	Ms               int64  `json:"ms,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	CachedTokens     int    `json:"cached_tokens,omitempty"`
	ToolCalls        int    `json:"tool_calls,omitempty"` // tool calls the model asked for in this response
}

// Target is the system under test. It runs one Task and reports what happened.
// Implement it in-process for a Go agent, or use ExecTarget to drive any agent
// that runs as a subprocess (Python, Rust, a shell script, ...).
type Target interface {
	Run(ctx context.Context, task Task) (RunOutput, error)
}

// TargetFunc adapts a plain function into a Target.
type TargetFunc func(ctx context.Context, task Task) (RunOutput, error)

// Run implements Target.
func (f TargetFunc) Run(ctx context.Context, task Task) (RunOutput, error) { return f(ctx, task) }

// Sample merges a Task with the Target's RunOutput into a gradeable Sample.
func (t Task) Sample(out RunOutput) Sample {
	ctxChunks := out.Context
	if len(ctxChunks) == 0 {
		ctxChunks = t.Context
	}
	meta := map[string]string{}
	for k, v := range t.Meta {
		meta[k] = v
	}
	turns := out.Turns
	if len(turns) == 0 && len(t.Turns) == 0 {
		turns = nil
	}
	return Sample{
		Turns:         turns,
		State:         out.State,
		Usage:         out.Usage,
		Check:         t.Check,
		Name:          t.Name,
		Input:         t.Input,
		Output:        out.Output,
		Expected:      t.Expected,
		Context:       ctxChunks,
		Rubric:        t.Rubric,
		Meta:          meta,
		Plan:          out.Plan,
		Trajectory:    out.Trajectory,
		ToolCalls:     out.ToolCalls,
		ExpectedTools: t.ExpectedTools,
	}
}

// Runner drives a set of Tasks through a Target, concurrently, and returns one
// Sample per Task in input order. A Target error never aborts the batch: it
// becomes a Sample whose Output records the failure and whose Meta["run_error"]
// is set, so the metrics grade it as a failed run rather than silently dropping.
type Runner struct {
	Target      Target
	Concurrency int           // tasks run in parallel; default 4
	Timeout     time.Duration // per-task wall clock; 0 = no per-task timeout
	OnResult    func(task Task, s Sample, err error)
}

// Run executes every Task and returns the resulting Samples (input order).
func (r Runner) Run(ctx context.Context, tasks []Task) []Sample {
	conc := r.Concurrency
	if conc <= 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	samples := make([]Sample, len(tasks))

	for i, task := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, task Task) {
			defer wg.Done()
			defer func() { <-sem }()

			runCtx := ctx
			var cancel context.CancelFunc
			if r.Timeout > 0 {
				runCtx, cancel = context.WithTimeout(ctx, r.Timeout)
				defer cancel()
			}

			start := time.Now()
			out, err := r.Target.Run(runCtx, task)
			wall := time.Since(start).Milliseconds()
			if err == nil {
				err = checkTurns(task, out)
			}
			var s Sample
			if err != nil {
				s = task.Sample(RunOutput{Output: "RUN ERROR: " + err.Error()})
				if s.Meta == nil {
					s.Meta = map[string]string{}
				}
				s.Meta["run_error"] = err.Error()
			} else {
				s = task.Sample(out)
			}
			if s.Usage == nil {
				s.Usage = &RunUsage{ToolCalls: len(s.ToolCalls)}
			}
			s.Usage.WallMs = wall
			samples[i] = s
			if r.OnResult != nil {
				r.OnResult(task, s, err)
			}
		}(i, task)
	}
	wg.Wait()
	return samples
}

// checkTurns rejects a multi-turn run that did not go through every turn. An
// adapter written before multi-turn existed answers Input alone and exits; its
// output would otherwise be graded against a conversation it never had.
func checkTurns(task Task, out RunOutput) error {
	if len(task.Turns) == 0 {
		return nil
	}
	users := 0
	for _, t := range out.Turns {
		if t.Role == "user" {
			users++
		}
	}
	if want := 1 + len(task.Turns); users < want {
		return fmt.Errorf("multi-turn task has %d user turns but the target reported %d (does the adapter read EVAL_TURNS and return turns?)", want, users)
	}
	return nil
}

// Bench is the end-to-end agent-eval entrypoint: run every Task through Target
// to produce Samples, then score them with Metrics. Returns the scored Report
// and the Samples (so you can persist or inspect the raw runs).
type Bench struct {
	Target      Target
	Tasks       []Task
	Metrics     []Metric
	Concurrency int
	Timeout     time.Duration
	OnResult    func(task Task, s Sample, err error)
}

// Run produces Samples from Tasks via the Target, then scores them.
func (b Bench) Run(ctx context.Context) (Report, []Sample) {
	samples := Runner{
		Target:      b.Target,
		Concurrency: b.Concurrency,
		Timeout:     b.Timeout,
		OnResult:    b.OnResult,
	}.Run(ctx, b.Tasks)
	report := Suite{Samples: samples, Metrics: b.Metrics, Concurrency: b.Concurrency}.Run(ctx)
	return report, samples
}

// ExecTarget drives an agent that runs as a subprocess — so eval-go can evaluate
// an agent written in any language. The Task is passed both as JSON on stdin and
// as EVAL_* environment variables; the program prints a JSON RunOutput (or a
// bare Sample, or a one-element array of either) to stdout.
//
// Environment passed to the program:
//
//	EVAL_NAME, EVAL_INPUT, EVAL_RUBRIC, EVAL_EXPECTED, EVAL_EXPECTED_TOOLS (CSV),
//	EVAL_CONTEXT (JSON array), EVAL_FILES (JSON object of seed fixtures),
//	EVAL_TURNS (JSON array of follow-up user messages; "[]" when single-turn),
//	EVAL_TOOL_SCRIPT (the task's tool_script JSON, or empty), EVAL_META (JSON object)
//
// All of these are additions an older adapter can ignore, except EVAL_TURNS:
// a multi-turn task is failed unless the output's "turns" shows every user turn.
type ExecTarget struct {
	Command []string          // argv, e.g. ["python", "runner.py"] or ["./agent"]
	Dir     string            // working directory (optional)
	Env     map[string]string // extra environment (e.g. API keys); merged over os.Environ
}

// Run implements Target by executing the configured command.
func (e ExecTarget) Run(ctx context.Context, task Task) (RunOutput, error) {
	if len(e.Command) == 0 {
		return RunOutput{}, fmt.Errorf("ExecTarget: empty Command")
	}
	cmd := exec.CommandContext(ctx, e.Command[0], e.Command[1:]...)
	cmd.Dir = e.Dir

	env := os.Environ()
	add := func(k, v string) { env = append(env, k+"="+v) }
	add("EVAL_NAME", task.Name)
	add("EVAL_INPUT", task.Input)
	add("EVAL_RUBRIC", task.Rubric)
	add("EVAL_EXPECTED", task.Expected)
	add("EVAL_EXPECTED_TOOLS", strings.Join(task.ExpectedTools, ","))
	if b, err := json.Marshal(task.Context); err == nil {
		add("EVAL_CONTEXT", string(b))
	}
	if b, err := json.Marshal(task.Files); err == nil {
		add("EVAL_FILES", string(b))
	}
	turns := task.Turns
	if turns == nil {
		turns = []string{}
	}
	if b, err := json.Marshal(turns); err == nil {
		add("EVAL_TURNS", string(b))
	}
	add("EVAL_TOOL_SCRIPT", string(task.ToolScript))
	if b, err := json.Marshal(task.Meta); err == nil {
		add("EVAL_META", string(b))
	}
	for k, v := range e.Env {
		add(k, v)
	}
	cmd.Env = env

	if b, err := json.Marshal(task); err == nil {
		cmd.Stdin = strings.NewReader(string(b))
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 500 {
			tail = tail[len(tail)-500:]
		}
		return RunOutput{}, fmt.Errorf("%s: %v: %s", e.Command[0], err, tail)
	}
	return parseRunOutput(stdout.String())
}

// parseRunOutput extracts a RunOutput from a program's stdout. It accepts a
// RunOutput object, a bare Sample, or a one-element array of either — so a
// runner that already emits eval-go Samples works unchanged.
func parseRunOutput(out string) (RunOutput, error) {
	text := strings.TrimSpace(out)
	if text == "" {
		return RunOutput{}, fmt.Errorf("target produced no output")
	}
	// Unwrap a one-element JSON array.
	if strings.HasPrefix(text, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal([]byte(text), &arr); err == nil && len(arr) > 0 {
			text = string(arr[0])
		}
	}
	// A RunOutput and a Sample share the run fields, so one struct decodes both.
	var probe struct {
		Output     string          `json:"output"`
		ToolCalls  []ToolCall      `json:"tool_calls"`
		Trajectory []string        `json:"trajectory"`
		Plan       string          `json:"plan"`
		Context    []string        `json:"context"`
		Turns      []Turn          `json:"turns"`
		State      json.RawMessage `json:"state"`
		Usage      *RunUsage       `json:"usage"`
	}
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return RunOutput{}, fmt.Errorf("parse target output as JSON RunOutput/Sample: %w", err)
	}
	return RunOutput{
		Output:     probe.Output,
		ToolCalls:  probe.ToolCalls,
		Trajectory: probe.Trajectory,
		Plan:       probe.Plan,
		Context:    probe.Context,
		Turns:      probe.Turns,
		State:      probe.State,
		Usage:      probe.Usage,
	}, nil
}
