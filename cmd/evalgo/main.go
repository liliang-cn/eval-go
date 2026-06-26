// Command evalgo is a CLI for evaluating LLM / RAG / agent outputs from a golden dataset.
//
//	evalgo -d golden.json -m nonempty,citation
//	evalgo -d golden.json --judge env -m faithfulness,answer_relevancy,rubric -f json -o report.json --fail-under 0.8
//	evalgo -d agent.json -m tool_correctness,task_completion --judge env
//	evalgo metrics                      # list registered metric names
//
// Judge mode "env" reads LLM_BASE_URL / LLM_API_KEY / LLM_MODEL (any
// OpenAI-compatible endpoint). Exit code is 1 when the suite fails its gate and
// 2 on a config/runtime error, so it drops straight into CI.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	evalgo "github.com/liliang-cn/eval-go"
	"github.com/liliang-cn/eval-go/llmjudge"

	"github.com/spf13/cobra"
)

// options holds the resolved CLI flags for one run.
type options struct {
	dataset     string
	metrics     string
	judgeMode   string
	format      string
	out         string
	concurrency int
	rps         float64
	threshold   float64
	failUnder   float64
	cache       string
	costIn      float64
	costOut     float64
	where       string
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "evalgo:", err)
		os.Exit(2) // 2 = config/runtime error; 1 = eval gate failure (raised in RunE)
	}
}

func newRootCmd() *cobra.Command {
	var o options

	root := &cobra.Command{
		Use:   "evalgo",
		Short: "Evaluate LLM / RAG / agent outputs from a golden dataset",
		Long: "evalgo runs deterministic and LLM-as-a-judge metrics over a JSON golden\n" +
			"dataset and applies a CI gate. Run 'evalgo metrics' to list metric names.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true, // usage on flag errors only, not on eval failures
		SilenceErrors: true, // main prints the error once
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runEval(cmd.OutOrStdout(), o)
		},
	}

	f := root.Flags()
	f.StringVarP(&o.dataset, "dataset", "d", "", "path to golden dataset JSON (array of samples); '-' for stdin (required)")
	f.StringVarP(&o.metrics, "metrics", "m", "nonempty,citation", "comma-separated metrics ("+strings.Join(evalgo.RegisteredMetrics(), ",")+")")
	f.StringVar(&o.judgeMode, "judge", "none", `judge mode: "env" (OpenAI-compatible via LLM_* env) or "none"`)
	f.StringVarP(&o.format, "format", "f", "console", "report format: console | json")
	f.StringVarP(&o.out, "out", "o", "", "also write a JSON report to this file")
	f.IntVar(&o.concurrency, "concurrency", 4, "samples evaluated in parallel")
	f.Float64Var(&o.rps, "rps", 4, "judge rate limit (requests/sec); <=0 disables")
	f.Float64Var(&o.threshold, "threshold", 0.5, "pass threshold for answer_relevancy / context_precision / rubric / agent judge metrics")
	f.Float64Var(&o.failUnder, "fail-under", 0, "exit 1 if the fraction of fully-passing samples is below this (0 = fail on any failure)")
	f.StringVar(&o.cache, "cache", "", "directory to cache judge responses in (skips re-billing identical prompts)")
	f.Float64Var(&o.costIn, "cost-in", 0, "USD per 1M prompt tokens (for the cost estimate)")
	f.Float64Var(&o.costOut, "cost-out", 0, "USD per 1M completion tokens (for the cost estimate)")
	f.StringVar(&o.where, "where", "", "only evaluate samples whose meta matches key=value (e.g. attack=jailbreak)")
	_ = root.MarkFlagRequired("dataset")

	root.AddCommand(newMetricsCmd())
	root.AddCommand(newGenCmd())
	root.AddCommand(newRedteamCmd())
	root.AddCommand(newDiffCmd())
	return root
}

