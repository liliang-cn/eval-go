package evalgo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Agent metrics evaluate a recorded agent run carried in the Sample (Plan,
// Trajectory, ToolCalls, ExpectedTools) — the record-then-evaluate counterpart
// to DeepEval's tracing-based agent metrics. They split across the same three
// layers DeepEval names:
//
//   - Action:    ToolCorrectness (deterministic), ArgumentCorrectness (judge)
//   - Overall:   TaskCompletion, StepEfficiency (judge)
//   - Reasoning: PlanQuality, PlanAdherence (judge)
//
// ToolCorrectness is free and offline; the rest follow the RAGAS-style
// decompose-then-verify pattern or a single rubric verdict.

// ToolCorrectness checks the tools the agent actually invoked against the
// ground-truth ExpectedTools (order-independent set comparison). Score is the
// Jaccard overlap so missing AND extraneous tool calls both cost points; the
// metric passes only on an exact set match. Deterministic — no LLM.
func ToolCorrectness() Metric {
	return MetricFunc{"tool_correctness", func(_ context.Context, s Sample) (Result, error) {
		if len(s.ExpectedTools) == 0 {
			return Result{Metric: "tool_correctness", Passed: true, Score: 1, Reason: "no expected tools (skipped)"}, nil
		}
		expected := toSet(s.ExpectedTools)
		actual := toSet(toolNames(s.ToolCalls))

		var missing, extra []string
		union := map[string]struct{}{}
		matched := 0
		for t := range expected {
			union[t] = struct{}{}
			if _, ok := actual[t]; ok {
				matched++
			} else {
				missing = append(missing, t)
			}
		}
		for t := range actual {
			union[t] = struct{}{}
			if _, ok := expected[t]; !ok {
				extra = append(extra, t)
			}
		}
		score := float64(matched) / float64(len(union))
		passed := len(missing) == 0 && len(extra) == 0

		reason := fmt.Sprintf("%d/%d expected tools called", matched, len(expected))
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			reason += "; missing: " + strings.Join(missing, ", ")
		}
		if len(extra) > 0 {
			reason += "; unexpected: " + strings.Join(extra, ", ")
		}
		return Result{Metric: "tool_correctness", Score: score, Passed: passed, Reason: reason}, nil
	}}
}

// ArgumentCorrectness asks the judge, for EACH recorded tool call, whether the
// arguments are correct and sufficient to accomplish the INPUT task. Score =
// correct calls / total. Catches right-tool-wrong-arguments failures.
func ArgumentCorrectness(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"argument_correctness", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.ToolCalls) == 0 {
			return Result{Metric: "argument_correctness", Passed: true, Score: 1, Reason: "no tool calls (skipped)"}, nil
		}
		var resp struct {
			Verdicts []struct {
				Verdict string `json:"verdict"` // yes | no
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		prompt := fmt.Sprintf(`For EACH numbered tool call, decide whether its arguments are correct and appropriate
for accomplishing the INPUT task. Answer "no" if an argument is wrong, missing, or malformed.
Return STRICTLY JSON: {"verdicts":[{"verdict":"yes|no","reason":"only if no"}]} one per call, in order.

INPUT:
%s

TOOL CALLS:
%s

JSON:`, s.Input, formatToolCalls(s.ToolCalls))
		if err := callJSON(ctx, judge, prompt, &resp); err != nil {
			return Result{}, err
		}
		correct, total := 0, len(s.ToolCalls)
		var problems []string
		for i, v := range resp.Verdicts {
			if strings.EqualFold(v.Verdict, "no") {
				if v.Reason != "" {
					problems = append(problems, v.Reason)
				} else if i < len(s.ToolCalls) {
					problems = append(problems, "bad args: "+s.ToolCalls[i].Name)
				}
			} else {
				correct++
			}
		}
		if len(resp.Verdicts) < total { // judge returned fewer; treat remainder as ok
			correct += total - len(resp.Verdicts)
		}
		score := float64(correct) / float64(total)
		reason := fmt.Sprintf("%d/%d tool calls well-formed", correct, total)
		if len(problems) > 0 {
			reason += "; " + strings.Join(problems, " | ")
		}
		return Result{Metric: "argument_correctness", Score: score, Passed: score >= passThreshold, Reason: reason}, nil
	}}
}

