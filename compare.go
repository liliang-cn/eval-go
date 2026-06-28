package evalgo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// This file generalizes a cross-agent benchmark: run the SAME tasks through
// SEVERAL agents (Targets) and score each run, so you can compare frameworks or
// model configs side by side and render a task x agent PASS/FAIL grid. Agents
// and tasks can be declared in JSON files and run with `evalgo bench`.

// NamedTarget pairs a label with a Target so a Comparison can attribute results.
type NamedTarget struct {
	Name   string
	Target Target
}

// ExecTargetSpec is the JSON-config form of an ExecTarget, so the agents under
// comparison can be declared in a file rather than in Go.
type ExecTargetSpec struct {
	Name    string            `json:"name"`
	Command []string          `json:"command"`
	Dir     string            `json:"dir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// LoadTargets reads a JSON array of ExecTargetSpec and builds NamedTargets,
// each backed by an ExecTarget.
func LoadTargets(r io.Reader) ([]NamedTarget, error) {
	var specs []ExecTargetSpec
	if err := json.NewDecoder(r).Decode(&specs); err != nil {
		return nil, fmt.Errorf("parse targets JSON (want an array of {name,command,dir,env}): %w", err)
	}
	out := make([]NamedTarget, 0, len(specs))
	for i, s := range specs {
		if s.Name == "" {
			return nil, fmt.Errorf("target %d: missing name", i)
		}
		if len(s.Command) == 0 {
			return nil, fmt.Errorf("target %q: missing command", s.Name)
		}
		out = append(out, NamedTarget{Name: s.Name, Target: ExecTarget{Command: s.Command, Dir: s.Dir, Env: s.Env}})
	}
	return out, nil
}

// LoadTasks reads a JSON array of Task (the agent-benchmark input set).
func LoadTasks(r io.Reader) ([]Task, error) {
	var tasks []Task
	if err := json.NewDecoder(r).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("parse tasks JSON (want an array of tasks): %w", err)
	}
	return tasks, nil
}

// Comparison runs every Task through every Target and scores each run, so agents
// can be compared on identical tasks. Targets run sequentially (one agent at a
// time); a target's tasks run with Concurrency.
type Comparison struct {
	Targets     []NamedTarget
	Tasks       []Task
	Metrics     []Metric
	Gate        []string // metric names that must pass for a cell to PASS; empty = all metrics
	Concurrency int
	Timeout     time.Duration
	OnResult    func(target string, task Task, s Sample, err error)
}

// TargetReport is one agent's scored results across all tasks.
type TargetReport struct {
	Name    string   `json:"name"`
	Report  Report   `json:"report"`
	Samples []Sample `json:"samples"`
}

// ComparisonReport holds every agent's results plus the gate used to derive
// PASS/FAIL, and can render a task x agent grid.
type ComparisonReport struct {
	Targets []TargetReport `json:"targets"`
	Gate    []string       `json:"gate,omitempty"`
}

// Run executes the comparison and returns the full report.
func (c Comparison) Run(ctx context.Context) ComparisonReport {
	cr := ComparisonReport{Gate: c.Gate}
	for _, nt := range c.Targets {
		var onResult func(Task, Sample, error)
		if c.OnResult != nil {
			name := nt.Name
			onResult = func(t Task, s Sample, err error) { c.OnResult(name, t, s, err) }
		}
		bench := Bench{
			Target:      nt.Target,
			Tasks:       c.Tasks,
			Metrics:     c.Metrics,
			Concurrency: c.Concurrency,
			Timeout:     c.Timeout,
			OnResult:    onResult,
		}
		report, samples := bench.Run(ctx)
		cr.Targets = append(cr.Targets, TargetReport{Name: nt.Name, Report: report, Samples: samples})
	}
	return cr
}

// gateMatches reports whether a Result metric name satisfies a gate name. It is
// lenient so a gate written as the registry key matches the metric's reported
// name: "rubric" matches "rubric_judge", and "nonempty" matches "non_empty".
func gateMatches(resultMetric, gate string) bool {
	if resultMetric == gate || resultMetric == gate+"_judge" {
		return true
	}
	norm := func(s string) string { return strings.ReplaceAll(s, "_", "") }
	return norm(resultMetric) == norm(gate)
}

// samplePassed reports whether a SampleReport passes the gate: every gate metric
// present must have passed. An empty gate means every metric must pass.
func samplePassed(sr SampleReport, gate []string) bool {
	if len(gate) == 0 {
		return sr.Passed
	}
	for _, g := range gate {
		found := false
		for _, res := range sr.Results {
			if gateMatches(res.Metric, g) {
				found = true
				if !res.Passed {
					return false
				}
			}
		}
		if !found {
			return false // a required gate metric was not scored
		}
	}
	return true
}

// Grid returns passed[taskName][targetName] derived from the gate.
func (cr ComparisonReport) Grid() map[string]map[string]bool {
	grid := map[string]map[string]bool{}
	for _, tr := range cr.Targets {
		for _, sr := range tr.Report.Samples {
			if grid[sr.Sample] == nil {
				grid[sr.Sample] = map[string]bool{}
			}
			grid[sr.Sample][tr.Name] = samplePassed(sr, cr.Gate)
		}
	}
	return grid
}

// taskOrder returns task names in first-seen order across targets.
func (cr ComparisonReport) taskOrder() []string {
	seen := map[string]bool{}
	var order []string
	for _, tr := range cr.Targets {
		for _, sr := range tr.Report.Samples {
			if !seen[sr.Sample] {
				seen[sr.Sample] = true
				order = append(order, sr.Sample)
			}
		}
	}
	return order
}

// RenderGrid writes a task x agent PASS/FAIL table to w.
func (cr ComparisonReport) RenderGrid(w io.Writer) {
	grid := cr.Grid()
	tasks := cr.taskOrder()
	sort.Strings(tasks)

	const col = 14
	fmt.Fprintf(w, "%-22s", "task")
	for _, tr := range cr.Targets {
		fmt.Fprintf(w, "%-*s", col, tr.Name)
	}
	fmt.Fprintln(w)
	for _, task := range tasks {
		fmt.Fprintf(w, "%-22s", task)
		for _, tr := range cr.Targets {
			mark := "FAIL"
			if grid[task][tr.Name] {
				mark = "PASS"
			}
			fmt.Fprintf(w, "%-*s", col, mark)
		}
		fmt.Fprintln(w)
	}
	if len(cr.Gate) > 0 {
		fmt.Fprintf(w, "\ngate: %s (all must pass)\n", strings.Join(cr.Gate, ", "))
	}
}
