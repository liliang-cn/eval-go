package evalgo

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// GateAuto is the default bench gate. Each task is gated by the metrics that
// can actually check it, chosen per task:
//
//   - a task that sets Gate is gated by exactly those metrics;
//   - otherwise a task with a Rubric is gated by rubric_judge, and a task with
//     a Check by tool_trajectory (both, when it has both);
//   - a task with neither falls back to task_completion.
//
// task_completion is still scored and reported everywhere it is requested, but
// it does not gate a task that has a rubric. It asks "did the agent do what was
// asked", and for a request that must be refused ("order me a pizza" with no
// ordering tool) the correct run does NOT do what was asked: a gate on
// task_completion fails every correct refusal. The rubric says what correct
// means for that task; the gate follows the rubric.
const GateAuto = "auto"

// metricAliases maps a registry key to the name the metric reports results
// under, for the keys where they differ ("rubric" -> "rubric_judge",
// "nonempty" -> "non_empty"). It is derived from the registry itself, so it
// cannot drift from it. Gate names are resolved through it once, up front, so
// a gate never silently matches nothing.
var metricAliases = sync.OnceValue(func() map[string]string {
	m := map[string]string{}
	for key, f := range registry {
		if name := f.build(MetricSpec{Threshold: 0.5}).Name(); name != key {
			m[key] = name
		}
	}
	return m
})

// IsAutoGate reports whether gate is the auto gate (["auto"]). An empty gate
// is different: every scored metric must pass.
func IsAutoGate(gate []string) bool {
	return len(gate) == 1 && strings.EqualFold(gate[0], GateAuto)
}

// AutoGate returns the gate for one task given the metric names in the run
// (the names Results carry). See GateAuto.
func AutoGate(t Task, available map[string]bool) []string {
	if len(t.Gate) > 0 {
		out := make([]string, len(t.Gate))
		for i, g := range t.Gate {
			out[i] = canonicalMetric(g)
		}
		return out
	}
	var g []string
	if t.Rubric != "" && available["rubric_judge"] {
		g = append(g, "rubric_judge")
	}
	if t.Check != nil && available["tool_trajectory"] {
		g = append(g, "tool_trajectory")
	}
	if len(g) == 0 && available["task_completion"] {
		g = append(g, "task_completion")
	}
	return g
}

func canonicalMetric(name string) string {
	name = strings.TrimSpace(name)
	if a, ok := metricAliases()[name]; ok {
		return a
	}
	return name
}

// ResolveGate validates a gate against the metrics that will actually be
// scored and returns it in reported-name form. An unknown name is an error
// listing the valid ones, never a gate that quietly matches nothing.
//
// With the auto gate it also checks every task: a task with a rubric needs
// rubric_judge in the run, a task with a check needs tool_trajectory, and a
// per-task Gate may only name metrics that are scored.
func ResolveGate(gate []string, metrics []Metric, tasks []Task) ([]string, error) {
	available := map[string]bool{}
	var names []string
	for _, m := range metrics {
		available[m.Name()] = true
		names = append(names, m.Name())
	}
	sort.Strings(names)
	valid := strings.Join(names, ", ")

	var clean []string
	for _, g := range gate {
		if g = strings.TrimSpace(g); g != "" {
			clean = append(clean, g)
		}
	}
	if len(clean) == 0 {
		return nil, nil // every metric must pass
	}

	if IsAutoGate(clean) {
		var noRubric, noCheck []string
		for _, t := range tasks {
			for _, g := range t.Gate {
				if !available[canonicalMetric(g)] {
					return nil, fmt.Errorf("task %q: gate %q is not a scored metric (scored: %s)", t.Name, g, valid)
				}
			}
			if len(t.Gate) > 0 {
				continue
			}
			if t.Rubric != "" && !available["rubric_judge"] {
				noRubric = append(noRubric, t.Name)
			}
			if t.Check != nil && !available["tool_trajectory"] {
				noCheck = append(noCheck, t.Name)
			}
		}
		if len(noRubric) > 0 {
			return nil, fmt.Errorf("auto gate: %d task(s) have a rubric but rubric_judge is not in --metrics (e.g. %s)", len(noRubric), noRubric[0])
		}
		if len(noCheck) > 0 {
			return nil, fmt.Errorf("auto gate: %d task(s) have a check but tool_trajectory is not in --metrics (e.g. %s)", len(noCheck), noCheck[0])
		}
		for _, t := range tasks {
			if len(AutoGate(t, available)) == 0 {
				return nil, fmt.Errorf("auto gate: task %q has no rubric, no check, and task_completion is not scored, so nothing would gate it", t.Name)
			}
		}
		return []string{GateAuto}, nil
	}

	out := make([]string, 0, len(clean))
	for _, g := range clean {
		if strings.EqualFold(g, GateAuto) {
			return nil, fmt.Errorf("gate %q cannot be combined with other names", GateAuto)
		}
		c := canonicalMetric(g)
		if !available[c] {
			return nil, fmt.Errorf("unknown gate metric %q: not among the scored metrics (%s); add it to --metrics or use --gate auto", g, valid)
		}
		out = append(out, c)
	}
	return out, nil
}
