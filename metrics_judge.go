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
Think briefly, then return STRICTLY a JSON object and nothing else:
{"passed": <bool>, "score": <number 0.0-1.0>, "reason": "<short justification>"}

INPUT:
%s

CRITERION:
%s

ACTUAL OUTPUT:
%s

JSON:`, s.Input, s.Rubric, s.Output)

		v, err := callVerdict(ctx, judge, prompt)
		if err != nil {
			return Result{}, err
		}
		passed := v.Passed && v.Score >= passThreshold
		return Result{Metric: "rubric_judge", Score: v.Score, Passed: passed, Reason: v.Reason}, nil
	}}
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
