// Command evalgo is a CLI for evaluating LLM / RAG outputs from a golden dataset.
//
//	evalgo -dataset golden.json -metrics nonempty,citation
//	evalgo -dataset golden.json -judge env -metrics faithfulness,answer_relevancy,rubric -format json -out report.json -fail-under 0.8
//
// Judge mode "env" reads LLM_BASE_URL / LLM_API_KEY / LLM_MODEL (any
// OpenAI-compatible endpoint). Exit code is non-zero when the suite fails its
// gate, so it drops straight into CI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	evalgo "github.com/liliang-cn/eval-go"
	"github.com/liliang-cn/eval-go/llmjudge"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "evalgo:", err)
		os.Exit(2) // 2 = config/runtime error; 1 = eval gate failure
	}
}

func run() error {
	var (
		dataset     = flag.String("dataset", "", "path to golden dataset JSON (array of samples); '-' for stdin")
		metricsFlag = flag.String("metrics", "nonempty,citation", "comma-separated metrics ("+strings.Join(evalgo.RegisteredMetrics(), ",")+")")
		judgeMode   = flag.String("judge", "none", `judge mode: "env" (OpenAI-compatible via LLM_* env) or "none"`)
		format      = flag.String("format", "console", "report format: console | json")
		out         = flag.String("out", "", "also write a JSON report to this file")
		concurrency = flag.Int("concurrency", 4, "samples evaluated in parallel")
		rps         = flag.Float64("rps", 4, "judge rate limit (requests/sec); <=0 disables")
		threshold   = flag.Float64("threshold", 0.5, "pass threshold for answer_relevancy / context_precision / rubric")
		failUnder   = flag.Float64("fail-under", 0, "exit 1 if the fraction of fully-passing samples is below this (0 = fail on any failure)")
	)
	flag.Parse()

	if *dataset == "" {
		flag.Usage()
		return fmt.Errorf("-dataset is required")
	}

	samples, err := loadDataset(*dataset)
	if err != nil {
		return err
	}

	var judge evalgo.JudgeFunc
	if *judgeMode == "env" {
		judge, err = llmjudge.FromEnv()
		if err != nil {
			return fmt.Errorf("judge: %w", err)
		}
		if *rps > 0 {
			judge = evalgo.RateLimit(judge, *rps, 2)
		}
	} else if *judgeMode != "none" {
		return fmt.Errorf("unknown -judge mode %q (use env|none)", *judgeMode)
	}

	metrics, err := evalgo.BuildMetrics(strings.Split(*metricsFlag, ","), evalgo.MetricSpec{
		Judge: judge, Threshold: *threshold,
	})
	if err != nil {
		return err
	}

	suite := evalgo.Suite{Samples: samples, Metrics: metrics, Concurrency: *concurrency}
	report := suite.Run(context.Background())

	switch *format {
	case "json":
		if err := report.WriteJSON(os.Stdout); err != nil {
			return err
		}
	case "console":
		report.WriteConsole(os.Stdout)
	default:
		return fmt.Errorf("unknown -format %q (use console|json)", *format)
	}

	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := report.WriteJSON(f); err != nil {
			return err
		}
	}

	if failed := gateFailed(report, *failUnder); failed {
		os.Exit(1)
	}
	return nil
}

// gateFailed applies the CI gate: with fail-under > 0, require that fraction of
// fully-passing samples; otherwise fail on any failure.
func gateFailed(r evalgo.Report, failUnder float64) bool {
	if failUnder <= 0 {
		return r.Failed()
	}
	if len(r.Samples) == 0 {
		return false
	}
	passed := 0
	for _, s := range r.Samples {
		if s.Passed {
			passed++
		}
	}
	rate := float64(passed) / float64(len(r.Samples))
	return rate < failUnder
}

func loadDataset(path string) ([]evalgo.Sample, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	var samples []evalgo.Sample
	if err := json.Unmarshal(data, &samples); err != nil {
		return nil, fmt.Errorf("parse dataset JSON (want an array of samples): %w", err)
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}
	return samples, nil
}
