package evalgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// JudgeFunc executes one LLM-as-a-judge call: given a prompt, return raw text
// (expected to contain JSON). Wrap with RateLimit to pace provider QPS. Build one
// from agent-go via the sibling package ./llmjudge.
type JudgeFunc func(ctx context.Context, prompt string) (string, error)

// verdict is the structured shape every judge prompt is asked to return.
type verdict struct {
	Passed bool    `json:"passed"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// RubricJudge is a G-Eval-style metric: the judge scores Output against
// Sample.Rubric with chain-of-thought, returning {passed, score, reason}.
// passThreshold is the minimum score (0..1) required to pass.
func RubricJudge(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"rubric_judge", func(ctx context.Context, s Sample) (Result, error) {
		if s.Rubric == "" {
			return Result{Metric: "rubric_judge", Passed: true, Score: 1, Reason: "no rubric"}, nil
		}
		prompt := fmt.Sprintf(`You are a strict, objective evaluation judge.
Decide whether the ACTUAL OUTPUT satisfies the CRITERION for the given INPUT.
The tool calls, their arguments and their results were recorded by the harness:
they are ground truth. What the assistant SAYS it did is not — if it claims an
action that no recorded call performed, that claim is false.
Judge only against the CRITERION; a refusal or a clarifying question is correct
when the criterion says so.
Think briefly, then return STRICTLY a JSON object and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

INPUT:
%s
%s
CRITERION:
%s

ACTUAL OUTPUT:
%s

TOOL CALLS THE AGENT ACTUALLY MADE (recorded by the harness, in order, with results):
%s
%s
JSON:`, s.Input, conversationBlock(s), s.Rubric, s.Output, toolCallsOrNone(s.ToolCalls), stateBlock(s))

		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "rubric_judge", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
}

// toolCallsOrNone shows the judge what the agent invoked. A criterion like
// "uses translate, then set_reminder" cannot be checked from the final text: an
// agent that called both and answered in one sentence reads exactly like one
// that called neither and claimed it did.
func toolCallsOrNone(calls []ToolCall) string {
	if len(calls) == 0 {
		return "(none)"
	}
	return strings.TrimRight(formatToolCalls(calls), "\n")
}

// stateBlock shows the judge the tool environment's final state, when the
// target reported one: for "move it to 8pm" the state is the outcome. Each
// top-level collection is clipped on its own, so a long address book cannot
// push the reminders the task is about out of the judge's view.
func stateBlock(s Sample) string {
	if len(s.State) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(s.State, &obj) != nil {
		return "\nFINAL TOOL STATE (recorded by the harness):\n" + clipRunes(string(s.State), judgeMaxStateText) + "\n"
	}
	var b strings.Builder
	b.WriteString("\nFINAL TOOL STATE (recorded by the harness):\n")
	for _, k := range sortedKeys(obj) {
		line := fmt.Sprintf("%s: %s\n", k, clipRunes(string(obj[k]), judgeMaxStateKey))
		if b.Len()+len(line) > judgeMaxStateText {
			b.WriteString("... (more state not shown)\n")
			break
		}
		b.WriteString(line)
	}
	return b.String()
}

// callVerdict runs a judge prompt and parses a {passed,score,reason} verdict.
func callVerdict(ctx context.Context, judge JudgeFunc, prompt string) (verdict, error) {
	if judge == nil {
		return verdict{}, errors.New("no judge configured")
	}
	raw, err := judge(ctx, prompt)
	if err != nil {
		return verdict{}, err
	}
	js, err := extractJSON(raw)
	if err != nil {
		return verdict{}, fmt.Errorf("judge returned non-JSON: %w", err)
	}
	var v verdict
	if err := json.Unmarshal([]byte(js), &v); err != nil {
		return verdict{}, fmt.Errorf("parse verdict: %w", err)
	}
	// Tolerate judges that emit only `passed` with no score.
	if v.Score == 0 && v.Passed {
		v.Score = 1
	}
	return v, nil
}

// callJSON runs a judge prompt and unmarshals the embedded JSON into out.
func callJSON(ctx context.Context, judge JudgeFunc, prompt string, out any) error {
	if judge == nil {
		return errors.New("no judge configured")
	}
	raw, err := judge(ctx, prompt)
	if err != nil {
		return err
	}
	js, err := extractJSON(raw)
	if err != nil {
		return fmt.Errorf("judge returned non-JSON: %w", err)
	}
	return json.Unmarshal([]byte(js), out)
}

// extractJSON pulls the first balanced JSON object or array out of model text,
// tolerating ```json fences and surrounding prose.
func extractJSON(s string) (string, error) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		rest = strings.TrimPrefix(rest, "json")
		rest = strings.TrimPrefix(rest, "JSON")
		if j := strings.Index(rest, "```"); j >= 0 {
			s = strings.TrimSpace(rest[:j])
		}
	}
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return "", errors.New("no JSON found")
	}
	open := s[start]
	close := byte('}')
	if open == '[' {
		close = ']'
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == open:
			depth++
		case c == close:
			depth--
			if depth == 0 {
				return s[start : i+1], nil
			}
		}
	}
	return "", errors.New("unbalanced JSON")
}
