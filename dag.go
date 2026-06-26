package evalgo

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// DAG builds a metric from a decision tree of judge questions instead of one
// vague 0-1 score. Each interior node asks the judge a categorical question
// about the Sample and routes to a child by the answer; each leaf assigns a
// concrete score. This makes complex pass/fail logic deterministic and
// explainable — the judge only makes small, local decisions (the DeepEval "DAG
// metric" idea).
//
//	root := YesNo("Is the OUTPUT valid JSON?",
//	    YesNo("Does it contain a 'name' field?", Leaf(1, "ok"), Leaf(0.5, "missing name")),
//	    Leaf(0, "not JSON"))
//	m := DAG(judge, "json_shape", 0.99, root)
//
// passThreshold is the minimum leaf score (0..1) required to pass.
func DAG(judge JudgeFunc, name string, passThreshold float64, root DAGNode) Metric {
	return MetricFunc{name, func(ctx context.Context, s Sample) (Result, error) {
		if root == nil {
			return Result{}, fmt.Errorf("dag %q: nil root", name)
		}
		score, reason, err := root.decide(ctx, judge, s)
		if err != nil {
			return Result{}, err
		}
		return Result{Metric: name, Score: score, Passed: score >= passThreshold, Reason: reason}, nil
	}}
}

// DAGNode is one node in a DAG metric's decision tree.
type DAGNode interface {
	decide(ctx context.Context, judge JudgeFunc, s Sample) (score float64, reason string, err error)
}

// Leaf is a terminal node that assigns a fixed score and reason.
func Leaf(score float64, reason string) DAGNode { return leaf{score, reason} }

type leaf struct {
	score  float64
	reason string
}

func (l leaf) decide(context.Context, JudgeFunc, Sample) (float64, string, error) {
	return l.score, l.reason, nil
}

// Branch asks the judge to answer question with exactly one of the choice labels
// and routes to that child. If the answer matches no label, it routes to
// fallback; a nil fallback with no match scores 0.
func Branch(question string, choices map[string]DAGNode, fallback DAGNode) DAGNode {
	return &branch{question: question, choices: choices, fallback: fallback}
}

// YesNo is the common binary Branch: answer "yes" → yes, "no" → no.
func YesNo(question string, yes, no DAGNode) DAGNode {
	return Branch(question, map[string]DAGNode{"yes": yes, "no": no}, nil)
}

type branch struct {
	question string
	choices  map[string]DAGNode
	fallback DAGNode
}

func (b *branch) decide(ctx context.Context, judge JudgeFunc, s Sample) (float64, string, error) {
	labels := make([]string, 0, len(b.choices))
	for k := range b.choices {
		labels = append(labels, k)
	}
	sort.Strings(labels)

	var resp struct {
		Choice string `json:"choice"`
		Reason string `json:"reason"`
	}
	prompt := fmt.Sprintf(`You are evaluating an AI system. Answer the QUESTION about the sample by replying with
EXACTLY ONE of these labels: [%s]. Return STRICTLY JSON: {"choice":"<label>","reason":"<short>"}

INPUT:
%s

ACTUAL OUTPUT:
%s

CONTEXT:
%s

QUESTION:
%s

JSON:`, strings.Join(labels, ", "), s.Input, s.Output, joinContext(s.Context), b.question)

	if err := callJSON(ctx, judge, prompt, &resp); err != nil {
		return 0, "", err
	}
	choice := strings.ToLower(strings.TrimSpace(resp.Choice))
	for label, child := range b.choices {
		if strings.EqualFold(label, choice) {
			score, reason, err := child.decide(ctx, judge, s)
			if err != nil {
				return 0, "", err
			}
			return score, joinReason(b.question, choice, resp.Reason, reason), err
		}
	}
	if b.fallback != nil {
		return b.fallback.decide(ctx, judge, s)
	}
	return 0, fmt.Sprintf("%s → %q (no matching branch)", b.question, resp.Choice), nil
}

// joinReason threads the path taken into the final reason for explainability.
func joinReason(question, choice, branchReason, childReason string) string {
	step := fmt.Sprintf("%s → %s", strings.TrimRight(question, "?"), choice)
	if branchReason != "" {
		step += " (" + branchReason + ")"
	}
	if childReason == "" {
		return step
	}
	return step + "; " + childReason
}