// newDiffCmd compares two JSON reports and flags regressions (CI gate).
func newDiffCmd() *cobra.Command {
	var failOnRegression bool
	cmd := &cobra.Command{
		Use:           "diff <old-report.json> <new-report.json>",
		Short:         "Compare two JSON reports and flag regressions between versions",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			oldRep, err := loadReport(args[0])
			if err != nil {
				return err
			}
			newRep, err := loadReport(args[1])
			if err != nil {
				return err
			}
			d := evalgo.DiffReports(oldRep, newRep)
			d.WriteConsole(cmd.OutOrStdout())
			if failOnRegression && d.Regressions() > 0 {
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&failOnRegression, "fail-on-regression", true, "exit 1 if any metric regressed")
	return cmd
}

func loadReport(path string) (evalgo.Report, error) {
	data, err := readInput(path)
	if err != nil {
		return evalgo.Report{}, fmt.Errorf("read report %s: %w", path, err)
	}
	return evalgo.LoadReport(strings.NewReader(string(data)))
}

// newRedteamCmd generates an adversarial dataset to probe a system's safety.
// Offline by default; --enhance uses the LLM to obfuscate each probe.
func newRedteamCmd() *cobra.Command {
	var (
		out     string
		kinds   string
		enhance int
		rps     float64
		cache   string
	)
	cmd := &cobra.Command{
		Use:   "redteam",
		Short: "Generate an adversarial dataset to probe your system's safety (offline)",
		Long: "redteam emits attack Samples (prompt injection, jailbreak, PII extraction,\n" +
			"harmful requests). Run them through your system, fill in outputs, then score\n" +
			"with: evalgo -d attacks.json --judge env -m attack_resistance,pii_leakage,toxicity\n" +
			"--enhance N rewrites each probe with the LLM (needs LLM_* env).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt := evalgo.RedTeam{Enhance: enhance}
			for _, name := range strings.Split(kinds, ",") {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if !validAttackKind(name) {
					return fmt.Errorf("unknown attack kind %q (known: %s)", name, strings.Join(evalgo.AttackKinds(), ", "))
				}
				rt.Kinds = append(rt.Kinds, evalgo.AttackKind(name))
			}
			if enhance > 0 {
				judge, err := llmjudge.FromEnv()
				if err != nil {
					return fmt.Errorf("judge: %w", err)
				}
				if rps > 0 {
					judge = evalgo.RateLimit(judge, rps, 2)
				}
				rt.Judge = evalgo.Cache(judge, cache)
			}

			samples, err := rt.Generate(cmd.Context())
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(samples, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "generated %d attack samples\n", len(samples))
			if out == "" {
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}
			return os.WriteFile(out, data, 0o644)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&out, "out", "o", "", "write the dataset here (default: stdout)")
	f.StringVar(&kinds, "kinds", "", "comma-separated attack kinds ("+strings.Join(evalgo.AttackKinds(), ",")+"); empty = all")
	f.IntVar(&enhance, "enhance", 0, "LLM obfuscation passes per probe (needs LLM_* env)")
	f.Float64Var(&rps, "rps", 4, "LLM rate limit (requests/sec); <=0 disables")
	f.StringVar(&cache, "cache", "", "directory to cache LLM responses in")
	return cmd
}

func validAttackKind(name string) bool {
	for _, k := range evalgo.AttackKinds() {
		if k == name {
			return true
		}
	}
	return false
}

// newGenCmd synthesizes a golden dataset with an LLM, from documents or context
// groups, and writes it as a JSON array of samples ready for `evalgo -d`.
func newGenCmd() *cobra.Command {
	var (
		docs       string
		contexts   string
		out        string
		perContext int
		evolutions int
		chunk      int
		rps        float64
		cache      string
		costIn     float64
		costOut    float64
	)
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Synthesize a golden dataset from documents or contexts (requires an LLM)",
		Long: "gen uses LLM_BASE_URL / LLM_API_KEY / LLM_MODEL to generate question/answer\n" +
			"goldens grounded in your sources. Provide --docs (free text) or --contexts\n" +
			"(JSON array of context-chunk groups).",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (docs == "") == (contexts == "") {
				return fmt.Errorf("provide exactly one of --docs or --contexts")
			}
			judge, err := llmjudge.FromEnv()
			if err != nil {
				return fmt.Errorf("judge: %w", err)
			}
			if rps > 0 {
				judge = evalgo.RateLimit(judge, rps, 2)
			}
			meter := evalgo.NewMeter(costIn, costOut)
			judge = meter.Wrap(judge)
			judge = evalgo.Cache(judge, cache) // no-op when --cache is unset
			sy := evalgo.Synthesizer{Judge: judge, PerContext: perContext, Evolutions: evolutions}

			var samples []evalgo.Sample
			if docs != "" {
				text, err := readInput(docs)
				if err != nil {
					return err
				}
				samples, err = sy.FromDocuments(cmd.Context(), []string{string(text)}, chunk)
				if err != nil {
					return err
				}
			} else {
				raw, err := readInput(contexts)
				if err != nil {
					return err
				}
				var groups [][]string
				if err := json.Unmarshal(raw, &groups); err != nil {
					return fmt.Errorf("parse --contexts JSON (want [[\"chunk\",...],...]): %w", err)
				}
				samples, err = sy.FromContexts(cmd.Context(), groups)
				if err != nil {
					return err
				}
			}

			data, err := json.MarshalIndent(samples, "", "  ")
			if err != nil {
				return err
			}
			// usage to stderr so stdout stays clean JSON
			u := meter.Usage()
			fmt.Fprintf(cmd.ErrOrStderr(), "generated %d samples; %d calls, ~%d tokens, %.1fs",
				len(samples), u.Calls, u.TotalTokens, u.JudgeSeconds)
			if u.Cost > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), ", ~$%.4f", u.Cost)
			}
			fmt.Fprintln(cmd.ErrOrStderr())

			if out == "" {
				fmt.Fprintln(cmd.OutOrStdout(), string(data))
				return nil
			}
			return os.WriteFile(out, data, 0o644)
		},
	}
	f := cmd.Flags()
	f.StringVar(&docs, "docs", "", "source text file to chunk and generate from ('-' for stdin)")
	f.StringVar(&contexts, "contexts", "", "JSON file of context-chunk groups [[\"chunk\",...],...] ('-' for stdin)")
	f.StringVarP(&out, "out", "o", "", "write the dataset here (default: stdout)")
	f.IntVarP(&perContext, "per-context", "n", 2, "goldens generated per context group")
	f.IntVar(&evolutions, "evolutions", 0, "complexity-evolution passes per question")
	f.IntVar(&chunk, "chunk", 1000, "chunk size (runes) when splitting --docs")
	f.Float64Var(&rps, "rps", 4, "LLM rate limit (requests/sec); <=0 disables")
	f.StringVar(&cache, "cache", "", "directory to cache LLM responses in (skips re-billing identical prompts)")
	f.Float64Var(&costIn, "cost-in", 0, "USD per 1M prompt tokens (for the cost estimate)")
	f.Float64Var(&costOut, "cost-out", 0, "USD per 1M completion tokens (for the cost estimate)")
	return cmd
}

