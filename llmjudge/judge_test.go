package llmjudge_test

import (
	"os"
	"testing"

	evalgo "github.com/liliang-cn/eval-go"
	"github.com/liliang-cn/eval-go/llmjudge"
)

// TestRAGGoldenSet is the native-go-test pattern from the framework's design:
// table-driven, t.Parallel via RunT, gated behind RUN_EVALS so it never runs on
// a plain `go test` (no tokens spent on local commits).
//
//	RUN_EVALS=true LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=qwen-plus go test ./llmjudge -v
func TestRAGGoldenSet(t *testing.T) {
	if os.Getenv("RUN_EVALS") != "true" {
		t.Skip("skipping LLM evals; set RUN_EVALS=true (and LLM_* env) to run")
	}
	judge, err := llmjudge.FromEnv()
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}
	judge = evalgo.RateLimit(judge, 4, 2) // be gentle on the provider

	suite := evalgo.Suite{
		Concurrency: 4,
		Metrics: []evalgo.Metric{
			evalgo.CitationPresent(),
			evalgo.Faithfulness(judge),
			evalgo.AnswerRelevancy(judge, 0.5), // 0.5 is the DeepEval default; judge statement-splitting is noisy
			evalgo.ContextualPrecision(judge, 0.5),
		},
		Samples: []evalgo.Sample{
			{
				Name:    "savings-rate-grounded",
				Input:   "What is the savings account interest rate?",
				Context: []string{"The savings account pays 0.30% for balances below 50000 and 0.55% above 50000."},
				Output:  "The savings rate is tiered: 0.30% below 50000 and 0.55% above 50000 [KB-001].",
			},
			{
				Name:    "aml-threshold-grounded",
				Input:   "What is the AML cash deposit review threshold?",
				Context: []string{"AML monitoring flags any single cash deposit at or above 50000 for manual review."},
				Output:  "Single cash deposits at or above 50000 trigger manual review [KB-004].",
			},
		},
	}
	evalgo.RunT(t, suite)
}
