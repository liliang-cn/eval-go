package evalgo

import (
	"context"
	"testing"
)

// RunT drives a Suite through Go's native test runner: one subtest per sample,
// executed with t.Parallel() so hundreds of rows evaluate concurrently. Failed
// metrics become t.Error, so `go test` exit codes and CI reporting work for free.
//
//	func TestRAG(t *testing.T) {
//	    if os.Getenv("RUN_EVALS") != "true" { t.Skip("set RUN_EVALS=true") }
//	    evalgo.RunT(t, suite)
//	}
func RunT(t *testing.T, s Suite) {
	t.Helper()
	for _, sample := range s.Samples {
		t.Run(sample.Name, func(t *testing.T) {
			t.Parallel()
			for _, m := range s.Metrics {
				res, err := m.Score(context.Background(), sample)
				if err != nil {
					t.Errorf("%s: judge error: %v", m.Name(), err)
					continue
				}
				if !res.Passed {
					t.Errorf("%s FAILED score=%.2f: %s", res.Metric, res.Score, res.Reason)
				}
			}
		})
	}
}
