package evalgo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
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
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields() // a misspelt "enviroment" must not run a target with no env
	if err := dec.Decode(&specs); err != nil {
		return nil, fmt.Errorf("parse targets JSON (want an array of {name,command,dir,env}): %w", err)
	}
	out := make([]NamedTarget, 0, len(specs))
	seen := map[string]bool{}
	for i, s := range specs {
		if s.Name == "" {
			return nil, fmt.Errorf("target %d: missing name", i)
		}
		if seen[s.Name] {
			return nil, fmt.Errorf("target %q: duplicate name (results are keyed by it)", s.Name)
		}
		seen[s.Name] = true
		if len(s.Command) == 0 {
			return nil, fmt.Errorf("target %q: missing command", s.Name)
		}
		out = append(out, NamedTarget{Name: s.Name, Target: ExecTarget{Command: s.Command, Dir: s.Dir, Env: s.Env}})
	}
	return out, nil
}

// LoadTasks reads a JSON array of Task (the agent-benchmark input set).
//
// Unknown fields are an error, not ignored: a task that says "expected_tool" or
// "chekc" would otherwise load with no ground truth and pass everything.
func LoadTasks(r io.Reader) ([]Task, error) {
	var tasks []Task
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tasks); err != nil {
		return nil, fmt.Errorf("parse tasks JSON (want an array of tasks): %w", err)
	}
	seen := map[string]bool{}
	for i, t := range tasks {
		if t.Name == "" {
			return nil, fmt.Errorf("task %d: missing name", i)
		}
		if seen[t.Name] {
			return nil, fmt.Errorf("task %q: duplicate name (results are keyed by it)", t.Name)
		}
		seen[t.Name] = true
		if strings.TrimSpace(t.Input) == "" {
			return nil, fmt.Errorf("task %q: empty input", t.Name)
		}
		if err := t.Check.Validate(); err != nil {
			return nil, fmt.Errorf("task %q: check: %w", t.Name, err)
		}
		if len(t.ToolScript) > 0 && !json.Valid(t.ToolScript) {
			return nil, fmt.Errorf("task %q: tool_script is not valid JSON", t.Name)
		}
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

	// Runs repeats the whole comparison this many times (default 1). An agent is
	// not deterministic: the same target, tasks, tools, model and judge can score
	// 68 on one pass and 51 on the next, because a five-step chain that drifts
	// once loses the whole task. A single pass therefore cannot rank agents whose
	// true scores are close — the ordering it produces is mostly noise.
	//
	// With Runs > 1 each pass is scored separately and Stats() reports the mean
	// and spread, so "A beat B" can be checked against how far either moves on
	// its own.
	Runs int
}

// TargetReport is one agent's scored results across all tasks.
type TargetReport struct {
	Name    string   `json:"name"`
	Run     int      `json:"run,omitempty"` // 1-based pass index when Comparison.Runs > 1
	Report  Report   `json:"report"`
	Samples []Sample `json:"samples"`
}

// ComparisonReport holds every agent's results plus the gate used to derive
// PASS/FAIL, and can render a task x agent grid.
//
// Targets carries one entry per target per run, so a repeated comparison keeps
// every pass rather than averaging them away: which task flipped between passes
// is the interesting part, and a mean alone hides it. Run indexes the pass.
type ComparisonReport struct {
	Targets []TargetReport `json:"targets"`
	Gate    []string       `json:"gate,omitempty"`
	Runs    int            `json:"runs,omitempty"`
}

// Run executes the comparison and returns the full report.
func (c Comparison) Run(ctx context.Context) ComparisonReport {
	runs := c.Runs
	if runs <= 0 {
		runs = 1
	}
	cr := ComparisonReport{Gate: c.Gate, Runs: runs}
	auto := IsAutoGate(c.Gate)
	available := map[string]bool{}
	for _, m := range c.Metrics {
		available[m.Name()] = true
	}
	byName := map[string]Task{}
	for _, t := range c.Tasks {
		byName[t.Name] = t
	}
	for run := 1; run <= runs; run++ {
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
			if auto {
				for i := range report.Samples {
					report.Samples[i].Gate = AutoGate(byName[report.Samples[i].Sample], available)
				}
			}
			cr.Targets = append(cr.Targets, TargetReport{
				Name: nt.Name, Run: run, Report: report, Samples: samples,
			})
		}
	}
	return cr
}