// newMetricsCmd lists the registered metric names (handy for scripting / docs).
func newMetricsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "metrics",
		Short: "List registered metric names",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			for _, n := range evalgo.RegisteredMetrics() {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
		},
	}
}

// runEval loads the dataset, builds metrics, runs the suite, writes the report,
// and applies the CI gate. A failed gate exits 1 directly; other problems return
// an error so main exits 2.
func runEval(stdout io.Writer, o options) error {
	samples, err := loadDataset(o.dataset)
	if err != nil {
		return err
	}
	if o.where != "" {
		k, v, ok := strings.Cut(o.where, "=")
		if !ok {
			return fmt.Errorf("--where must be key=value, got %q", o.where)
		}
		samples = evalgo.FilterMeta(samples, k, v)
		if len(samples) == 0 {
			return fmt.Errorf("no samples match --where %s", o.where)
		}
	}

	var judge evalgo.JudgeFunc
	var meter *evalgo.Meter
	switch o.judgeMode {
	case "env":
		judge, err = llmjudge.FromEnv()
		if err != nil {
			return fmt.Errorf("judge: %w", err)
		}
		if o.rps > 0 {
			judge = evalgo.RateLimit(judge, o.rps, 2)
		}
		// Meter inside the cache: cache hits return before reaching the meter,
		// so usage reflects only real (billed) provider calls.
		meter = evalgo.NewMeter(o.costIn, o.costOut)
		judge = meter.Wrap(judge)
		judge = evalgo.Cache(judge, o.cache) // no-op when --cache is unset
	case "none":
	default:
		return fmt.Errorf("unknown --judge mode %q (use env|none)", o.judgeMode)
	}

	metrics, err := evalgo.BuildMetrics(strings.Split(o.metrics, ","), evalgo.MetricSpec{
		Judge: judge, Threshold: o.threshold,
	})
	if err != nil {
		return err
	}

	suite := evalgo.Suite{Samples: samples, Metrics: metrics, Concurrency: o.concurrency}
	report := suite.Run(context.Background())
	if meter != nil {
		u := meter.Usage()
		report.Usage = &u
	}

	switch o.format {
	case "json":
		if err := report.WriteJSON(stdout); err != nil {
			return err
		}
	case "console":
		report.WriteConsole(stdout)
	default:
		return fmt.Errorf("unknown --format %q (use console|json)", o.format)
	}

	if o.out != "" {
		f, err := os.Create(o.out)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := report.WriteJSON(f); err != nil {
			return err
		}
	}

	if gateFailed(report, o.failUnder) {
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

// readInput reads a file, or stdin when path is "-".
func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// loadDataset reads a dataset, picking the loader by file extension
// (.jsonl / .csv, else JSON). Stdin ("-") is treated as JSON.
func loadDataset(path string) ([]evalgo.Sample, error) {
	data, err := readInput(path)
	if err != nil {
		return nil, fmt.Errorf("read dataset: %w", err)
	}
	r := strings.NewReader(string(data))
	var samples []evalgo.Sample
	switch {
	case strings.HasSuffix(path, ".jsonl"):
		samples, err = evalgo.LoadJSONL(r)
	case strings.HasSuffix(path, ".csv"):
		samples, err = evalgo.LoadCSV(r)
	default:
		samples, err = evalgo.LoadJSON(r)
	}
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("dataset is empty")
	}
	return samples, nil
}
