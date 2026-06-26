package evalgo

import (
	"context"
	"fmt"
	"strings"
)

// ContextualRecall measures whether the retrieved Context contains everything the
// reference answer needs: decompose Expected into statements, then check each can be
// attributed to the retrieval context. Score = attributable statements / total.
// Low recall means the retriever missed information the answer depends on.
func ContextualRecall(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"contextual_recall", func(ctx context.Context, s Sample) (Result, error) {
		if s.Expected == "" {
			return Result{Metric: "contextual_recall", Passed: true, Score: 1, Reason: "no expected answer (skipped)"}, nil
		}
		if len(s.Context) == 0 {
			return Result{Metric: "contextual_recall", Passed: false, Score: 0, Reason: "no context retrieved"}, nil
		}
		stmts, err := extractStatements(ctx, judge, s.Expected)
		if err != nil {
			return Result{}, err
		}
		if len(stmts) == 0 {
			return Result{Metric: "contextual_recall", Passed: true, Score: 1, Reason: "no statements in expected (skipped)"}, nil
		}
		var resp struct {
			Verdicts []struct {
				Verdict string `json:"verdict"` // yes | no
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		prompt := fmt.Sprintf(`For EACH numbered statement from the reference answer, decide whether it can be
attributed to / is supported by the RETRIEVAL CONTEXT. Answer "yes" if the statement is
attributable to the context, "no" otherwise.
Return STRICTLY JSON: {"verdicts":[{"verdict":"yes|no","reason":"only if no"}]} one per statement, in order.

RETRIEVAL CONTEXT:
%s

STATEMENTS:
%s

JSON:`, joinContext(s.Context), joinNumbered(stmts))
		if err := callJSON(ctx, judge, prompt, &resp); err != nil {
			return Result{}, err
		}
		attributable, total := 0, len(stmts)
		for _, v := range resp.Verdicts {
			if !strings.EqualFold(v.Verdict, "no") {
				attributable++
			}
		}
		if len(resp.Verdicts) < total {
			attributable += total - len(resp.Verdicts)
		}
		score := float64(attributable) / float64(total)
		return Result{Metric: "contextual_recall", Score: score, Passed: score >= passThreshold,
			Reason: fmt.Sprintf("%d/%d expected statements supported by context", attributable, total)}, nil
	}}
}

// ContextualRelevancy measures retrieval noise: of the statements present in the
// retrieved Context, how many are relevant to the Input. Score = relevant statements
// / total. Low relevancy means the context is padded with off-topic material.
func ContextualRelevancy(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"contextual_relevancy", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.Context) == 0 {
			return Result{Metric: "contextual_relevancy", Passed: false, Score: 0, Reason: "no context retrieved"}, nil
		}
		stmts, err := extractStatements(ctx, judge, joinContext(s.Context))
		if err != nil {
			return Result{}, err
		}
		if len(stmts) == 0 {
			return Result{Metric: "contextual_relevancy", Passed: true, Score: 1, Reason: "no statements in context (skipped)"}, nil
		}
		var resp struct {
			Verdicts []struct {
				Verdict string `json:"verdict"` // yes | no
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		prompt := fmt.Sprintf(`For EACH numbered statement, decide if it is relevant/useful for answering the INPUT.
Answer "yes" if the statement is relevant, "no" otherwise.
Return STRICTLY JSON: {"verdicts":[{"verdict":"yes|no","reason":"only if no"}]} one per statement, in order.

INPUT:
%s

STATEMENTS:
%s

JSON:`, s.Input, joinNumbered(stmts))
		if err := callJSON(ctx, judge, prompt, &resp); err != nil {
			return Result{}, err
		}
		relevant, total := 0, len(stmts)
		for _, v := range resp.Verdicts {
			if strings.EqualFold(v.Verdict, "yes") {
				relevant++
			}
		}
		score := float64(relevant) / float64(total)
		return Result{Metric: "contextual_relevancy", Score: score, Passed: score >= passThreshold,
			Reason: fmt.Sprintf("%d/%d context statements relevant", relevant, total)}, nil
	}}
}