// TargetStats summarises one target across the runs of a repeated comparison.
//
// The three rates are the standard family for a non-deterministic agent, and
// they answer different questions:
//
//	Pass1  — mean success over all attempts. "How often does it work?"
//	PassK  — task succeeded in AT LEAST ONE run. "Can it do this at all?"
//	PassCaretK — task succeeded in EVERY run. "Can I rely on it?"
//
// The gap between PassK and PassCaretK is the flakiness: tasks it can do but
// does not do consistently. An agent picked on Pass1 alone can be one whose
// successes never repeat.
type TargetStats struct {
	Name       string    `json:"name"`
	Runs       int       `json:"runs"`
	Pass1      float64   `json:"pass_1"`
	PassK      float64   `json:"pass_k"`
	PassCaretK float64   `json:"pass_caret_k"`
	Min        float64   `json:"min"`
	Max        float64   `json:"max"`
	Each       []float64 `json:"each"` // per-run pass rate, in run order

	// CILow/CIHigh is a 95% interval for Pass1, computed over tasks (see stats.go).
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
	Tasks  int     `json:"tasks"`
	// Flaky lists the tasks solved in some runs but not all.
	Flaky []string `json:"flaky,omitempty"`
}

// Spread is how far the target moved between its best and worst run. Two agents
// whose means differ by less than either one's spread have not been separated.
func (t TargetStats) Spread() float64 { return t.Max - t.Min }

// outcomes is per-target, per-task pass/fail across runs.
func (cr ComparisonReport) outcomes() (map[string]map[string][]bool, []string) {
	by := map[string]map[string][]bool{}
	var order []string
	for _, tr := range cr.Targets {
		if _, seen := by[tr.Name]; !seen {
			by[tr.Name] = map[string][]bool{}
			order = append(order, tr.Name)
		}
		for _, sr := range tr.Report.Samples {
			by[tr.Name][sr.Sample] = append(by[tr.Name][sr.Sample], samplePassed(sr, cr.Gate))
		}
	}
	return by, order
}

// Stats aggregates each target across runs, best Pass1 first.
func (cr ComparisonReport) Stats() []TargetStats {
	by, order := cr.outcomes()
	out := make([]TargetStats, 0, len(order))

	for _, name := range order {
		tasks := by[name]
		if len(tasks) == 0 {
			continue
		}
		s := TargetStats{Name: name}
		runs := 0
		var attempts, wins int
		for _, res := range tasks {
			runs = max(runs, len(res))
			any, all := false, len(res) > 0
			for _, ok := range res {
				attempts++
				if ok {
					wins++
					any = true
				} else {
					all = false
				}
			}
			if any {
				s.PassK++
			}
			if all {
				s.PassCaretK++
			}
		}
		n := float64(len(tasks))
		s.Runs = runs
		s.Tasks = len(tasks)
		s.PassK /= n
		s.PassCaretK /= n
		if attempts > 0 {
			s.Pass1 = float64(wins) / float64(attempts)
		}
		rates, _ := cr.taskRates()
		var per []float64
		for _, tr := range rates[name] {
			per = append(per, tr.Rate)
			if tr.Flaky() {
				s.Flaky = append(s.Flaky, tr.Task)
			}
		}
		_, s.CILow, s.CIHigh = meanCI(per)

		// Per-run rates, for the spread.
		s.Each = make([]float64, runs)
		for r := range runs {
			passed, total := 0, 0
			for _, res := range tasks {
				if r < len(res) {
					total++
					if res[r] {
						passed++
					}
				}
			}
			if total > 0 {
				s.Each[r] = float64(passed) / float64(total)
			}
		}
		s.Min, s.Max = s.Each[0], s.Each[0]
		for _, v := range s.Each {
			s.Min, s.Max = min(s.Min, v), max(s.Max, v)
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pass1 > out[j].Pass1 })
	return out
}

// Discordance is a paired comparison of two targets on the tasks both attempted.
// A raw score gap says nothing about whether it would survive another run;
// pairing by task removes the "some tasks are just hard" variance that dominates
// an unpaired comparison of two small suites.
type Discordance struct {
	A      string  `json:"a"`
	B      string  `json:"b"`
	AOnly  int     `json:"a_only"` // A passed every run, B did not
	BOnly  int     `json:"b_only"`
	PValue float64 `json:"p_value"` // exact two-sided sign test on the discordant pairs
}

// Compare runs an exact McNemar-style sign test between two targets, using each
// task's consistent outcome (passed in every run) so a flaky success is not
// counted as a win.
func (cr ComparisonReport) Compare(a, b string) Discordance {
	by, _ := cr.outcomes()
	ta, tb := by[a], by[b]
	d := Discordance{A: a, B: b, PValue: 1}
	for task, ra := range ta {
		rb, ok := tb[task]
		if !ok || len(ra) == 0 || len(rb) == 0 {
			continue
		}
		allA, allB := true, true
		for _, v := range ra {
			allA = allA && v
		}
		for _, v := range rb {
			allB = allB && v
		}
		switch {
		case allA && !allB:
			d.AOnly++
		case allB && !allA:
			d.BOnly++
		}
	}
	d.PValue = signTestP(d.AOnly, d.BOnly)
	return d
}

