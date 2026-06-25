package evalgo

import (
	"context"
	"fmt"
	"strings"
)

// RAG metrics follow DeepEval/RAGAS-style decomposition: instead of asking the
// judge for one vague 0-1 score, break the answer into atomic units and verify
// each — far more reliable and explainable.

// Faithfulness measures how grounded Output is in Context: extract the claims the
// answer makes, then check each against the retrieval context. Score =
// non-contradicted claims / total claims. Catches hallucination.
func Faithfulness(judge JudgeFunc) Metric {
	return MetricFunc{"faithfulness", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.Context) == 0 {
			return Result{Metric: "faithfulness", Passed: true, Score: 1, Reason: "no context (skipped)"}, nil
		}
		claims, err := extractClaims(ctx, judge, s.Output)
		if err != nil {
			return Result{}, err
		}
		if len(claims) == 0 {
			return Result{Metric: "faithfulness", Passed: true, Score: 1, Reason: "no factual claims"}, nil
		}

		var resp struct {
			Verdicts []struct {
				Verdict string `json:"verdict"` // yes | no | idk
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		prompt := fmt.Sprintf(`For EACH claim, decide whether it is supported by (does not contradict) the RETRIEVAL CONTEXT.
Return STRICTLY JSON: {"verdicts":[{"verdict":"yes|no|idk","reason":"only if no/idk"}]} with one entry per claim, in order.

RETRIEVAL CONTEXT:
%s

CLAIMS:
%s

JSON:`, joinContext(s.Context), joinNumbered(claims))

		if err := callJSON(ctx, judge, prompt, &resp); err != nil {
			return Result{}, err
		}

		supported, total := 0, len(claims)
		var problems []string
		for i, v := range resp.Verdicts {
			if strings.EqualFold(v.Verdict, "no") {
				if v.Reason != "" {
					problems = append(problems, v.Reason)
				} else if i < len(claims) {
					problems = append(problems, "unsupported: "+claims[i])
				}
			} else {
				supported++
			}
		}
		// If the judge returned fewer verdicts than claims, treat the remainder as unknown-but-ok.
		if len(resp.Verdicts) < total {
			supported += total - len(resp.Verdicts)
		}
		score := float64(supported) / float64(total)
		reason := fmt.Sprintf("%d/%d claims grounded", supported, total)
		if len(problems) > 0 {
			reason += "; " + strings.Join(problems, " | ")
		}
		return Result{Metric: "faithfulness", Score: score, Passed: score >= 0.99, Reason: reason}, nil
	}}
}

// AnswerRelevancy measures how much of Output actually addresses Input: extract
// the answer's statements, classify each as relevant to the question. Score =
// relevant statements / total. Catches rambling / off-topic answers.
func AnswerRelevancy(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"answer_relevancy", func(ctx context.Context, s Sample) (Result, error) {
		stmts, err := extractStatements(ctx, judge, s.Output)
		if err != nil {
			return Result{}, err
		}
		if len(stmts) == 0 {
			return Result{Metric: "answer_relevancy", Passed: false, Score: 0, Reason: "empty answer"}, nil
		}
		var resp struct {
			Verdicts []struct {
				Verdict string `json:"verdict"` // yes | no | idk
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		prompt := fmt.Sprintf(`For EACH statement, decide if it helps address the INPUT question.
A statement is RELEVANT ("yes") if it contributes any information toward answering the
INPUT, even partially. Only answer "no" if the statement is clearly off-topic or unrelated.
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
			if !strings.EqualFold(v.Verdict, "no") {
				relevant++
			}
		}
		if len(resp.Verdicts) < total {
			relevant += total - len(resp.Verdicts)
		}
		score := float64(relevant) / float64(total)
		return Result{Metric: "answer_relevancy", Score: score, Passed: score >= passThreshold,
			Reason: fmt.Sprintf("%d/%d statements relevant", relevant, total)}, nil
	}}
}

// ContextualPrecision measures retrieval quality: of the Context chunks supplied,
// how many are actually relevant to the Input. Score = relevant chunks / total.
// Low precision means the retriever is surfacing noise.
func ContextualPrecision(judge JudgeFunc, passThreshold float64) Metric {
	return MetricFunc{"contextual_precision", func(ctx context.Context, s Sample) (Result, error) {
		if len(s.Context) == 0 {
			return Result{Metric: "contextual_precision", Passed: false, Score: 0, Reason: "no context retrieved"}, nil
		}
		var resp struct {
			Verdicts []struct {
				Verdict string `json:"verdict"`
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		prompt := fmt.Sprintf(`For EACH numbered context chunk, decide whether it is relevant/useful for answering the INPUT.
Return STRICTLY JSON: {"verdicts":[{"verdict":"yes|no","reason":"only if no"}]} one per chunk, in order.

INPUT:
%s

CONTEXT CHUNKS:
%s

JSON:`, s.Input, joinNumbered(s.Context))
		if err := callJSON(ctx, judge, prompt, &resp); err != nil {
			return Result{}, err
		}
		relevant, total := 0, len(s.Context)
		for _, v := range resp.Verdicts {
			if strings.EqualFold(v.Verdict, "yes") {
				relevant++
			}
		}
		score := float64(relevant) / float64(total)
		return Result{Metric: "contextual_precision", Score: score, Passed: score >= passThreshold,
			Reason: fmt.Sprintf("%d/%d chunks relevant", relevant, total)}, nil
	}}
}

// --- shared judge sub-steps ---

func extractClaims(ctx context.Context, judge JudgeFunc, output string) ([]string, error) {
	var resp struct {
		Claims []string `json:"claims"`
	}
	prompt := `Extract a comprehensive list of FACTUAL claims made in the AI OUTPUT below.
Take the text at face value; do not add outside knowledge. Each claim must stand on its own with full context.
Return STRICTLY JSON: {"claims":["...","..."]}

AI OUTPUT:
` + output + `

JSON:`
	if err := callJSON(ctx, judge, prompt, &resp); err != nil {
		return nil, err
	}
	return resp.Claims, nil
}

func extractStatements(ctx context.Context, judge JudgeFunc, output string) ([]string, error) {
	var resp struct {
		Statements []string `json:"statements"`
	}
	prompt := `Break the AI OUTPUT below into a list of self-contained factual statements (one idea each).
Do NOT split a single fact into fragments, and EXCLUDE citation markers like [KB-001].
Return STRICTLY JSON: {"statements":["...","..."]}

AI OUTPUT:
` + output + `

JSON:`
	if err := callJSON(ctx, judge, prompt, &resp); err != nil {
		return nil, err
	}
	return resp.Statements, nil
}

func joinContext(chunks []string) string { return joinNumbered(chunks) }

func joinNumbered(items []string) string {
	var b strings.Builder
	for i, it := range items {
		fmt.Fprintf(&b, "%d. %s\n", i+1, it)
	}
	return b.String()
}
