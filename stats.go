package evalgo

import (
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
)

// This file turns a repeated comparison into numbers that say how sure they
// are: pass@1 with a confidence interval, which tasks are flaky, and a paired
// test of whether one target really beats another.
//
// The unit of evidence is the TASK, not the attempt. Three runs of one task are
// not three independent observations of the agent: they share the task's
// difficulty. So pass@1 is the mean of per-task pass rates and its interval is
// computed over tasks (a cluster-robust standard error), and the paired test
// permutes per-task differences. Treating 45 tasks x 3 runs as 135 coin flips
// would make every gap look significant.

// TaskRate is one target's outcome on one task across runs.
type TaskRate struct {
	Task   string  `json:"task"`
	Passed int     `json:"passed"`
	Runs   int     `json:"runs"`
	Rate   float64 `json:"rate"`
}

// Flaky reports a task the target solved in some runs but not all.
func (t TaskRate) Flaky() bool { return t.Passed > 0 && t.Passed < t.Runs }

// taskRates is per-target per-task pass rates, tasks in first-seen order.
func (cr ComparisonReport) taskRates() (map[string][]TaskRate, []string) {
	by, order := cr.outcomes()
	tasks := cr.taskOrder()
	out := map[string][]TaskRate{}
	for _, name := range order {
		for _, task := range tasks {
			res, ok := by[name][task]
			if !ok || len(res) == 0 {
				continue
			}
			tr := TaskRate{Task: task, Runs: len(res)}
			for _, v := range res {
				if v {
					tr.Passed++
				}
			}
			tr.Rate = float64(tr.Passed) / float64(tr.Runs)
			out[name] = append(out[name], tr)
		}
	}
	return out, order
}

// meanCI returns the mean of xs and a 95% interval for it, clamped to [0,1].
// With n<2 the interval is the whole range: one observation bounds nothing.
func meanCI(xs []float64) (mean, lo, hi float64) {
	n := float64(len(xs))
	if n == 0 {
		return 0, 0, 1
	}
	for _, x := range xs {
		mean += x
	}
	mean /= n
	if n < 2 {
		return mean, 0, 1
	}
	var ss float64
	for _, x := range xs {
		ss += (x - mean) * (x - mean)
	}
	se := math.Sqrt(ss / (n - 1) / n)
	t := tCrit(int(n) - 1)
	lo, hi = mean-t*se, mean+t*se
	// A target that passed (or failed) every task has zero variance; the
	// normal interval collapses to a point it has not earned. Fall back to the
	// Wilson interval on the task count, which never claims more than n tasks
	// can show.
	if se == 0 {
		lo, hi = wilson(mean, n)
	}
	return mean, math.Max(0, lo), math.Min(1, hi)
}

// wilson is the 95% Wilson score interval for a proportion p over n trials.
func wilson(p, n float64) (float64, float64) {
	const z = 1.96
	den := 1 + z*z/n
	c := (p + z*z/(2*n)) / den
	h := z * math.Sqrt(p*(1-p)/n+z*z/(4*n*n)) / den
	return c - h, c + h
}

// tCrit is the two-sided 95% Student t critical value for df degrees of freedom.
func tCrit(df int) float64 {
	table := []float64{0, 12.706, 4.303, 3.182, 2.776, 2.571, 2.447, 2.365, 2.306, 2.262, 2.228,
		2.201, 2.179, 2.160, 2.145, 2.131, 2.120, 2.110, 2.101, 2.093, 2.086,
		2.080, 2.074, 2.069, 2.064, 2.060, 2.056, 2.052, 2.048, 2.045, 2.042}
	switch {
	case df <= 0:
		return math.Inf(1)
	case df < len(table):
		return table[df]
	case df < 60:
		return 2.00
	default:
		return 1.96
	}
}

// PairedComparison is a paired test of two targets over the tasks both ran.
type PairedComparison struct {
	A        string  `json:"a"`
	B        string  `json:"b"`
	Tasks    int     `json:"tasks"`     // tasks both attempted
	MeanDiff float64 `json:"mean_diff"` // mean per-task pass-rate difference, A - B
	CILow    float64 `json:"ci_low"`    // 95% interval for MeanDiff
	CIHigh   float64 `json:"ci_high"`
	AWins    int     `json:"a_wins"` // tasks where A's pass rate is higher
	BWins    int     `json:"b_wins"`
	// PValue is a two-sided paired sign-flip permutation test on the per-task
	// differences: exact for up to 20 differing tasks, else 200k seeded draws.
	PValue      float64 `json:"p_value"`
	Significant bool    `json:"significant"` // p < 0.05
}