// signTestP is the exact two-sided binomial test at p=0.5 on discordant pairs.
func signTestP(x, y int) float64 {
	n := x + y
	if n == 0 {
		return 1
	}
	k := min(x, y)
	// 2 * P(X <= k), clamped — the standard two-sided exact form.
	var tail float64
	for i := 0; i <= k; i++ {
		tail += choose(n, i)
	}
	p := 2 * tail / math.Pow(2, float64(n))
	return min(p, 1)
}

func choose(n, k int) float64 {
	if k < 0 || k > n {
		return 0
	}
	r := 1.0
	for i := range k {
		r = r * float64(n-i) / float64(i+1)
	}
	return r
}

// WriteStats prints pass@1 / pass@k / pass^k with the run-to-run spread, then
// says plainly whether the top two were actually separated. A leaderboard that
// prints only a mean invites a ranking the data does not support: published
// audits of repeated agent runs report spreads near 19 points on an unchanged
// configuration, which is wider than most leaderboard margins.
func (cr ComparisonReport) WriteStats(w io.Writer) {
	stats := cr.Stats()
	if len(stats) == 0 {
		return
	}
	fmt.Fprintf(w, "\n--- %d run(s) ---\n", cr.Runs)
	fmt.Fprintf(w, "%-20s %-9s %-15s %-9s %-9s %-9s %-7s %s\n",
		"agent", "pass@1", "95% CI", "pass@k", "pass^k", "spread", "flaky", "per run")
	for _, s := range stats {
		each := make([]string, len(s.Each))
		for i, v := range s.Each {
			each[i] = fmt.Sprintf("%.0f%%", v*100)
		}
		fmt.Fprintf(w, "%-20s %-9s %-15s %-9s %-9s %-9s %-7s %s\n",
			s.Name,
			fmt.Sprintf("%.1f%%", s.Pass1*100),
			fmt.Sprintf("[%.0f, %.0f]", s.CILow*100, s.CIHigh*100),
			fmt.Sprintf("%.0f%%", s.PassK*100),
			fmt.Sprintf("%.0f%%", s.PassCaretK*100),
			fmt.Sprintf("±%.0f", s.Spread()*100/2),
			fmt.Sprintf("%d/%d", len(s.Flaky), s.Tasks),
			strings.Join(each, " "))
	}

	if cr.Runs > 1 {
		fmt.Fprintln(w, "\npass@1 is the mean per-task pass rate; its interval is over tasks, not attempts.")
		fmt.Fprintln(w, "pass@k = solved at least once; pass^k = solved every run; flaky = solved in some runs only.")
		for _, s := range stats {
			if len(s.Flaky) > 0 {
				fmt.Fprintf(w, "  flaky %-14s %s\n", s.Name+":", strings.Join(s.Flaky, ", "))
			}
		}
	}

	if len(stats) > 1 {
		fmt.Fprintln(w, "\n--- paired comparison (per-task pass rates, sign-flip permutation test) ---")
		for i := 0; i < len(stats); i++ {
			for j := i + 1; j < len(stats); j++ {
				p, err := cr.Paired(stats[i].Name, stats[j].Name)
				if err != nil {
					continue
				}
				fmt.Fprintln(w, p.String())
			}
		}
		if cr.Runs < 2 {
			fmt.Fprintln(w, "One run per task: a flaky task looks like a win or a loss. Repeat with --runs 3 before ranking.")
		}
	}
}

// gateMatches reports whether a Result metric name satisfies a gate name: the
// same name, or a registry key whose metric reports under another name
// ("rubric" -> "rubric_judge"). Nothing fuzzier: a near-miss must fail
// ResolveGate up front, not match by accident.
func gateMatches(resultMetric, gate string) bool {
	return resultMetric == canonicalMetric(gate)
}

// samplePassed reports whether a SampleReport passes the gate: every gate metric
// present must have passed. An empty gate means every metric must pass; the
// auto gate uses the per-sample gate Comparison recorded.
func samplePassed(sr SampleReport, gate []string) bool {
	if IsAutoGate(gate) {
		gate = sr.Gate
		if len(gate) == 0 {
			return sr.Passed
		}
	}
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
	switch {
	case IsAutoGate(cr.Gate):
		fmt.Fprintln(w, "\ngate: auto (per task: rubric_judge if it has a rubric, tool_trajectory if it has a check, else task_completion)")
	case len(cr.Gate) > 0:
		fmt.Fprintf(w, "\ngate: %s (all must pass)\n", strings.Join(cr.Gate, ", "))
	}
}
