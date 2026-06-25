package evalgo_test

import (
	"context"
	"fmt"

	evalgo "github.com/liliang-cn/eval-go"
)

// Example shows Eval-Go used as a library: build a Suite of deterministic
// metrics over an in-memory golden set and inspect the aggregated report.
// (Semantic metrics additionally take a judge — see package llmjudge.)
func Example() {
	suite := evalgo.Suite{
		Metrics: []evalgo.Metric{
			evalgo.ValidJSON(),
			evalgo.JSONHasFields("name", "age"),
		},
		Samples: []evalgo.Sample{
			{Name: "ok", Output: `{"name":"Jane","age":30}`},
			{Name: "missing-age", Output: `{"name":"Jane"}`},
		},
	}

	report := suite.Run(context.Background())
	for _, ms := range report.Summary() {
		fmt.Printf("%s: %d/%d passed\n", ms.Metric, ms.Passed, ms.Total)
	}
	fmt.Println("failed:", report.Failed())
	// Output:
	// json_has_fields: 1/2 passed
	// valid_json: 2/2 passed
	// failed: true
}