// String is the one-line verdict WriteStats prints.
func (p PairedComparison) String() string {
	verdict := "not separated"
	if p.Significant {
		if p.MeanDiff > 0 {
			verdict = p.A + " is better (p<0.05)"
		} else {
			verdict = p.B + " is better (p<0.05)"
		}
	}
	return fmt.Sprintf("%s vs %s: %+.1f points [%+.1f, %+.1f] over %d tasks; wins %d-%d; p=%.3f — %s",
		p.A, p.B, p.MeanDiff*100, p.CILow*100, p.CIHigh*100, p.Tasks, p.AWins, p.BWins, p.PValue, verdict)
}

// Paired compares targets a and b task by task. It errors on a name that is
// not in the report rather than comparing against nothing.
func (cr ComparisonReport) Paired(a, b string) (PairedComparison, error) {
	rates, order := cr.taskRates()
	for _, n := range []string{a, b} {
		if _, ok := rates[n]; !ok {
			return PairedComparison{}, fmt.Errorf("no target %q in the report (have: %s)", n, strings.Join(order, ", "))
		}
	}
	rb := map[string]float64{}
	for _, t := range rates[b] {
		rb[t.Task] = t.Rate
	}
	p := PairedComparison{A: a, B: b}
	var diffs []float64
	for _, t := range rates[a] {
		vb, ok := rb[t.Task]
		if !ok {
			continue
		}
		d := t.Rate - vb
		diffs = append(diffs, d)
		switch {
		case d > 0:
			p.AWins++
		case d < 0:
			p.BWins++
		}
	}
	p.Tasks = len(diffs)
	p.MeanDiff, p.CILow, p.CIHigh = meanDiffCI(diffs)
	p.PValue = signFlipP(diffs)
	p.Significant = p.PValue < 0.05
	return p, nil
}

func meanDiffCI(d []float64) (mean, lo, hi float64) {
	n := float64(len(d))
	if n == 0 {
		return 0, -1, 1
	}
	for _, x := range d {
		mean += x
	}
	mean /= n
	if n < 2 {
		return mean, -1, 1
	}
	var ss float64
	for _, x := range d {
		ss += (x - mean) * (x - mean)
	}
	se := math.Sqrt(ss / (n - 1) / n)
	t := tCrit(int(n) - 1)
	return mean, math.Max(-1, mean-t*se), math.Min(1, mean+t*se)
}

// signFlipP is the two-sided paired permutation test: under the null that A
// and B are exchangeable, each task's difference is equally likely to have
// either sign.
func signFlipP(d []float64) float64 {
	var nz []float64
	for _, x := range d {
		if x != 0 {
			nz = append(nz, x)
		}
	}
	if len(nz) == 0 {
		return 1
	}
	obs := 0.0
	for _, x := range nz {
		obs += x
	}
	obs = math.Abs(obs) - 1e-12
	extreme, total := 0, 0
	if len(nz) <= 20 {
		for mask := 0; mask < 1<<len(nz); mask++ {
			s := 0.0
			for i, x := range nz {
				if mask&(1<<i) != 0 {
					s -= x
				} else {
					s += x
				}
			}
			if math.Abs(s) >= obs {
				extreme++
			}
			total++
		}
	} else {
		rng := rand.New(rand.NewPCG(1, 2)) // seeded: the same report gives the same p
		const draws = 200000
		for range draws {
			s := 0.0
			for _, x := range nz {
				if rng.IntN(2) == 0 {
					s -= x
				} else {
					s += x
				}
			}
			if math.Abs(s) >= obs {
				extreme++
			}
		}
		total = draws
	}
	return float64(extreme) / float64(total)
}

// UsageStats is a target's mean cost per task attempt.
type UsageStats struct {
	Name             string  `json:"name"`
	Attempts         int     `json:"attempts"`
	WallSec          float64 `json:"wall_sec"` // mean wall time per attempt
	WallP90Sec       float64 `json:"wall_p90_sec"`
	ModelCalls       float64 `json:"model_calls"`
	AuxModelCalls    float64 `json:"aux_model_calls"`
	ToolCalls        float64 `json:"tool_calls"`
	PromptTokens     float64 `json:"prompt_tokens"`
	CompletionTokens float64 `json:"completion_tokens"`
	CachedTokens     float64 `json:"cached_tokens"`
	Reported         int     `json:"reported"` // attempts whose target reported usage beyond wall time
}

