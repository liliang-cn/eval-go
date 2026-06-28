package evalgo_test

import (
	"context"
	"fmt"

	evalgo "github.com/liliang-cn/eval-go"
)

// ExampleBench shows the end-to-end agent-eval flow: define Tasks, plug an agent
// in as a Target, and let Bench run + score it. Here the Target is an in-process
// stub and the metrics are deterministic (no LLM judge) so the output is stable.
func ExampleBench() {
	// The system under test: any func that runs a Task and reports the run.
	agent := evalgo.TargetFunc(func(_ context.Context, t evalgo.Task) (evalgo.RunOutput, error) {
		return evalgo.RunOutput{
			Output:    "handled " + t.Name,
			ToolCalls: []evalgo.ToolCall{{Name: "write_file"}},
		}, nil
	})

	bench := evalgo.Bench{
		Target: agent,
		Tasks: []evalgo.Task{
			{Name: "task-a", Input: "do A", ExpectedTools: []string{"write_file"}},
		},
		Metrics: []evalgo.Metric{evalgo.NonEmpty(), evalgo.ToolCorrectness()},
	}

	report, samples := bench.Run(context.Background())
	fmt.Println("ran samples:", len(samples))
	for _, sr := range report.Samples {
		fmt.Printf("%s passed=%v\n", sr.Sample, sr.Passed)
	}
	// Output:
	// ran samples: 1
	// task-a passed=true
}