// TaskCompletion judges whether the agent actually accomplished the INPUT task,
// given everything it did (trajectory + tool calls) and its final OUTPUT.
func TaskCompletion(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"task_completion", func(ctx context.Context, s Sample) (Result, error) {
		prompt := fmt.Sprintf(`You are a strict evaluator of an AI agent. Decide whether the agent ACCOMPLISHED
the user's TASK, judging by what it did and its final answer. A partial or abandoned task scores low.
Think briefly, then return STRICTLY JSON and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

TASK:
%s

AGENT TRAJECTORY:
%s

FINAL OUTPUT:
%s

JSON:`, s.Input, trajectoryText(s), s.Output)
		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "task_completion", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}

// StepEfficiency judges whether the agent reached the goal without unnecessary,
// redundant, or repeated steps. A correct-but-wasteful run scores low.
func StepEfficiency(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"step_efficiency", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.Trajectory) == 0 && len(s.ToolCalls) == 0 {
			return Result{Metric: "step_efficiency", Passed: true, Score: 1, Reason: "no steps recorded (skipped)"}, nil
		}
		prompt := fmt.Sprintf(`Evaluate the EFFICIENCY of the AI agent's steps for the TASK. A high score means the
agent reached the goal directly; a low score means it took redundant, repeated, or unnecessary steps.
Return STRICTLY JSON and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

TASK:
%s

AGENT STEPS:
%s

JSON:`, s.Input, trajectoryText(s))
		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "step_efficiency", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}

// PlanQuality judges the agent's stated Plan for the INPUT: is it logical,
// complete, and efficient? Skipped (pass) when the sample carries no plan.
func PlanQuality(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"plan_quality", func(ctx context.Context, s Sample) (Result, error) {
		if strings.TrimSpace(s.Plan) == "" {
			return Result{Metric: "plan_quality", Passed: true, Score: 1, Reason: "no plan (skipped)"}, nil
		}
		prompt := fmt.Sprintf(`Judge the AI agent's PLAN for the TASK. A good plan is logical, complete (covers what the
task needs), and efficient (no superfluous steps). Return STRICTLY JSON and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

TASK:
%s

PLAN:
%s

JSON:`, s.Input, s.Plan)
		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "plan_quality", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}

// PlanAdherence judges whether the agent's actual Trajectory followed its own
// Plan. Needs both a plan and a trajectory; skipped (pass) otherwise.
func PlanAdherence(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"plan_adherence", func(ctx context.Context, s Sample) (Result, error) {
		if strings.TrimSpace(s.Plan) == "" {
			return Result{Metric: "plan_adherence", Passed: true, Score: 1, Reason: "no plan (skipped)"}, nil
		}
		if len(s.Trajectory) == 0 && len(s.ToolCalls) == 0 {
			return Result{Metric: "plan_adherence", Passed: true, Score: 1, Reason: "no trajectory (skipped)"}, nil
		}
		prompt := fmt.Sprintf(`Decide how closely the AI agent's ACTUAL EXECUTION followed its stated PLAN. A high score
means it executed the plan; a low score means it deviated, skipped, or improvised away from it.
Return STRICTLY JSON and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

PLAN:
%s

ACTUAL EXECUTION:
%s

JSON:`, s.Plan, trajectoryText(s))
		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "plan_adherence", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}

// --- shared agent helpers ---

func toolNames(calls []ToolCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Name
	}
	return out
}

func toSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			m[it] = struct{}{}
		}
	}
	return m
}

// formatToolCalls renders tool calls as "1. name({json args})" for a judge prompt.
func formatToolCalls(calls []ToolCall) string {
	var b strings.Builder
	for i, c := range calls {
		args := ""
		if len(c.Args) > 0 {
			if j, err := json.Marshal(c.Args); err == nil {
				args = string(j)
			}
		}
		fmt.Fprintf(&b, "%d. %s(%s)\n", i+1, c.Name, args)
	}
	return b.String()
}

// trajectoryText renders the recorded run for a judge prompt, combining the
// reasoning steps and the tool calls (whichever are present).
func trajectoryText(s Sample) string {
	var b strings.Builder
	for i, step := range s.Trajectory {
		fmt.Fprintf(&b, "%d. %s\n", i+1, step)
	}
	if len(s.ToolCalls) > 0 {
		b.WriteString("tool calls:\n")
		b.WriteString(formatToolCalls(s.ToolCalls))
	}
	if b.Len() == 0 {
		return "(none recorded)"
	}
	return b.String()
}