// Usage aggregates RunUsage per target across all runs and tasks.
func (cr ComparisonReport) Usage() []UsageStats {
	idx := map[string]int{}
	var out []UsageStats
	walls := map[string][]float64{}
	for _, tr := range cr.Targets {
		i, ok := idx[tr.Name]
		if !ok {
			i = len(out)
			idx[tr.Name] = i
			out = append(out, UsageStats{Name: tr.Name})
		}
		u := &out[i]
		for _, s := range tr.Samples {
			u.Attempts++
			if s.Usage == nil {
				continue
			}
			walls[tr.Name] = append(walls[tr.Name], float64(s.Usage.WallMs)/1000)
			u.WallSec += float64(s.Usage.WallMs) / 1000
			u.ToolCalls += float64(len(s.ToolCalls))
			if s.Usage.ModelCalls > 0 || s.Usage.PromptTokens > 0 {
				u.Reported++
			}
			u.ModelCalls += float64(s.Usage.ModelCalls)
			u.AuxModelCalls += float64(s.Usage.AuxModelCalls)
			u.PromptTokens += float64(s.Usage.PromptTokens)
			u.CompletionTokens += float64(s.Usage.CompletionTokens)
			u.CachedTokens += float64(s.Usage.CachedTokens)
		}
	}
	for i := range out {
		u := &out[i]
		if u.Attempts == 0 {
			continue
		}
		n := float64(u.Attempts)
		u.WallSec /= n
		u.ToolCalls /= n
		if u.Reported > 0 {
			r := float64(u.Reported)
			u.ModelCalls /= r
			u.AuxModelCalls /= r
			u.PromptTokens /= r
			u.CompletionTokens /= r
			u.CachedTokens /= r
		}
		w := walls[u.Name]
		sort.Float64s(w)
		if len(w) > 0 {
			u.WallP90Sec = w[min(len(w)-1, int(math.Ceil(0.9*float64(len(w))))-1)]
		}
	}
	return out
}

// WriteUsage prints per-target mean cost per attempt.
func (cr ComparisonReport) WriteUsage(w io.Writer) {
	us := cr.Usage()
	if len(us) == 0 {
		return
	}
	fmt.Fprintln(w, "\n--- cost per task attempt (mean) ---")
	fmt.Fprintf(w, "%-20s %8s %8s %8s %8s %10s %10s %10s\n",
		"agent", "wall s", "p90 s", "model", "aux", "tools", "prompt tk", "compl tk")
	for _, u := range us {
		fmt.Fprintf(w, "%-20s %8.1f %8.1f %8.1f %8.1f %10.1f %10.0f %10.0f",
			u.Name, u.WallSec, u.WallP90Sec, u.ModelCalls, u.AuxModelCalls, u.ToolCalls, u.PromptTokens, u.CompletionTokens)
		if u.Reported < u.Attempts {
			fmt.Fprintf(w, "  (usage reported for %d/%d)", u.Reported, u.Attempts)
		}
		fmt.Fprintln(w)
	}
}

// WritePerTask prints one row per task with each target's pass count and mean
// cost, so "slow" and "flaky" can be traced to the task that causes them.
func (cr ComparisonReport) WritePerTask(w io.Writer) {
	type acc struct {
		pass, runs                 int
		wall, model, tools, tokens float64
	}
	cells := map[string]map[string]*acc{}
	var names []string
	seen := map[string]bool{}
	for _, tr := range cr.Targets {
		if !seen[tr.Name] {
			seen[tr.Name] = true
			names = append(names, tr.Name)
		}
		passed := map[string]bool{}
		for _, sr := range tr.Report.Samples {
			passed[sr.Sample] = samplePassed(sr, cr.Gate)
		}
		for _, s := range tr.Samples {
			if cells[s.Name] == nil {
				cells[s.Name] = map[string]*acc{}
			}
			a := cells[s.Name][tr.Name]
			if a == nil {
				a = &acc{}
				cells[s.Name][tr.Name] = a
			}
			a.runs++
			if passed[s.Name] {
				a.pass++
			}
			a.tools += float64(len(s.ToolCalls))
			if s.Usage != nil {
				a.wall += float64(s.Usage.WallMs) / 1000
				a.model += float64(s.Usage.ModelCalls)
				a.tokens += float64(s.Usage.PromptTokens + s.Usage.CompletionTokens)
			}
		}
	}
	tasks := cr.taskOrder()
	sort.Strings(tasks)
	fmt.Fprintln(w, "\n--- per task: pass/runs  wall s  model calls  tool calls  k tokens ---")
	fmt.Fprintf(w, "%-30s", "task")
	for _, n := range names {
		fmt.Fprintf(w, " | %-30s", n)
	}
	fmt.Fprintln(w)
	for _, t := range tasks {
		fmt.Fprintf(w, "%-30s", clipRunes(t, 30))
		for _, n := range names {
			a := cells[t][n]
			if a == nil || a.runs == 0 {
				fmt.Fprintf(w, " | %-30s", "-")
				continue
			}
			r := float64(a.runs)
			fmt.Fprintf(w, " | %-30s", fmt.Sprintf("%d/%d %5.1fs %4.1fm %4.1ft %5.1fk",
				a.pass, a.runs, a.wall/r, a.model/r, a.tools/r, a.tokens/r/1000))
		}
		fmt.Fprintln(w)
	}
}
